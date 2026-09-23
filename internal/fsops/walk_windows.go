package fsops

import (
	"encoding/binary"
	"errors"
	"os"
	"slices"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// readDirSys は、フォルダのハンドルに GetFileInformationByHandleEx を使って、名前・属性・リパースタグ・ファイル ID・サイズ・更新日時を
// 1 回の列挙で得る（§13.1）。FileIdExtdDirectoryInfo が ERROR_INVALID_PARAMETER で失敗するボリューム（exFAT・FAT32。V14）では
// FileIdBothDirectoryInfo に切り替える。
func readDirSys(s string) ([]dirEntry, error) {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(s16, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &os.PathError{Op: "CreateFile", Path: s, Err: err}
	}
	defer windows.CloseHandle(h)
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bi); err != nil {
		return nil, &os.PathError{Op: "GetFileInformationByHandle", Path: s, Err: err}
	}
	if bi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 || bi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		// フォルダでない、またはリンク・ジャンクション。中に入らない（I4）。
		return nil, &os.PathError{Op: "readdir", Path: s, Err: windows.ERROR_DIRECTORY}
	}
	dirID, err := statIDHandle(h)
	if err != nil {
		return nil, &os.PathError{Op: "GetFileInformationByHandleEx", Path: s, Err: err}
	}
	entries, err := enumerateDir(h, windows.FileIdExtdDirectoryRestartInfo, windows.FileIdExtdDirectoryInfo, true, dirID.id.vol)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		entries, err = enumerateDir(h, windows.FileIdBothDirectoryRestartInfo, windows.FileIdBothDirectoryInfo, false, uint64(bi.VolumeSerialNumber))
	}
	if err != nil {
		return nil, &os.PathError{Op: "GetFileInformationByHandleEx", Path: s, Err: err}
	}
	slices.SortFunc(entries, func(a, b dirEntry) int { return strings.Compare(a.name, b.name) })
	return entries, nil
}

// enumerateDir は、フォルダのハンドル h を列挙する。extd が真なら FILE_ID_EXTD_DIR_INFO（128 ビットのファイル ID、
// fileID は FileIdInfo と同じ方法）、偽なら FILE_ID_BOTH_DIR_INFO（64 ビットのファイル ID、GetFileInformationByHandle と同じ方法）として読む。
func enumerateDir(h windows.Handle, restartClass, class uint32, extd bool, vol uint64) ([]dirEntry, error) {
	backing := make([]uint64, 64*1024/8) // LARGE_INTEGER のため 8 バイト境界にそろえる
	buf := unsafe.Slice((*byte)(unsafe.Pointer(&backing[0])), len(backing)*8)
	var entries []dirEntry
	for c := restartClass; ; c = class {
		err := windows.GetFileInformationByHandleEx(h, c, &buf[0], uint32(len(buf)))
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
		for off := 0; ; {
			e := buf[off:]
			next := binary.LittleEndian.Uint32(e[0:])
			lastWrite := windows.Filetime{LowDateTime: binary.LittleEndian.Uint32(e[24:]), HighDateTime: binary.LittleEndian.Uint32(e[28:])}
			size := int64(binary.LittleEndian.Uint64(e[40:]))
			attrs := binary.LittleEndian.Uint32(e[56:])
			nameLen := int(binary.LittleEndian.Uint32(e[60:]))
			var tag uint32
			var id fileID
			var nameOff int
			if extd {
				tag = binary.LittleEndian.Uint32(e[68:])
				id = fileID{method: idMethodFileID, vol: vol}
				copy(id.id[:], e[72:88])
				nameOff = 88
			} else {
				if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
					tag = binary.LittleEndian.Uint32(e[64:]) // リパースポイントでは EaSize の位置にタグが入る
				}
				id = fileID{method: idMethodByHandle, vol: vol}
				copy(id.id[:8], e[96:104])
				nameOff = 104
			}
			name := windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&e[nameOff])), nameLen/2))
			if name != "." && name != ".." {
				t := entryTypeFromAttrs(attrs, tag)
				info := EntryInfo{Type: t, ModTime: time.Unix(0, lastWrite.Nanoseconds())}
				if t == TypeFile {
					info.Size = size
				}
				entries = append(entries, dirEntry{name: name, info: info, id: id})
			}
			if next == 0 {
				break
			}
			off += int(next)
		}
	}
}

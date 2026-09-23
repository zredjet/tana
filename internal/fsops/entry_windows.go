package fsops

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows に定義がないリパースタグ。
const (
	ioReparseTagCloud     = 0x9000001A // IO_REPARSE_TAG_CLOUD。CLOUD_1〜CLOUD_F はビット 12〜15 が異なる
	ioReparseTagCloudMask = 0x0000F000 // IO_REPARSE_TAG_CLOUD_MASK
	ioReparseTagDedup     = 0x80000013 // IO_REPARSE_TAG_DEDUP
)

// fileAttributeTagInfo は FILE_ATTRIBUTE_TAG_INFO（x/sys/windows に定義がない）。
type fileAttributeTagInfo struct {
	FileAttributes uint32
	ReparseTag     uint32
}

// lstatEntrySys は、sysPath で変換済みのパス p を調べる。
// Go のモードビットは Go のバージョンによってリパースポイントの扱いが変わってきたため使わず、
// ファイル属性とリパースタグで判定する（SPEC §14.1）。
func lstatEntrySys(p string) (EntryInfo, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return EntryInfo{}, err
	}
	attrs := fi.Sys().(*syscall.Win32FileAttributeData).FileAttributes
	var tag uint32
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		// 属性とタグは同じハンドルから取り、両者が別のエントリのものにならないようにする。
		attrs, tag, err = reparseTag(p)
		if err != nil {
			return EntryInfo{}, err
		}
	}
	return entryInfo(entryTypeFromAttrs(attrs, tag), fi), nil
}

// reparseTag は、p をリパースポイントを辿らずに開き、ファイル属性とリパースタグを返す。
func reparseTag(p string) (attrs, tag uint32, err error) {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return 0, 0, err
	}
	// FILE_READ_ATTRIBUTES だけで開くので、ほかのプロセスの共有モードに妨げられない。
	h, err := windows.CreateFile(p16, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0, 0, &os.PathError{Op: "CreateFile", Path: p, Err: err}
	}
	defer windows.CloseHandle(h)
	var ti fileAttributeTagInfo
	if err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo,
		(*byte)(unsafe.Pointer(&ti)), uint32(unsafe.Sizeof(ti))); err != nil {
		return 0, 0, &os.PathError{Op: "GetFileInformationByHandleEx", Path: p, Err: err}
	}
	return ti.FileAttributes, ti.ReparseTag, nil
}

// entryTypeFromAttrs はファイル属性とリパースタグから種類を判定する（SPEC §14.1）。
// tag は attrs に FILE_ATTRIBUTE_REPARSE_POINT があるときだけ意味を持つ。
func entryTypeFromAttrs(attrs, tag uint32) EntryType {
	plain := TypeFile
	if attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		plain = TypeDir
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return plain
	}
	switch {
	case tag == windows.IO_REPARSE_TAG_SYMLINK:
		return TypeSymlink
	case tag == windows.IO_REPARSE_TAG_MOUNT_POINT:
		return TypeJunction
	case tag&^ioReparseTagCloudMask == ioReparseTagCloud, tag == ioReparseTagDedup:
		// OneDrive などのクラウドファイルと重複除去は、通常のファイル・フォルダとして扱う。
		return plain
	}
	return TypeSpecial
}

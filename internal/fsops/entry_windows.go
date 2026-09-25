package fsops

import (
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows に定義がないリパースタグ。
const (
	ioReparseTagCloud     = 0x9000001A // IO_REPARSE_TAG_CLOUD。CLOUD_1〜CLOUD_F はビット 12〜15 が異なる
	ioReparseTagCloudMask = 0x0000F000 // IO_REPARSE_TAG_CLOUD_MASK
	ioReparseTagDedup     = 0x80000013 // IO_REPARSE_TAG_DEDUP
	ioReparseTagWOF       = 0x80000017 // IO_REPARSE_TAG_WOF（compact /exe、CompactOS による透過圧縮）
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
	var fi os.FileInfo
	err, pending := callDeletePending(func() error {
		var err error
		fi, err = os.Lstat(p)
		return err
	})
	if pending {
		return EntryInfo{}, deletePendingErr("lstat", p, err) // 削除待ち（§17）
	}
	if err != nil {
		return EntryInfo{}, err
	}
	attrs := fi.Sys().(*syscall.Win32FileAttributeData).FileAttributes
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
		return entryInfo(entryTypeFromAttrs(attrs, 0), fi), nil
	}
	return reparseEntry(p)
}

// reparseEntry は、p をリパースポイントを辿らずに開き、種類・サイズ・更新日時をすべてそのハンドルから求める。
// os.Lstat の結果と混ぜないのは、その間に置き換えられた場合に、別のエントリの情報が混ざらないようにするため。
func reparseEntry(p string) (EntryInfo, error) {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return EntryInfo{}, err
	}
	// FILE_READ_ATTRIBUTES だけで開くので、ほかのプロセスの共有モードに妨げられない。
	h, err := windows.CreateFile(p16, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return EntryInfo{}, &os.PathError{Op: "CreateFile", Path: p, Err: err}
	}
	defer windows.CloseHandle(h)
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bi); err != nil {
		return EntryInfo{}, &os.PathError{Op: "GetFileInformationByHandle", Path: p, Err: err}
	}
	var ti fileAttributeTagInfo
	if err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo,
		(*byte)(unsafe.Pointer(&ti)), uint32(unsafe.Sizeof(ti))); err != nil {
		return EntryInfo{}, &os.PathError{Op: "GetFileInformationByHandleEx", Path: p, Err: err}
	}
	info := EntryInfo{
		Type:    entryTypeFromAttrs(ti.FileAttributes, ti.ReparseTag),
		ModTime: time.Unix(0, bi.LastWriteTime.Nanoseconds()),
	}
	if info.Type == TypeFile {
		info.Size = int64(bi.FileSizeHigh)<<32 | int64(bi.FileSizeLow)
	}
	return info, nil
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
	case tag&^ioReparseTagCloudMask == ioReparseTagCloud, tag == ioReparseTagDedup, tag == ioReparseTagWOF:
		// OneDrive などのクラウドファイル、重複除去、WOF の透過圧縮は、通常のファイル・フォルダとして扱う。
		return plain
	}
	return TypeSpecial
}

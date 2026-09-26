package fsops

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// notLocalAttrs は、中身が手元にない（開く・読むと取得が始まる）ことを示す属性（§14.4）。
const notLocalAttrs = windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS | windows.FILE_ATTRIBUTE_RECALL_ON_OPEN | windows.FILE_ATTRIBUTE_OFFLINE

// readHeadSys は ReadHead の Windows の実装。開く前に属性を調べ（フォルダ、中身が手元にないもの）、
// 共有モードをすべて許して開き（リンクは辿る）、開いたハンドルでもう一度調べてから読む。
func readHeadSys(s string, max int) (Head, error) {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return Head{}, err
	}
	var fa windows.Win32FileAttributeData
	if err := windows.GetFileAttributesEx(s16, windows.GetFileExInfoStandard, (*byte)(unsafe.Pointer(&fa))); err != nil {
		return Head{}, &os.PathError{Op: "GetFileAttributesEx", Path: s, Err: err}
	}
	size := int64(fa.FileSizeHigh)<<32 | int64(fa.FileSizeLow)
	switch {
	case fa.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0: // フォルダと、フォルダへのリンク・ジャンクション
		return Head{}, &os.PathError{Op: "GetFileAttributesEx", Path: s, Err: errNotRegular}
	case fa.FileAttributes&notLocalAttrs != 0:
		return Head{Size: size, NotLocal: true}, nil
	}
	h, err := windows.CreateFile(s16, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_SEQUENTIAL_SCAN, 0)
	if err != nil {
		return Head{}, &os.PathError{Op: "CreateFile", Path: s, Err: err}
	}
	defer windows.CloseHandle(h)
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bi); err != nil {
		return Head{}, &os.PathError{Op: "GetFileInformationByHandle", Path: s, Err: err}
	}
	if t, err := windows.GetFileType(h); err != nil || t != windows.FILE_TYPE_DISK || bi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return Head{}, &os.PathError{Op: "GetFileType", Path: s, Err: errNotRegular}
	}
	size = int64(bi.FileSizeHigh)<<32 | int64(bi.FileSizeLow)
	if bi.FileAttributes&notLocalAttrs != 0 { // リンク先のファイル
		return Head{Size: size, NotLocal: true}, nil
	}
	buf := make([]byte, max)
	n := 0
	for n < max {
		var done uint32
		if err := windows.ReadFile(h, buf[n:], &done, nil); err != nil {
			return Head{}, &os.PathError{Op: "ReadFile", Path: s, Err: err}
		}
		if done == 0 {
			break
		}
		n += int(done)
	}
	return Head{Data: buf[:n], Size: size}, nil
}

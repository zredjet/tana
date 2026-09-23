package fsops

import (
	"encoding/binary"
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileIDInfo は FILE_ID_INFO（x/sys/windows に定義がない）。
type fileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

// statIDSys は、sysPath で変換済みのパス p の fileID を返す。follow が偽ならリンク・ジャンクションを辿らない。
func statIDSys(p string, follow bool) (idStat, error) {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return idStat{}, err
	}
	flags := uint32(windows.FILE_FLAG_BACKUP_SEMANTICS)
	if !follow {
		flags |= windows.FILE_FLAG_OPEN_REPARSE_POINT
	}
	// FILE_READ_ATTRIBUTES だけで開くので、ほかのプロセスの共有モードに妨げられない。
	h, err := windows.CreateFile(p16, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return idStat{}, &os.PathError{Op: "CreateFile", Path: p, Err: err}
	}
	defer windows.CloseHandle(h)
	st, err := statIDHandle(h)
	if err != nil {
		return idStat{}, &os.PathError{Op: "GetFileInformationByHandle", Path: p, Err: err}
	}
	return st, nil
}

// statIDHandle は、開いたハンドルの fileID を返す（§8.3）。
// FileIdInfo を使い、ERROR_INVALID_PARAMETER で失敗するボリューム（exFAT・FAT32。V14）では
// GetFileInformationByHandle のボリュームシリアル番号とファイルインデックスを使う。
func statIDHandle(h windows.Handle) (idStat, error) {
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bi); err != nil {
		return idStat{}, err
	}
	st := idStat{
		isDir: bi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 && bi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0,
		nlink: uint64(bi.NumberOfLinks),
	}
	var fi fileIDInfo
	err := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&fi)), uint32(unsafe.Sizeof(fi)))
	switch {
	case err == nil:
		st.id = fileID{method: idMethodFileID, vol: fi.VolumeSerialNumber, id: fi.FileID}
	case errors.Is(err, windows.ERROR_INVALID_PARAMETER):
		st.id = fileID{method: idMethodByHandle, vol: uint64(bi.VolumeSerialNumber)}
		binary.LittleEndian.PutUint64(st.id.id[:], uint64(bi.FileIndexHigh)<<32|uint64(bi.FileIndexLow))
	default:
		return idStat{}, err
	}
	return st, nil
}

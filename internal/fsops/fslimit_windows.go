package fsops

import "golang.org/x/sys/windows"

// fileSizeLimitPath は、フォルダ path のあるファイルシステムのファイルの大きさの上限を返す（§6.4、§10.6）。上限がない・調べられなければ 0。
func fileSizeLimitPath(path string) int64 {
	s, err := sysPath(path)
	if err != nil {
		return 0
	}
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return 0
	}
	h, err := windows.CreateFile(s16, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	return fileSizeLimitHandle(h)
}

// fileSizeLimit は、確かめて開いたこのフォルダのあるファイルシステムのファイルの大きさの上限を返す（§10.6）。上限がない・調べられなければ 0。
func (d *secDir) fileSizeLimit() int64 { return fileSizeLimitHandle(d.h) }

// fileSizeLimitHandle は、ハンドル h のあるファイルシステムの名前（GetVolumeInformationByHandleW）が FAT・FAT32 なら、その上限を返す。
func fileSizeLimitHandle(h windows.Handle) int64 {
	var name [windows.MAX_PATH + 1]uint16
	if err := windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, nil, &name[0], uint32(len(name))); err != nil {
		return 0
	}
	switch windows.UTF16ToString(name[:]) {
	case "FAT", "FAT32":
		return fatFileSizeLimit
	}
	return 0
}

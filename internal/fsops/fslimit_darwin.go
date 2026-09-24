package fsops

import "golang.org/x/sys/unix"

// fileSizeLimitPath は、フォルダ path のあるファイルシステムのファイルの大きさの上限を返す（§6.4、§10.6）。上限がない・調べられなければ 0。
func fileSizeLimitPath(path string) int64 {
	var st unix.Statfs_t
	if err := ignoringEINTR(func() error { return unix.Statfs(path, &st) }); err != nil {
		return 0
	}
	return fileSizeLimitStatfs(&st)
}

// fileSizeLimit は、確かめて開いたこのフォルダのあるファイルシステムのファイルの大きさの上限を返す（§10.6）。上限がない・調べられなければ 0。
func (d *secDir) fileSizeLimit() int64 {
	var st unix.Statfs_t
	if err := ignoringEINTR(func() error { return unix.Fstatfs(d.fd, &st) }); err != nil {
		return 0
	}
	return fileSizeLimitStatfs(&st)
}

// fileSizeLimitStatfs は、ファイルシステム名（f_fstypename）が msdos（FAT 系）なら、その上限を返す。
func fileSizeLimitStatfs(st *unix.Statfs_t) int64 {
	if unix.ByteSliceToString(st.Fstypename[:]) == "msdos" {
		return fatFileSizeLimit
	}
	return 0
}

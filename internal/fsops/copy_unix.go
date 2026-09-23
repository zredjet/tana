//go:build unix

package fsops

import (
	"os"

	"golang.org/x/sys/unix"
)

// openSourceSys は、コピー元のファイル s を開く（§10.1 の手順 1）。
// O_NOFOLLOW|O_NONBLOCK で開き（FIFO に置き換えられていても open で止まらないため）、fstat で通常のファイルであることと、
// fileID が走査時の want と一致することを確かめる。違えば KindSourceChanged の *OpError を返す。
func openSourceSys(s string, want fileID) (*os.File, error) {
	fd, err := unix.Open(s, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: s, Err: err}
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, &os.PathError{Op: "fstat", Path: s, Err: err}
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || idStatFromStat(&st).id != want {
		unix.Close(fd)
		return nil, &OpError{Op: "open", Path: s, Kind: KindSourceChanged}
	}
	// 通常のファイルでは O_NONBLOCK は意味を持たないが、os.NewFile がポーラーに登録しようとしないよう外しておく。
	if err := unix.SetNonblock(fd, false); err != nil {
		unix.Close(fd)
		return nil, &os.PathError{Op: "fcntl", Path: s, Err: err}
	}
	return os.NewFile(uintptr(fd), s), nil
}

// inUseSys は、Windows の「使用中で置き換えられない」の判定（§9.3）。Unix にはこの状態がないので常に偽。
func inUseSys(string) bool { return false }

// clearReadOnlySys は、Windows で fsops の一時ファイルの読み取り専用属性を外す。
// Unix では権限が削除を妨げないので何もせず、偽を返す。
func clearReadOnlySys(string) bool { return false }

// syncDirSys は、フォルダ s を開いて Sync する（§10.5。ディレクトリエントリの永続化）。
func syncDirSys(s string) error {
	f, err := os.Open(s)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

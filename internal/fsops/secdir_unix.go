//go:build unix

package fsops

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// secDir は、§13.1 の方法で確かめてから開いたフォルダ。削除（§13.2、§13.3）と同一ボリュームのマージ移動（§11.1）で使う。
// Unix では、トップレベルのフォルダはパスで、それより下は親フォルダのハンドルからの相対で O_NOFOLLOW を付けて開く。
type secDir struct {
	path string // \\?\ の付かない形のパス（結果とフックに使う）
	fd   int
}

// openSecDir は、フォルダ path を開き、fileID が want と一致することを確かめる（§13.1）。
// parent が nil ならパスで、そうでなければ parent からの相対で name を開く。
// リンク・ジャンクションに置き換えられていた場合や fileID が違う場合は KindSourceChanged の *OpError を返す。
func openSecDir(parent *secDir, path, name string, want fileID) (*secDir, error) {
	const flags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	var fd int
	var err error
	if parent == nil {
		fd, err = unix.Open(path, flags, 0)
	} else {
		fd, err = unix.Openat(parent.fd, name, flags, 0)
	}
	if err != nil {
		if err == unix.ELOOP || err == unix.ENOTDIR {
			return nil, &OpError{Op: "open", Path: path, Kind: KindSourceChanged, Err: err}
		}
		return nil, &OpError{Op: "open", Path: path, Kind: classify(err, classifyOpts{}), Err: &os.PathError{Op: "open", Path: path, Err: err}}
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, &OpError{Op: "open", Path: path, Kind: classify(err, classifyOpts{}), Err: err}
	}
	if idStatFromStat(&st).id != want {
		unix.Close(fd)
		return nil, &OpError{Op: "open", Path: path, Kind: KindSourceChanged}
	}
	return &secDir{path: path, fd: fd}, nil
}

func (d *secDir) close() { unix.Close(d.fd) }

// list は中身を名前のバイト順で列挙する。
func (d *secDir) list() ([]dirEntry, error) { return listFD(d.fd, d.path) }

// stat は、中の name をリンクを辿らずに調べる（§13.3 の照合、削除に失敗した後の調べ直し）。
func (d *secDir) stat(name string) (dirEntry, error) {
	e, err := statAt(d.fd, name)
	if err != nil {
		return dirEntry{}, &os.PathError{Op: "fstatat", Path: d.path + "/" + name, Err: err}
	}
	return e, nil
}

// remove は、中の e を §13.2 の方法（unlinkat）で削除する。clearReadOnly は Unix では使わない。
func (d *secDir) remove(e dirEntry, clearReadOnly bool) error {
	flags := 0
	if e.dirAttr {
		flags = unix.AT_REMOVEDIR
	}
	if err := unix.Unlinkat(d.fd, e.name, flags); err != nil {
		return &os.PathError{Op: "unlinkat", Path: d.path + "/" + e.name, Err: err}
	}
	return nil
}

// removeTop は、トップレベルのエントリ path を §13.2 の方法（rmdir・unlink）で削除する。
func removeTop(path string, e dirEntry, clearReadOnly bool) error {
	var err error
	if e.dirAttr {
		err = unix.Rmdir(path)
	} else {
		err = unix.Unlink(path)
	}
	if err != nil {
		return &os.PathError{Op: "remove", Path: path, Err: err}
	}
	return nil
}

// statTop は、トップレベルのエントリ path をリンクを辿らずに調べる。
func statTop(path string) (dirEntry, error) {
	e, err := statAt(unix.AT_FDCWD, path)
	if err != nil {
		return dirEntry{}, &os.PathError{Op: "lstat", Path: path, Err: err}
	}
	return e, nil
}

// isMismatchRemoveErr は、種類に合わない方法での削除の失敗を示しうるエラーか（§13.2。EISDIR・ENOTDIR・EPERM）。
func isMismatchRemoveErr(err error) bool {
	for _, e := range []unix.Errno{unix.EISDIR, unix.ENOTDIR, unix.EPERM} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

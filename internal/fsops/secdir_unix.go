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
	path  string // \\?\ の付かない形のパス（結果とフックに使う）
	fd    int
	id    fileID // 開いたフォルダの fileID
	dirty bool   // 中の名前を変えた（作成・リネーム）。§10.5 の同期に使う
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
	return &secDir{path: path, fd: fd, id: want}, nil
}

// openNewSecDir は、フォルダ parent の中に作ったばかりのフォルダ name（パスは path）を、リンクを辿らずに開く（§10.2）。
// fileID は、開いたものから記録する。リンクに置き換えられていれば KindSourceChanged の *OpError を返す。
func openNewSecDir(parent *secDir, path, name string) (*secDir, error) {
	fd, err := unix.Openat(parent.fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
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
	return &secDir{path: path, fd: fd, id: idStatFromStat(&st).id}, nil
}

// openDestRoot は、コピー先・移動先のフォルダ（DestDir）path を開き、fileID が計画時の want（リンクを辿った先）と一致することを確かめる（§13.1）。
// DestDir 自体はリンクでもよいので、辿って開く。違えば KindSourceChanged の *OpError を返す。
func openDestRoot(path string, want fileID) (*secDir, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
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
	return &secDir{path: path, fd: fd, id: want}, nil
}

// join は、中の name の \\?\ の付かない形のパスを返す（結果とエラーに使う）。
func (d *secDir) join(name string) string { return d.path + "/" + name }

// createFile は、中に name を O_CREAT|O_EXCL|O_WRONLY|O_NOFOLLOW、0o600 で作る（§10.1 の一時ファイル）。
func (d *secDir) createFile(name string) (*os.File, error) {
	fd, err := unix.Openat(d.fd, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: d.join(name), Err: err}
	}
	return os.NewFile(uintptr(fd), d.join(name)), nil
}

// mkdir は、中に name のフォルダを作る（§10.2。0o700 で作り、パーミッションは中身の後に設定する）。
func (d *secDir) mkdir(name string) error {
	if err := unix.Mkdirat(d.fd, name, 0o700); err != nil {
		return &os.PathError{Op: "mkdirat", Path: d.join(name), Err: err}
	}
	return nil
}

// symlink は、中に name のシンボリックリンクを作る（§14.2）。dir は Windows のフォルダ用のリンクの区別で、Unix では使わない。
func (d *secDir) symlink(target, name string, dir bool) error {
	if err := unix.Symlinkat(target, d.fd, name); err != nil {
		return &os.LinkError{Op: "symlinkat", Old: target, New: d.join(name), Err: err}
	}
	return nil
}

// unlinkTemp は、中の fsops の一時ファイル name を削除する（§10.1 の手順 8）。
func (d *secDir) unlinkTemp(name string) error {
	if err := unix.Unlinkat(d.fd, name, 0); err != nil {
		return &os.PathError{Op: "unlinkat", Path: d.join(name), Err: err}
	}
	return nil
}

// openRegular は、中の name を読むために開き、通常のファイルで fileID が want であることを確かめる（§10.4 の読み直し）。
func (d *secDir) openRegular(name string, want fileID) (*os.File, srcMeta, error) {
	return openRegularAt(d.fd, name, want)
}

// targetReadOnly は、中の上書き先 name が読み取り専用（§9.3）かを返す。
func (d *secDir) targetReadOnly(name string) bool {
	var st unix.Stat_t
	return unix.Fstatat(d.fd, name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && statReadOnly(&st)
}

// sync は、このフォルダを同期する（§10.5。ディレクトリエントリの永続化）。
func (d *secDir) sync() error {
	dup, err := unix.Dup(d.fd)
	if err != nil {
		return &os.PathError{Op: "dup", Path: d.path, Err: err}
	}
	f := os.NewFile(uintptr(dup), d.path)
	defer f.Close()
	return f.Sync() // macOS の Go は F_FULLFSYNC を使う
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

// renameBetween は、from の中の fromName を、to の中の toName へリネームする（§13.1。確かめて開いたフォルダからの相対）。
// from が nil なら fromName はパス（sysPath で変換したもの）。replace が偽なら排他リネーム（§8.4）、真なら置換リネーム（ファイルの上書き）。
func renameBetween(from *secDir, fromName string, to *secDir, toName string, replace bool) error {
	sfd := unix.AT_FDCWD
	if from != nil {
		sfd = from.fd
	}
	if !replace {
		return renameAtExclusiveSys(sfd, fromName, to.fd, toName)
	}
	if err := unix.Renameat(sfd, fromName, to.fd, toName); err != nil {
		return &os.LinkError{Op: "renameat", Old: fromName, New: to.join(toName), Err: err}
	}
	return nil
}

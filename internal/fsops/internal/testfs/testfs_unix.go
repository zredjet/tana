//go:build unix

package testfs

import (
	"io/fs"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// ExtendedPath は Windows の \\?\ 形式への変換に合わせた関数。Unix ではそのまま返す。
func ExtendedPath(p string) string { return p }

// isRealDir は、フォルダかを返す（Lstat の結果なのでリンクは含まない）。
func isRealDir(fi fs.FileInfo) bool { return fi.IsDir() }

// isLink は、シンボリックリンクかを返す。
func isLink(fi fs.FileInfo) bool { return fi.Mode()&fs.ModeSymlink != 0 }

// CreateSymlink は link にシンボリックリンクを作る。target の文字列はそのまま使う。
// Unix にはファイル用・フォルダ用の区別がないので dir は使わない。
func CreateSymlink(t testing.TB, target, link string, dir bool) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// CreateJunction は Windows 専用なので t.Skip する。
func CreateJunction(t testing.TB, target, link string) {
	t.Helper()
	t.Skip("junctions are only available on Windows")
}

// CreateFIFO は path に FIFO を作る（TypeSpecial の確認用）。
func CreateFIFO(t testing.TB, path string) {
	t.Helper()
	if err := unix.Mkfifo(path, 0o644); err != nil {
		t.Fatalf("mkfifo %s: %v", path, err)
	}
}

// Lock は Windows 専用なので t.Skip する（Unix には共有違反にあたるロックがない）。
func Lock(t testing.TB, path string) {
	t.Helper()
	t.Skip("file locking by share mode is only available on Windows")
}

func setReadOnly(path string) (restore func() error, err error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, &os.PathError{Op: "setReadOnly", Path: path, Err: fs.ErrInvalid} // chmod はリンクを辿るため
	}
	mode := fi.Mode().Perm()
	if err := os.Chmod(path, mode&^0o222); err != nil {
		return nil, err
	}
	return func() error { return os.Chmod(path, mode) }, nil
}

func clearReadOnly(path string) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode().Perm()&0o200 == 0 {
		os.Chmod(path, fi.Mode().Perm()|0o700)
	}
}

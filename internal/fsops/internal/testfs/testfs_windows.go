package testfs

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// symbolicLinkFlagAllowUnprivilegedCreate は SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE（x/sys/windows に定義がない）。
const symbolicLinkFlagAllowUnprivilegedCreate = 0x2

// ExtendedPath は絶対パスを \\?\ 形式にする（UNC は \\?\UNC\...）。既に \\?\ 形式のパスと、それ以外の形式はそのまま返す。
func ExtendedPath(p string) string {
	if strings.HasPrefix(p, `\\?\`) {
		return p
	}
	c := filepath.Clean(p)
	switch {
	case len(c) >= 3 && c[1] == ':' && c[2] == '\\':
		return `\\?\` + c
	case strings.HasPrefix(c, `\\`) && !strings.HasPrefix(c, `\\.\`):
		return `\\?\UNC\` + c[2:]
	}
	return p
}

func attrsOf(fi fs.FileInfo) uint32 {
	return fi.Sys().(*syscall.Win32FileAttributeData).FileAttributes
}

// isRealDir は、リパースポイントでないフォルダかを返す。
func isRealDir(fi fs.FileInfo) bool {
	a := attrsOf(fi)
	return a&windows.FILE_ATTRIBUTE_DIRECTORY != 0 && a&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}

// isLink は、リパースポイント（シンボリックリンク・ジャンクションなど）かを返す。
func isLink(fi fs.FileInfo) bool {
	return attrsOf(fi)&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}

// CreateSymlink は link にシンボリックリンクを作る。target の文字列はそのまま使う。
// dir が真ならフォルダ用のリンクにする（リンク先の有無・種類は調べない）。
// 作る権限がなければ t.Skip する（V8）。
func CreateSymlink(t testing.TB, target, link string, dir bool) {
	t.Helper()
	flags := uint32(symbolicLinkFlagAllowUnprivilegedCreate)
	if dir {
		flags |= windows.SYMBOLIC_LINK_FLAG_DIRECTORY
	}
	l16, err := windows.UTF16PtrFromString(ExtendedPath(link))
	if err != nil {
		t.Fatal(err)
	}
	t16, err := windows.UTF16PtrFromString(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.CreateSymbolicLink(l16, t16, flags); err != nil {
		if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
			t.Skipf("no privilege to create a symbolic link %s: %v (V8)", link, err)
		}
		t.Fatalf("CreateSymbolicLink %s -> %s: %v", link, target, err)
	}
}

// CreateJunction は link にジャンクション（マウントポイント）を作る（cmd /c mklink /J）。
// cmd.exe が解釈する文字（& | < > ^ %）を含むパスは、別のコマンドやパスとして扱われるため t.Fatal にする。
// t.TempDir のフォルダ名にはテスト名のこれらの文字が残るので、そうしたテスト名でジャンクションを使わないこと。
func CreateJunction(t testing.TB, target, link string) {
	t.Helper()
	if strings.ContainsAny(link+target, "&|<>^%") {
		t.Fatalf("CreateJunction: %q or %q contains a character that cmd.exe interprets (& | < > ^ %%)", link, target)
	}
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J %s %s: %v\n%s", link, target, err, out)
	}
}

// CreateFIFO は Windows では作れないので t.Skip する。
func CreateFIFO(t testing.TB, path string) {
	t.Helper()
	t.Skip("FIFO is not available on Windows")
}

// Lock は path を共有モード 0 で開いたままにし、ほかから開けないようにする。テストの終了時に閉じる。
func Lock(t testing.TB, path string) {
	t.Helper()
	p16, err := windows.UTF16PtrFromString(ExtendedPath(path))
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p16, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatalf("lock %s: %v", path, err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
}

// SetImmutable は macOS 専用なので t.Skip する。
func SetImmutable(t testing.TB, path string) {
	t.Helper()
	t.Skip("UF_IMMUTABLE is only available on macOS")
}

func getAttrs(path string) (*uint16, uint32, error) {
	p16, err := windows.UTF16PtrFromString(ExtendedPath(path))
	if err != nil {
		return nil, 0, err
	}
	a, err := windows.GetFileAttributes(p16)
	if err != nil {
		return nil, 0, &os.PathError{Op: "GetFileAttributes", Path: path, Err: err}
	}
	return p16, a, nil
}

func setReadOnly(path string) (restore func() error, err error) {
	p16, a, err := getAttrs(path)
	if err != nil {
		return nil, err
	}
	if err := windows.SetFileAttributes(p16, a|windows.FILE_ATTRIBUTE_READONLY); err != nil {
		return nil, &os.PathError{Op: "SetFileAttributes", Path: path, Err: err}
	}
	return func() error {
		if _, _, err := getAttrs(path); err != nil {
			return err
		}
		return windows.SetFileAttributes(p16, a)
	}, nil
}

func clearReadOnly(path string) {
	if p16, a, err := getAttrs(path); err == nil && a&windows.FILE_ATTRIBUTE_READONLY != 0 {
		windows.SetFileAttributes(p16, a&^windows.FILE_ATTRIBUTE_READONLY)
	}
}

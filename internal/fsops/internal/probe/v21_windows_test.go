package probe

import (
	"os"
	"path/filepath"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// v21Ops は、Windows で試す不可分な操作（TestV21）。どれも既存のファイル existing を上書きしない。
func v21Ops(d string) []v21Op {
	src := func(name string) string {
		p := filepath.Join(d, name)
		os.WriteFile(testfs.ExtendedPath(p), []byte(name), 0o644)
		return p
	}
	move := func(from, to string) error {
		f, err := windows.UTF16PtrFromString(testfs.ExtendedPath(from))
		if err != nil {
			return err
		}
		t, err := windows.UTF16PtrFromString(testfs.ExtendedPath(to))
		if err != nil {
			return err
		}
		return windows.MoveFileEx(f, t, 0)
	}
	return []v21Op{
		{"MoveFileExW(0, new name)", func() error { return move(src("s1"), filepath.Join(d, "n1")) }},
		{"MoveFileExW(0, existing name)", func() error { return move(src("s2"), filepath.Join(d, "existing")) }},
	}
}

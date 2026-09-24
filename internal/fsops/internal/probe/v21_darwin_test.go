package probe

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// v21Ops は、macOS で試す不可分な操作（TestV21）。どれも既存のファイル existing を上書きしない。
func v21Ops(d string) []v21Op {
	src := func(name string) string {
		p := filepath.Join(d, name)
		os.WriteFile(p, []byte(name), 0o644)
		return p
	}
	return []v21Op{
		{"renamex_np(RENAME_EXCL, new name)", func() error { return unix.RenamexNp(src("s1"), filepath.Join(d, "n1"), unix.RENAME_EXCL) }},
		{"renamex_np(RENAME_SWAP)", func() error { return unix.RenamexNp(src("s2"), src("s3"), unix.RENAME_SWAP) }},
		{"clonefile(new name)", func() error { return unix.Clonefile(src("s4"), filepath.Join(d, "n4"), 0) }},
	}
}

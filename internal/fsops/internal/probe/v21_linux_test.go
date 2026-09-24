package probe

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// v21Ops は、Linux で試す不可分な操作（TestV21）。どれも既存のファイル existing を上書きしない。
func v21Ops(d string) []v21Op {
	src := func(name string) string {
		p := filepath.Join(d, name)
		os.WriteFile(p, []byte(name), 0o644)
		return p
	}
	return []v21Op{
		{"renameat2(RENAME_NOREPLACE, new name)", func() error {
			return unix.Renameat2(unix.AT_FDCWD, src("s1"), unix.AT_FDCWD, filepath.Join(d, "n1"), unix.RENAME_NOREPLACE)
		}},
		{"renameat2(RENAME_EXCHANGE)", func() error {
			return unix.Renameat2(unix.AT_FDCWD, src("s2"), unix.AT_FDCWD, src("s3"), unix.RENAME_EXCHANGE)
		}},
	}
}

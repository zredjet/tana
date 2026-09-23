//go:build unix

package fsops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestClassifyErrnoUnix(t *testing.T) {
	t.Parallel()
	cases := []errnoCase{
		{unix.ENOENT, KindNotFound},
		{unix.ENOTDIR, KindNotFound},
		{unix.EEXIST, KindExist},
		{unix.EACCES, KindPermission},
		{unix.EPERM, KindPermission},
		{unix.EBUSY, KindLocked},
		{unix.EROFS, KindReadOnly},
		{unix.ENOSPC, KindNoSpace},
		{unix.EDQUOT, KindNoSpace},
		{unix.ENOTEMPTY, KindNotEmpty},
		{unix.EXDEV, KindCrossDevice},
		{unix.ENAMETOOLONG, KindInvalidName},
		{unix.EILSEQ, KindInvalidName},
		{unix.ELOOP, KindSourceChanged},
		// 表にない番号
		{unix.EINVAL, KindUnknown},
		{unix.EIO, KindUnknown},
	}
	testErrnoTable(t, cases, unix.EPERM)
	testReadOnlyErrno(t, unix.EPERM)
}

// TestClassifyRealErrorsUnix は、Unix に固有の実際のエラーが期待どおりに分類されることを確かめる。
func TestClassifyRealErrorsUnix(t *testing.T) {
	t.Parallel()
	dir := tempDir(t)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		op   func() error
		want Kind
	}{
		// O_NOFOLLOW でリンクに当たると ELOOP になる（SPEC §17 の SourceChanged）。
		{"O_NOFOLLOW on a symlink", func() error {
			f, err := os.OpenFile(link, os.O_RDONLY|unix.O_NOFOLLOW, 0)
			if err == nil {
				f.Close()
			}
			return err
		}, KindSourceChanged},
		{"name too long", func() error {
			_, err := os.Lstat(filepath.Join(dir, strings.Repeat("a", 300)))
			return err
		}, KindInvalidName},
	}
	for _, tt := range tests {
		err := tt.op()
		if err == nil {
			t.Errorf("%s: err = nil, want an error of %v", tt.name, tt.want)
			continue
		}
		if got := classify(err, classifyOpts{}); got != tt.want {
			t.Errorf("%s: classify(%v) = %v, want %v", tt.name, err, got, tt.want)
		}
	}
}

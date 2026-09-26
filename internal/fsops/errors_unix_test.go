//go:build unix

package fsops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/unix"
)

func TestClassifyErrnoUnix(t *testing.T) {
	t.Parallel()
	cases := []errnoCase{
		{unix.ENOENT, KindNotFound},
		{unix.ENOTDIR, KindNotFound},
		{unix.EEXIST, KindExist},
		{unix.EACCES, KindPermission},
		{unix.EBUSY, KindLocked},
		{unix.EROFS, KindReadOnly},
		{unix.ENOSPC, KindNoSpace},
		{unix.EFBIG, KindFileTooLarge},
		{unix.EDQUOT, KindNoSpace},
		{unix.ENOTEMPTY, KindNotEmpty},
		{unix.EXDEV, KindCrossDevice},
		{unix.ENAMETOOLONG, KindInvalidName},
		{unix.EILSEQ, KindInvalidName},
		// 応答しないネットワークの場所（filer VU7）
		{unix.ENETDOWN, KindUnreachable},
		{unix.ENETUNREACH, KindUnreachable},
		{unix.ENETRESET, KindUnreachable},
		{unix.ECONNABORTED, KindUnreachable},
		{unix.ECONNRESET, KindUnreachable},
		{unix.ENOTCONN, KindUnreachable},
		{unix.ETIMEDOUT, KindUnreachable},
		{unix.EHOSTDOWN, KindUnreachable},
		{unix.EHOSTUNREACH, KindUnreachable},
		// O_NOFOLLOW 以外の ELOOP（リンクの循環など）は対応表にない（SPEC §17）
		{unix.ELOOP, KindUnknown},
		// 表にない番号
		{unix.EINVAL, KindUnknown},
		{unix.EIO, KindUnknown},
	}
	testErrnoTable(t, cases)
	testReadOnlyErrno(t, unix.EPERM)
	testErrnoWithOpts(t, unix.ELOOP, classifyOpts{noFollow: true}, KindSourceChanged)
	// リンクを作れないボリューム（Linux の vfat。V20）での EPERM は LinkUnsupported。
	// ただし、対象（リンクを作るフォルダ）が読み取り専用（macOS のロック）なら ReadOnly のまま。
	testErrnoWithOpts(t, unix.EPERM, classifyOpts{symlinkCreate: true}, KindLinkUnsupported)
	testErrnoWithOpts(t, unix.EPERM, classifyOpts{symlinkCreate: true, readOnly: func() bool { return true }}, KindReadOnly)
	// symlinkCreate は EPERM 以外の分類を変えない。
	testErrnoWithOpts(t, unix.EACCES, classifyOpts{symlinkCreate: true}, KindPermission)
	// noFollow は ELOOP 以外の分類を変えない。
	testErrnoWithOpts(t, unix.ENOENT, classifyOpts{noFollow: true}, KindNotFound)
}

// TestClassifyRealErrorsUnix は、Unix に固有の実際のエラーが期待どおりに分類されることを確かめる。
func TestClassifyRealErrorsUnix(t *testing.T) {
	t.Parallel()
	dir := testfs.TempDir(t)
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	loopA, loopB := filepath.Join(dir, "loopA"), filepath.Join(dir, "loopB")
	if err := os.Symlink(loopB, loopA); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(loopA, loopB); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		op   func() error
		o    classifyOpts
		want Kind
	}{
		// O_NOFOLLOW でリンクに当たると ELOOP になる（SPEC §17 の SourceChanged）。
		{"O_NOFOLLOW on a symlink", func() error {
			f, err := os.OpenFile(link, os.O_RDONLY|unix.O_NOFOLLOW, 0)
			if err == nil {
				f.Close()
			}
			return err
		}, classifyOpts{noFollow: true}, KindSourceChanged},
		// リンクの循環による ELOOP は SourceChanged にしない。
		{"symlink loop", func() error { _, err := os.Stat(loopA); return err }, classifyOpts{}, KindUnknown},
		{"name too long", func() error {
			_, err := os.Lstat(filepath.Join(dir, strings.Repeat("a", 300)))
			return err
		}, classifyOpts{}, KindInvalidName},
	}
	for _, tt := range tests {
		err := tt.op()
		if err == nil {
			t.Errorf("%s: err = nil, want an error of %v", tt.name, tt.want)
			continue
		}
		if got := classify(err, tt.o); got != tt.want {
			t.Errorf("%s: classify(%v) = %v, want %v", tt.name, err, got, tt.want)
		}
	}
}

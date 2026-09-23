//go:build unix

package fsops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/unix"
)

// symlinkPrivilegeErr は、シンボリックリンクを作れない場合のエラーと、その分類を返す（テストの注入用）。
// Unix には Windows の ERROR_PRIVILEGE_NOT_HELD にあたる番号がないので、リンクを作れないファイルシステム（Linux の vfat。V20）が返す EPERM を使う。
func symlinkPrivilegeErr() (error, Kind) { return unix.EPERM, KindLinkUnsupported }

// noSpaceErr は、書き込み中の容量不足のエラー（§10.3）を返す（テストの注入用）。
func noSpaceErr() error { return unix.ENOSPC }

// TestCopyPermissions は、Unix のパーミッション（0o777 の範囲）が保持され、setuid・setgid・sticky が保持されないことを確かめる（§15）。
func TestCopyPermissions(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/tree/a": testfs.File("a"), "src/tree/suid": testfs.File("s"), "src/tree/sub/b": testfs.File("b"), "dest": testfs.Dir()})
	src := filepath.Join(root, "src", "tree")
	for rel, mode := range map[string]os.FileMode{"a": 0o754, "suid": 0o755 | os.ModeSetuid, "sub": 0o750 | os.ModeSticky} {
		if err := os.Chmod(filepath.Join(src, rel), mode); err != nil {
			t.Fatal(err)
		}
	}
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{src}, DestDir: filepath.Join(root, "dest")})
	if res := execPlan(t, context.Background(), plan, ExecOptions{}); res.Status != StatusCompleted {
		t.Fatalf("result = %+v", res)
	}
	for rel, want := range map[string]os.FileMode{"a": 0o754, "suid": 0o755, "sub": os.ModeDir | 0o750, "sub/b": 0o644 &^ umask(t)} {
		fi, err := os.Lstat(filepath.Join(root, "dest", "tree", rel))
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode()&^os.ModeType | fi.Mode()&os.ModeDir; got != want {
			t.Errorf("%s: mode %v, want %v", rel, got, want)
		}
	}
}

// umask は、プロセスの umask を返す（変更せずに調べる方法がないので、0o022 と仮定せずに作ったファイルから求める）。
func umask(t *testing.T) os.FileMode {
	t.Helper()
	p := filepath.Join(testfs.TempDir(t), "probe")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return 0o666 &^ fi.Mode().Perm()
}

// TestCopyTempFilePerm は、コピー中（フックで停止）の一時ファイルの権限が 0o600 であることを確かめる（§10.1 の手順 2、§18.4「メタデータ」）。
// 最終名にした後は、コピー元の権限になる。
func TestCopyTempFilePerm(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/secret": testfs.File(strings.Repeat("s", copyBufSize+1)), "src/public": testfs.File(strings.Repeat("p", copyBufSize+1)), "dest": testfs.Dir()})
	if err := os.Chmod(filepath.Join(root, "src", "secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "src", "public"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "secret"), filepath.Join(root, "src", "public")}, DestDir: dest})
	checked := 0
	h := &testHooks{onWrite: func(dst string, written int64) error {
		for _, name := range testfs.ListNames(t, dest) {
			if strings.HasPrefix(name, ".fsops-") {
				fi, err := os.Lstat(filepath.Join(dest, name))
				if err != nil {
					t.Error(err)
				} else if fi.Mode().Perm() != 0o600 {
					t.Errorf("temporary file of %s: mode %v, want 0600", filepath.Base(dst), fi.Mode().Perm())
				}
				checked++
			}
		}
		return nil
	}}
	if res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h}); res.Status != StatusCompleted {
		t.Fatalf("result = %+v", res)
	}
	if checked == 0 {
		t.Fatal("no temporary file was seen")
	}
	for name, want := range map[string]os.FileMode{"secret": 0o600, "public": 0o644} {
		fi, err := os.Lstat(filepath.Join(dest, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v, want %v", name, fi.Mode().Perm(), want)
		}
	}
}

// crossDeviceErr は、ボリューム違いのリネームのエラー（§11.1）を返す（テストの注入用）。
func crossDeviceErr() error { return unix.EXDEV }

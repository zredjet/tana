package fsops

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/unix"
)

const quarantineName = "com.apple.quarantine"

// quarantine は、path（リンクを辿らない）の com.apple.quarantine を返す。なければ nil。
func quarantine(t *testing.T, path string) []byte {
	t.Helper()
	buf := make([]byte, 1024)
	n, err := unix.Lgetxattr(path, quarantineName, buf)
	if errors.Is(err, unix.ENOATTR) {
		return nil
	}
	if err != nil {
		t.Fatalf("getxattr %s: %v", path, err)
	}
	return buf[:n]
}

// TestCopyQuarantine は、com.apple.quarantine がファイルに保持され、exFAT・FAT32 のコピー先でも保持されることを確かめる（§15、V6）。
// フォルダとリンクには付けない。
func TestCopyQuarantine(t *testing.T) {
	t.Parallel()
	const q = "0081;6500a000;Safari;"
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/tree/dl.txt": testfs.File("downloaded"), "src/tree/plain.txt": testfs.File("plain"), "src/tree/link": testfs.Symlink("dl.txt")})
	src := filepath.Join(root, "src", "tree")
	for _, p := range []string{filepath.Join(src, "dl.txt"), src} {
		if err := unix.Lsetxattr(p, quarantineName, []byte(q), 0); err != nil {
			t.Fatal(err)
		}
	}
	check := func(t *testing.T, destDir string) {
		res := execPlan(t, context.Background(), mustPlan(t, Request{Op: OpCopy, Sources: []string{src}, DestDir: destDir}), ExecOptions{})
		if it := res.Items[0]; it.Outcome != OutcomeDone || len(it.Warnings) != 0 {
			t.Fatalf("result = %+v", it)
		}
		dest := filepath.Join(destDir, "tree")
		if got := quarantine(t, filepath.Join(dest, "dl.txt")); string(got) != q {
			t.Errorf("dl.txt: quarantine = %q, want %q", got, q)
		}
		for _, name := range []string{"plain.txt", "link", "."} {
			if got := quarantine(t, filepath.Join(dest, name)); got != nil {
				t.Errorf("%s: quarantine = %q, want none", name, got)
			}
		}
	}
	t.Run("APFS", func(t *testing.T) { check(t, testfs.TempDir(t)) })
	for _, env := range []string{testfs.ExFATEnv, testfs.FAT32Env} {
		t.Run(env, func(t *testing.T) { check(t, testfs.EnvDir(t, env)) })
	}
}

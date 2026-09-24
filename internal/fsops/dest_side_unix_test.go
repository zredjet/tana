//go:build unix

package fsops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestCopyDestNotWritable は、コピー先のフォルダに書き込み権限がないとき、KindPermission を OnDest（コピー先側）で報告し、
// コピー元の問題と取り違えさせないことを確かめる（§17）。
func TestCopyDestNotWritable(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root ignores folder permissions")
	}
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("f"), "src/d/x": testfs.File("x"), "dest": testfs.Dir()})
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt"), filepath.Join(root, "src", "d")}, DestDir: dest})
	if err := os.Chmod(dest, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dest, 0o755) })
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	for _, it := range res.Items {
		if it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindPermission || !it.Err.OnDest {
			t.Errorf("%s: %+v (%v), want Failed with KindPermission on the destination side", it.Src, it, it.Err)
		}
	}
}

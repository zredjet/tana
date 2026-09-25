package fsops

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestUnkeptMetadataWarning は、fsops が保持しないメタデータ（macOS のタグ、Linux の user.* の拡張属性、Windows の代替データストリーム）が
// コピー元のファイル・フォルダにあれば、コピーとボリュームをまたぐ移動で KindMetadata の警告にし、ないものには警告しないことを確かめる（§15）。
func TestUnkeptMetadataWarning(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/tagged.txt": testfs.File("t"), "src/plain.txt": testfs.File("p"), "src/dir/x": testfs.File("x"), "dest": testfs.Dir()})
	src := filepath.Join(root, "src")
	for _, p := range []string{filepath.Join(src, "tagged.txt"), filepath.Join(src, "dir")} {
		if !setUnkeptMetadata(t, p) {
			t.Skip("cannot set such metadata here")
		}
	}
	warned := func(it ItemResult) bool {
		return slices.ContainsFunc(it.Warnings, func(w *OpError) bool { return w.Kind == KindMetadata && !w.OnDest })
	}
	check := func(t *testing.T, res *Result) {
		t.Helper()
		for _, it := range res.Items {
			want := filepath.Base(it.Src) != "plain.txt"
			if it.Outcome != OutcomeDone || warned(it) != want {
				t.Errorf("%s: %+v (warnings %v), want Done with a KindMetadata warning: %v", it.Src, it, it.Warnings, want)
			}
		}
	}
	srcs := []string{filepath.Join(src, "tagged.txt"), filepath.Join(src, "plain.txt"), filepath.Join(src, "dir")}
	t.Run("copy", func(t *testing.T) {
		check(t, execPlan(t, context.Background(), mustPlan(t, Request{Op: OpCopy, Sources: srcs, DestDir: filepath.Join(root, "dest")}), ExecOptions{}))
	})
	t.Run("move across volumes", func(t *testing.T) {
		dest := testfs.CrossVolDir(t)
		check(t, execPlan(t, context.Background(), mustPlan(t, Request{Op: OpMove, Sources: srcs, DestDir: dest}), ExecOptions{}))
	})
}

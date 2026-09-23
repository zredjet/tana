package fsops

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// trashAvailableHere は、このビルドでごみ箱を使えるはずか（§12.3、§12.4）を返す。
func trashAvailableHere() bool {
	switch runtime.GOOS {
	case "windows":
		return true
	case "darwin":
		return cgoEnabled
	}
	return false
}

// TestNewPlanTrashPrecheck は、計画時のごみ箱の事前確認（§12.1）を確かめる。
// Linux と cgo なしの macOS では、計画時の Item.Err が KindTrashUnavailable になる（§18.4 の I5 の計画時の部分）。
func TestNewPlanTrashPrecheck(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"a.txt": testfs.File("a"), "dir/x": testfs.File("x")})
	plan := mustPlan(t, Request{Op: OpTrash, Sources: []string{filepath.Join(root, "a.txt"), filepath.Join(root, "dir")}})
	for _, it := range plan.Items() {
		if it.Method != MethodTrash || it.Dst != "" {
			t.Errorf("item = %+v, want MethodTrash and no Dst", it)
		}
		if trashAvailableHere() {
			if it.Err != nil {
				t.Errorf("%s: Item.Err = %v, want nil", it.Src, it.Err)
			}
		} else if KindOf(it.Err) != KindTrashUnavailable {
			t.Errorf("%s: Item.Err = %v, want KindTrashUnavailable (%s, cgo=%v)", it.Src, it.Err, runtime.GOOS, cgoEnabled)
		}
	}
}

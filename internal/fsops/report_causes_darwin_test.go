package fsops

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestDeleteInLockedFolder は、macOS でロック（uchg）されたフォルダの中のファイルが削除できないとき、原因は親のロックなので
// 「権限がありません」ではなく KindReadOnly と報告することを確かめる（§17。ReadOnly は macOS のロックを含む）。
func TestDeleteInLockedFolder(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"tree/locked/x.txt": testfs.File("x"), "tree/other.txt": testfs.File("o")})
	locked := filepath.Join(root, "tree", "locked")
	testfs.SetImmutable(t, locked)
	res := execDelete(t, context.Background(), nil, filepath.Join(root, "tree"))
	it := res.Items[0]
	if !slices.ContainsFunc(it.Details, func(e EntryResult) bool {
		return e.Src == filepath.Join(locked, "x.txt") && e.Err != nil && e.Err.Kind == KindReadOnly
	}) {
		t.Errorf("details = %+v, want x.txt with KindReadOnly (its folder is locked)", it.Details)
	}
}

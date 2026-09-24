//go:build !windows && !(darwin && cgo)

package fsops

import (
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestTrashSysUnavailable は、ごみ箱が使えないビルド（Linux、cgo なしの macOS）の trashSys が、何も削除せずに
// KindTrashUnavailable を返すことを確かめる（§12.4、I5）。事前確認で止まるので Execute からは呼ばれないが、最後の防御として確かめる。
func TestTrashSysUnavailable(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"f.txt": testfs.File("keep"), "d/x.txt": testfs.File("x")})
	for _, name := range []string{"f.txt", "d"} {
		p := filepath.Join(root, name)
		info, err := lstatEntry(p)
		if err != nil {
			t.Fatal(err)
		}
		trashed, err := trashSys(p, info)
		if KindOf(err) != KindTrashUnavailable || trashed != "" {
			t.Errorf("trashSys(%s) = %q, %v; want KindTrashUnavailable", name, trashed, err)
		}
	}
	wantFiles(t, root, map[string]string{"f.txt": "keep", "d/x.txt": "x"})
}

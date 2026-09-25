package fsops

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// TestReadOnlyFolderAttribute は、Windows でフォルダの読み取り専用属性を「読み取り専用」の根拠にしないことを確かめる（§17、§13.2。
// Windows はフォルダのこの属性で書き込みを妨げず、Documents などのシェルフォルダには保護の意味なしに付いている）。ファイルの属性は根拠にする。
func TestReadOnlyFolderAttribute(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"dir": testfs.Dir(), "f.txt": testfs.File("f")})
	for _, name := range []string{"dir", "f.txt"} {
		p16, _ := windows.UTF16PtrFromString(testfs.ExtendedPath(filepath.Join(root, name)))
		a, err := windows.GetFileAttributes(p16)
		if err != nil {
			t.Fatal(err)
		}
		if err := windows.SetFileAttributes(p16, a|windows.FILE_ATTRIBUTE_READONLY); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { windows.SetFileAttributes(p16, a) })
	}
	if readOnlySys(testfs.ExtendedPath(filepath.Join(root, "dir")))() {
		t.Error("a folder with the read-only attribute was treated as read-only")
	}
	if !readOnlySys(testfs.ExtendedPath(filepath.Join(root, "f.txt")))() {
		t.Error("a file with the read-only attribute was not treated as read-only")
	}
}

// cancelAfterCtx は、Err が n 回呼ばれた後からキャンセルを返す context（時間に頼らずに、処理の途中でキャンセルさせるため）。
type cancelAfterCtx struct {
	context.Context
	n atomic.Int64
}

func (c *cancelAfterCtx) Err() error {
	if c.n.Add(-1) < 0 {
		return context.Canceled
	}
	return nil
}

// TestNewPlanCanceledDuringTrashPrecheck は、最後の項目のごみ箱の事前確認（中身の大きさを数える走査）の途中でキャンセルされたら、
// NewPlan が KindCanceled のエラーを返すことを確かめる（§6。キャンセルが Item.Err に入ったまま計画が返り、Execute が
// StatusCanceled になっていた）。
func TestNewPlanCanceledDuringTrashPrecheck(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"dir/a": testfs.File("a"), "dir/b": testfs.File("b")})
	ctx := &cancelAfterCtx{Context: context.Background()}
	ctx.n.Store(1) // ループの冒頭の確認は通し、事前確認の走査の中でキャンセルされる
	plan, err := NewPlan(ctx, Request{Op: OpTrash, Sources: []string{filepath.Join(root, "dir")}})
	if KindOf(err) != KindCanceled || plan != nil {
		t.Errorf("NewPlan = %v, %v; want nil and KindCanceled", plan, err)
	}
}

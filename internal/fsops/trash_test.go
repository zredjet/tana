package fsops

import (
	"context"
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

// TestTrashUnavailable は、ごみ箱が使えないビルド（Linux、cgo なしの macOS）で、計画時の Item.Err と実行結果の両方が
// KindTrashUnavailable になり、ファイルが残ることを確かめる（§18.4 の I5、§12.3、§12.4）。CI の ubuntu と macOS の CGO_ENABLED=0 で実行する（§19）。
func TestTrashUnavailable(t *testing.T) {
	t.Parallel()
	if trashAvailableHere() {
		t.Skipf("the trash is available on %s (cgo=%v)", runtime.GOOS, cgoEnabled)
	}
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"a.txt": testfs.File("a"), "dir/x": testfs.File("x")})
	before := testfs.Take(t, root)
	plan := mustPlan(t, Request{Op: OpTrash, Sources: []string{filepath.Join(root, "a.txt"), filepath.Join(root, "dir")}})
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	for i, it := range res.Items {
		if KindOf(plan.Items()[i].Err) != KindTrashUnavailable {
			t.Errorf("%s: Item.Err = %v, want KindTrashUnavailable", it.Src, plan.Items()[i].Err)
		}
		if it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindTrashUnavailable {
			t.Errorf("%s: %+v, want Failed with KindTrashUnavailable", it.Src, it)
		}
	}
	if d := testfs.Diff(before, testfs.Take(t, root)); d != nil {
		t.Errorf("I5 violated: %q", d)
	}
}

// TestTrash は、ファイル・フォルダ・シンボリックリンクがごみ箱に入り、元の場所から消え、ごみ箱の中にあることを確かめる（§18.4「ごみ箱」）。
// リンクを含むフォルダ（Windows ではジャンクションも）をごみ箱に入れても、リンクの先の目印ファイルが残る（§18.4 の I4）。
// トップレベルのシンボリックリンクは、リンク自体だけが入る。FSOPS_TEST_TRASH=1 のときだけ実行する。
func TestTrash(t *testing.T) {
	testfs.RequireTrash(t)
	t.Parallel()
	if !trashAvailableHere() {
		t.Skipf("the trash is not available on %s (cgo=%v)", runtime.GOOS, cgoEnabled)
	}
	root := testfs.TempDir(t)
	markerTree(t, root)
	outside := filepath.Join(root, "outside")
	tree := testfs.Tree{
		"src/file.txt":               testfs.File("file"),
		"src/dir/a.txt":              testfs.File("a"),
		"src/dir/sub/b.txt":          testfs.File("b"),
		"src/links/dirlink":          testfs.DirSymlink(outside),
		"src/links/filelink":         testfs.Symlink(filepath.Join(root, "outside-file.txt")),
		"src/links/c.txt":            testfs.File("c"),
		"src/toplink":                testfs.DirSymlink(outside),
		"src/" + testfs.NameJapanese: testfs.File("ja"),
	}
	if runtime.GOOS == "windows" {
		tree["src/links/junction"] = testfs.Junction(outside)
	}
	testfs.Build(t, root, tree)
	names := []string{"file.txt", "dir", "links", "toplink", testfs.NameJapanese}
	var srcs []string
	for _, n := range names {
		srcs = append(srcs, filepath.Join(root, "src", n))
	}
	plan := mustPlan(t, Request{Op: OpTrash, Sources: srcs})
	var stages []Stage
	res := execPlan(t, context.Background(), plan, ExecOptions{Progress: func(p Progress) { stages = append(stages, p.Stage) }})
	for i, it := range res.Items {
		if it.Outcome != OutcomeDone || it.Err != nil {
			t.Errorf("%s: %+v, want Done", it.Src, it)
			continue
		}
		if testfs.Exists(t, it.Src) {
			t.Errorf("%s is still in its original place", it.Src)
		}
		checkTrashed(t, it.Src, it.TrashedPath, plan.Items()[i].Info)
	}
	checkMarkers(t, root)
	if len(stages) == 0 || stages[0] != StageTrash {
		t.Errorf("stages = %v, want StageTrash", stages)
	}
}

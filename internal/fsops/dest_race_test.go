package fsops

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// 書き込み先のフォルダを、確かめた（または作った）後にリンク（Windows ではジャンクション）へ置き換える注入（総点検の穴 4）。
// 書き込み・移動がリンクの先に入らないことを確かめる。注入の時点は 2 つ。
//   - open: 書き込み先のフォルダを開いて確かめる直前（beforeOpenDest）。開いて確かめる段階で見つけ、そのフォルダには何も書かない。
//   - enter: 開いた後、中身を処理する直前（beforeEnterDir）。Unix では、書き込みは開いたフォルダ（別名に移された元のフォルダ）に入る。
//     Windows では、開いている間はフォルダの名前を変えられないので、置き換え自体ができない。

// replaceDir は、dir を dir-moved に移し、dir に outside へのリンクを置く。移せなければ（Windows でハンドルを開いている間など）偽を返す。
func replaceDir(t *testing.T, root, dir string) bool {
	t.Helper()
	if err := os.Rename(testfs.ExtendedPath(dir), testfs.ExtendedPath(dir+"-moved")); err != nil {
		t.Logf("the folder could not be replaced (protected): %v", err)
		return false
	}
	if runtime.GOOS == "windows" {
		testfs.CreateJunction(t, filepath.Join(root, "outside"), dir)
	} else {
		testfs.CreateSymlink(t, filepath.Join(root, "outside"), dir, true)
	}
	return true
}

// destRaceHooks は、注入の時点 when で、書き込み先の dst をリンクへ置き換えるフックを返す。src はコピー元・移動元のフォルダ。
// 置き換えたかどうかを *replaced に入れる。
func destRaceHooks(t *testing.T, when, root, src, dst string, replaced *bool) *testHooks {
	done := false
	inject := func() {
		if !done {
			done = true
			*replaced = replaceDir(t, root, dst)
		}
	}
	if when == "open" {
		return &testHooks{beforeOpenDest: func(p string) {
			if p == dst {
				inject()
			}
		}}
	}
	return &testHooks{beforeEnterDir: func(p string) {
		if p == src {
			inject()
		}
	}}
}

// noWriteThroughLink は、リンクの先（outside）に何も書かれていないことを確かめる。
func noWriteThroughLink(t *testing.T, root string, before testfs.Snapshot) {
	t.Helper()
	if d := testfs.Diff(before, testfs.Take(t, filepath.Join(root, "outside"))); d != nil {
		t.Errorf("written through the link: %q", d)
	}
	checkMarkers(t, root)
}

// inOneOf は、rel のファイルが、dirs のどれか 1 か所にだけ、内容 want で存在することを確かめる（なくならず、増えもしない）。
func inOneOf(t *testing.T, rel, want string, dirs ...string) {
	t.Helper()
	n := 0
	for _, d := range dirs {
		p := filepath.Join(d, filepath.FromSlash(rel))
		if testfs.Exists(t, p) && !isLinkEntry(t, filepath.Dir(p)) {
			if got := testfs.ReadFile(t, p); got != want {
				t.Errorf("%s = %q, want %q", p, got, want)
			}
			n++
		}
	}
	if n != 1 {
		t.Errorf("%s exists in %d of %q, want exactly 1", rel, n, dirs)
	}
}

// TestCopyMergeDestReplacedAfterCheck は、コピーのマージで、マージ先を照合した後にリンクへ置き換えても、リンクの先に書かないことを確かめる。
func TestCopyMergeDestReplacedAfterCheck(t *testing.T) {
	t.Parallel()
	for _, when := range []string{"open", "enter"} {
		t.Run(when, func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			markerTree(t, root)
			testfs.Build(t, root, testfs.Tree{"src/m/a.txt": testfs.File("a"), "src/m/sub/b.txt": testfs.File("b"), "dest/m/old.txt": testfs.File("o")})
			before := testfs.Take(t, filepath.Join(root, "outside"))
			dest := filepath.Join(root, "dest")
			plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "m")}, DestDir: dest})
			decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
			var replaced bool
			res := execPlan(t, context.Background(), plan, ExecOptions{hooks: destRaceHooks(t, when, root, filepath.Join(root, "src", "m"), filepath.Join(dest, "m"), &replaced)})
			it := res.Items[0]
			if when == "open" && replaced && (it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist) {
				t.Errorf("result = %+v, want Skipped with KindExist (the merge target changed after the check)", it)
			}
			noWriteThroughLink(t, root, before)
		})
	}
}

// TestCopyCreatedDestReplacedAfterMkdir は、コピーで作ったフォルダを、作った後にリンクへ置き換えても、リンクの先に書かないことを確かめる。
func TestCopyCreatedDestReplacedAfterMkdir(t *testing.T) {
	t.Parallel()
	for _, when := range []string{"open", "enter"} {
		t.Run(when, func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			markerTree(t, root)
			testfs.Build(t, root, testfs.Tree{"src/tree/a.txt": testfs.File("a"), "src/tree/sub/b.txt": testfs.File("b"), "dest": testfs.Dir()})
			before := testfs.Take(t, filepath.Join(root, "outside"))
			dest := filepath.Join(root, "dest")
			plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "tree")}, DestDir: dest})
			var replaced bool
			res := execPlan(t, context.Background(), plan, ExecOptions{hooks: destRaceHooks(t, when, root, filepath.Join(root, "src", "tree"), filepath.Join(dest, "tree"), &replaced)})
			it := res.Items[0]
			if when == "open" && replaced && !hasKind(it, KindSourceChanged) {
				t.Errorf("result = %+v, want KindSourceChanged (the created folder was replaced)", it)
			}
			noWriteThroughLink(t, root, before)
		})
	}
}

// TestMoveMergeDestReplacedAfterCheck は、同一ボリュームのマージ移動で、マージ先を照合した後にリンクへ置き換えても、
// リンクの先へ移動せず、ファイルがなくならない（移動元か、確かめた元のマージ先のどちらかにある）ことを確かめる。
func TestMoveMergeDestReplacedAfterCheck(t *testing.T) {
	t.Parallel()
	for _, when := range []string{"open", "enter"} {
		t.Run(when, func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			markerTree(t, root)
			testfs.Build(t, root, testfs.Tree{"src/m/a.txt": testfs.File("a"), "src/m/sub/b.txt": testfs.File("b"), "dest/m/old.txt": testfs.File("o")})
			before := testfs.Take(t, filepath.Join(root, "outside"))
			dest := filepath.Join(root, "dest")
			plan := sameMove(t, root, "m")
			decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
			var replaced bool
			res := execPlan(t, context.Background(), plan, ExecOptions{hooks: destRaceHooks(t, when, root, filepath.Join(root, "src", "m"), filepath.Join(dest, "m"), &replaced)})
			it := res.Items[0]
			if when == "open" && replaced && (it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist) {
				t.Errorf("result = %+v, want Skipped with KindExist (the merge target changed after the check)", it)
			}
			noWriteThroughLink(t, root, before)
			dirs := []string{filepath.Join(root, "src", "m"), filepath.Join(dest, "m"), filepath.Join(dest, "m-moved")}
			inOneOf(t, "a.txt", "a", dirs...)
			inOneOf(t, "sub/b.txt", "b", dirs...)
		})
	}
}

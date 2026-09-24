package fsops

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// 書き込み先のフォルダを、確かめた（または作った）後・中身を書き込む前に、リンク（Windows ではジャンクション）へ置き換える注入。
// 書き込みがリンクの先に入らないことを確かめる（総点検の穴 4）。注入の時点は、コピー元・移動元のフォルダに入る直前（beforeEnterDir）。

// replaceDestOnEnter は、コピー元・移動元の src に入る直前に、書き込み先の dst をリンクへ置き換えるフックを返す。
func replaceDestOnEnter(t *testing.T, root, src, dst string) *testHooks {
	done := false
	return &testHooks{beforeEnterDir: func(p string) {
		if p == src && !done {
			done = true
			replaceWithLink(t, root, dst)
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

// TestCopyMergeDestReplacedAfterCheck は、コピーのマージで、マージ先を確かめた後にリンクへ置き換えても、リンクの先に書かないことを確かめる。
func TestCopyMergeDestReplacedAfterCheck(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	markerTree(t, root)
	testfs.Build(t, root, testfs.Tree{"src/m/a.txt": testfs.File("a"), "src/m/sub/b.txt": testfs.File("b"), "dest/m/old.txt": testfs.File("o")})
	before := testfs.Take(t, filepath.Join(root, "outside"))
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "m")}, DestDir: dest})
	decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: replaceDestOnEnter(t, root, filepath.Join(root, "src", "m"), filepath.Join(dest, "m"))})
	t.Logf("result: %+v", res.Items[0])
	noWriteThroughLink(t, root, before)
}

// TestCopyCreatedDestReplacedAfterMkdir は、コピーで作ったフォルダを、作った後にリンクへ置き換えても、リンクの先に書かないことを確かめる。
func TestCopyCreatedDestReplacedAfterMkdir(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	markerTree(t, root)
	testfs.Build(t, root, testfs.Tree{"src/tree/a.txt": testfs.File("a"), "src/tree/sub/b.txt": testfs.File("b"), "dest": testfs.Dir()})
	before := testfs.Take(t, filepath.Join(root, "outside"))
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "tree")}, DestDir: dest})
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: replaceDestOnEnter(t, root, filepath.Join(root, "src", "tree"), filepath.Join(dest, "tree"))})
	t.Logf("result: %+v", res.Items[0])
	noWriteThroughLink(t, root, before)
}

// TestMoveMergeDestReplacedAfterCheck は、同一ボリュームのマージ移動で、マージ先を確かめた後にリンクへ置き換えても、
// リンクの先へ移動しないことを確かめる。
func TestMoveMergeDestReplacedAfterCheck(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	markerTree(t, root)
	testfs.Build(t, root, testfs.Tree{"src/m/a.txt": testfs.File("a"), "src/m/sub/b.txt": testfs.File("b"), "dest/m/old.txt": testfs.File("o")})
	before := testfs.Take(t, filepath.Join(root, "outside"))
	dest := filepath.Join(root, "dest")
	plan := sameMove(t, root, "m")
	decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: replaceDestOnEnter(t, root, filepath.Join(root, "src", "m"), filepath.Join(dest, "m"))})
	t.Logf("result: %+v", res.Items[0])
	noWriteThroughLink(t, root, before)
	wantFiles(t, root, map[string]string{"src/m/a.txt": "a", "src/m/sub/b.txt": "b"})
}

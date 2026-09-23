package fsops

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// sameMove は、同一ボリュームの移動の計画を作る（src と dest は同じ TempDir の中）。
func sameMove(t *testing.T, root string, names ...string) *Plan {
	t.Helper()
	var srcs []string
	for _, n := range names {
		srcs = append(srcs, filepath.Join(root, "src", n))
	}
	plan := mustPlan(t, Request{Op: OpMove, Sources: srcs, DestDir: filepath.Join(root, "dest")})
	for _, it := range plan.Items() {
		if it.Method != MethodRename || it.Err != nil {
			t.Fatalf("item %s: method %v, err %v; want MethodRename", it.Src, it.Method, it.Err)
		}
	}
	return plan
}

// TestMoveSameVolume は、同一ボリュームの移動（§11.1）がリネームで行われる（fileID が変わらない）ことを確かめる。
func TestMoveSameVolume(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	moveTree(t, root)
	testfs.MkdirAll(t, filepath.Join(root, "dest"))
	ids := map[string]fileID{}
	for _, n := range []string{"tree", "top.txt", "tree/sub/b.txt"} {
		st, err := fileIDOf(filepath.Join(root, "src", filepath.FromSlash(n)))
		if err != nil {
			t.Fatal(err)
		}
		ids[n] = st.id
	}
	var stages []Stage
	res := execPlan(t, context.Background(), sameMove(t, root, "tree", "top.txt"), ExecOptions{Progress: func(p Progress) { stages = append(stages, p.Stage) }})
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone || it.Err != nil {
			t.Errorf("%s: %+v, want Done", it.Src, it)
		}
	}
	for n, want := range ids {
		st, err := fileIDOf(filepath.Join(root, "dest", filepath.FromSlash(n)))
		if err != nil || st.id != want {
			t.Errorf("%s: fileID changed or missing (%v): the move was not a rename", n, err)
		}
	}
	if names := testfs.ListNames(t, filepath.Join(root, "src")); len(names) != 0 {
		t.Errorf("left in the source: %+q", names)
	}
	if !slices.Contains(stages, StageMove) {
		t.Errorf("stages = %v, want StageMove", stages)
	}
}

// TestMoveConflicts は、同一ボリュームの移動での衝突の決定（上書き・自動リネーム・Skip・未設定）と、
// 計画後に現れた衝突（I1）を確かめる（§11.1、§9）。
func TestMoveConflicts(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/over.txt": testfs.File("new over"), "dest/over.txt": testfs.File("old over"),
		"src/ren.txt": testfs.File("new ren"), "dest/ren.txt": testfs.File("old ren"),
		"src/skip.txt": testfs.File("new skip"), "dest/skip.txt": testfs.File("old skip"),
		"src/unset.txt": testfs.File("new unset"), "dest/unset.txt": testfs.File("old unset"),
		"src/dir/x": testfs.File("x"), "dest/dir/y": testfs.File("y"),
		"src/appear.txt": testfs.File("mine"), "src/appeardir/z": testfs.File("z"),
	})
	dest := filepath.Join(root, "dest")
	plan := sameMove(t, root, "over.txt", "ren.txt", "skip.txt", "unset.txt", "dir", "appear.txt", "appeardir")
	decide(t, plan, filepath.Join(dest, "over.txt"), DecisionOverwrite)
	decide(t, plan, filepath.Join(dest, "ren.txt"), DecisionAutoRename)
	decide(t, plan, filepath.Join(dest, "skip.txt"), DecisionSkip)
	decide(t, plan, filepath.Join(dest, "dir"), DecisionAutoRename)
	testfs.Build(t, root, testfs.Tree{"dest/appear.txt": testfs.File("appeared"), "dest/appeardir/w": testfs.File("w")})
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	want := []struct {
		out  Outcome
		kind Kind // KindUnknown なら Err なし
		dst  string
	}{
		{OutcomeDone, KindUnknown, "over.txt"},
		{OutcomeDone, KindUnknown, "ren (2).txt"},
		{OutcomeSkipped, KindUnknown, "skip.txt"},
		{OutcomeSkipped, KindUnknown, "unset.txt"},
		{OutcomeDone, KindUnknown, "dir (2)"},
		{OutcomeSkipped, KindExist, "appear.txt"},
		{OutcomeSkipped, KindExist, "appeardir"},
	}
	for i, w := range want {
		it := res.Items[i]
		if it.Outcome != w.out || (w.kind == KindUnknown) != (it.Err == nil) || (it.Err != nil && it.Err.Kind != w.kind) || it.Dst != filepath.Join(dest, w.dst) {
			t.Errorf("%s: %+v, want %v %v at %s", it.Src, it, w.out, w.kind, w.dst)
		}
	}
	wantFiles(t, dest, map[string]string{
		"over.txt": "new over", "ren.txt": "old ren", "ren (2).txt": "new ren", "skip.txt": "old skip", "unset.txt": "old unset",
		"dir/y": "y", "dir (2)/x": "x", "appear.txt": "appeared", "appeardir/w": "w",
	})
	if got := testfs.ListNames(t, filepath.Join(root, "src")); !slices.Equal(got, []string{"appear.txt", "appeardir", "skip.txt", "unset.txt"}) {
		t.Errorf("left in the source: %+q", got)
	}
}

// TestMoveOverwriteReadOnly は、同一ボリュームの移動で読み取り専用の上書き先が KindReadOnly になり、両方とも元のまま残ることを確かめる（§11.1、§9.3）。
func TestMoveOverwriteReadOnly(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/ro.txt": testfs.File("new"), "dest/ro.txt": testfs.File("old").RO()})
	plan := sameMove(t, root, "ro.txt")
	decide(t, plan, filepath.Join(root, "dest", "ro.txt"), DecisionOverwrite)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindReadOnly {
		t.Errorf("result = %+v, want Failed with KindReadOnly", it)
	}
	wantFiles(t, root, map[string]string{"src/ro.txt": "new", "dest/ro.txt": "old"})
}

// TestMoveMerge は、同一ボリュームのマージ移動（§11.1）で、内側の衝突の決定がそれぞれ反映され、空になった移動元のフォルダが消え、
// 衝突の決定による Skip で残ったフォルダは残って Done になることを確かめる（§7.4）。
func TestMoveMerge(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/m/new.txt":  testfs.File("new"),
		"src/m/over.txt": testfs.File("new over"), "dest/m/over.txt": testfs.File("old over"),
		"src/m/skip.txt": testfs.File("new skip"), "dest/m/skip.txt": testfs.File("old skip"),
		"src/m/ren.txt": testfs.File("new ren"), "dest/m/ren.txt": testfs.File("old ren"),
		"src/m/sub/inner.txt": testfs.File("new inner"), "dest/m/sub/inner.txt": testfs.File("old inner"),
		"src/m/sub/deep/d.txt": testfs.File("d"),
		"src/m/gone/g.txt":     testfs.File("g"), "dest/m/gone/h.txt": testfs.File("h"),
		"dest/m/keep.txt": testfs.File("keep"),
	})
	dest := filepath.Join(root, "dest")
	plan := sameMove(t, root, "m")
	for rel, d := range map[string]Decision{
		"m": DecisionMerge, "m/over.txt": DecisionOverwrite, "m/skip.txt": DecisionSkip, "m/ren.txt": DecisionAutoRename,
		"m/sub": DecisionMerge, "m/sub/inner.txt": DecisionOverwrite, "m/gone": DecisionMerge,
	} {
		decide(t, plan, filepath.Join(dest, filepath.FromSlash(rel)), d)
	}
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	it := res.Items[0]
	if it.Outcome != OutcomeDone || it.Err != nil || res.Status != StatusCompleted {
		t.Errorf("result = %+v, want Done", it)
	}
	if len(it.Details) != 1 || filepath.Base(it.Details[0].Src) != "skip.txt" || it.Details[0].Outcome != OutcomeSkipped || it.Details[0].Err != nil {
		t.Errorf("details = %+v, want only skip.txt skipped by decision", it.Details)
	}
	wantFiles(t, dest, map[string]string{
		"m/new.txt": "new", "m/over.txt": "new over", "m/skip.txt": "old skip", "m/ren.txt": "old ren", "m/ren (2).txt": "new ren",
		"m/sub/inner.txt": "new inner", "m/sub/deep/d.txt": "d", "m/gone/g.txt": "g", "m/gone/h.txt": "h", "m/keep.txt": "keep",
	})
	if got := testfs.ListNames(t, filepath.Join(root, "src", "m")); !slices.Equal(got, []string{"skip.txt"}) {
		t.Errorf("left in src/m: %+q, want [skip.txt]", got)
	}
	wantFiles(t, root, map[string]string{"src/m/skip.txt": "new skip"})
}

// TestMoveMergeDirReplacedByLink は、同一ボリュームのマージ移動の走査で、フォルダと判定した後・入り込む前にリンクへ置き換えても（フックで注入）、
// リンクの先の目印ファイルが残り、移動もされないことを確かめる（§18.4 の I4、§13.1）。
func TestMoveMergeDirReplacedByLink(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	markerTree(t, root)
	testfs.Build(t, root, testfs.Tree{
		"src/m/a.txt": testfs.File("a"), "src/m/sub/b.txt": testfs.File("b"), "dest/m/sub/c.txt": testfs.File("c"),
		"src/top/x.txt": testfs.File("x"), "dest/top/y.txt": testfs.File("y"),
	})
	dest := filepath.Join(root, "dest")
	plan := sameMove(t, root, "m", "top")
	decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
	decide(t, plan, filepath.Join(dest, "m", "sub"), DecisionMerge)
	decide(t, plan, filepath.Join(dest, "top"), DecisionMerge)
	victims := map[string]bool{filepath.Join(root, "src", "m", "sub"): true, filepath.Join(root, "src", "top"): true}
	h := &testHooks{beforeEnterDir: func(p string) {
		if victims[p] {
			delete(victims, p)
			replaceWithLink(t, root, p)
		}
	}}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomePartial || !hasKind(it, KindSourceChanged) {
		t.Errorf("m = %+v, want Partial with sub failed by KindSourceChanged", it)
	}
	if it := res.Items[1]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindSourceChanged {
		t.Errorf("top = %+v, want Failed with KindSourceChanged", it)
	}
	checkMarkers(t, root)
	for _, rel := range []string{"m/sub/marker.txt", "m/sub/sub", "top/marker.txt"} {
		if testfs.Exists(t, filepath.Join(dest, filepath.FromSlash(rel))) {
			t.Errorf("%s was moved through the link", rel)
		}
	}
	wantFiles(t, dest, map[string]string{"m/a.txt": "a", "m/sub/c.txt": "c", "top/y.txt": "y"})
}

// TestMoveMergeLinks は、同一ボリュームのマージ移動で、中のリンクがリンクとして移動され、リンクの先の目印ファイルが残ることを確かめる（§18.4 の I4、§14.2）。
func TestMoveMergeLinks(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	markerTree(t, root)
	outside := filepath.Join(root, "outside")
	tree := testfs.Tree{"src/m/dirlink": testfs.DirSymlink(outside), "src/m/filelink": testfs.Symlink(filepath.Join(root, "outside-file.txt")), "dest/m/old": testfs.File("o")}
	testfs.Build(t, root, tree)
	plan := sameMove(t, root, "m")
	decide(t, plan, filepath.Join(root, "dest", "m"), DecisionMerge)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeDone {
		t.Errorf("result = %+v, want Done", it)
	}
	for _, name := range []string{"dirlink", "filelink"} {
		if !isLinkEntry(t, filepath.Join(root, "dest", "m", name)) {
			t.Errorf("%s was not moved as a link", name)
		}
	}
	if testfs.Exists(t, filepath.Join(root, "src", "m")) {
		t.Error("the emptied source folder was not removed")
	}
	checkMarkers(t, root)
}

// TestMoveMergeCancel は、同一ボリュームのマージ移動の途中でキャンセルすると Partial（KindCanceled）になり、移動元のフォルダが残ることを確かめる（§16）。
func TestMoveMergeCancel(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	tree := testfs.Tree{"dest/m/old": testfs.File("o")}
	for i := range 6 {
		tree["src/m/f"+string(rune('a'+i))] = testfs.File("x")
	}
	testfs.Build(t, root, tree)
	plan := sameMove(t, root, "m")
	decide(t, plan, filepath.Join(root, "dest", "m"), DecisionMerge)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	h := &testHooks{beforeMoveRename: func(string, string) error {
		if n++; n == 3 {
			cancel()
		}
		return nil
	}}
	res := execPlan(t, ctx, plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomePartial || it.Err == nil || it.Err.Kind != KindCanceled {
		t.Errorf("result = %+v, want Partial with KindCanceled", it)
	}
	left := testfs.ListNames(t, filepath.Join(root, "src", "m"))
	moved := testfs.ListNames(t, filepath.Join(root, "dest", "m"))
	if len(left)+len(moved) != 7 || len(left) == 0 || len(moved) == 1 {
		t.Errorf("left %+q, moved %+q: want a partial move without losing anything", left, moved)
	}
}

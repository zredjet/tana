package fsops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// moveTree は、移動元のツリー（src/tree と src/top.txt）を作る。
func moveTree(t *testing.T, root string) {
	t.Helper()
	testfs.Build(t, root, testfs.Tree{
		"src/tree/a.txt":     testfs.File("a"),
		"src/tree/sub/b.txt": testfs.File(strings.Repeat("b", copyBufSize+5)),
		"src/tree/sub/c.txt": testfs.File("c"),
		"src/tree/empty":     testfs.Dir(),
		"src/top.txt":        testfs.File("top"),
	})
}

// crossMove は、ボリュームをまたぐ移動の計画を作る（src は TempDir、移動先は FSOPS_CROSSVOL_DIR）。
func crossMove(t *testing.T, root, dest string, names ...string) *Plan {
	t.Helper()
	var srcs []string
	for _, n := range names {
		srcs = append(srcs, filepath.Join(root, "src", n))
	}
	plan := mustPlan(t, Request{Op: OpMove, Sources: srcs, DestDir: dest})
	for _, it := range plan.Items() {
		if it.Method != MethodCopyThenRemove || it.Err != nil {
			t.Fatalf("item %s: method %v, err %v; want MethodCopyThenRemove", it.Src, it.Method, it.Err)
		}
	}
	return plan
}

// checkUnchanged は、移動元が完全に残っていることを確かめる（I2）。
func checkUnchanged(t *testing.T, before testfs.Snapshot, dir string) {
	t.Helper()
	if d := testfs.Diff(before, testfs.Take(t, dir)); d != nil {
		t.Errorf("I2 violated: the source changed: %q", d)
	}
}

// TestMoveCrossVolume は、ボリュームをまたぐ移動（§11.2）で、移動先が完全にでき、移動元が消えることを確かめる。
func TestMoveCrossVolume(t *testing.T) {
	t.Parallel()
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	moveTree(t, root)
	want := testfs.Take(t, filepath.Join(root, "src", "tree"))
	plan := crossMove(t, root, dest, "tree", "top.txt")
	var stages []Stage
	res := execPlan(t, context.Background(), plan, ExecOptions{Progress: func(p Progress) { stages = append(stages, p.Stage) }})
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone || it.Err != nil || len(it.Details) != 0 {
			t.Errorf("%s: %+v, want Done", it.Src, it)
		}
	}
	if names := testfs.ListNames(t, filepath.Join(root, "src")); len(names) != 0 {
		t.Errorf("left in the source: %+q", names)
	}
	got := testfs.Take(t, filepath.Join(dest, "tree"))
	for rel, n := range want {
		m := got[rel]
		if m.Type != n.Type || m.SHA256 != n.SHA256 || (n.Type == "file" && m.ModTime != n.ModTime) {
			t.Errorf("%s: %+v, want %+v", rel, m, n)
		}
	}
	wantFiles(t, dest, map[string]string{"top.txt": "top"})
	if !slices.Contains(stages, StageCopy) || !slices.Contains(stages, StageRemoveSource) {
		t.Errorf("stages = %v, want StageCopy and StageRemoveSource", stages)
	}
	noTempFiles(t, dest)
}

// TestMoveCrossVolumeFault は、ボリュームをまたぐ移動の途中で障害を注入すると、その項目の移動元が完全に残り、
// ほかの項目は移動されることを確かめる（§18.4 の I2）。
func TestMoveCrossVolumeFault(t *testing.T) {
	t.Parallel()
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	moveTree(t, root)
	before := testfs.Take(t, filepath.Join(root, "src", "tree"))
	plan := crossMove(t, root, dest, "tree", "top.txt")
	injected := errors.New("injected")
	h := &testHooks{onWrite: func(dst string, written int64) error {
		if filepath.Base(dst) == "c.txt" {
			return injected
		}
		return nil
	}}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomePartial || it.Err == nil || !errors.Is(it.Err, injected) {
		t.Errorf("tree = %+v, want Partial with the injected error", it)
	}
	if it := res.Items[1]; it.Outcome != OutcomeDone {
		t.Errorf("top.txt = %+v, want Done", it)
	}
	checkUnchanged(t, before, filepath.Join(root, "src", "tree"))
	noTempFiles(t, dest)
}

// TestMoveCrossVolumeCancel は、ボリュームをまたぐ移動の途中でキャンセルすると、移動元が完全に残ることを確かめる（§18.4 の I2）。
func TestMoveCrossVolumeCancel(t *testing.T) {
	t.Parallel()
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	moveTree(t, root)
	before := testfs.Take(t, filepath.Join(root, "src"))
	plan := crossMove(t, root, dest, "tree", "top.txt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &testHooks{onWrite: func(dst string, written int64) error {
		if filepath.Base(dst) == "b.txt" {
			cancel()
		}
		return nil
	}}
	res := execPlan(t, ctx, plan, ExecOptions{hooks: h})
	if res.Status != StatusCanceled {
		t.Errorf("Status = %v", res.Status)
	}
	if it := res.Items[0]; it.Outcome != OutcomePartial || it.Err == nil || it.Err.Kind != KindCanceled {
		t.Errorf("tree = %+v, want Partial with KindCanceled", it)
	}
	if it := res.Items[1]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindCanceled {
		t.Errorf("top.txt = %+v, want Skipped with KindCanceled", it)
	}
	checkUnchanged(t, before, filepath.Join(root, "src"))
	noTempFiles(t, dest)
}

// TestMoveCrossVolumeAddedFile は、コピーの後・移動元の削除の前に移動元へ追加したファイルが消えず、
// そのファイル自体が Details で報告されることを確かめる（§18.4 の I2、§11.2 の手順 4）。
func TestMoveCrossVolumeAddedFile(t *testing.T) {
	t.Parallel()
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	moveTree(t, root)
	added := filepath.Join(root, "src", "tree", "sub", "added.txt")
	plan := crossMove(t, root, dest, "tree")
	h := &testHooks{beforeRemoveSource: func(string) { testfs.WriteFile(t, added, "added during the move") }}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	it := res.Items[0]
	if it.Outcome != OutcomeCopiedSourceKept || !keptInDetails(it, added, KindSourceChanged) {
		t.Errorf("result = %+v, want CopiedSourceKept with added.txt kept by KindSourceChanged (it is not in the destination)", it)
	}
	if got := testfs.ReadFile(t, added); got != "added during the move" {
		t.Errorf("I2 violated: added.txt = %q", got)
	}
	if got := testfs.ListNames(t, filepath.Join(root, "src", "tree")); !slices.Equal(got, []string{"sub"}) {
		t.Errorf("left in src/tree: %+q, want [sub]", got)
	}
	wantFiles(t, filepath.Join(dest, "tree"), map[string]string{"a.txt": "a", "sub/c.txt": "c"})
}

// keptInDetails は、Details に path を kind で移動元に残したエントリがあるかを返す。
func keptInDetails(it ItemResult, path string, kind Kind) bool {
	return slices.ContainsFunc(it.Details, func(e EntryResult) bool {
		return e.Src == path && e.Outcome == OutcomeCopiedSourceKept && e.Err != nil && e.Err.Kind == kind
	})
}

// TestMoveCrossVolumeEditedFile は、コピーの後・移動元の削除の前に書き換えた移動元のファイルが消えず、
// OutcomeCopiedSourceKept になることを確かめる（§18.4 の I2）。
func TestMoveCrossVolumeEditedFile(t *testing.T) {
	t.Parallel()
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	moveTree(t, root)
	edited := filepath.Join(root, "src", "tree", "a.txt")
	plan := crossMove(t, root, dest, "tree", "top.txt")
	h := &testHooks{beforeRemoveSource: func(src string) {
		if filepath.Base(src) == "tree" {
			testfs.WriteFile(t, edited, "edited after the copy")
		}
	}}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomeCopiedSourceKept || it.Err == nil || !keptInDetails(it, edited, KindSourceChanged) {
		t.Errorf("tree = %+v, want CopiedSourceKept with a.txt kept by KindSourceChanged", it)
	}
	if got := testfs.ReadFile(t, edited); got != "edited after the copy" {
		t.Errorf("I2 violated: a.txt = %q", got)
	}
	if testfs.Exists(t, filepath.Join(root, "src", "tree", "sub")) {
		t.Error("the unchanged sub folder was not removed")
	}
	if it := res.Items[1]; it.Outcome != OutcomeDone {
		t.Errorf("top.txt = %+v", it)
	}
	if res.Status != StatusCompletedWithErrors {
		t.Errorf("Status = %v", res.Status)
	}
}

// TestMoveCrossVolumeMergeSkip は、ボリュームをまたぐマージ移動で内側の衝突を Skip に決めると、スキップしたものだけが移動元に残り、
// ほかは移動され、結果は Done であることを確かめる（§18.4 の I2、§7.4）。
func TestMoveCrossVolumeMergeSkip(t *testing.T) {
	t.Parallel()
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	moveTree(t, root)
	testfs.Build(t, root, testfs.Tree{"src/tree/skip.txt": testfs.File("new skip"), "src/tree/sub/unset.txt": testfs.File("new unset")})
	testfs.Build(t, dest, testfs.Tree{"tree/skip.txt": testfs.File("old skip"), "tree/sub/unset.txt": testfs.File("old unset"), "tree/keep": testfs.File("keep")})
	plan := crossMove(t, root, dest, "tree")
	decide(t, plan, filepath.Join(dest, "tree"), DecisionMerge)
	decide(t, plan, filepath.Join(dest, "tree", "sub"), DecisionMerge)
	decide(t, plan, filepath.Join(dest, "tree", "skip.txt"), DecisionSkip)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	it := res.Items[0]
	if it.Outcome != OutcomeDone || it.Err != nil || res.Status != StatusCompleted {
		t.Errorf("result = %+v, want Done", it)
	}
	for _, d := range it.Details {
		if d.Outcome != OutcomeSkipped || d.Err != nil {
			t.Errorf("detail %+v, want only skips by decision", d)
		}
	}
	src := filepath.Join(root, "src", "tree")
	if got := testfs.ListNames(t, src); !slices.Equal(got, []string{"skip.txt", "sub"}) {
		t.Errorf("left in src/tree: %+q, want [skip.txt sub]", got)
	}
	if got := testfs.ListNames(t, filepath.Join(src, "sub")); !slices.Equal(got, []string{"unset.txt"}) {
		t.Errorf("left in src/tree/sub: %+q, want [unset.txt]", got)
	}
	wantFiles(t, root, map[string]string{"src/tree/skip.txt": "new skip", "src/tree/sub/unset.txt": "new unset"})
	wantFiles(t, filepath.Join(dest, "tree"), map[string]string{
		"skip.txt": "old skip", "sub/unset.txt": "old unset", "keep": "keep", "a.txt": "a", "sub/c.txt": "c",
	})
}

// TestMoveCrossVolumeLocked は、移動元の削除に失敗（ロック）すると OutcomeCopiedSourceKept になり、移動先は完全であることを確かめる（§18.4 の I2。Windows）。
func TestMoveCrossVolumeLocked(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("file locking by share mode is only available on Windows")
	}
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	moveTree(t, root)
	locked := filepath.Join(root, "src", "tree", "sub", "c.txt")
	plan := crossMove(t, root, dest, "tree")
	var res *Result
	t.Run("locked", func(t *testing.T) {
		h := &testHooks{beforeRemoveSource: func(string) { testfs.Lock(t, locked) }}
		res = execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	})
	if res == nil {
		t.Fatal("no result")
	}
	it := res.Items[0]
	if it.Outcome != OutcomeCopiedSourceKept || !keptInDetails(it, locked, KindLocked) {
		t.Errorf("result = %+v, want CopiedSourceKept with c.txt kept by KindLocked", it)
	}
	wantFiles(t, root, map[string]string{"src/tree/sub/c.txt": "c"})
	wantFiles(t, filepath.Join(dest, "tree"), map[string]string{"a.txt": "a", "sub/c.txt": "c", "sub/b.txt": strings.Repeat("b", copyBufSize+5)})
}

// TestMoveCrossVolumeCancelRemoval は、移動元の削除中にキャンセルすると OutcomeCopiedSourceKept（KindCanceled）になり、
// 移動先は完全で、残りの項目は Skipped になることを確かめる（§18.4 の I2、§16）。
func TestMoveCrossVolumeCancelRemoval(t *testing.T) {
	t.Parallel()
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	moveTree(t, root)
	plan := crossMove(t, root, dest, "tree", "top.txt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	h := &testHooks{beforeRemove: func(string) {
		if n++; n == 2 {
			cancel()
		}
	}}
	res := execPlan(t, ctx, plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomeCopiedSourceKept || it.Err == nil || it.Err.Kind != KindCanceled {
		t.Errorf("tree = %+v, want CopiedSourceKept with KindCanceled", it)
	}
	if it := res.Items[1]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindCanceled {
		t.Errorf("top.txt = %+v, want Skipped with KindCanceled", it)
	}
	if res.Status != StatusCanceled {
		t.Errorf("Status = %v", res.Status)
	}
	wantFiles(t, filepath.Join(dest, "tree"), map[string]string{"a.txt": "a", "sub/c.txt": "c", "sub/b.txt": strings.Repeat("b", copyBufSize+5)})
	wantFiles(t, root, map[string]string{"src/top.txt": "top"})
}

// TestMoveCrossVolumeReadOnly は、読み取り専用のファイルをボリュームをまたいで移動すると、移動元が消え、移動先で読み取り専用が保持されることを確かめる（§18.4 の I2）。
func TestMoveCrossVolumeReadOnly(t *testing.T) {
	t.Parallel()
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/ro.txt": testfs.File("ro").RO(), "src/tree/ro2.txt": testfs.File("ro2").RO()})
	plan := crossMove(t, root, dest, "ro.txt", "tree")
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone {
			t.Errorf("%s: %+v, want Done", it.Src, it)
		}
	}
	if names := testfs.ListNames(t, filepath.Join(root, "src")); len(names) != 0 {
		t.Errorf("left in the source: %+q", names)
	}
	for _, rel := range []string{"ro.txt", "tree/ro2.txt"} {
		if !readOnly(t, filepath.Join(dest, filepath.FromSlash(rel))) {
			t.Errorf("%s is not read-only in the destination", rel)
		}
	}
}

// TestMoveCrossVolumeLinks は、リンクを含むツリーをボリュームをまたいで移動しても、リンクの先の目印ファイルが残ることを確かめる（§18.4 の I4）。
// シンボリックリンクはリンクとして移動され、移動元から消える。ジャンクション（Windows）・FIFO（Unix）は複製しないので、その項目の移動元には一切手を付けない（§14.2、§11.2）。
func TestMoveCrossVolumeLinks(t *testing.T) {
	t.Parallel()
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	markerTree(t, root)
	outside := filepath.Join(root, "outside")
	testfs.Build(t, root, testfs.Tree{
		"src/links/a.txt":    testfs.File("a"),
		"src/links/dirlink":  testfs.DirSymlink(outside),
		"src/links/filelink": testfs.Symlink(filepath.Join(root, "outside-file.txt")),
	})
	special := testfs.FIFO()
	if runtime.GOOS == "windows" {
		special = testfs.Junction(outside)
	}
	testfs.Build(t, root, testfs.Tree{"src/special/a.txt": testfs.File("a"), "src/special/x": special})
	before := testfs.Take(t, filepath.Join(root, "src", "special"))
	plan := crossMove(t, root, dest, "links", "special")
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeDone {
		t.Errorf("links = %+v, want Done", it)
	}
	if testfs.Exists(t, filepath.Join(root, "src", "links")) {
		t.Error("the moved folder is left in the source")
	}
	for _, name := range []string{"dirlink", "filelink"} {
		if !isLinkEntry(t, filepath.Join(dest, "links", name)) {
			t.Errorf("%s was not moved as a link", name)
		}
	}
	if it := res.Items[1]; it.Outcome != OutcomePartial || !hasKind(it, KindLinkUnsupported) && !hasKind(it, KindUnsupportedType) {
		t.Errorf("special = %+v, want Partial with the entry skipped", it)
	}
	checkUnchanged(t, before, filepath.Join(root, "src", "special"))
	checkMarkers(t, root)
}

// TestMoveRenameFallback は、同一ボリュームの移動がボリューム違いのエラーで失敗したら（フックで注入）、§11.2 の方式でやり直すことを確かめる（§11.1）。
func TestMoveRenameFallback(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	moveTree(t, root)
	testfs.MkdirAll(t, filepath.Join(root, "dest"))
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(root, "src", "tree"), filepath.Join(root, "src", "top.txt")}, DestDir: dest})
	for _, it := range plan.Items() {
		if it.Method != MethodRename {
			t.Fatalf("%s: method %v, want MethodRename", it.Src, it.Method)
		}
	}
	h := &testHooks{beforeMoveRename: func(src, dst string) error {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: crossDeviceErr()}
	}}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	for i, it := range res.Items {
		if it.Outcome != OutcomeDone {
			t.Errorf("%s: %+v, want Done", it.Src, it)
		}
		// 計画は同一ボリュームの移動だが、実際に使った方式を返す（§7.4 の Partial の意味が変わるため）。
		if plan.Items()[i].Method != MethodRename || it.Method != MethodCopyThenRemove {
			t.Errorf("%s: planned %v, reported %v; want planned MethodRename and reported MethodCopyThenRemove", it.Src, plan.Items()[i].Method, it.Method)
		}
	}
	if names := testfs.ListNames(t, filepath.Join(root, "src")); len(names) != 0 {
		t.Errorf("left in the source: %+q", names)
	}
	wantFiles(t, dest, map[string]string{"tree/a.txt": "a", "tree/sub/c.txt": "c", "top.txt": "top"})
}

// TestMoveOtherVolumes は、exFAT・FAT32 へのボリュームをまたぐ移動（§11.2）で、移動先が完全にでき、移動元が消え、
// 衝突の決定による Skip で残したものだけが移動元に残ることを確かめる。
func TestMoveOtherVolumes(t *testing.T) {
	t.Parallel()
	for _, env := range []string{testfs.ExFATEnv, testfs.FAT32Env} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			dest := testfs.EnvDir(t, env)
			root := testfs.TempDir(t)
			moveTree(t, root)
			testfs.Build(t, root, testfs.Tree{"src/tree/skip.txt": testfs.File("new")})
			testfs.Build(t, dest, testfs.Tree{"tree/skip.txt": testfs.File("old")})
			plan := crossMove(t, root, dest, "tree", "top.txt")
			decide(t, plan, filepath.Join(dest, "tree"), DecisionMerge)
			res := execPlan(t, context.Background(), plan, ExecOptions{})
			for _, it := range res.Items {
				if it.Outcome != OutcomeDone {
					t.Errorf("%s: %+v (%v), want Done", it.Src, it, it.Err)
				}
			}
			if got := testfs.ListNames(t, filepath.Join(root, "src")); !slices.Equal(got, []string{"tree"}) {
				t.Errorf("left in the source: %+q", got)
			}
			if got := testfs.ListNames(t, filepath.Join(root, "src", "tree")); !slices.Equal(got, []string{"skip.txt"}) {
				t.Errorf("left in src/tree: %+q", got)
			}
			wantFiles(t, dest, map[string]string{"tree/a.txt": "a", "tree/sub/c.txt": "c", "tree/skip.txt": "old", "top.txt": "top"})
			noTempFiles(t, dest)
		})
	}
}

// TestMoveCrossVolumeDetailsOrder は、ボリュームをまたぐ移動の Details が、コピーの結果と移動元の削除の結果を合わせて名前順になることを確かめる（§5）。
func TestMoveCrossVolumeDetailsOrder(t *testing.T) {
	t.Parallel()
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/tree/a.txt": testfs.File("a"), "src/tree/m-x.txt": testfs.File("m"), "src/tree/sub/b.txt": testfs.File("b"), "src/tree/z.txt": testfs.File("new z")})
	testfs.Build(t, dest, testfs.Tree{"tree/z.txt": testfs.File("old z"), "tree/m-x.txt": testfs.File("old m")})
	plan := crossMove(t, root, dest, "tree")
	decide(t, plan, filepath.Join(dest, "tree"), DecisionMerge)
	decide(t, plan, filepath.Join(dest, "tree", "z.txt"), DecisionSkip)
	decide(t, plan, filepath.Join(dest, "tree", "m-x.txt"), DecisionSkip)
	h := &testHooks{beforeRemoveSource: func(string) {
		testfs.WriteFile(t, filepath.Join(root, "src", "tree", "a.txt"), "edited")
		testfs.WriteFile(t, filepath.Join(root, "src", "tree", "sub", "b.txt"), "edited")
	}}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	var got []string
	for _, d := range res.Items[0].Details {
		rel, _ := filepath.Rel(filepath.Join(root, "src", "tree"), d.Src)
		got = append(got, filepath.ToSlash(rel))
	}
	if want := []string{"a.txt", "m-x.txt", "sub/b.txt", "z.txt"}; !slices.Equal(got, want) {
		t.Errorf("Details = %+q, want %+q (name order)", got, want)
	}
}

// TestMoveSyncFailureKeepsSource は、ボリュームをまたぐ移動で Unix のフォルダの同期に失敗したら（フックで注入）、
// 移動元を削除しないことを確かめる（§11.2 の手順 1、I2）。§11.1 の切り替えを使い、同じボリュームの中で §11.2 の方式を実行する。
func TestMoveSyncFailureKeepsSource(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	moveTree(t, root)
	testfs.MkdirAll(t, filepath.Join(root, "dest"))
	before := testfs.Take(t, filepath.Join(root, "src", "tree"))
	plan := mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(root, "src", "tree")}, DestDir: filepath.Join(root, "dest")})
	injected := errors.New("injected sync failure")
	h := &testHooks{
		beforeMoveRename: func(src, dst string) error {
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: crossDeviceErr()}
		},
		beforeSyncDir: func(string) error { return injected },
	}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomePartial || it.Err == nil || !errors.Is(it.Err, injected) || it.Err.Kind != KindSyncFailed {
		t.Errorf("result = %+v, want Partial with the sync error (KindSyncFailed)", it)
	}
	checkUnchanged(t, before, filepath.Join(root, "src", "tree"))
}

// TestCopyDirSyncFailureWarning は、SyncAlways のコピーで、フォルダの同期に失敗したら（フックで注入）、データは書き終えているので Done のまま
// KindSyncFailed の警告にすることを確かめる（§10.5）。
func TestCopyDirSyncFailureWarning(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("f"), "dest": testfs.Dir()})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: filepath.Join(root, "dest")})
	injected := errors.New("injected sync failure")
	res := execPlan(t, context.Background(), plan, ExecOptions{Sync: SyncAlways, hooks: &testHooks{beforeSyncDir: func(string) error { return injected }}})
	it := res.Items[0]
	if it.Outcome != OutcomeDone || len(it.Warnings) != 1 || it.Warnings[0].Kind != KindSyncFailed || !errors.Is(it.Warnings[0], injected) {
		t.Errorf("result = %+v (warnings %v), want Done with a KindSyncFailed warning", it, it.Warnings)
	}
}

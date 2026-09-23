package fsops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// execPlan は計画を実行する。
func execPlan(t *testing.T, ctx context.Context, plan *Plan, opt ExecOptions) *Result {
	t.Helper()
	res, err := plan.Execute(ctx, opt)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res
}

// noTempFiles は、dir 以下に一時ファイル（.fsops-*.tmp）が残っていないことを確かめる（I3）。
// ファイルを開かずに名前だけを調べる（ロックしたファイルがあっても調べられるように）。
func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	for _, name := range testfs.ListNames(t, dir) {
		p := filepath.Join(dir, name)
		if strings.HasPrefix(name, ".fsops-") {
			t.Errorf("I3 violated: temporary file %s is left", p)
		}
		if fi, err := os.Lstat(testfs.ExtendedPath(p)); err == nil && fi.IsDir() {
			noTempFiles(t, p)
		}
	}
}

// entriesIn は、dir の中のエントリのスナップショットを返す。dir 自身は含めない
// （一時ファイルを作って消すと dir 自身の更新日時は変わるが、中の既存のエントリは変わってはならない）。
func entriesIn(t *testing.T, dir string) testfs.Snapshot {
	t.Helper()
	s := testfs.Take(t, dir)
	delete(s, ".")
	return s
}

// decide は、Dst が dst の衝突に決定 d を設定する。
func decide(t *testing.T, plan *Plan, dst string, d Decision) {
	t.Helper()
	for _, c := range plan.Conflicts() {
		if c.Dst == dst {
			if err := plan.Decide(c.ID, d); err != nil {
				t.Fatalf("Decide(%s, %v): %v", dst, d, err)
			}
			return
		}
	}
	t.Fatalf("no conflict for %s", dst)
}

// ---- I1: 承認されていない上書きをしない ----

// TestCopyConflictAfterPlan は、計画の後に同名のファイル・フォルダを作ってから実行すると、Skipped（KindExist）になり、
// 既存のものが元のままであることを確かめる（§18.4 の I1）。
func TestCopyConflictAfterPlan(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("new"), "src/dir/x": testfs.File("x"), "dest": testfs.Dir()})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt"), filepath.Join(root, "src", "dir")}, DestDir: filepath.Join(root, "dest")})
	if len(plan.Conflicts()) != 0 {
		t.Fatal("unexpected conflicts")
	}
	testfs.Build(t, root, testfs.Tree{"dest/f.txt": testfs.File("existing"), "dest/dir/y": testfs.File("existing y")})
	before := entriesIn(t, filepath.Join(root, "dest"))
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	for _, it := range res.Items {
		if it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist {
			t.Errorf("%s: %+v, want Skipped with KindExist", it.Src, it)
		}
	}
	if d := testfs.Diff(before, entriesIn(t, filepath.Join(root, "dest"))); d != nil {
		t.Errorf("I1 violated: the existing entries changed: %q", d)
	}
	noTempFiles(t, root)
}

// TestCopyConflictBeforeFinalRename は、一時ファイルを最終名にする直前に同名のファイルが現れても上書きしないことを確かめる（I1、§7.3）。
func TestCopyConflictBeforeFinalRename(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("new"), "src/dir/inner.txt": testfs.File("in"), "dest": testfs.Dir()})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt"), filepath.Join(root, "src", "dir")}, DestDir: filepath.Join(root, "dest")})
	h := &testHooks{beforeFinalRename: func(dst string) { testfs.WriteFile(t, dst, "appeared") }}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist {
		t.Errorf("file: %+v, want Skipped with KindExist", it)
	}
	if it := res.Items[1]; it.Outcome != OutcomePartial || !hasKind(it, KindExist) {
		t.Errorf("dir: %+v, want Partial with KindExist in Details", it)
	}
	for _, rel := range []string{"f.txt", "dir/inner.txt"} {
		if got := testfs.ReadFile(t, filepath.Join(root, "dest", filepath.FromSlash(rel))); got != "appeared" {
			t.Errorf("I1 violated: %s = %q, want the file that appeared", rel, got)
		}
	}
	noTempFiles(t, root)
}

// TestCopyUnsetDecisionSkips は、決定が未設定の衝突が Skip され、Err が nil であることを確かめる（§18.4 の I1、§7.4）。
func TestCopyUnsetDecisionSkips(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/f.txt": testfs.File("new"), "src/dir/x": testfs.File("new x"),
		"dest/f.txt": testfs.File("old"), "dest/dir/x": testfs.File("old x"),
	})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt"), filepath.Join(root, "src", "dir")}, DestDir: filepath.Join(root, "dest")})
	before := entriesIn(t, filepath.Join(root, "dest"))
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	for _, it := range res.Items {
		if it.Outcome != OutcomeSkipped || it.Err != nil {
			t.Errorf("%s: %+v, want Skipped with nil Err", it.Src, it)
		}
	}
	if res.Status != StatusCompleted {
		t.Errorf("Status = %v, want StatusCompleted (skips by decision are not errors)", res.Status)
	}
	if d := testfs.Diff(before, entriesIn(t, filepath.Join(root, "dest"))); d != nil {
		t.Errorf("I1 violated: %q", d)
	}
}

// TestCopyOverwriteTargetReplaced は、計画後に上書き先を別のファイルに置き換えてから実行すると、Skipped（KindExist）で
// 置き換えたファイルが元のままであることを確かめる（§18.4「計画」、§7.3）。
func TestCopyOverwriteTargetReplaced(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("new"), "dest/f.txt": testfs.File("old")})
	dst := filepath.Join(root, "dest", "f.txt")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: filepath.Join(root, "dest")})
	decide(t, plan, dst, DecisionOverwrite)
	if err := os.Remove(testfs.ExtendedPath(dst)); err != nil {
		t.Fatal(err)
	}
	testfs.WriteFile(t, dst, "replaced by someone")
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist {
		t.Errorf("result = %+v, want Skipped with KindExist", it)
	}
	if got := testfs.ReadFile(t, dst); got != "replaced by someone" {
		t.Errorf("I1 violated: %q", got)
	}
	noTempFiles(t, root)
}

// TestCopyMergeTargetReplacedByLink は、計画後にマージ先を別の場所へのリンクに置き換えてから実行すると、Skipped（KindExist）で
// リンク先に何も書かれないことを確かめる（§18.4「計画」、§7.3）。
func TestCopyMergeTargetReplacedByLink(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	markerTree(t, root)
	testfs.Build(t, root, testfs.Tree{"src/dir/x": testfs.File("x"), "dest/dir/y": testfs.File("y")})
	dst := filepath.Join(root, "dest", "dir")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "dir")}, DestDir: filepath.Join(root, "dest")})
	decide(t, plan, dst, DecisionMerge)
	before := testfs.Take(t, filepath.Join(root, "outside"))
	replaceWithLink(t, root, dst)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist {
		t.Errorf("result = %+v, want Skipped with KindExist", it)
	}
	if d := testfs.Diff(before, testfs.Take(t, filepath.Join(root, "outside"))); d != nil {
		t.Errorf("something was written through the link: %q", d)
	}
}

// TestCopyMergeNewEntryAfterPlan は、マージ先に計画後に現れたエントリを上書きしないことを確かめる（I1）。
func TestCopyMergeNewEntryAfterPlan(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/dir/a": testfs.File("new a"), "src/dir/b": testfs.File("new b"), "dest/dir/old": testfs.File("o")})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "dir")}, DestDir: filepath.Join(root, "dest")})
	decide(t, plan, filepath.Join(root, "dest", "dir"), DecisionMerge)
	testfs.WriteFile(t, filepath.Join(root, "dest", "dir", "b"), "appeared")
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	it := res.Items[0]
	if it.Outcome != OutcomePartial || !hasKind(it, KindExist) {
		t.Errorf("result = %+v, want Partial with KindExist for b", it)
	}
	if testfs.ReadFile(t, filepath.Join(root, "dest", "dir", "b")) != "appeared" || testfs.ReadFile(t, filepath.Join(root, "dest", "dir", "a")) != "new a" {
		t.Error("I1 violated, or a was not copied")
	}
}

// ---- I3: 書きかけのファイルを最終名で残さない ----

// bigFile は、5 MiB 以上のファイルを作る。
func bigFile(t *testing.T, path string) {
	t.Helper()
	testfs.MkdirAll(t, filepath.Dir(path))
	testfs.WriteFile(t, path, strings.Repeat("0123456789abcdef", 6<<16)) // 6 MiB
}

// TestCopyCancelMidFile は、ファイルの途中でキャンセルすると、最終名のファイルも一時ファイルも残らないことを確かめる（§18.4 の I3）。
func TestCopyCancelMidFile(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.MkdirAll(t, filepath.Join(root, "dest"))
	bigFile(t, filepath.Join(root, "src", "big.bin"))
	testfs.WriteFile(t, filepath.Join(root, "src", "next.txt"), "n")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "big.bin"), filepath.Join(root, "src", "next.txt")}, DestDir: filepath.Join(root, "dest")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &testHooks{onWrite: func(dst string, written int64) error {
		if written >= 2<<20 {
			cancel()
		}
		return nil
	}}
	res := execPlan(t, ctx, plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindCanceled {
		t.Errorf("file in progress = %+v, want Skipped with KindCanceled (no partial result)", it)
	}
	if it := res.Items[1]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindCanceled {
		t.Errorf("remaining = %+v, want Skipped with KindCanceled", it)
	}
	if res.Status != StatusCanceled {
		t.Errorf("Status = %v", res.Status)
	}
	if names := testfs.ListNames(t, filepath.Join(root, "dest")); len(names) != 0 {
		t.Errorf("I3 violated: dest has %+q", names)
	}
}

// TestCopyWriteFailure は、書き込みの途中に障害を注入すると、最終名のファイルも一時ファイルも残らないことを確かめる（§18.4 の I3）。
// 上書きの場合は、上書き先が元のまま残ることも確かめる。
func TestCopyWriteFailure(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	bigFile(t, filepath.Join(root, "src", "big.bin"))
	bigFile(t, filepath.Join(root, "src", "over.bin"))
	testfs.Build(t, root, testfs.Tree{"dest/over.bin": testfs.File("old content"), "src/dir/inner.bin": testfs.File(strings.Repeat("z", 3<<20))})
	plan := mustPlan(t, Request{Op: OpCopy, DestDir: filepath.Join(root, "dest"), Sources: []string{
		filepath.Join(root, "src", "big.bin"), filepath.Join(root, "src", "over.bin"), filepath.Join(root, "src", "dir"),
	}})
	decide(t, plan, filepath.Join(root, "dest", "over.bin"), DecisionOverwrite)
	injected := errors.New("injected write failure")
	h := &testHooks{onWrite: func(dst string, written int64) error {
		if written >= 1<<20 {
			return injected
		}
		return nil
	}}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	for i, want := range []Outcome{OutcomeFailed, OutcomeFailed, OutcomePartial} {
		it := res.Items[i]
		if it.Outcome != want || it.Err == nil || !errors.Is(it.Err, injected) {
			t.Errorf("%s: %+v, want %v with the injected error", it.Src, it, want)
		}
	}
	if testfs.Exists(t, filepath.Join(root, "dest", "big.bin")) || testfs.Exists(t, filepath.Join(root, "dest", "dir", "inner.bin")) {
		t.Error("I3 violated: a partially written file has its final name")
	}
	if got := testfs.ReadFile(t, filepath.Join(root, "dest", "over.bin")); got != "old content" {
		t.Errorf("the overwrite target changed: %q", got)
	}
	noTempFiles(t, root)
}

// TestCopyOtherVolumes は、exFAT・FAT32・別のボリュームへのコピーで I1・I3 を満たすことを確かめる。
// macOS の exFAT では、最終名にする排他リネームに §8.4 の代わりの手段が使われる（§18.4 の I1、V12）。
func TestCopyOtherVolumes(t *testing.T) {
	t.Parallel()
	for _, env := range []string{testfs.ExFATEnv, testfs.FAT32Env, testfs.CrossVolEnv} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			dest := testfs.EnvDir(t, env)
			src := testfs.TempDir(t)
			testfs.Build(t, src, testfs.Tree{
				"new.txt": testfs.File("new"), "appear.txt": testfs.File("mine"), "over.txt": testfs.File("new over"),
				"ren.txt": testfs.File("new ren"), "tree/a.txt": testfs.File("a"), "tree/sub/b.txt": testfs.File("b"),
			})
			testfs.Build(t, dest, testfs.Tree{"over.txt": testfs.File("old over"), "ren.txt": testfs.File("old ren")})
			var srcs []string
			for _, n := range []string{"new.txt", "appear.txt", "over.txt", "ren.txt", "tree"} {
				srcs = append(srcs, filepath.Join(src, n))
			}
			plan := mustPlan(t, Request{Op: OpCopy, Sources: srcs, DestDir: dest})
			decide(t, plan, filepath.Join(dest, "over.txt"), DecisionOverwrite)
			decide(t, plan, filepath.Join(dest, "ren.txt"), DecisionAutoRename)
			appear := filepath.Join(dest, "appear.txt")
			h := &testHooks{beforeFinalRename: func(dst string) {
				if dst == appear {
					testfs.WriteFile(t, dst, "appeared")
				}
			}}
			res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
			for i, it := range res.Items {
				if i == 1 {
					if it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist {
						t.Errorf("appear.txt: %+v, want Skipped with KindExist", it)
					}
				} else if it.Outcome != OutcomeDone {
					t.Errorf("%s: %+v (%v), want Done", it.Src, it, it.Err)
				}
			}
			wantFiles(t, dest, map[string]string{
				"new.txt": "new", "appear.txt": "appeared", "over.txt": "new over", "ren.txt": "old ren", "ren (2).txt": "new ren",
				"tree/a.txt": "a", "tree/sub/b.txt": "b",
			})
			noTempFiles(t, dest)
		})
	}
}

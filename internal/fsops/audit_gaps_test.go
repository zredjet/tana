package fsops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// 2026-09-25 の不変条件の監査で、テストで守られていないとわかった経路のテスト。

// replaceSameSizeAndTime は、p を、同じ大きさ・同じ更新日時の別のファイル（内容 data）に置き換える。
// 新しいファイルを別名で作ってから名前を置き換えるので、fileID は必ず変わる（先に消すと、ext4 は番号を再利用しうる。V16）。
func replaceSameSizeAndTime(t *testing.T, p, data string) {
	t.Helper()
	fi, err := os.Lstat(testfs.ExtendedPath(p))
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(data)) != fi.Size() {
		t.Fatalf("replacement for %s must be %d bytes", p, fi.Size())
	}
	tmp := p + ".replacement"
	testfs.WriteFile(t, tmp, data)
	if err := os.Chtimes(testfs.ExtendedPath(tmp), fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(testfs.ExtendedPath(tmp), testfs.ExtendedPath(p)); err != nil {
		t.Fatal(err)
	}
}

// replaceWithRealDir は、フォルダ p を別の名前へ移し、同じ名前に別の実フォルダ（リンクではない）を作る。
func replaceWithRealDir(t *testing.T, p string) {
	t.Helper()
	if err := os.Rename(testfs.ExtendedPath(p), testfs.ExtendedPath(p+"-moved")); err != nil {
		t.Fatal(err)
	}
	testfs.MkdirAll(t, p)
}

// ---- I1 ----

// TestOverwriteTargetReplacedSameSize は、上書きの決定があっても、上書き先が計画後に同じ大きさ・同じ更新日時の別のファイルに
// 置き換えられていれば、上書きしないことを確かめる（I1、§7.3。checkTarget の fileID の照合）。コピーと移動の両方。
func TestOverwriteTargetReplacedSameSize(t *testing.T) {
	t.Parallel()
	for _, op := range []OpKind{OpCopy, OpMove} {
		t.Run(op.String(), func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("new"), "dest/f.txt": testfs.File("old")})
			dst := filepath.Join(root, "dest", "f.txt")
			plan := mustPlan(t, Request{Op: op, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: filepath.Join(root, "dest")})
			decide(t, plan, dst, DecisionOverwrite)
			replaceSameSizeAndTime(t, dst, "OLD")
			res := execPlan(t, context.Background(), plan, ExecOptions{})
			if it := res.Items[0]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist {
				t.Errorf("result = %+v, want Skipped with KindExist", it)
			}
			if got := testfs.ReadFile(t, dst); got != "OLD" {
				t.Errorf("I1 violated: dest = %q", got)
			}
			if got := testfs.ReadFile(t, filepath.Join(root, "src", "f.txt")); got != "new" {
				t.Errorf("source = %q", got)
			}
			noTempFiles(t, root)
		})
	}
}

// TestMoveOverwriteTargetReplaced は、移動の上書きで、上書き先が計画後に置き換えられていれば上書きしないことを確かめる（I1、§7.3）。
func TestMoveOverwriteTargetReplaced(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("new"), "dest/f.txt": testfs.File("old")})
	dst := filepath.Join(root, "dest", "f.txt")
	plan := mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: filepath.Join(root, "dest")})
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
	if !testfs.Exists(t, filepath.Join(root, "src", "f.txt")) {
		t.Error("the source was moved")
	}
}

// TestMoveOverwriteTargetGone は、上書きの決定があり、上書き先が計画後に消えていれば、衝突なしとして移動することを確かめる
// （§9.3。消えた後に現れた同名のものは、排他リネームで上書きしない）。
func TestMoveOverwriteTargetGone(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("new"), "dest/f.txt": testfs.File("old")})
	dst := filepath.Join(root, "dest", "f.txt")
	plan := mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: filepath.Join(root, "dest")})
	decide(t, plan, dst, DecisionOverwrite)
	if err := os.Remove(testfs.ExtendedPath(dst)); err != nil {
		t.Fatal(err)
	}
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeDone {
		t.Errorf("result = %+v, want Done", it)
	}
	if got := testfs.ReadFile(t, dst); got != "new" {
		t.Errorf("dest = %q", got)
	}
}

// TestMoveAutoRenameExistingCandidate は、移動の自動リネームで、候補の名前が既にある（計画後に現れたものを含む）場合に、
// それを上書きせず次の番号を使うことを確かめる（I1、§9.2）。
func TestMoveAutoRenameExistingCandidate(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/ren.txt": testfs.File("moved"), "dest/ren.txt": testfs.File("orig"), "dest/ren (2).txt": testfs.File("two")})
	dst := filepath.Join(root, "dest", "ren.txt")
	plan := mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(root, "src", "ren.txt")}, DestDir: filepath.Join(root, "dest")})
	decide(t, plan, dst, DecisionAutoRename)
	testfs.WriteFile(t, filepath.Join(root, "dest", "ren (3).txt"), "three, after the plan")
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeDone || it.Dst != filepath.Join(root, "dest", "ren (4).txt") {
		t.Errorf("result = %+v, want Done to ren (4).txt", it)
	}
	wantFiles(t, filepath.Join(root, "dest"), map[string]string{"ren.txt": "orig", "ren (2).txt": "two", "ren (3).txt": "three, after the plan", "ren (4).txt": "moved"})
}

// TestMergeTargetReplacedByDir は、マージの決定があっても、マージ先が計画後に別の実フォルダ（リンクではない）に置き換えられていれば、
// その中に書かずに Skipped（KindExist）にすることを確かめる（I1、§7.3。fileID の照合）。コピーと移動の両方。
func TestMergeTargetReplacedByDir(t *testing.T) {
	t.Parallel()
	for _, op := range []OpKind{OpCopy, OpMove} {
		t.Run(op.String(), func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			testfs.Build(t, root, testfs.Tree{"src/dir/x": testfs.File("x"), "dest/dir/y": testfs.File("y")})
			dst := filepath.Join(root, "dest", "dir")
			plan := mustPlan(t, Request{Op: op, Sources: []string{filepath.Join(root, "src", "dir")}, DestDir: filepath.Join(root, "dest")})
			decide(t, plan, dst, DecisionMerge)
			replaceWithRealDir(t, dst)
			res := execPlan(t, context.Background(), plan, ExecOptions{})
			if it := res.Items[0]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist {
				t.Errorf("result = %+v, want Skipped with KindExist", it)
			}
			if names := testfs.ListNames(t, dst); len(names) != 0 {
				t.Errorf("I1 violated: wrote %v into the replacing folder", names)
			}
			if got := testfs.ReadFile(t, filepath.Join(root, "src", "dir", "x")); got != "x" {
				t.Errorf("source x = %q", got)
			}
		})
	}
}

// ---- I2 ----

// TestRemoveRecordedKeepsSameSizeReplacement は、記録の後に、同じ大きさ・同じ更新日時の別のファイルへ置き換えられた移動元を
// 消さないことを確かめる（I2、§13.3。recordEntry.matches の fileID の照合）。
func TestRemoveRecordedKeepsSameSizeReplacement(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/a.txt": testfs.File("aaaa"), "src/b.txt": testfs.File("b")})
	src := filepath.Join(root, "src")
	rec := recordOf(t, src)
	replaceSameSizeAndTime(t, filepath.Join(src, "a.txt"), "AAAA")
	out := removeRec(t, context.Background(), nil, src, rec)
	if !keptWith(out, filepath.Join(src, "a.txt"), KindSourceChanged) {
		t.Errorf("a.txt was not reported as kept: %+v", out.details)
	}
	if got := testfs.ReadFile(t, filepath.Join(src, "a.txt")); got != "AAAA" {
		t.Errorf("I2 violated: a.txt = %q", got)
	}
	if testfs.Exists(t, filepath.Join(src, "b.txt")) {
		t.Error("b.txt was not removed")
	}
}

// TestFileSyncFailure は、ファイルの同期（§10.5）の失敗で、そのファイルを最終名にせず一時ファイルも残さないこと、
// ボリュームをまたぐ移動では移動元を消さないことを確かめる（I2、I3）。
func TestFileSyncFailure(t *testing.T) {
	t.Parallel()
	injected := errors.New("injected file sync failure")
	h := &testHooks{beforeSyncFile: func(string) error { return injected }}
	t.Run("copy with SyncAlways", func(t *testing.T) {
		t.Parallel()
		root := testfs.TempDir(t)
		testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("f"), "dest": testfs.Dir()})
		dest := filepath.Join(root, "dest")
		plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: dest})
		res := execPlan(t, context.Background(), plan, ExecOptions{Sync: SyncAlways, hooks: h})
		if it := res.Items[0]; it.Outcome != OutcomeFailed || !errors.Is(it.Err, injected) {
			t.Errorf("result = %+v, want Failed with the injected error", it)
		}
		if names := testfs.ListNames(t, dest); len(names) != 0 {
			t.Errorf("dest has %v, want nothing", names)
		}
	})
	t.Run("move across volumes", func(t *testing.T) {
		t.Parallel()
		root := testfs.TempDir(t)
		dest := testfs.CrossVolDir(t)
		testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("f"), "src/d/x.txt": testfs.File("x")})
		plan := mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(root, "src", "f.txt"), filepath.Join(root, "src", "d")}, DestDir: dest})
		res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
		for _, it := range res.Items {
			if it.Outcome == OutcomeDone {
				t.Errorf("%s: Done despite the sync failure", it.Src)
			}
		}
		wantFiles(t, filepath.Join(root, "src"), map[string]string{"f.txt": "f", "d/x.txt": "x"})
		noTempFiles(t, dest)
	})
}

// ---- I4 ----

// TestDestDirReplacedAfterPlan は、計画の後に DestDir が別の実フォルダに置き換えられていれば、その中に書かずに失敗にすることを
// 確かめる（§13.1。openDestRoot の fileID の照合）。コピーと移動の両方。
func TestDestDirReplacedAfterPlan(t *testing.T) {
	t.Parallel()
	for _, op := range []OpKind{OpCopy, OpMove} {
		t.Run(op.String(), func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("f"), "src/d/x": testfs.File("x"), "dest": testfs.Dir()})
			dest := filepath.Join(root, "dest")
			plan := mustPlan(t, Request{Op: op, Sources: []string{filepath.Join(root, "src", "f.txt"), filepath.Join(root, "src", "d")}, DestDir: dest})
			replaceWithRealDir(t, dest)
			res := execPlan(t, context.Background(), plan, ExecOptions{})
			for _, it := range res.Items {
				if it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindSourceChanged {
					t.Errorf("%s: %+v, want Failed with KindSourceChanged", it.Src, it)
				}
			}
			if names := testfs.ListNames(t, dest); len(names) != 0 {
				t.Errorf("wrote %v into the replacing folder", names)
			}
			wantFiles(t, filepath.Join(root, "src"), map[string]string{"f.txt": "f", "d/x": "x"})
		})
	}
}

// ---- I7 ----

// TestLockRetryCancelPaths は、§17.1 のやり直しの待ちの間にキャンセルされても、各経路で I1〜I3 が保たれることを確かめる（I7）。
// コピー元を開く・検証の読み直し・上書き・自動リネーム・同一ボリュームの移動・マージの中身・マージの後の移動元のフォルダの削除。
func TestLockRetryCancelPaths(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		op       OpKind
		fault    string
		tree     testfs.Tree
		sources  []string
		decide   map[string]Decision
		verify   VerifyMode
		wantSrc  map[string]string // src の中に残っていなければならないもの
		wantDest map[string]string // dest の中身（すべて）
	}{
		{name: "copy open", op: OpCopy, fault: "open", tree: testfs.Tree{"src/f.txt": testfs.File("f"), "dest": testfs.Dir()},
			sources: []string{"f.txt"}, wantSrc: map[string]string{"f.txt": "f"}, wantDest: map[string]string{}},
		{name: "copy verify", op: OpCopy, fault: "verify", verify: VerifyHash, tree: testfs.Tree{"src/f.txt": testfs.File("f"), "dest": testfs.Dir()},
			sources: []string{"f.txt"}, wantSrc: map[string]string{"f.txt": "f"}, wantDest: map[string]string{}},
		{name: "copy overwrite", op: OpCopy, fault: "rename", tree: testfs.Tree{"src/f.txt": testfs.File("new"), "dest/f.txt": testfs.File("old")},
			sources: []string{"f.txt"}, decide: map[string]Decision{"f.txt": DecisionOverwrite},
			wantSrc: map[string]string{"f.txt": "new"}, wantDest: map[string]string{"f.txt": "old"}},
		{name: "copy auto-rename", op: OpCopy, fault: "rename", tree: testfs.Tree{"src/f.txt": testfs.File("new"), "dest/f.txt": testfs.File("old")},
			sources: []string{"f.txt"}, decide: map[string]Decision{"f.txt": DecisionAutoRename},
			wantSrc: map[string]string{"f.txt": "new"}, wantDest: map[string]string{"f.txt": "old"}},
		{name: "move rename", op: OpMove, fault: "rename", tree: testfs.Tree{"src/f.txt": testfs.File("f"), "dest": testfs.Dir()},
			sources: []string{"f.txt"}, wantSrc: map[string]string{"f.txt": "f"}, wantDest: map[string]string{}},
		{name: "move overwrite", op: OpMove, fault: "rename", tree: testfs.Tree{"src/f.txt": testfs.File("new"), "dest/f.txt": testfs.File("old")},
			sources: []string{"f.txt"}, decide: map[string]Decision{"f.txt": DecisionOverwrite},
			wantSrc: map[string]string{"f.txt": "new"}, wantDest: map[string]string{"f.txt": "old"}},
		{name: "move merge entry", op: OpMove, fault: "rename", tree: testfs.Tree{"src/d/x": testfs.File("x"), "dest/d/y": testfs.File("y")},
			sources: []string{"d"}, decide: map[string]Decision{"d": DecisionMerge},
			wantSrc: map[string]string{"d/x": "x"}, wantDest: map[string]string{"d/y": "y"}},
		{name: "move merge source removal", op: OpMove, fault: "remove", tree: testfs.Tree{"src/d/x": testfs.File("x"), "dest/d/y": testfs.File("y")},
			sources: []string{"d"}, decide: map[string]Decision{"d": DecisionMerge},
			wantSrc: map[string]string{}, wantDest: map[string]string{"d/x": "x", "d/y": "y"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			testfs.Build(t, root, tc.tree)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			li := newLockInjector(map[string]int{tc.fault: -1})
			li.onWait = func(string, int) { cancel() }
			var srcs []string
			for _, s := range tc.sources {
				srcs = append(srcs, filepath.Join(root, "src", s))
			}
			dest := filepath.Join(root, "dest")
			plan := mustPlan(t, Request{Op: tc.op, Sources: srcs, DestDir: dest})
			for name, d := range tc.decide {
				decide(t, plan, filepath.Join(dest, name), d)
			}
			res := execPlan(t, ctx, plan, ExecOptions{Verify: tc.verify, hooks: li.hooks()})
			if res.Status != StatusCanceled {
				t.Errorf("status = %v, want Canceled (items %+v)", res.Status, res.Items)
			}
			if len(li.waits) != 1 {
				t.Errorf("waits = %v, want 1 (stopped at the first wait)", li.waits)
			}
			for rel, want := range tc.wantSrc {
				if got := testfs.ReadFile(t, filepath.Join(root, "src", filepath.FromSlash(rel))); got != want {
					t.Errorf("src/%s = %q, want %q", rel, got, want)
				}
			}
			wantFiles(t, dest, tc.wantDest)
			noTempFiles(t, root)
		})
	}
}

// TestLockRetryCancelSourceRemoval は、ボリュームをまたぐ移動で、移動元の削除のやり直しを待つ間にキャンセルされたら、
// 残りの移動元を消さずに OutcomeCopiedSourceKept（KindCanceled）にすることを確かめる（I2、I7）。
func TestLockRetryCancelSourceRemoval(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	dest := testfs.CrossVolDir(t)
	testfs.Build(t, root, testfs.Tree{"src/d/a.txt": testfs.File("a"), "src/d/b.txt": testfs.File("b")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	li := newLockInjector(map[string]int{"remove": -1})
	li.onWait = func(string, int) { cancel() }
	plan := mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(root, "src", "d")}, DestDir: dest})
	res := execPlan(t, ctx, plan, ExecOptions{hooks: li.hooks()})
	if it := res.Items[0]; it.Outcome != OutcomeCopiedSourceKept || it.Err == nil || it.Err.Kind != KindCanceled {
		t.Errorf("result = %+v, want CopiedSourceKept with KindCanceled", it)
	}
	wantFiles(t, filepath.Join(root, "src"), map[string]string{"d/a.txt": "a", "d/b.txt": "b"})
	wantFiles(t, filepath.Join(dest, "d"), map[string]string{"a.txt": "a", "b.txt": "b"})
}

// TestLockRetryCancelTempCleanup は、書き込み中にキャンセルされた一時ファイルの削除（lockRetrier.removeTemp）が使用中なら、
// キャンセルの後もやり直して消すことを確かめる（I3、§17.1）。
func TestLockRetryCancelTempCleanup(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("data"), "dest": testfs.Dir()})
	dest := filepath.Join(root, "dest")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	li := newLockInjector(map[string]int{"unlink-temp": 2})
	h := li.hooks()
	h.onWrite = func(string, int64) error { cancel(); return nil } // 書き込み中にキャンセル
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: dest})
	res := execPlan(t, ctx, plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindCanceled {
		t.Errorf("result = %+v, want Skipped with KindCanceled", it)
	}
	if li.attempts["unlink-temp"] != 3 {
		t.Errorf("temp file removal attempts = %d, want 3 (retried after cancel)", li.attempts["unlink-temp"])
	}
	if names := testfs.ListNames(t, dest); len(names) != 0 {
		t.Errorf("dest has %v, want nothing (I3)", names)
	}
}

// TestMoveOverwriteHardLinkToSource は、移動の上書きで、上書き先が移動元と同じファイルへのハードリンクなら、リネームせずに
// Failed（KindSameFile）にし、両方の名前が残ることを確かめる（§7.3。Unix の rename は何もせずに成功を返し、Done と報告していた）。
func TestMoveOverwriteHardLinkToSource(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("data"), "dest": testfs.Dir()})
	src, dst := filepath.Join(root, "src", "f.txt"), filepath.Join(root, "dest", "f.txt")
	if err := os.Link(testfs.ExtendedPath(src), testfs.ExtendedPath(dst)); err != nil {
		t.Skipf("hard links are not available here: %v", err)
	}
	plan := mustPlan(t, Request{Op: OpMove, Sources: []string{src}, DestDir: filepath.Join(root, "dest")})
	decide(t, plan, dst, DecisionOverwrite)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindSameFile {
		t.Errorf("result = %+v, want Failed with KindSameFile", it)
	}
	wantFiles(t, root, map[string]string{"src/f.txt": "data", "dest/f.txt": "data"})
}

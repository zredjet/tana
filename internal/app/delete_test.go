package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

// ごみ箱と完全削除の流れ（filer §8.2・§8.5・§8.6）のテスト。
// ごみ箱の計画は偽物にする（本物のごみ箱に触れない。CLAUDE.md のテストのルール）。完全削除は本物の fsops で t.TempDir() の中を消す。

// trashHarness は、ごみ箱の計画を偽物 fp に、それ以外の計画を本物の fsops にした harness（pane 0 は root、pane 1 は root/sub）。
// ops には、作ろうとした計画の操作を順に記録する（UI が自分から完全削除の計画を作らないことを確かめるため。fsops I5 の UI 側）。
func trashHarness(t *testing.T, fp *fakePlan, ops *[]fsops.OpKind, mod func(*Config)) (*harness, string) {
	t.Helper()
	root := tree(t)
	h := newHarness(t, func(c *Config) {
		c.NewPlan = func(ctx context.Context, req fsops.Request) (Plan, error) {
			*ops = append(*ops, req.Op)
			if req.Op == fsops.OpTrash {
				fp.req = req
				return fp, nil
			}
			return newFsopsPlan(ctx, req)
		}
		if mod != nil {
			mod(c)
		}
	}, root, filepath.Join(root, "sub"))
	return h, root
}

// trashResult は、Sources の名前ごとの結果を返す Execute（偽物）。results にない名前は完了にする。
func trashResult(fp *fakePlan, results map[string]fsops.ItemResult) func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
	return func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
		res := &fsops.Result{Status: fsops.StatusCompleted}
		for _, s := range fp.req.Sources {
			it, ok := results[filepath.Base(s)]
			if !ok {
				it = fsops.ItemResult{Outcome: fsops.OutcomeDone, Method: fsops.MethodTrash}
			}
			it.Src = s
			if it.Outcome != fsops.OutcomeDone {
				res.Status = fsops.StatusCompletedWithErrors
			}
			res.Items = append(res.Items, it)
		}
		return res, nil
	}
}

func unavailable() *fsops.OpError {
	return &fsops.OpError{Op: "trash", Kind: fsops.KindTrashUnavailable}
}

func exists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Lstat(p)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return err == nil
}

// markNames は、操作中のペインで names の項目をマークする。
func (h *harness) markNames(names ...string) {
	h.t.Helper()
	for _, n := range names {
		h.moveTo(n)
		h.do(ActMark)
	}
}

// TestTrashFlow は、ごみ箱の確認画面（合計を出さない）と、すべて完了したときのメッセージを確かめる（filer §8.2・§8.5）。
func TestTrashFlow(t *testing.T) {
	t.Parallel()
	var ops []fsops.OpKind
	fp := &fakePlan{}
	fp.exec = trashResult(fp, nil)
	h, root := trashHarness(t, fp, &ops, nil)
	h.moveTo("a.txt")
	h.do(ActTrash)
	if h.a.Screen() != ScreenConfirm {
		t.Fatalf("screen %v, want the confirmation", h.a.Screen())
	}
	v := h.a.Confirm()
	if v.Op != fsops.OpTrash || v.Count != 1 || v.Runnable != 1 || v.Untrashable != 0 || v.Dest != "" {
		t.Errorf("Confirm = %+v", v)
	}
	if got := fp.req.Sources; len(got) != 1 || got[0] != filepath.Join(root, "a.txt") {
		t.Errorf("Sources = %q", got)
	}
	h.confirm()
	h.wantMessage(msg.Done(fsops.OpTrash, 1, 0, false))
	if !slices.Equal(ops, []fsops.OpKind{fsops.OpTrash}) {
		t.Errorf("plans %v", ops)
	}
}

// TestTrashNeverPurges は、ごみ箱に入らなかった項目を、UI が自分から完全削除に切り替えないことを確かめる（filer U2、fsops I5 の UI 側）。
// 結果の画面を出し（U3）、D で完全削除の確認へ進めて、y を押したときだけ消す。
func TestTrashNeverPurges(t *testing.T) {
	t.Parallel()
	var ops []fsops.OpKind
	fp := &fakePlan{}
	fp.exec = trashResult(fp, map[string]fsops.ItemResult{"a.txt": {Outcome: fsops.OutcomeFailed, Err: unavailable(), Method: fsops.MethodTrash}})
	h, root := trashHarness(t, fp, &ops, nil)
	h.markNames("a.txt", "b.txt")
	h.do(ActTrash)
	h.confirm()
	if h.a.Screen() != ScreenResult {
		t.Fatalf("screen %v, want the result (U3)", h.a.Screen())
	}
	if !exists(t, filepath.Join(root, "a.txt")) || !slices.Equal(ops, []fsops.OpKind{fsops.OpTrash}) {
		t.Fatalf("the item that did not go to the trash was deleted or a deletion was planned without asking (I5, U2): plans %v", ops)
	}
	if v := h.a.Result(); v.Untrashable != 1 {
		t.Errorf("Untrashable = %d, want 1", v.Untrashable)
	}
	h.do(ActPurge) // 結果の画面を描く前に届いた D
	if h.a.Screen() != ScreenResult || len(ops) != 1 {
		t.Fatalf("D typed before the result was drawn went on (screen %v, plans %v)", h.a.Screen(), ops)
	}
	h.a.Drawn()
	h.do(ActPurge)
	if h.a.Screen() != ScreenDelete {
		t.Fatalf("screen %v, want the deletion confirmation", h.a.Screen())
	}
	v := h.a.Delete()
	if !v.FromTrash || v.Count != 1 || len(v.Items) != 1 || v.Items[0].Name != "a.txt" || v.Dir != root {
		t.Fatalf("Delete = %+v", v)
	}
	h.a.Drawn()
	h.do(ActYes)
	if exists(t, filepath.Join(root, "a.txt")) {
		t.Error("y did not delete")
	}
	h.wantMessage(msg.Done(fsops.OpDelete, 1, 0, false))
	if !exists(t, filepath.Join(root, "b.txt")) {
		t.Error("b.txt (in the trash in this test, so still on disk) was deleted")
	}
}

// TestPurgeTargets は、完全削除を勧めるのが「失敗」で Kind が KindTrashUnavailable の項目だけであることを確かめる（filer §8.5）。
// 「ごみ箱に入ったか確かめられない」は Kind が同じでも元の場所から消えているので、ほかの理由で失敗した項目はごみ箱が使えるかもしれないので、勧めない。
func TestPurgeTargets(t *testing.T) {
	t.Parallel()
	var ops []fsops.OpKind
	fp := &fakePlan{}
	fp.exec = trashResult(fp, map[string]fsops.ItemResult{
		"a.txt": {Outcome: fsops.OutcomeFailed, Err: unavailable(), Method: fsops.MethodTrash},
		"b.txt": {Outcome: fsops.OutcomeTrashUnconfirmed, Err: unavailable(), Method: fsops.MethodTrash},
		"sub":   {Outcome: fsops.OutcomeFailed, Err: &fsops.OpError{Op: "trash", Kind: fsops.KindPermission}, Method: fsops.MethodTrash},
	})
	h, root := trashHarness(t, fp, &ops, nil)
	h.markNames("sub", "a.txt", "b.txt")
	h.do(ActTrash)
	h.confirm()
	if v := h.a.Result(); v.Untrashable != 1 {
		t.Fatalf("Untrashable = %d, want 1 (only a.txt)", v.Untrashable)
	}
	h.a.Drawn()
	h.do(ActPurge)
	v := h.a.Delete()
	if len(v.Items) != 1 || v.Items[0].Name != "a.txt" {
		t.Fatalf("deletion targets %+v, want only a.txt", v.Items)
	}
	h.do(ActCancel)
	if h.a.Screen() != ScreenBrowse || !exists(t, filepath.Join(root, "a.txt")) {
		t.Errorf("Esc: screen %v", h.a.Screen())
	}
}

// TestTrashAllUnavailable は、計画の時点ですべての項目がごみ箱に入らないと分かったとき、確認画面から D で完全削除の確認へ進めることを確かめる
// （filer §8.2。フェーズ20で決めた）。Enter では何もしない。
func TestTrashAllUnavailable(t *testing.T) {
	t.Parallel()
	var ops []fsops.OpKind
	fp := &fakePlan{errs: map[string]fsops.Kind{"a.txt": fsops.KindTrashUnavailable, "b.txt": fsops.KindNotFound}}
	fp.exec = func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
		t.Error("executed a plan with nothing to run")
		return &fsops.Result{}, nil
	}
	h, root := trashHarness(t, fp, &ops, nil)
	h.markNames("a.txt", "b.txt")
	h.do(ActTrash)
	v := h.a.Confirm()
	if v.Runnable != 0 || v.Untrashable != 1 || len(v.NotRunnable) != 2 || v.NotRunnable[0].Reason != msg.TrashUnavailableSkip {
		t.Fatalf("Confirm = %+v", v)
	}
	h.do(ActPurge) // 確認画面を描く前に届いた D
	if h.a.Screen() != ScreenConfirm {
		t.Fatalf("D typed before the confirmation was drawn went on (screen %v)", h.a.Screen())
	}
	h.confirm() // Enter は何もしない
	if h.a.Screen() != ScreenConfirm {
		t.Fatalf("screen %v after Enter", h.a.Screen())
	}
	h.do(ActPurge)
	if h.a.Screen() != ScreenDelete {
		t.Fatalf("screen %v, want the deletion confirmation", h.a.Screen())
	}
	if v := h.a.Delete(); !v.FromTrash || len(v.Items) != 1 || v.Items[0].Name != "a.txt" {
		t.Fatalf("Delete = %+v, want only a.txt (b.txt was not found, not untrashable)", v)
	}
	if !slices.Equal(ops, []fsops.OpKind{fsops.OpTrash, fsops.OpDelete}) {
		t.Errorf("plans %v", ops)
	}
	h.do(ActCancel)
	if !exists(t, filepath.Join(root, "a.txt")) {
		t.Error("deleted after Esc")
	}
}

// TestPurgeKeys は、完全削除が、確認の画面を描いた後の y でだけ確定することを確かめる（filer U2・§8.6）。
// 計画を作っている間のキー、描く前の y、貼り付けた文字、Enter・n・Esc では消さない。
func TestPurgeKeys(t *testing.T) {
	t.Parallel()
	root := tree(t)
	a := filepath.Join(root, "a.txt")
	h := newHarness(t, nil, root)
	h.moveTo("a.txt")
	h.hold = true
	h.do(ActPurge)
	for _, k := range []ActionKind{ActYes, ActSubmit, ActYes} {
		h.do(k) // 計画を作っている間に打たれたキー
	}
	h.release()
	if h.a.Screen() != ScreenDelete {
		t.Fatalf("screen %v, want the deletion confirmation", h.a.Screen())
	}
	h.do(ActYes) // 確認を描く前に届いた y
	h.act(Action{Kind: ActInsert, Text: "y"})
	if h.a.Screen() != ScreenDelete || !exists(t, a) {
		t.Fatal("deleted by a key typed before the confirmation was drawn (U2)")
	}
	h.a.Drawn()
	h.act(Action{Kind: ActInsert, Text: "y"}) // 貼り付け（tui は貼り付けを ActYes にしない。ここでは文字の操作が確定しないことを見る）
	if h.a.Screen() != ScreenDelete {
		t.Fatal("text input moved the deletion confirmation")
	}
	for _, k := range []ActionKind{ActSubmit, ActNo, ActCancel} {
		h.do(ActPurge)
		h.a.Drawn()
		h.do(k)
		if h.a.Screen() != ScreenBrowse || !exists(t, a) {
			t.Fatalf("%v: screen %v, exists %v (want canceled)", k, h.a.Screen(), exists(t, a))
		}
	}
	h.do(ActPurge)
	h.a.Drawn()
	h.do(ActYes)
	if exists(t, a) {
		t.Fatal("y after the confirmation was drawn did not delete")
	}
	h.wantMessage(msg.Done(fsops.OpDelete, 1, 0, false))
	if slices.Contains(h.names(0), "a.txt") {
		t.Errorf("the pane was not reloaded: %q", h.names(0))
	}
}

// TestPurgeView は、完全削除の確認の内容（合計、項目の種類とサイズ、場所）を確かめる（filer §8.6）。
func TestPurgeView(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.markNames("sub", "a.txt")
	h.do(ActPurge)
	v := h.a.Delete()
	if v.FromTrash || v.Dir != root || v.Count != 2 || v.Runnable != 2 || v.Files != 2 || v.Bytes != 2 {
		t.Errorf("Delete = %+v", v)
	}
	if len(v.Items) != 2 || v.Items[0].Name != "sub" || v.Items[0].Info.Type != fsops.TypeDir ||
		v.Items[1].Name != "a.txt" || v.Items[1].Info.Size != 1 {
		t.Errorf("Items = %+v", v.Items)
	}
}

// TestPurgeNothingRunnable は、完全削除の対象がすべて実行できないとき、y でも実行しないことを確かめる。
func TestPurgeNothingRunnable(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.moveTo("a.txt")
	if err := os.Remove(filepath.Join(root, "a.txt")); err != nil { // テストが作ったファイル
		t.Fatal(err)
	}
	h.do(ActPurge)
	if v := h.a.Delete(); v.Runnable != 0 || len(v.NotRunnable) != 1 {
		t.Fatalf("Delete = %+v", v)
	}
	h.a.Drawn()
	h.do(ActYes)
	if h.a.Screen() != ScreenDelete {
		t.Errorf("screen %v: executed a plan with nothing to run", h.a.Screen())
	}
}

// TestTrashDialogHint は、Windows でごみ箱へ移動中に 3 秒以上進捗が変わらないとき、確認ダイアログの可能性を知らせることを確かめる（filer §8.4）。
func TestTrashDialogHint(t *testing.T) {
	t.Parallel()
	for _, mayAsk := range []bool{true, false} {
		now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
		release, started := make(chan struct{}), make(chan struct{})
		var ops []fsops.OpKind
		fp := &fakePlan{}
		fp.exec = func(_ context.Context, opt fsops.ExecOptions) (*fsops.Result, error) {
			opt.Progress(fsops.Progress{Stage: fsops.StageTrash, Current: fp.req.Sources[0], TotalFiles: 2})
			close(started)
			<-release
			opt.Progress(fsops.Progress{Stage: fsops.StageTrash, Current: fp.req.Sources[1], DoneFiles: 1, TotalFiles: 2})
			return &fsops.Result{Status: fsops.StatusCanceled}, nil
		}
		h, _ := trashHarness(t, fp, &ops, func(c *Config) {
			c.Now = func() time.Time { return now }
			c.TrashMayAsk = mayAsk
		})
		h.markNames("a.txt", "b.txt")
		h.do(ActTrash)
		h.hold = true
		h.confirm()
		done := make(chan struct{})
		go func() { h.held[0].Run(); close(done) }()
		<-started
		h.a.Refresh()
		now = now.Add(2 * time.Second)
		if h.a.Progress().TrashDialog {
			t.Errorf("mayAsk %v: hint after 2 s", mayAsk)
		}
		now = now.Add(time.Second)
		if got := h.a.Progress().TrashDialog; got != mayAsk {
			t.Errorf("mayAsk %v: hint after 3 s = %v", mayAsk, got)
		}
		close(release)
		<-done
		h.a.Refresh() // 進捗が変わった
		if h.a.Progress().TrashDialog {
			t.Errorf("mayAsk %v: hint remains after the progress changed", mayAsk)
		}
	}
}

// TestTrashAfterwards は、ごみ箱の後、完了した項目のマークを外し、元の場所から消えた項目（ごみ箱に入ったか確かめられないものを含む）を
// 覚えた項目から除き、ほかは残すことを確かめる。
func TestTrashAfterwards(t *testing.T) {
	t.Parallel()
	var ops []fsops.OpKind
	fp := &fakePlan{}
	fp.exec = trashResult(fp, map[string]fsops.ItemResult{
		"b.txt": {Outcome: fsops.OutcomeFailed, Err: &fsops.OpError{Kind: fsops.KindLocked}},
		"sub":   {Outcome: fsops.OutcomeTrashUnconfirmed, Err: &fsops.OpError{Kind: fsops.KindUnknown}},
	})
	h, _ := trashHarness(t, fp, &ops, nil)
	h.markNames("sub", "a.txt", "b.txt")
	h.do(ActYank)
	h.do(ActTrash)
	h.confirm()
	if p := h.pane(0); p.Marked("a.txt") || !p.Marked("b.txt") {
		t.Errorf("marks: a.txt %v (want false), b.txt %v (want true)", p.Marked("a.txt"), p.Marked("b.txt"))
	}
	if h.a.Yanked() != 1 {
		t.Errorf("yanked %d, want 1 (only b.txt; the items gone from their place are forgotten)", h.a.Yanked())
	}
}

// TestNoTarget は、対象がないとき（.. だけ）のメッセージを確かめる。
func TestNoTarget(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.do(ActHome)
	for _, k := range []ActionKind{ActTrash, ActPurge} {
		h.do(k)
		h.wantMessage(msg.NoTarget)
		if h.a.Screen() != ScreenBrowse {
			t.Errorf("%v: screen %v", k, h.a.Screen())
		}
	}
}

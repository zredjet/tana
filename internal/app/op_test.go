package app

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeData(t *testing.T, p, data string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pasteInto は、操作中のペインで name を覚え、ペイン pane に移って貼り付ける（計画まで。確認画面が出る）。
func (h *harness) pasteInto(name string, pane int, act ActionKind) {
	h.t.Helper()
	h.moveTo(name)
	h.do(ActYank)
	for h.a.Active() != pane {
		h.do(ActNextPane)
	}
	h.do(act)
}

// confirm は、描いた後に Enter を押す（確認画面・衝突の画面を先へ進める）。
func (h *harness) confirm() {
	h.a.Drawn()
	h.do(ActSubmit)
}

// TestYankPasteCopy は、覚えて貼り付けるコピーの流れを確かめる（filer §7・§8）。コピーの後も覚えた項目は残る。
func TestYankPasteCopy(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root, filepath.Join(root, "sub"))
	h.moveTo("a.txt")
	h.do(ActYank)
	h.wantMessage(msg.Yanked(1))
	h.do(ActNextPane)
	h.do(ActPasteCopy)
	if h.a.Screen() != ScreenConfirm {
		t.Fatalf("screen %v, want the confirmation", h.a.Screen())
	}
	v := h.a.Confirm()
	if v.Op != fsops.OpCopy || v.Count != 1 || v.Runnable != 1 || v.Dest != filepath.Join(root, "sub") || v.Files != 1 || v.Bytes != 1 || v.Conflicts != 0 {
		t.Errorf("Confirm = %+v", v)
	}
	h.confirm()
	if h.a.Screen() != ScreenBrowse {
		t.Fatalf("screen %v after execution, want the listing (no result screen when all done)", h.a.Screen())
	}
	h.wantMessage(msg.Done(fsops.OpCopy, 1, 0, false))
	if readFile(t, filepath.Join(root, "sub", "a.txt")) != "x" || !slices.Contains(h.names(1), "a.txt") {
		t.Errorf("not copied or the destination pane not reloaded: %q", h.names(1))
	}
	if h.a.Yanked() != 1 {
		t.Error("the yanked items are forgotten after a copy")
	}
}

// TestPasteMove は、移動の後に覚えた項目を忘れ、項目のあったペインも読み直すことを確かめる。
func TestPasteMove(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root, filepath.Join(root, "sub"))
	h.pasteInto("a.txt", 1, ActPasteMove)
	h.confirm()
	if _, err := os.Lstat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) || readFile(t, filepath.Join(root, "sub", "a.txt")) != "x" {
		t.Errorf("not moved: %v", err)
	}
	if h.a.Yanked() != 0 {
		t.Error("the yanked items remain after a move")
	}
	if slices.Contains(h.names(0), "a.txt") {
		t.Errorf("the source pane not reloaded: %q", h.names(0))
	}
}

// TestUnsetConflictSkips は、未選択の衝突がスキップになり、上書きしないことを確かめる（U1）。
func TestUnsetConflictSkips(t *testing.T) {
	t.Parallel()
	root := tree(t)
	writeData(t, filepath.Join(root, "sub", "a.txt"), "old")
	h := newHarness(t, nil, root, filepath.Join(root, "sub"))
	h.pasteInto("a.txt", 1, ActPasteCopy)
	if v := h.a.Confirm(); v.Conflicts != 1 || v.TopLvl != 1 {
		t.Fatalf("Confirm = %+v", v)
	}
	h.confirm()
	if h.a.Screen() != ScreenConflicts {
		t.Fatalf("screen %v, want the conflicts", h.a.Screen())
	}
	v := h.a.Conflicts()
	if v.Unset != 1 || len(v.Rows) != 1 || v.Rows[0].Decision != fsops.DecisionUnset {
		t.Fatalf("Conflicts = %+v", v)
	}
	h.confirm()
	if got := readFile(t, filepath.Join(root, "sub", "a.txt")); got != "old" {
		t.Errorf("overwritten without a decision (U1): %q", got)
	}
	h.wantMessage(msg.Done(fsops.OpCopy, 0, 1, false)) // 選択によるスキップは画面を出さずに知らせる（filer §8.5）
}

// TestSelfConflictAutoRename は、同じフォルダへのコピーの衝突が、最初から自動リネームになっていることを確かめる（U1）。
func TestSelfConflictAutoRename(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.pasteInto("a.txt", 0, ActPasteCopy)
	h.confirm()
	if rows := h.a.Conflicts().Rows; len(rows) != 1 || rows[0].Decision != fsops.DecisionAutoRename {
		t.Fatalf("rows = %+v, want auto rename", rows)
	}
	h.confirm()
	if readFile(t, filepath.Join(root, "a.txt")) != "x" || !slices.Contains(h.names(0), "a (2).txt") {
		t.Errorf("names = %q, want a (2).txt next to the unchanged a.txt", h.names(0))
	}
}

// TestTypeahead は、計画を作っている間のキーを捨て、確認画面・衝突の画面は描いた後のキーでだけ進むことを確かめる（U2）。
func TestTypeahead(t *testing.T) {
	t.Parallel()
	root := tree(t)
	writeData(t, filepath.Join(root, "sub", "a.txt"), "old")
	h := newHarness(t, nil, root, filepath.Join(root, "sub"))
	h.hold = true
	h.pasteInto("a.txt", 1, ActPasteCopy)
	for _, k := range []ActionKind{ActSubmit, ActDown, ActSubmit, ActYes, ActQuit} {
		h.do(k) // 計画を作っている間に打たれたキー
	}
	h.release()
	if h.a.Screen() != ScreenConfirm || h.a.Quit() {
		t.Fatalf("screen %v, quit %v: keys during planning were not discarded", h.a.Screen(), h.a.Quit())
	}
	h.do(ActSubmit) // 確認画面を描く前に届いた Enter
	if h.a.Screen() != ScreenConfirm {
		t.Fatal("the confirmation was accepted by a key typed before it was drawn (U2)")
	}
	h.confirm()
	h.do(ActSubmit) // 衝突の画面を描く前に届いた Enter
	if h.a.Screen() != ScreenConflicts {
		t.Fatal("the conflicts were accepted by a key typed before they were drawn (U2)")
	}
	h.do(ActInsert) // 貼り付けなどの文字は、確認画面の操作にならない
	if h.a.Screen() != ScreenConflicts {
		t.Fatal("text input moved the conflicts screen")
	}
}

// TestPlanningCancel は、計画を作っている間の Esc で中止し、後から届いた計画を捨てることを確かめる。
func TestPlanningCancel(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root, filepath.Join(root, "sub"))
	h.hold = true
	h.pasteInto("a.txt", 1, ActPasteCopy)
	for _, c := range h.delayed {
		h.run(h.a.Update(c.Run())) // 0.2 秒が過ぎた
	}
	if !h.a.Planning() {
		t.Error("the planning notice is not shown after 0.2 s")
	}
	h.do(ActCancel)
	h.release()
	if h.a.Screen() != ScreenBrowse || h.a.Planning() {
		t.Errorf("screen %v after canceling the planning", h.a.Screen())
	}
	h.wantMessage(msg.Kind(fsops.KindCanceled))
}

// TestConflictDecisions は、1 行ずつの決定・まとめての決定・新しいときだけ上書き・展開・未選択だけの表示を確かめる（filer §8.3）。
func TestConflictDecisions(t *testing.T) {
	t.Parallel()
	root := tempDir(t)
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	for _, d := range []string{src, dst, filepath.Join(src, "d"), filepath.Join(dst, "d"), filepath.Join(dst, "x")} {
		mkdir(t, d)
	}
	base := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	files := []struct {
		path string
		mod  time.Time
	}{
		{filepath.Join(src, "f.txt"), base.Add(10 * time.Second)}, {filepath.Join(dst, "f.txt"), base},
		{filepath.Join(src, "same.txt"), base.Add(time.Second)}, {filepath.Join(dst, "same.txt"), base},
		{filepath.Join(src, "d", "in.txt"), base}, {filepath.Join(dst, "d", "in.txt"), base},
		{filepath.Join(src, "x"), base},
	}
	for _, f := range files {
		writeData(t, f.path, "new "+filepath.Base(f.path))
		if err := os.Chtimes(f.path, f.mod, f.mod); err != nil {
			t.Fatal(err)
		}
	}
	writeData(t, filepath.Join(dst, "f.txt"), "old f")
	writeData(t, filepath.Join(dst, "same.txt"), "old same")
	writeData(t, filepath.Join(dst, "d", "in.txt"), "old in")
	for _, f := range files[:6] {
		if err := os.Chtimes(f.path, f.mod, f.mod); err != nil {
			t.Fatal(err)
		}
	}
	h := newHarness(t, nil, src, dst)
	h.do(ActMarkAll)
	h.do(ActYank)
	h.do(ActNextPane)
	h.do(ActPasteCopy)
	h.confirm()
	names := func() []string {
		var out []string
		for _, r := range h.a.Conflicts().Rows {
			out = append(out, r.Name)
		}
		return out
	}
	v := h.a.Conflicts()
	if v.All != 5 || v.Top != 4 || v.Unset != 5 {
		t.Fatalf("counts %d %d %d, want 5 conflicts (4 top-level) all unset", v.All, v.Top, v.Unset)
	}
	// d の内側の衝突は、d がマージでないので件数だけの行にする。
	if got := names(); !slices.Equal(got, []string{"d", "", "f.txt", "same.txt", "x"}) || v.Rows[1].Inner != 1 || v.Rows[1].InnerHow != msg.InnerHidden {
		t.Fatalf("rows %q %+v", got, v.Rows[1])
	}
	h.do(ActEnd) // x（コピー元はファイル、コピー先はフォルダ）
	h.act(Action{Kind: ActDecide, Decision: fsops.DecisionOverwrite})
	h.wantMessage(msg.DecisionNotAllowed(fsops.DecisionOverwrite))
	if r := h.a.Conflicts().Rows[4]; r.Decision != fsops.DecisionUnset || r.Allowed[fsops.DecisionOverwrite] {
		t.Errorf("x: %+v", r)
	}
	h.act(Action{Kind: ActDecideAll, Decision: fsops.DecisionMerge})
	h.wantMessage(msg.NotChanged(4)) // マージを使えるのは d だけ
	if got := names(); !slices.Equal(got, []string{"d", "in.txt", "f.txt", "same.txt", "x"}) || h.a.Conflicts().Rows[1].Depth != 1 {
		t.Fatalf("after merging d: %q", got)
	}
	h.do(ActHome)
	h.do(ActToggle) // d を折りたたむ
	if rows := h.a.Conflicts().Rows; rows[1].ID != 0 || rows[1].InnerHow != msg.InnerCollapsed {
		t.Fatalf("collapsed: %+v", rows[1])
	}
	h.do(ActDown)
	h.do(ActToggle) // 件数の行で展開する
	if got := names(); got[1] != "in.txt" {
		t.Fatalf("expanded again: %q", got)
	}
	h.do(ActNewerOnly)
	h.wantMessage(msg.NotChanged(2)) // d と x はファイル同士でない
	want := map[string]fsops.Decision{"d": fsops.DecisionMerge, "in.txt": fsops.DecisionSkip, "f.txt": fsops.DecisionOverwrite,
		"same.txt": fsops.DecisionSkip, "x": fsops.DecisionUnset} // same.txt は 1 秒の差なので同じとみなす
	for _, r := range h.a.Conflicts().Rows {
		if r.Decision != want[r.Name] {
			t.Errorf("%s: %v, want %v", r.Name, r.Decision, want[r.Name])
		}
	}
	if r := h.a.Conflicts().Rows[2]; !r.SrcNewer || r.DstNewer {
		t.Errorf("f.txt newer marks: %+v", r)
	}
	h.do(ActUnsetOnly)
	if got := names(); !slices.Equal(got, []string{"x"}) {
		t.Errorf("unset only: %q", got)
	}
	h.do(ActUnsetOnly)
	h.confirm()
	if readFile(t, filepath.Join(dst, "f.txt")) != "new f.txt" || readFile(t, filepath.Join(dst, "same.txt")) != "old same" ||
		readFile(t, filepath.Join(dst, "d", "in.txt")) != "old in" {
		t.Error("the decisions were not applied")
	}
	h.wantMessage(msg.Done(fsops.OpCopy, 2, 3, false))
}

// TestAllowedMatchesFsops は、app の決定の規則が fsops の Decide と同じであることを確かめる。
func TestAllowedMatchesFsops(t *testing.T) {
	t.Parallel()
	root := tempDir(t)
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	for _, d := range []string{src, dst, filepath.Join(src, "d"), filepath.Join(dst, "d"), filepath.Join(dst, "x"), filepath.Join(src, "y")} {
		mkdir(t, d)
	}
	for _, f := range []string{filepath.Join(src, "f"), filepath.Join(dst, "f"), filepath.Join(src, "x"), filepath.Join(dst, "y")} {
		writeData(t, f, "z")
	}
	check := func(req fsops.Request) {
		plan, err := fsops.NewPlan(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan.Conflicts()) == 0 {
			t.Fatalf("no conflicts for %+v", req)
		}
		for _, c := range plan.Conflicts() {
			for d := fsops.DecisionUnset; d <= fsops.DecisionMerge; d++ {
				if got, want := allowed(c, d), plan.Decide(c.ID, d) == nil; got != want {
					t.Errorf("%s (self %v): allowed(%v) = %v, fsops says %v", c.Src, c.Self, d, got, want)
				}
			}
		}
	}
	var srcs []string
	for _, n := range []string{"d", "f", "x", "y"} {
		srcs = append(srcs, filepath.Join(src, n))
	}
	check(fsops.Request{Op: fsops.OpCopy, Sources: srcs, DestDir: dst})
	check(fsops.Request{Op: fsops.OpCopy, Sources: srcs, DestDir: src}) // 自分自身への衝突
}

// TestMarksAfterOperation は、完了した項目のマークを外し、それ以外（選択によるスキップ）のマークを残すことを確かめる（filer §6）。
func TestMarksAfterOperation(t *testing.T) {
	t.Parallel()
	root := tree(t)
	writeData(t, filepath.Join(root, "sub", "b.txt"), "old")
	h := newHarness(t, nil, root, filepath.Join(root, "sub"))
	h.moveTo("a.txt")
	h.do(ActMark)
	h.do(ActMark) // b.txt
	h.do(ActYank)
	h.do(ActNextPane)
	h.do(ActPasteCopy)
	h.confirm()
	h.confirm() // 衝突（b.txt）は未選択のまま
	if p := h.pane(0); p.Marked("a.txt") || !p.Marked("b.txt") {
		t.Errorf("marks: a.txt %v (want false), b.txt %v (want true)", p.Marked("a.txt"), p.Marked("b.txt"))
	}
}

// TestPasteNothing は、覚えていないときと、覚えるものがないときのメッセージを確かめる。
func TestPasteNothing(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.do(ActPasteCopy)
	h.wantMessage(msg.NothingYanked)
	h.do(ActHome) // ..
	h.do(ActYank)
	h.wantMessage(msg.NothingToYank)
}

// TestDestNotFound は、貼り付けるフォルダが消えていたとき、理由を出して終わることを確かめる（filer §8.1）。
func TestDestNotFound(t *testing.T) {
	t.Parallel()
	root := tree(t)
	gone := filepath.Join(root, "gone")
	mkdir(t, gone)
	h := newHarness(t, nil, root, gone)
	h.moveTo("a.txt")
	h.do(ActYank)
	if err := os.Remove(gone); err != nil { // テストが作ったフォルダ
		t.Fatal(err)
	}
	h.do(ActNextPane)
	h.do(ActPasteCopy)
	if h.a.Screen() != ScreenBrowse {
		t.Fatalf("screen %v", h.a.Screen())
	}
	h.wantMessage(msg.DestNotFound)
}

// TestResultOnFailure は、失敗した項目があれば結果の画面を出し、描く前のキーでは閉じず、L でもう一度出せることを確かめる（U3）。
func TestResultOnFailure(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the read-only attribute of a folder does not protect it on Windows; TestResultScreenRules covers the rules")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission of folders")
	}
	root := tree(t)
	ro := filepath.Join(root, "sub")
	if err := os.Chmod(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o755) })
	h := newHarness(t, nil, root, ro)
	h.pasteInto("a.txt", 1, ActPasteCopy)
	h.confirm()
	if h.a.Screen() != ScreenResult {
		t.Fatalf("screen %v, want the result (U3)", h.a.Screen())
	}
	v := h.a.Result()
	if len(v.Rows) != 1 || v.Rows[0].Outcome != fsops.OutcomeFailed || v.Rows[0].Reason == "" {
		t.Errorf("rows %+v", v.Rows)
	}
	h.do(ActSubmit) // 結果の画面を描く前に届いた Enter
	if h.a.Screen() != ScreenResult {
		t.Fatal("the result was closed by a key typed before it was drawn (U3)")
	}
	h.confirm()
	if h.a.Screen() != ScreenBrowse {
		t.Fatal("Enter did not close the result")
	}
	h.do(ActLastResult)
	if h.a.Screen() != ScreenResult {
		t.Error("L did not show the last result again")
	}
}

// fakePlan は、結果や進捗を自由に作るための計画（偽物）。
type fakePlan struct {
	req       fsops.Request
	conflicts []fsops.Conflict
	exec      func(ctx context.Context, opt fsops.ExecOptions) (*fsops.Result, error)
}

func (p *fakePlan) Request() fsops.Request { return p.req }
func (p *fakePlan) Items() []fsops.Item {
	var out []fsops.Item
	for _, s := range p.req.Sources {
		out = append(out, fsops.Item{Src: s, Dst: filepath.Join(p.req.DestDir, filepath.Base(s)), Method: fsops.MethodCopy})
	}
	return out
}
func (p *fakePlan) Conflicts() []fsops.Conflict { return p.conflicts }
func (p *fakePlan) Decide(id fsops.ConflictID, d fsops.Decision) error {
	p.conflicts[id-1].Decision = d
	return nil
}
func (p *fakePlan) TotalFiles() int            { return 4 }
func (p *fakePlan) TotalBytes() int64          { return 1000 }
func (p *fakePlan) Warnings() []*fsops.OpError { return nil }
func (p *fakePlan) Execute(ctx context.Context, opt fsops.ExecOptions) (*fsops.Result, error) {
	return p.exec(ctx, opt)
}

// fakeHarness は、計画を偽物 fp にした harness（pane 0 は root、pane 1 は root/sub）。now は時計。
func fakeHarness(t *testing.T, fp *fakePlan, now *time.Time) *harness {
	root := tree(t)
	return newHarness(t, func(c *Config) {
		c.NewPlan = func(_ context.Context, req fsops.Request) (Plan, error) {
			fp.req = req
			return fp, nil
		}
		if now != nil {
			c.Now = func() time.Time { return *now }
		}
	}, root, filepath.Join(root, "sub"))
}

// TestResultScreenRules は、結果の画面を出す場合と、メッセージ行だけにする場合を確かめる（filer §8.5。U3）。
func TestResultScreenRules(t *testing.T) {
	t.Parallel()
	failed := &fsops.OpError{Op: "copy", Kind: fsops.KindLocked}
	for _, tt := range []struct {
		name    string
		res     fsops.Result
		screen  bool
		message string
	}{
		{"done", fsops.Result{Status: fsops.StatusCompleted, Items: []fsops.ItemResult{{Outcome: fsops.OutcomeDone}}},
			false, msg.Done(fsops.OpCopy, 1, 0, false)},
		{"skipped by choice", fsops.Result{Status: fsops.StatusCompleted, Items: []fsops.ItemResult{{Outcome: fsops.OutcomeSkipped}}},
			false, msg.Done(fsops.OpCopy, 0, 1, false)},
		{"metadata warning", fsops.Result{Status: fsops.StatusCompleted, Items: []fsops.ItemResult{{Outcome: fsops.OutcomeDone,
			Warnings: []*fsops.OpError{{Kind: fsops.KindMetadata}}}}}, false, msg.Done(fsops.OpCopy, 1, 0, true)},
		{"failed", fsops.Result{Status: fsops.StatusCompletedWithErrors, Items: []fsops.ItemResult{{Outcome: fsops.OutcomeFailed, Err: failed}}}, true, ""},
		{"skipped with an error", fsops.Result{Status: fsops.StatusCompletedWithErrors, Items: []fsops.ItemResult{{Outcome: fsops.OutcomeSkipped,
			Err: &fsops.OpError{Kind: fsops.KindExist}}}}, true, ""},
		{"canceled", fsops.Result{Status: fsops.StatusCanceled, Items: []fsops.ItemResult{{Outcome: fsops.OutcomeDone}}}, true, ""},
		{"error inside a done folder", fsops.Result{Status: fsops.StatusCompletedWithErrors, Items: []fsops.ItemResult{{Outcome: fsops.OutcomeDone,
			Details: []fsops.EntryResult{{Outcome: fsops.OutcomeFailed, Err: failed}}}}}, true, ""},
		{"source kept", fsops.Result{Status: fsops.StatusCompletedWithErrors, Items: []fsops.ItemResult{{Outcome: fsops.OutcomeCopiedSourceKept,
			Method: fsops.MethodCopyThenRemove}}}, true, ""},
		{"trash unconfirmed", fsops.Result{Status: fsops.StatusCompletedWithErrors, Items: []fsops.ItemResult{{Outcome: fsops.OutcomeTrashUnconfirmed}}}, true, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fp := &fakePlan{}
			fp.exec = func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
				res := tt.res
				for i := range res.Items {
					res.Items[i].Src = fp.req.Sources[0]
				}
				return &res, nil
			}
			h := fakeHarness(t, fp, nil)
			h.pasteInto("a.txt", 1, ActPasteCopy)
			h.confirm()
			if got := h.a.Screen() == ScreenResult; got != tt.screen {
				t.Fatalf("result screen %v, want %v", got, tt.screen)
			}
			if !tt.screen {
				h.wantMessage(tt.message)
			}
		})
	}
}

// TestResultRows は、結果の一覧の並べ方（問題のあるもの、スキップ、完了の順）と、詳細の展開・英語の詳細を確かめる（filer §8.5）。
func TestResultRows(t *testing.T) {
	t.Parallel()
	locked := &fsops.OpError{Op: "copy", Path: "/p", Kind: fsops.KindLocked}
	fp := &fakePlan{}
	fp.exec = func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
		s := fp.req.Sources
		return &fsops.Result{Status: fsops.StatusCompletedWithErrors, Items: []fsops.ItemResult{
			{Src: s[0], Outcome: fsops.OutcomeDone},
			{Src: s[1], Outcome: fsops.OutcomeSkipped},
			{Src: s[2], Outcome: fsops.OutcomePartial, Method: fsops.MethodCopy, Details: []fsops.EntryResult{
				{Src: filepath.Join(s[2], "in.txt"), Outcome: fsops.OutcomeFailed, Err: locked}}},
		}}, nil
	}
	h := fakeHarness(t, fp, nil)
	h.do(ActMarkAll)
	h.do(ActYank)
	h.do(ActNextPane)
	h.do(ActPasteCopy)
	h.confirm()
	v := h.a.Result()
	var got []fsops.Outcome
	for _, r := range v.Rows {
		got = append(got, r.Outcome)
	}
	if !slices.Equal(got, []fsops.Outcome{fsops.OutcomePartial, fsops.OutcomeSkipped, fsops.OutcomeDone}) {
		t.Fatalf("order %v", got)
	}
	if v.Rows[0].Reason != msg.Partial(fsops.MethodCopy, fsops.OutcomePartial) || v.Rows[1].Reason != msg.SkippedByChoice || !v.Rows[0].Expandable {
		t.Errorf("reasons %+v", v.Rows)
	}
	h.a.Drawn()
	h.do(ActToggle)
	v = h.a.Result()
	if len(v.Rows) != 4 || !v.Rows[1].Detail || v.Rows[1].Name != "in.txt" || v.Rows[1].Reason != msg.Kind(fsops.KindLocked) {
		t.Fatalf("expanded rows %+v", v.Rows)
	}
	h.do(ActEnglish)
	if r := h.a.Result().Rows[1]; r.Reason != locked.Error() {
		t.Errorf("English detail %q, want %q", r.Reason, locked.Error())
	}
	if c := h.a.Result().Counts; len(c) != 3 || c[0].Outcome != fsops.OutcomePartial {
		t.Errorf("counts %+v", c)
	}
}

// TestProgressAndCancel は、進捗（速度・残り時間）と、中止の確認（描いた後の y でだけ中止）を確かめる（filer §8.4。U2）。
// 実行中は、ほかの操作を受け付けない。
func TestProgressAndCancel(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	fp := &fakePlan{}
	fp.exec = func(ctx context.Context, opt fsops.ExecOptions) (*fsops.Result, error) {
		opt.Progress(fsops.Progress{Stage: fsops.StageCopy, Current: fp.req.Sources[0], DoneFiles: 1, TotalFiles: 4, DoneBytes: 200, TotalBytes: 1000})
		close(started)
		<-ctx.Done()
		return &fsops.Result{Status: fsops.StatusCanceled, Items: []fsops.ItemResult{
			{Src: fp.req.Sources[0], Outcome: fsops.OutcomeSkipped, Err: &fsops.OpError{Kind: fsops.KindCanceled}}}}, nil
	}
	h := fakeHarness(t, fp, &now)
	h.pasteInto("a.txt", 1, ActPasteCopy)
	h.hold = true
	h.confirm()
	if h.a.Screen() != ScreenProgress || len(h.held) != 1 {
		t.Fatalf("screen %v, held %d", h.a.Screen(), len(h.held))
	}
	result := make(chan any, 1)
	go func() { result <- h.held[0].Run() }()
	<-started
	now = now.Add(2 * time.Second)
	h.a.Refresh()
	p := h.a.Progress()
	if p.DoneFiles != 1 || p.Elapsed != 2*time.Second || p.Speed != 100 || p.Remaining != 8*time.Second {
		t.Errorf("progress %+v", p)
	}
	h.do(ActQuit)
	h.do(ActDown)
	if h.a.Quit() || h.a.Screen() != ScreenProgress {
		t.Fatal("accepted another operation while running (filer §7)")
	}
	h.do(ActCancel)
	h.do(ActYes) // 中止の確認を描く前に届いた y
	if p := h.a.Progress(); !p.AskCancel || p.Canceling {
		t.Fatalf("canceled by a key typed before the question was drawn (U2): %+v", p)
	}
	h.a.Drawn()
	h.do(ActYes)
	if !h.a.Progress().Canceling {
		t.Fatal("y did not cancel")
	}
	h.hold = false
	h.run(h.a.Update(<-result))
	if h.a.Screen() != ScreenResult || h.a.Result().Status != fsops.StatusCanceled {
		t.Errorf("screen %v after canceling, want the result (canceled)", h.a.Screen())
	}
}

// TestUnresponsive は、中止しても Execute が戻らないとき、10 秒後に知らせて Q で終われることを確かめる（filer §8.4）。
func TestUnresponsive(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	release, started := make(chan struct{}), make(chan struct{})
	fp := &fakePlan{}
	fp.exec = func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
		close(started)
		<-release // ctx を見ない（応答しないネットワークドライブ）
		return &fsops.Result{Status: fsops.StatusCanceled}, nil
	}
	h := fakeHarness(t, fp, &now)
	h.pasteInto("a.txt", 1, ActPasteCopy)
	h.hold = true
	h.confirm()
	done := make(chan struct{})
	go func() { h.held[0].Run(); close(done) }()
	t.Cleanup(func() { close(release); <-done })
	<-started
	h.do(ActCancel)
	h.a.Drawn()
	h.do(ActYes)
	tick := h.delayed[len(h.delayed)-1]
	now = now.Add(5 * time.Second)
	h.run(h.a.Update(tick.Run()))
	if h.a.Progress().Unresponsive {
		t.Fatal("unresponsive after 5 s")
	}
	h.do(ActForceQuit)
	if h.a.Quit() {
		t.Fatal("Q quit before the unresponsive notice")
	}
	now = now.Add(6 * time.Second)
	h.run(h.a.Update(tick.Run()))
	if !h.a.Progress().Unresponsive {
		t.Fatal("no unresponsive notice after 11 s")
	}
	h.do(ActForceQuit)
	if !h.a.Quit() {
		t.Error("Q did not quit")
	}
}

// TestAbort は、シグナルで終わるとき、実行中の操作を中止し、Execute が戻るのを待つことを確かめる（filer §10）。
func TestAbort(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	fp := &fakePlan{}
	fp.exec = func(ctx context.Context, _ fsops.ExecOptions) (*fsops.Result, error) {
		close(started)
		<-ctx.Done()
		return &fsops.Result{Status: fsops.StatusCanceled}, nil
	}
	h := fakeHarness(t, fp, nil)
	h.pasteInto("a.txt", 1, ActPasteCopy)
	h.hold = true
	h.confirm()
	finished := make(chan struct{})
	go func() { h.held[0].Run(); close(finished) }()
	<-started
	start := time.Now()
	h.a.Abort(time.Minute)
	select {
	case <-finished:
	default:
		t.Fatal("Abort returned before Execute returned")
	}
	if time.Since(start) > 30*time.Second {
		t.Error("Abort waited for the whole timeout")
	}
}

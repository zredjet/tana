package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops"
)

// 門（filer §4 の「UI の骨格」）: 画面を描く前に届いた操作を、画面ごと・操作ごとに、行う（free）か捨てる（afterdraw）か。
// 今の動作を写したもので、部品の作り直し（フェーズ22）の前後で変わらないことを確かめる。

// gateConflicts は、覚えた a.txt・b.txt の、ファイル同士の衝突（上書きを選べる）。
func gateConflicts(root string) []fsops.Conflict {
	file := func(t time.Time) fsops.EntryInfo { return fsops.EntryInfo{Type: fsops.TypeFile, Size: 1, ModTime: t} }
	newer, older := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC), time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var cs []fsops.Conflict
	for i, n := range []string{"a.txt", "b.txt"} {
		cs = append(cs, fsops.Conflict{ID: fsops.ConflictID(i + 1), Src: filepath.Join(root, n), Dst: filepath.Join(root, "sub", n),
			SrcInfo: file(newer), DstInfo: file(older)})
	}
	return cs
}

// gateFailed は、すべての項目が失敗する実行。最初の項目には、フォルダの中の結果（Space で展開できる）を付ける。
func gateFailed(fp *fakePlan) func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
	return func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
		res := &fsops.Result{Status: fsops.StatusCompletedWithErrors}
		for i, s := range fp.req.Sources {
			it := fsops.ItemResult{Src: s, Outcome: fsops.OutcomeFailed, Err: &fsops.OpError{Kind: fsops.KindLocked}}
			if i == 0 {
				it.Details = []fsops.EntryResult{{Src: filepath.Join(s, "x"), Outcome: fsops.OutcomeFailed, Err: &fsops.OpError{Kind: fsops.KindLocked}}}
			}
			res.Items = append(res.Items, it)
		}
		return res, nil
	}
}

// gateCopy は、フォルダ root（tree で作ったもの）で、a.txt と b.txt を覚えて sub に貼り付け、確認画面を出した harness（計画は偽物）。
func gateCopy(t *testing.T, root string, hold bool) *harness {
	t.Helper()
	fp := &fakePlan{}
	fp.exec = gateFailed(fp)
	h := fakeHarnessAt(t, root, fp, nil)
	fp.conflicts = gateConflicts(root)
	h.moveTo("a.txt")
	h.do(ActMark) // a.txt（カーソルは b.txt へ）
	h.do(ActMark) // b.txt
	h.do(ActYank)
	h.do(ActNextPane)
	h.hold = hold
	h.do(ActPasteCopy)
	return h
}

// gateStates は、門を確かめる画面と、その作り方。root は tree で作ったフォルダ。
// 描く前と後を比べる 2 つの harness は、同じ root で作る（状態を、パスを置き換えずにそのまま比べるため）。
var gateStates = []struct {
	name   string
	screen Screen
	dialog DialogKind
	build  func(t *testing.T, root string) *harness
}{
	{"help", ScreenBrowse, DialogHelp, func(t *testing.T, root string) *harness {
		h := newHarness(t, nil, root)
		h.do(ActHelp)
		return h
	}},
	{"path", ScreenBrowse, DialogPath, func(t *testing.T, root string) *harness {
		h := newHarness(t, nil, root)
		h.do(ActGoPath)
		return h
	}},
	{"rename", ScreenBrowse, DialogRename, func(t *testing.T, root string) *harness {
		h := newHarness(t, nil, root)
		h.moveTo("a.txt")
		h.do(ActRename)
		return h
	}},
	{"newdir", ScreenBrowse, DialogNewDir, func(t *testing.T, root string) *harness {
		h := newHarness(t, nil, root)
		h.do(ActNewDir)
		return h
	}},
	{"exec", ScreenBrowse, DialogExec, func(t *testing.T, root string) *harness {
		h := newHarness(t, func(c *Config) { c.IsExecutable = func(string, bool) bool { return true } }, root)
		h.moveTo("a.txt")
		h.do(ActEnter)
		return h
	}},
	{"planning", ScreenBrowse, DialogNone, func(t *testing.T, root string) *harness {
		h := gateCopy(t, root, true)
		h.fire() // 0.2 秒が過ぎた（「計画を作成中」を出す。Planning で見分けられるように）
		if !h.a.Planning() {
			t.Fatal("no planning notice")
		}
		return h
	}},
	{"confirm", ScreenConfirm, DialogNone, func(t *testing.T, root string) *harness { return gateCopy(t, root, false) }},
	{"conflicts", ScreenConflicts, DialogNone, func(t *testing.T, root string) *harness {
		h := gateCopy(t, root, false)
		h.confirm()
		return h
	}},
	{"progress", ScreenProgress, DialogNone, func(t *testing.T, root string) *harness {
		h := gateCopy(t, root, false)
		h.confirm()
		h.a.Drawn()
		h.hold = true
		h.do(ActSubmit) // 実行は held に残す
		return h
	}},
	{"cancelask", ScreenProgress, DialogNone, func(t *testing.T, root string) *harness {
		h := gateCopy(t, root, false)
		h.confirm()
		h.a.Drawn()
		h.hold = true
		h.do(ActSubmit)
		h.do(ActCancel)
		return h
	}},
	{"result", ScreenResult, DialogNone, func(t *testing.T, root string) *harness {
		h := gateCopy(t, root, false)
		h.confirm()
		h.confirm()
		return h
	}},
	{"result-trash", ScreenResult, DialogNone, func(t *testing.T, root string) *harness {
		fp := &fakePlan{}
		fp.exec = trashResult(fp, map[string]fsops.ItemResult{"a.txt": {Outcome: fsops.OutcomeFailed, Err: unavailable()}})
		h := trashHarnessAt(t, root, fp, new([]fsops.OpKind), nil)
		h.moveTo("a.txt")
		h.do(ActTrash)
		h.confirm()
		return h
	}},
	{"delete", ScreenDelete, DialogNone, func(t *testing.T, root string) *harness {
		h := trashHarnessAt(t, root, &fakePlan{}, new([]fsops.OpKind), nil)
		h.moveTo("a.txt")
		h.do(ActPurge)
		return h
	}},
}

// gateWant は、画面ごとの門の今の動作。載っていない操作は、描く前も後も何もしない（none）。
var gateWant = map[string]map[ActionKind]string{
	"path": {ActInsert: "free", ActBackspace: "free", ActLeft: "free", ActLineHome: "free", ActSubmit: "free", ActCancel: "free"},
	"rename": {ActInsert: "free", ActBackspace: "free", ActDelete: "free", ActLeft: "free", ActRight: "free", ActLineHome: "free",
		ActLineEnd: "free", ActSubmit: "free", ActCancel: "free"},
	"newdir":    {ActInsert: "free", ActSubmit: "free", ActCancel: "free"},
	"exec":      {ActYes: "afterdraw", ActNo: "afterdraw", ActCancel: "afterdraw"},
	"planning":  {ActCancel: "free"},
	"confirm":   {ActCancel: "free", ActSubmit: "afterdraw"},
	"conflicts": {ActCancel: "free", ActSubmit: "afterdraw", ActDown: "afterdraw", ActPageDown: "afterdraw", ActEnd: "afterdraw", ActDecide: "afterdraw", ActDecideAll: "afterdraw", ActNewerOnly: "afterdraw", ActUnsetOnly: "afterdraw"},
	"progress":  {ActCancel: "free"},
	"cancelask": {ActYes: "afterdraw", ActNo: "free", ActCancel: "free"},
	"result": {ActDown: "free", ActPageDown: "free", ActEnd: "free", ActToggle: "free", ActEnglish: "free",
		ActSubmit: "afterdraw", ActCancel: "afterdraw"},
	"result-trash": {ActEnglish: "free", ActSubmit: "afterdraw", ActCancel: "afterdraw", ActPurge: "afterdraw"},
	"delete":       {ActCancel: "free", ActYes: "afterdraw", ActNo: "afterdraw", ActSubmit: "afterdraw"},
}

func init() {
	// ヘルプは Esc で閉じる（どのキーも Esc にするのは tui の keymap。フェーズ23）。
	gateWant["help"] = map[ActionKind]string{ActCancel: "free"}
}

// gateSig は、門で比べる状態（メッセージ行は除く。キー入力のたびに門より前で消すため）。
func gateSig(h *harness) string {
	a := h.a
	var b strings.Builder
	fmt.Fprintf(&b, "screen=%v dialog=%v quit=%v planning=%v opened=%d active=%d held=%d", a.Screen(), a.Dialog(), a.Quit(), a.Planning(),
		len(h.opened), a.Active(), len(h.held))
	for i, p := range a.Panes() {
		_, marks, _ := p.Counts()
		fmt.Fprintf(&b, " pane%d=%s:%d:%d", i, p.Dir(), p.Cursor(), marks)
	}
	switch a.Screen() {
	case ScreenConfirm:
		fmt.Fprintf(&b, " %+v", a.Confirm())
	case ScreenConflicts:
		fmt.Fprintf(&b, " %+v", a.Conflicts())
	case ScreenProgress:
		fmt.Fprintf(&b, " %+v", a.Progress())
	case ScreenResult:
		fmt.Fprintf(&b, " %+v", a.Result())
	case ScreenDelete:
		fmt.Fprintf(&b, " %+v", a.Delete())
	}
	switch a.Dialog() {
	case DialogPath:
		e := a.PathEditor()
		fmt.Fprintf(&b, " edit=%q@%d", e.Text(), e.Cursor())
	case DialogRename, DialogNewDir:
		v := a.NameDialog()
		fmt.Fprintf(&b, " edit=%q@%d err=%q busy=%v", v.Edit.Text(), v.Edit.Cursor(), v.Err, v.Busy)
	case DialogExec:
		b.WriteString(" exec=" + a.ExecName())
	}
	return b.String()
}

// gateAction は、門を確かめる操作 k（決定は上書き、文字は x）。
func gateAction(k ActionKind) Action {
	return Action{Kind: k, Text: "x", Decision: fsops.DecisionOverwrite}
}

// TestGateTable は、画面ごと・操作ごとに、描く前に届いた操作を行うか捨てるかを確かめる（U2・U3）。
// free: 描く前も後も同じ結果になる。afterdraw: 描く前は何も変えず、描いた後は変える。none: どちらも何も変えない。
func TestGateTable(t *testing.T) {
	t.Parallel()
	for _, st := range gateStates {
		t.Run(st.name, func(t *testing.T) {
			t.Parallel()
			want := gateWant[st.name]
			for k := ActUp; k <= ActNewDir; k++ {
				root := tree(t)
				before := st.build(t, root)
				if before.a.Screen() != st.screen || before.a.Dialog() != st.dialog {
					t.Fatalf("built screen %v dialog %v, want %v %v", before.a.Screen(), before.a.Dialog(), st.screen, st.dialog)
				}
				initB := gateSig(before)
				before.act(gateAction(k))
				resB := gateSig(before)
				after := st.build(t, root) // 同じフォルダで作る（描く前の harness は、ファイルシステムを変えていない）
				after.a.Drawn()
				initA := gateSig(after)
				after.act(gateAction(k))
				resA := gateSig(after)
				got := "none"
				switch changedB, changedA := resB != initB, resA != initA; {
				case !changedB && !changedA:
				case !changedB:
					got = "afterdraw"
				case resB == resA:
					got = "free"
				default:
					got = "other"
				}
				w := want[k]
				if w == "" {
					w = "none"
				}
				if got != w {
					t.Errorf("action %d: %s, want %s", k, got, w)
				}
			}
		})
	}
}

// progressHarness は、実行中の進捗の画面の harness を作る。実行は ctx が終わるまで、steps の数だけ進捗を送る。
// next に値を送るたびに、次の進捗を送り、sent に知らせる。
func progressHarness(t *testing.T, now *time.Time) (h *harness, next chan struct{}, sent chan struct{}) {
	t.Helper()
	fp := &fakePlan{}
	next, sent = make(chan struct{}), make(chan struct{})
	fp.exec = func(ctx context.Context, opt fsops.ExecOptions) (*fsops.Result, error) {
		for i := 1; ; i++ {
			select {
			case <-next:
			case <-ctx.Done():
				return &fsops.Result{Status: fsops.StatusCanceled}, nil
			}
			opt.Progress(fsops.Progress{Stage: fsops.StageCopy, Current: fp.req.Sources[0], DoneFiles: i, TotalFiles: 4})
			sent <- struct{}{}
		}
	}
	h = fakeHarness(t, fp, now)
	h.pasteInto("a.txt", 1, ActPasteCopy)
	h.hold = true
	h.confirm()
	if h.a.Screen() != ScreenProgress || len(h.held) != 1 {
		t.Fatalf("screen %v, held %d", h.a.Screen(), len(h.held))
	}
	run := h.held[0]
	h.held = nil
	done := make(chan struct{})
	go func() { run.Run(); close(done) }()
	t.Cleanup(func() { h.a.Abort(time.Second); <-done })
	return h, next, sent
}

// TestRefreshWhileAsking は、中止の確認を出している間も、進捗を読み直すことを確かめる（filer §8.4）。
func TestRefreshWhileAsking(t *testing.T) {
	t.Parallel()
	h, next, sent := progressHarness(t, nil)
	next <- struct{}{}
	<-sent
	h.a.Refresh()
	h.do(ActCancel) // 中止の確認
	if !h.a.Progress().AskCancel {
		t.Fatal("no cancel question")
	}
	next <- struct{}{}
	<-sent
	h.a.Refresh()
	if p := h.a.Progress(); p.DoneFiles != 2 || !p.AskCancel {
		t.Errorf("progress while asking: %+v, want 2 files done", p)
	}
}

// TestTickWhileAsking は、中止の確認を出している間も、経過時間の描き直しの知らせが続くことを確かめる（filer §8.4）。
func TestTickWhileAsking(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	h, _, _ := progressHarness(t, &now)
	h.do(ActCancel)
	tick := h.delayed[len(h.delayed)-1]
	n := len(h.delayed)
	now = now.Add(3 * time.Second)
	h.run(h.a.Update(tick.Run()))
	if len(h.delayed) != n+1 {
		t.Errorf("delayed %d after a tick while asking, want %d (the next tick)", len(h.delayed), n+1)
	}
	if p := h.a.Progress(); p.Elapsed != 3*time.Second || !p.AskCancel {
		t.Errorf("progress %+v, want 3 s elapsed while asking", p)
	}
}

// TestLastResultKeepsState は、L で結果の画面を開き直したとき、カーソル・展開・英語の詳細が保たれることを確かめる。
func TestLastResultKeepsState(t *testing.T) {
	t.Parallel()
	h := gateCopy(t, tree(t), false)
	h.confirm()
	h.confirm()
	if h.a.Screen() != ScreenResult {
		t.Fatalf("screen %v", h.a.Screen())
	}
	h.do(ActToggle)  // 最初の項目の中を展開する
	h.do(ActEnglish) // 英語の詳細
	h.do(ActDown)
	want := h.a.Result()
	h.a.Drawn()
	h.do(ActSubmit)
	if h.a.Screen() != ScreenBrowse {
		t.Fatalf("screen %v after closing the result", h.a.Screen())
	}
	h.do(ActLastResult)
	if got := h.a.Result(); fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
		t.Errorf("reopened result:\n got %+v\nwant %+v", got, want)
	}
}

// TestExecNotUnderOverlay は、開く前の確認の間にほかの画面を開いたら、実行の確認を出さないことを確かめる（filer §7）。
// 出すと、ほかの画面の下に隠れたまま（またはヘルプを置き換えて）、描いた後の扱いになり、見ていない確認を確定できる（U2）。
func TestExecNotUnderOverlay(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name    string
		overlay func(h *harness)
		screen  Screen
		dialog  DialogKind
	}{
		{"help", func(h *harness) { h.do(ActHelp) }, ScreenBrowse, DialogHelp},
		{"path", func(h *harness) { h.do(ActGoPath) }, ScreenBrowse, DialogPath},
		{"last result", func(h *harness) { h.do(ActLastResult) }, ScreenResult, DialogNone},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fp := &fakePlan{}
			fp.exec = gateFailed(fp)
			root := tree(t)
			h := newHarness(t, func(c *Config) {
				c.NewPlan = func(_ context.Context, req fsops.Request) (Plan, error) { fp.req = req; return fp, nil }
				c.IsExecutable = func(string, bool) bool { return true }
			}, root, filepath.Join(root, "sub"))
			// 直前の操作の結果を作っておく（L で開き直せるように）。
			h.pasteInto("b.txt", 1, ActPasteCopy)
			h.confirm()
			h.a.Drawn()
			h.do(ActSubmit)
			h.do(ActNextPane)
			h.moveTo("a.txt")
			h.hold = true
			h.do(ActEnter) // 開く前の確認（実行ファイルか）は held に残る
			tt.overlay(h)
			h.release()
			if h.a.Screen() != tt.screen || h.a.Dialog() != tt.dialog {
				t.Fatalf("screen %v dialog %v, want the %s kept (the exec confirmation must not replace or hide under it)", h.a.Screen(), h.a.Dialog(), tt.name)
			}
			h.a.Drawn()
			h.do(ActCancel) // ほかの画面を閉じる
			if tt.screen == ScreenResult {
				h.do(ActSubmit)
			}
			if h.a.Screen() != ScreenBrowse || h.a.Dialog() != DialogNone || len(h.opened) != 0 {
				t.Errorf("after closing: screen %v dialog %v opened %q, want nothing left", h.a.Screen(), h.a.Dialog(), h.opened)
			}
		})
	}
	t.Run("link", func(t *testing.T) {
		t.Parallel()
		root := tree(t)
		if err := os.Symlink("a.txt", filepath.Join(root, "filelink")); err != nil {
			t.Skipf("cannot create a symbolic link: %v", err)
		}
		h := newHarness(t, func(c *Config) { c.IsExecutable = func(string, bool) bool { return true } }, root)
		h.moveTo("filelink")
		h.hold = true
		h.do(ActEnter) // リンクに入る読み込み（リンク先はファイルなので、開く処理に移る）
		h.do(ActHelp)
		h.release()
		h.release()
		if h.a.Dialog() != DialogHelp {
			t.Errorf("dialog %v, want the help kept", h.a.Dialog())
		}
	})
}

// TestPaneResultsByID は、作業用の goroutine の結果が、並びの番号ではなくペインの ID で届くことを確かめる（filer §4）。
// ペインの並びが変わっても別のペインに届かず、なくなったペイン宛ての結果は捨てる。
func TestPaneResultsByID(t *testing.T) {
	t.Parallel()
	root := tree(t)
	other := filepath.Join(root, "other")
	mkdir(t, other)
	h := newHarness(t, nil, root, other)
	h.moveTo("sub")
	h.hold = true
	h.do(ActEnter) // ペイン 0 で sub に入る（読み込みは held に残す）
	h.a.panes[0], h.a.panes[1] = h.a.panes[1], h.a.panes[0]
	h.release()
	if got := h.pane(1).Dir(); got != filepath.Join(root, "sub") {
		t.Errorf("the pane that entered sub shows %q", got)
	}
	if got := h.pane(0).Dir(); got != other {
		t.Errorf("the other pane shows %q, want %q unchanged", got, other)
	}
	h.hold = true
	h.do(ActParent)           // sub のペインで親へ（読み込みは held に残す）
	h.a.panes = h.a.panes[:1] // 読み込みを始めたペインがなくなった
	h.a.active = h.a.panes[0].id
	h.release()
	if got := h.pane(0).Dir(); got != other {
		t.Errorf("a result for a removed pane changed another pane: %q", got)
	}
}

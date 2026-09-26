package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/keys"
	"github.com/zredjet/tana/internal/msg"
)

// opPlan は、ファイル操作の画面を描くための計画（偽物）。
type opPlan struct {
	req       fsops.Request
	notFound  string // この名前の項目は実行されない（計画の時点で見つからない）
	conflicts []fsops.Conflict
	warnings  []*fsops.OpError
	exec      func(ctx context.Context, opt fsops.ExecOptions) (*fsops.Result, error)
}

func (p *opPlan) Request() fsops.Request { return p.req }
func (p *opPlan) Items() []fsops.Item {
	var out []fsops.Item
	for _, s := range p.req.Sources {
		it := fsops.Item{Src: s, Dst: filepath.Join(p.req.DestDir, filepath.Base(s)), Method: fsops.MethodCopy}
		if filepath.Base(s) == p.notFound {
			it.Err = &fsops.OpError{Op: "plan", Path: s, Kind: fsops.KindNotFound}
		}
		out = append(out, it)
	}
	return out
}
func (p *opPlan) Conflicts() []fsops.Conflict { return p.conflicts }
func (p *opPlan) Decide(id fsops.ConflictID, d fsops.Decision) error {
	p.conflicts[id-1].Decision = d
	return nil
}
func (p *opPlan) TotalFiles() int            { return 58 }
func (p *opPlan) TotalBytes() int64          { return 1395864371 }
func (p *opPlan) Warnings() []*fsops.OpError { return p.warnings }
func (p *opPlan) Execute(ctx context.Context, opt fsops.ExecOptions) (*fsops.Result, error) {
	return p.exec(ctx, opt)
}

// opConflicts は、見本の衝突（filer §8.3 のモック）。写真/ の内側に 2 件、議事録/ の内側に 2 件。
func opConflicts(src, dst string) []fsops.Conflict {
	dir := func(t time.Time) fsops.EntryInfo { return fsops.EntryInfo{Type: fsops.TypeDir, ModTime: t} }
	file := func(size int64, t time.Time) fsops.EntryInfo {
		return fsops.EntryInfo{Type: fsops.TypeFile, Size: size, ModTime: t}
	}
	c := func(id, parent int, rel string, s, d fsops.EntryInfo) fsops.Conflict {
		return fsops.Conflict{ID: fsops.ConflictID(id), Parent: fsops.ConflictID(parent), Src: filepath.Join(src, rel), Dst: filepath.Join(dst, rel), SrcInfo: s, DstInfo: d}
	}
	return []fsops.Conflict{
		c(1, 0, "写真", dir(at(9, 12, 9, 15, 0)), dir(at(8, 2, 12, 0, 0))),
		c(2, 0, "議事録", dir(at(9, 20, 18, 2, 0)), dir(at(9, 1, 10, 0, 0))),
		c(3, 0, "メモ.txt", file(1234567, at(9, 24, 11, 19, 0)), file(1153434, at(9, 20, 9, 0, 0))),
		c(4, 1, filepath.Join("写真", "IMG_0012.jpg"), file(2<<20, at(9, 1, 0, 0, 0)), file(2<<20, at(9, 1, 0, 0, 1))),
		c(5, 1, filepath.Join("写真", "IMG_0013.jpg"), file(3<<20, at(9, 2, 0, 0, 0)), file(3<<20, at(8, 2, 0, 0, 0))),
		c(6, 2, filepath.Join("議事録", "2026-09-01.md"), file(4096, at(9, 1, 10, 0, 0)), file(4096, at(9, 1, 10, 0, 0))),
		c(7, 2, filepath.Join("議事録", "2026-09-08.md"), file(5324, at(9, 8, 10, 0, 0)), file(4198, at(9, 8, 9, 0, 0))),
	}
}

// newOpScene は、2 ペインの表示で、左（colDir）の 写真・議事録・メモ.txt と、見つからない old.txt を覚え、
// 右（colHome）に貼り付けて確認画面を出した場面を作る。now は時計。
func newOpScene(t *testing.T, op fsops.OpKind, exec func(ctx context.Context, opt fsops.ExecOptions) (*fsops.Result, error), now *time.Time) (*scene, *opPlan) {
	t.Helper()
	plan := &opPlan{notFound: "old.txt", warnings: []*fsops.OpError{{Kind: fsops.KindNoSpace, Path: colHome}}, exec: exec}
	sc := newColumnsSceneWith(t, func(c *app.Config) {
		c.NewPlan = func(_ context.Context, req fsops.Request) (app.Plan, error) {
			plan.req = req
			plan.req.Sources = append(plan.req.Sources, filepath.Join(filepath.Dir(req.Sources[0]), "old.txt"))
			plan.conflicts = opConflicts(filepath.Dir(req.Sources[0]), req.DestDir)
			return plan, nil
		}
		if now != nil {
			c.Now = func() time.Time { return *now }
		}
	})
	sc.keys(char('v')) // 2 ペインに戻す
	for _, n := range []string{"写真", "議事録", "メモ.txt"} {
		sc.moveTo(n)
		sc.keys(char(' '))
	}
	sc.keys(char('y'), key(keys.KeyTab))
	if op == fsops.OpMove {
		sc.keys(char('P'))
	} else {
		sc.keys(char('p'))
	}
	if sc.a.Screen() != app.ScreenConfirm {
		t.Fatalf("screen %v, want the confirmation", sc.a.Screen())
	}
	return sc, plan
}

// TestGoldenConfirmAndConflicts は、確認画面と衝突の画面を描く（filer §8.2・§8.3）。
func TestGoldenConfirmAndConflicts(t *testing.T) {
	t.Parallel()
	sc, _ := newOpScene(t, fsops.OpCopy, nil, nil)
	golden(t, "op-confirm-80x24", sc.draw(80, 24))
	sc.keys(key(keys.KeyEnter)) // 描いた後の Enter で、衝突の画面へ
	if sc.a.Screen() != app.ScreenConflicts {
		t.Fatalf("screen %v", sc.a.Screen())
	}
	golden(t, "op-conflicts-80x24", sc.draw(80, 24))
	sc.keys(char('m'))                            // 写真/ をマージ（内側の衝突を出す）
	sc.keys(key(keys.KeyDown), key(keys.KeyDown)) // IMG_0013.jpg
	sc.keys(char('o'))
	sc.keys(key(keys.KeyEnd), char('m')) // メモ.txt（ファイル）にマージは使えない
	golden(t, "op-conflicts-decided-120x40", sc.draw(120, 40))
	if text, isErr := sc.a.Message(); !isErr || text != msg.DecisionNotAllowed(fsops.DecisionMerge) {
		t.Errorf("message %q", text)
	}
}

// TestGoldenProgress は、進捗の画面と中止の確認を描く（filer §8.4）。
func TestGoldenProgress(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	sc, plan := newOpScene(t, fsops.OpCopy, nil, &now)
	plan.exec = func(ctx context.Context, opt fsops.ExecOptions) (*fsops.Result, error) {
		opt.Progress(fsops.Progress{Stage: fsops.StageCopy, Current: colDir + "/議事録/2026-09-08.md", // 画面に出すので、どの OS でも同じ文字列
			DoneFiles: 26, TotalFiles: 58, DoneBytes: 626000000, TotalBytes: 1395864371})
		close(started)
		<-ctx.Done()
		return &fsops.Result{Status: fsops.StatusCanceled, Items: []fsops.ItemResult{{Src: plan.req.Sources[0], Outcome: fsops.OutcomeSkipped,
			Err: &fsops.OpError{Kind: fsops.KindCanceled}}}}, nil
	}
	sc.draw(80, 24)
	sc.keys(key(keys.KeyEnter))
	sc.draw(80, 24)
	sc.hold = true
	sc.keys(key(keys.KeyEnter)) // 衝突の画面で実行
	if sc.a.Screen() != app.ScreenProgress || len(sc.held) != 1 {
		t.Fatalf("screen %v, held %d", sc.a.Screen(), len(sc.held))
	}
	result := make(chan any, 1)
	go func() { result <- sc.held[0].Run() }()
	<-started
	now = now.Add(7 * time.Second)
	sc.a.Refresh()
	golden(t, "op-progress-80x24", sc.draw(80, 24))
	sc.keys(key(keys.KeyEsc))
	golden(t, "op-cancel-80x24", sc.draw(80, 24))
	sc.keys(char('y'))
	golden(t, "op-canceling-80x24", sc.draw(80, 24))
	sc.hold = false
	sc.run(sc.a.Update(<-result))
	if sc.a.Screen() != app.ScreenResult {
		t.Fatalf("screen %v after canceling", sc.a.Screen())
	}
}

// TestGoldenResult は、結果の画面を描く（filer §8.5）。問題のあるものを先に並べ、詳細を展開し、英語の詳細を出す。
func TestGoldenResult(t *testing.T) {
	t.Parallel()
	locked := &fsops.OpError{Op: "remove", Path: filepath.Join(colDir, "写真", "IMG_0013.jpg"), Kind: fsops.KindLocked}
	var plan *opPlan
	sc, plan := newOpScene(t, fsops.OpMove, func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
		s := plan.req.Sources
		return &fsops.Result{Status: fsops.StatusCompletedWithErrors, Items: []fsops.ItemResult{
			{Src: s[0], Outcome: fsops.OutcomeCopiedSourceKept, Method: fsops.MethodCopyThenRemove, Details: []fsops.EntryResult{
				{Src: filepath.Join(s[0], "IMG_0012.jpg"), Outcome: fsops.OutcomeCopiedSourceKept, Err: &fsops.OpError{Kind: fsops.KindSourceChanged}},
				{Src: filepath.Join(s[0], "IMG_0013.jpg"), Outcome: fsops.OutcomeCopiedSourceKept, Err: locked}}},
			{Src: s[1], Outcome: fsops.OutcomeDone, Details: []fsops.EntryResult{{Src: filepath.Join(s[1], "2026-09-01.md"), Outcome: fsops.OutcomeSkipped}}},
			{Src: s[2], Outcome: fsops.OutcomeSkipped},
			{Src: s[3], Outcome: fsops.OutcomeFailed, Err: &fsops.OpError{Kind: fsops.KindNotFound}},
		}}, nil
	}, nil)
	sc.draw(80, 24)
	sc.keys(key(keys.KeyEnter))
	sc.draw(80, 24)
	sc.keys(key(keys.KeyEnter))
	if sc.a.Screen() != app.ScreenResult {
		t.Fatalf("screen %v", sc.a.Screen())
	}
	golden(t, "op-result-80x24", sc.draw(80, 24))
	sc.keys(char(' '), char('e'))
	golden(t, "op-result-expanded-120x40", sc.draw(120, 40))
	sc.keys(key(keys.KeyEnter)) // 描いた後なので閉じる
	golden(t, "op-after-80x24", sc.draw(80, 24))
}

// TestRunFilerCopy は、イベントループと本物の fsops で、覚えて貼り付けるコピーが最後まで動くことを確かめる。
// 画面に描かれたものを見てキーを送る（確認画面は、描かれた後の Enter で進む。filer U2）。
func TestRunFilerCopy(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src, dst := filepath.Join(root, "src"), filepath.Join(root, "dst")
	for _, d := range []string{src, dst} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFakeTerm(80, 24)
	step := 0
	f.onWrite = func(out string) {
		switch {
		case step == 0 && strings.Contains(out, "hello.txt"):
			step++
			f.send("jy\tp") // hello.txt を覚え、右のペインに移って貼り付ける
		case step == 1 && strings.Contains(out, "へコピーします"):
			step++
			f.send("\r")
		case step == 2 && strings.Contains(out, "コピーしました"): // 差分だけを書くので、直前のメッセージと同じ先頭は書かれない
			step++
			f.send("q")
		}
	}
	cfg := app.DefaultConfig([]string{src, dst})
	cfg.Open = func(p string) error { t.Errorf("opened %s", p); return nil }
	a, cmds := app.New(cfg)
	if err := RunFiler(New(f), a, cmds); err != nil {
		t.Fatalf("RunFiler: %v", err)
	}
	if step != 3 {
		t.Fatalf("stopped at step %d", step)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "hello.txt")); err != nil || string(b) != "hi" {
		t.Errorf("not copied: %q, %v", b, err)
	}
}

package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/keys"
	"github.com/zredjet/tana/internal/msg"
)

// ごみ箱・完全削除（filer §8.2・§8.5・§8.6）と、名前の変更・新しいフォルダ（filer §8.7）の画面のテスト。

// listPlan は、見本の一覧の項目で作る、ごみ箱・完全削除の計画（偽物）。
type listPlan struct {
	req   fsops.Request
	infos map[string]fsops.EntryInfo // 名前ごとの種類とサイズ（見本の一覧から）
	errs  map[string]fsops.Kind      // 名前ごとの、計画の時点で分かった実行しない理由
	exec  func(ctx context.Context, opt fsops.ExecOptions) (*fsops.Result, error)
}

func (p *listPlan) Request() fsops.Request { return p.req }
func (p *listPlan) Items() []fsops.Item {
	var out []fsops.Item
	for _, s := range p.req.Sources {
		it := fsops.Item{Src: s, Info: p.infos[filepath.Base(s)], Method: fsops.MethodTrash}
		if p.req.Op == fsops.OpDelete {
			it.Method = fsops.MethodRemove
		}
		if k, ok := p.errs[filepath.Base(s)]; ok {
			it.Err = &fsops.OpError{Op: "plan", Path: s, Kind: k}
		}
		out = append(out, it)
	}
	return out
}
func (p *listPlan) Conflicts() []fsops.Conflict { return nil }
func (p *listPlan) Decide(fsops.ConflictID, fsops.Decision) error {
	return errors.New("no conflicts")
}
func (p *listPlan) TotalFiles() int            { return 313 }
func (p *listPlan) TotalBytes() int64          { return 6549825126 }
func (p *listPlan) Warnings() []*fsops.OpError { return nil }
func (p *listPlan) Execute(ctx context.Context, opt fsops.ExecOptions) (*fsops.Result, error) {
	return p.exec(ctx, opt)
}

// deleteScene は、2 ペインの見本（左は leftDir）で、ごみ箱・完全削除の計画を偽物にした場面を作る。
// trash は、ごみ箱の計画に使う。完全削除の計画は、同じ見本の項目で作る。
func deleteScene(t *testing.T, trash *listPlan, mod func(*app.Config)) *scene {
	t.Helper()
	infos := map[string]fsops.EntryInfo{}
	for _, e := range fakeFS()[leftDir] {
		infos[e.Name] = e.Info
	}
	trash.infos = infos
	return newSceneWith(t, func(c *app.Config) {
		c.NewPlan = func(_ context.Context, req fsops.Request) (app.Plan, error) {
			if req.Op == fsops.OpTrash {
				trash.req = req
				return trash, nil
			}
			return &listPlan{req: req, infos: infos}, nil
		}
		if mod != nil {
			mod(c)
		}
	})
}

// TestGoldenTrash は、ごみ箱の確認画面（合計を出さない。ごみ箱に入らない項目を示す）と、ごみ箱に入らなかった項目の結果と、
// そこから進む完全削除の確認を描く（filer §8.2・§8.5・§8.6）。
func TestGoldenTrash(t *testing.T) {
	t.Parallel()
	trash := &listPlan{errs: map[string]fsops.Kind{"data.csv": fsops.KindTrashUnavailable}}
	trash.exec = func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
		s := trash.req.Sources
		return &fsops.Result{Status: fsops.StatusCompletedWithErrors, Items: []fsops.ItemResult{
			{Src: s[0], Outcome: fsops.OutcomeDone, Method: fsops.MethodTrash},
			{Src: s[1], Outcome: fsops.OutcomeFailed, Method: fsops.MethodTrash, Err: &fsops.OpError{Op: "trash", Path: s[1], Kind: fsops.KindTrashUnavailable}},
		}}, nil
	}
	sc := deleteScene(t, trash, nil)
	sc.moveTo("写真")
	sc.keys(char(' '))
	sc.moveTo("data.csv")
	sc.keys(char(' '), char('d'))
	if sc.a.Screen() != app.ScreenConfirm {
		t.Fatalf("screen %v", sc.a.Screen())
	}
	golden(t, "trash-confirm-80x24", sc.draw(80, 24))
	sc.keys(key(keys.KeyEnter))
	if sc.a.Screen() != app.ScreenResult {
		t.Fatalf("screen %v, want the result", sc.a.Screen())
	}
	golden(t, "trash-result-80x24", sc.draw(80, 24))
	sc.keys(char('D'))
	if sc.a.Screen() != app.ScreenDelete {
		t.Fatalf("screen %v, want the deletion confirmation", sc.a.Screen())
	}
	golden(t, "delete-from-trash-80x24", sc.draw(80, 24))
}

// TestGoldenTrashNone は、すべての項目がごみ箱に入らないときの確認画面を描く（D で完全削除の確認へ。フェーズ20で決めた）。
func TestGoldenTrashNone(t *testing.T) {
	t.Parallel()
	trash := &listPlan{errs: map[string]fsops.Kind{"data.csv": fsops.KindTrashUnavailable, "写真": fsops.KindTrashUnavailable}}
	sc := deleteScene(t, trash, nil)
	sc.moveTo("写真")
	sc.keys(char(' '))
	sc.moveTo("data.csv")
	sc.keys(char(' '), char('d'))
	golden(t, "trash-none-80x24", sc.draw(80, 24))
	sc.keys(char('D'))
	if sc.a.Screen() != app.ScreenDelete || !sc.a.Delete().FromTrash {
		t.Fatalf("screen %v, want the deletion confirmation from the trash", sc.a.Screen())
	}
}

// TestGoldenDelete は、D キーで直接行う完全削除の確認を描く（filer §8.6）。項目が多いときは、先頭の数件と「ほか n 項目」を出す。
// 制御文字を含む名前は、置き換えて描く（U6）。
func TestGoldenDelete(t *testing.T) {
	t.Parallel()
	sc := deleteScene(t, &listPlan{}, nil)
	sc.keys(char('a'), char('D'))
	if sc.a.Screen() != app.ScreenDelete {
		t.Fatalf("screen %v", sc.a.Screen())
	}
	golden(t, "delete-80x24", sc.draw(80, 24))
	sc.keys(key(keys.KeyEsc), char('a')) // マークを外す
	for _, n := range []string{"bad\x1b[31mname\u202etxt.exe", "か\u3099き\u3099.txt", "👨\u200d👩\u200d👧family.jpg"} {
		sc.moveTo(n)
		sc.keys(char(' '))
	}
	sc.keys(char('D'))
	golden(t, "delete-names-120x40", sc.draw(120, 40))
}

// TestDeleteKeys は、完全削除の確認のキーを確かめる（filer §8.6。U2）。確定は y だけで、貼り付けた y と Enter では確定しない。
func TestDeleteKeys(t *testing.T) {
	t.Parallel()
	ran := false
	sc := deleteScene(t, &listPlan{}, func(c *app.Config) {
		newPlan := c.NewPlan
		c.NewPlan = func(ctx context.Context, req fsops.Request) (app.Plan, error) {
			p, err := newPlan(ctx, req)
			if lp, ok := p.(*listPlan); ok && req.Op == fsops.OpDelete {
				lp.exec = func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
					ran = true
					return &fsops.Result{Status: fsops.StatusCompleted, Items: []fsops.ItemResult{{Src: req.Sources[0], Outcome: fsops.OutcomeDone}}}, nil
				}
			}
			return p, err
		}
	})
	sc.moveTo("README.md")
	sc.keys(char('D'), char('y')) // 描く前の y
	if sc.a.Screen() != app.ScreenDelete {
		t.Fatalf("screen %v", sc.a.Screen())
	}
	sc.draw(80, 24)
	sc.keys(paste("y"), paste("\r")) // 貼り付けはコマンドとして解釈しない（tui §5）
	if sc.a.Screen() != app.ScreenDelete || ran {
		t.Fatal("a paste confirmed or closed the deletion (U2)")
	}
	sc.keys(key(keys.KeyEnter)) // Enter はやめる
	if sc.a.Screen() != app.ScreenBrowse || ran {
		t.Fatalf("Enter: screen %v, ran %v", sc.a.Screen(), ran)
	}
	sc.keys(char('D'))
	sc.draw(80, 24)
	sc.keys(char('y'))
	if !ran {
		t.Error("y after drawing did not delete")
	}
}

// TestGoldenTrashDialog は、Windows でごみ箱へ移動中に進捗が変わらないときの知らせを描く（filer §8.4）。
func TestGoldenTrashDialog(t *testing.T) {
	t.Parallel()
	clock := now
	started, release := make(chan struct{}), make(chan struct{})
	trash := &listPlan{}
	trash.exec = func(_ context.Context, opt fsops.ExecOptions) (*fsops.Result, error) {
		opt.Progress(fsops.Progress{Stage: fsops.StageTrash, Current: leftDir + `\写真`, TotalFiles: 2}) // どの OS でも同じ文字列
		close(started)
		<-release // Windows の確認ダイアログで止まっている（ctx では中断できない）
		return &fsops.Result{Status: fsops.StatusCompleted}, nil
	}
	sc := deleteScene(t, trash, func(c *app.Config) {
		c.TrashMayAsk = true
		c.Now = func() time.Time { return clock }
	})
	sc.moveTo("写真")
	sc.keys(char(' '), char(' '), char('d'))
	sc.draw(80, 24)
	sc.hold = true
	sc.keys(key(keys.KeyEnter))
	done := make(chan struct{})
	go func() { sc.held[0].Run(); close(done) }()
	defer func() { close(release); <-done }()
	<-started
	sc.a.Refresh()
	clock = clock.Add(4 * time.Second)
	golden(t, "trash-dialog-80x24", sc.draw(80, 24))
}

// TestGoldenName は、名前の変更（エラーを入力欄の下に出す）と新しいフォルダの入力欄を描く（filer §8.7）。本物のカーソルは入力の位置に置く。
func TestGoldenName(t *testing.T) {
	t.Parallel()
	sc := newSceneWith(t, func(c *app.Config) {
		c.Rename = func(string, string) error { return &fsops.OpError{Op: "rename", Kind: fsops.KindExist} }
	})
	sc.moveTo("報告書.docx")
	sc.keys(char('r'))
	for _, r := range "_最終" {
		sc.keys(char(r))
	}
	sc.keys(key(keys.KeyEnter))
	if sc.a.Dialog() != app.DialogRename || sc.a.NameDialog().Err != msg.Kind(fsops.KindExist) {
		t.Fatalf("dialog %v, error %q", sc.a.Dialog(), sc.a.NameDialog().Err)
	}
	golden(t, "rename-80x24", sc.draw(80, 24))
	sc.keys(key(keys.KeyEsc), char('n'))
	sc.keys(paste("新しい\nフォルダ")) // 改行を除いて入れる
	if got := sc.a.NameDialog().Edit.Text(); got != "新しいフォルダ" {
		t.Errorf("pasted %q", got)
	}
	golden(t, "newdir-80x24", sc.draw(80, 24))
}

// TestRunFilerDelete は、イベントループと本物の fsops で、完全削除が、確認を描いた後の y で最後まで動くことを確かめる。
func TestRunFilerDelete(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "old.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := app.DefaultConfig([]string{root, root})
	cfg.Open = func(p string) error { t.Errorf("opened %s", p); return nil }
	a, cmds := app.New(cfg)
	f := newFakeTerm(80, 24)
	step := 0
	f.onWrite = func(string) { // イベントループの goroutine から呼ばれる。app の状態を見てキーを送る
		switch {
		case step == 0 && a.Panes()[0].Loaded():
			step++
			f.send("jD") // old.txt（.. の次）
		case step == 1 && a.Screen() == app.ScreenDelete:
			step++
			f.send("y") // 確認を描いた後の y
		case step == 2 && a.Screen() == app.ScreenBrowse:
			if text, _ := a.Message(); text == msg.Done(fsops.OpDelete, 1, 0, false) {
				step++
				f.send("q")
			}
		}
	}
	if err := RunFiler(New(f), a, cmds); err != nil {
		t.Fatalf("RunFiler: %v", err)
	}
	if step != 3 {
		t.Fatalf("stopped at step %d", step)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("not deleted: %v", err)
	}
}

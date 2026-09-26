package app

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

// ファイル操作（コピー・移動）の流れ（filer §8.1〜§8.5）。
// 覚える（y） → 貼り付け（p・P） → 計画（NewPlan） → 確認 → 衝突の決定 → 実行 → 進捗 → 結果 → 再読み込み。

const (
	opTickInterval   = time.Second      // 実行中に経過時間を描き直し、応答がないかを調べる間隔
	unresponsiveWait = 10 * time.Second // 中止してから、応答がないと知らせるまでの時間（filer §8.4）
	sameTimeWindow   = 2 * time.Second  // 更新日時の差がこれ以内なら同じとみなす（FAT32 は 2 秒単位。filer §8.3）
)

// Plan は、fsops の計画（*fsops.Plan が満たす）。tui のテストで偽物に差し替える。
type Plan interface {
	Request() fsops.Request
	Items() []fsops.Item
	Conflicts() []fsops.Conflict
	Decide(id fsops.ConflictID, d fsops.Decision) error
	TotalFiles() int
	TotalBytes() int64
	Warnings() []*fsops.OpError
	Execute(ctx context.Context, opt fsops.ExecOptions) (*fsops.Result, error)
}

// newFsopsPlan は、fsops.NewPlan を Plan を返す形にする（失敗したときに nil の *fsops.Plan を Plan に入れない）。
func newFsopsPlan(ctx context.Context, req fsops.Request) (Plan, error) {
	p, err := fsops.NewPlan(ctx, req)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Screen は、ファイル操作の画面（ダイアログと違い、画面全体か中央に大きく出す）。
type Screen int

const (
	ScreenBrowse    Screen = iota // 一覧（計画を作っている間も含む）
	ScreenConfirm                 // 確認（filer §8.2）
	ScreenConflicts               // 衝突の決定（filer §8.3）
	ScreenProgress                // 進捗（filer §8.4）
	ScreenResult                  // 結果（filer §8.5）
)

// operation は、進めているファイル操作。
type operation struct {
	gen    int
	req    fsops.Request
	from   string // 覚えたときのフォルダ（見出しに出す）
	pane   int    // 貼り付けたペイン
	screen Screen
	frame  int // この画面を出したときの a.frames。描いた後に届いたキーでだけ確定する（filer U2）
	cancel context.CancelFunc

	planning, slow bool // 計画を作っている。0.2 秒を超えた
	plan           Plan
	warnings       []string // 計画の警告の文言（決定を変えたときに計算し直す）

	// 衝突の画面
	collapsed map[fsops.ConflictID]bool
	unsetOnly bool
	cursor    int
	top       int
	rows      int // 最後に描いた一覧の行数

	// 進捗の画面
	slot         *progressSlot
	progress     fsops.Progress
	started      time.Time
	askCancel    bool // 中止の確認を出している
	askFrame     int
	canceling    bool
	cancelAt     time.Time
	unresponsive bool
	done         chan struct{} // Execute が戻ったら閉じる
}

// progressSlot は、最新の進捗を 1 つだけ置く場所（filer §10）。Execute の goroutine が書き、イベントループが読む。
type progressSlot struct {
	mu sync.Mutex
	p  fsops.Progress
}

func (s *progressSlot) set(p fsops.Progress) {
	s.mu.Lock()
	s.p = p
	s.mu.Unlock()
}

func (s *progressSlot) get() fsops.Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.p
}

// Screen は、いまのファイル操作の画面を返す。
func (a *App) Screen() Screen {
	if a.op == nil {
		if a.result != nil && a.result.open {
			return ScreenResult
		}
		return ScreenBrowse
	}
	return a.op.screen
}

// Planning は、計画を作っていて 0.2 秒を超えた（「計画を作成中」を出す）かを返す。
func (a *App) Planning() bool { return a.op != nil && a.op.planning && a.op.slow }

// Yanked は、覚えている項目の数を返す。
func (a *App) Yanked() int { return len(a.yanked) }

// ---- 覚える・貼り付け ----

// targets は、操作中のペインの対象（マークした項目。なければカーソル行の項目。.. は除く。filer §7）のパスを返す。
// パスは列挙で得た名前から作る（U4）。
func (a *App) targets() []string {
	p := a.panes[a.active]
	var out []string
	for _, i := range p.visible {
		if it := p.items[i]; !it.Parent && p.Marked(it.Name) {
			out = append(out, filepath.Join(p.dir, it.Name))
		}
	}
	if len(out) == 0 {
		if it, ok := p.current(); ok && !it.Parent {
			out = append(out, filepath.Join(p.dir, it.Name))
		}
	}
	return out
}

// yank は、対象を覚える（filer §7）。
func (a *App) yank() {
	t := a.targets()
	if len(t) == 0 {
		a.setMessage(msg.NothingToYank, false)
		return
	}
	a.yanked, a.yankDir = t, a.panes[a.active].dir
	a.setMessage(msg.Yanked(len(t)), false)
}

// paste は、覚えた項目を、操作中のペインのフォルダへコピー・移動する計画を作り始める（filer §8.1）。
func (a *App) paste(op fsops.OpKind) []Cmd {
	if len(a.yanked) == 0 {
		a.setMessage(msg.NothingYanked, false)
		return nil
	}
	p := a.panes[a.active]
	if !p.loaded {
		return nil
	}
	req := fsops.Request{Op: op, Sources: slices.Clone(a.yanked), DestDir: p.dir}
	a.opening = 0 // 関連付けで開く前の確認をやめる（操作の後に古い「実行しますか」を出さない）
	a.gen++
	gen := a.gen
	ctx, cancel := context.WithCancel(context.Background())
	a.op = &operation{gen: gen, req: req, from: a.yankDir, pane: a.active, cancel: cancel, planning: true, collapsed: map[fsops.ConflictID]bool{}}
	newPlan := a.cfg.NewPlan
	return []Cmd{
		{Run: func() any {
			plan, err := newPlan(ctx, req)
			return planned{gen: gen, plan: plan, err: err}
		}},
		{Delay: slowLoad, Run: func() any { return planSlow{gen: gen} }},
	}
}

type planned struct {
	gen  int
	plan Plan
	err  error
}

type planSlow struct{ gen int }

func (a *App) planned(m planned) {
	op := a.op
	if op == nil || op.gen != m.gen || !op.planning {
		return // 中止した
	}
	op.planning = false
	op.cancel()
	if m.err != nil {
		a.logErr(m.err)
		a.op = nil
		if oe, ok := errors.AsType[*fsops.OpError](m.err); ok && oe.Kind == fsops.KindNotFound && oe.Path == op.req.DestDir {
			a.setMessage(msg.DestNotFound, true)
		} else {
			a.setMessage(msg.CannotPlan(msg.Error(m.err)), true)
		}
		return
	}
	op.plan = m.plan
	// 自分自身への衝突（同じフォルダへのコピー）は、最初から自動リネームにする。既存のものを置き換えないため（U1）。
	for _, c := range op.plan.Conflicts() {
		if c.Self {
			if err := op.plan.Decide(c.ID, fsops.DecisionAutoRename); err != nil {
				a.logErr(err)
			}
		}
	}
	op.warnings = warningTexts(op.plan.Warnings())
	a.show(ScreenConfirm)
}

// show は、ファイル操作の画面を出す。これより前に届いたキーでは確定しない（filer U2）。
func (a *App) show(s Screen) {
	a.op.screen, a.op.frame = s, a.frames
}

// armed は、いまの画面を描いた後に届いたキーかを返す（先行入力を捨てる。filer U2）。
func (a *App) armed() bool { return a.frames > a.op.frame }

// discard は、計画を捨てる（実行前にやめた。NewPlan はファイルシステムを変更しないので、元に戻すものはない。filer §8.1）。
func (a *App) discard() {
	if a.op != nil && a.op.cancel != nil {
		a.op.cancel()
	}
	a.op = nil
}

// ---- 確認画面 ----

// ItemNote は、実行されない項目とその理由。
type ItemNote struct {
	Name, Reason string
}

// ConfirmView は、確認画面の内容（filer §8.2）。
type ConfirmView struct {
	Op                fsops.OpKind
	Count, Runnable   int // 項目の数、実行する項目の数
	Dest              string
	Files             int
	Bytes             int64 // 0 なら出さない（同一ボリュームの移動）
	NotRunnable       []ItemNote
	Conflicts, TopLvl int
	Warnings          []string
}

// Confirm は、確認画面の内容を返す（ScreenConfirm のとき）。
func (a *App) Confirm() ConfirmView {
	op := a.op
	pl := op.plan
	v := ConfirmView{Op: op.req.Op, Dest: op.req.DestDir, Files: pl.TotalFiles(), Bytes: pl.TotalBytes()}
	for _, it := range pl.Items() {
		v.Count++
		if it.Err != nil {
			v.NotRunnable = append(v.NotRunnable, ItemNote{Name: filepath.Base(it.Src), Reason: msg.Error(it.Err)})
		} else {
			v.Runnable++
		}
	}
	for _, c := range pl.Conflicts() {
		v.Conflicts++
		if c.Parent == 0 {
			v.TopLvl++
		}
	}
	v.Warnings = op.warnings
	return v
}

// warningTexts は、計画の警告の文言（filer §8.2）。
func warningTexts(ws []*fsops.OpError) []string {
	var out []string
	for _, w := range ws {
		if w.Kind == fsops.KindNoSpace {
			out = append(out, msg.SpaceWarning)
			continue
		}
		out = append(out, filepath.Base(w.Path)+": "+msg.Error(w))
	}
	return out
}

func (a *App) doConfirm(act Action) []Cmd {
	switch act.Kind {
	case ActCancel:
		a.discard()
	case ActSubmit:
		if !a.armed() {
			return nil // 確認画面を描く前に届いた Enter（先行入力。filer U2）
		}
		v := a.Confirm()
		switch {
		case v.Runnable == 0:
			// すべての項目が実行されないときは、Esc で閉じるだけにする（filer §8.2）
		case v.Conflicts > 0:
			a.show(ScreenConflicts)
		default:
			return a.execute()
		}
	}
	return nil
}

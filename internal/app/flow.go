package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

// ファイル操作の流れ（filer §8.1〜§8.6）と、持ち主と ID（filer §4 の「UI の骨格」）。
// 流れは fsops の計画 1 つに対応し、ID で引く。流れの画面の部品（計画を作っている間・確認・衝突・完全削除の確認・進捗・中止の確認）は、
// 流れを持ち主として重ね、流れの ID で置き換え・下ろす。進捗と中止の確認の部品は、流れの型ではなく runner（止められる実行）だけを見る。
// v0.1 では、流れは同時に 1 つだけ（filer §7。begin で確かめる）。

// flow は、進めているファイル操作の流れ。
type flow struct {
	id        int
	req       fsops.Request
	from      string // 項目のあったフォルダ（覚えたときのフォルダ。見出しに出す）
	fromTrash bool   // 完全削除: ごみ箱に入らなかった項目から進んだ（filer §8.6）
	cancel    context.CancelFunc

	planning, slow bool // 計画を作っている。0.2 秒を超えた
	plan           Plan
	warnings       []string // 計画の警告の文言（決定を変えたときに計算し直す）

	exec *execution // 実行（実行を始める前は nil）
}

// execution は、実行中の流れの状態（filer §8.4）。
type execution struct {
	slot         *progressSlot
	progress     fsops.Progress
	started      time.Time
	changed      time.Time // 進捗が最後に変わった時刻（ごみ箱の確認ダイアログの知らせ。filer §8.4）
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

// runState は、止められる実行の状態。
type runState int

const (
	runRunning      runState = iota
	runStopping              // 中止を求めた
	runUnresponsive          // 中止しても戻らない（応答しないネットワークドライブなど。filer §8.4）
)

// runner は、止められる実行（進捗と中止の確認の部品が見るもの）。後の版の別の種類の作業（圧縮・展開など）も、これを満たせば同じ部品で出せる（filer §16）。
type runner interface {
	ownerID() int                 // 持ち主の ID（中止の確認を重ねるときの持ち主）
	progress(a *App) ProgressView // 進捗の内容（中止の確認を出しているかは、部品の側で決める）
	stop(a *App)                  // 中止を求める
	state() runState
}

func (f *flow) ownerID() int { return f.id }

func (f *flow) state() runState {
	switch {
	case f.exec.unresponsive:
		return runUnresponsive
	case f.exec.canceling:
		return runStopping
	}
	return runRunning
}

// stop は、実行を中止する。処理中の項目は安全に中断され、残りはスキップになる（fsops）。
func (f *flow) stop(a *App) {
	f.exec.canceling, f.exec.cancelAt = true, a.cfg.Now()
	f.cancel()
}

// flowOf は、ID id の流れを返す（なければ nil。中止した後に届いた結果）。
func (a *App) flowOf(id int) *flow {
	for _, f := range a.flows {
		if f.id == id {
			return f
		}
	}
	return nil
}

// cancelOwned は、持ち主 id の作業を止める（filer §4。止める入口は 1 つ）。v0.1 では、流れの ctx だけ。
func (a *App) cancelOwned(id int) {
	if f := a.flowOf(id); f != nil && f.cancel != nil {
		f.cancel()
	}
}

// dropFlow は、流れを終える。流れの部品をすべて下ろし、作業を止める（実行が戻った後に止めても何も起きない）。
func (a *App) dropFlow(f *flow) {
	a.removeOwned(f.id)
	a.flows = slices.DeleteFunc(a.flows, func(x *flow) bool { return x == f })
}

// discard は、計画を捨てる（実行前にやめた。NewPlan はファイルシステムを変更しないので、元に戻すものはない。filer §8.1）。
func (a *App) discard(f *flow) { a.dropFlow(f) }

// begin は、操作 req の計画を作り始める（filer §8.1）。from は項目のあったフォルダ（見出しに出す）。
// fromTrash は、完全削除を、ごみ箱に入らなかった項目から始めたこと（filer §8.6）。
func (a *App) begin(req fsops.Request, from string, fromTrash bool) []Cmd {
	if len(a.flows) > 0 {
		return nil // v0.1 では、ファイル操作は同時に 1 つだけ（filer §7）
	}
	a.opening = 0 // 関連付けで開く前の確認をやめる（操作の後に古い「実行しますか」を出さない）
	ctx, cancel := context.WithCancel(context.Background())
	f := &flow{id: a.newID(), req: req, from: from, fromTrash: fromTrash, cancel: cancel, planning: true}
	a.flows = append(a.flows, f)
	a.push(&planningComp{f: f}, f.id)
	id, newPlan := f.id, a.cfg.NewPlan
	return []Cmd{
		{Run: func() any {
			plan, err := newPlan(ctx, req)
			return planned{flow: id, plan: plan, err: err}
		}},
		{Delay: slowLoad, Run: func() any { return planSlow{flow: id} }},
	}
}

// planned は、計画の結果。flow は流れの ID。
type planned struct {
	flow int
	plan Plan
	err  error
}

// planSlow は、計画が slowLoad を超えたこと。
type planSlow struct{ flow int }

func (a *App) planned(m planned) {
	f := a.flowOf(m.flow)
	if f == nil || !f.planning {
		return // 中止した（ctx は中止のときに止めた）
	}
	f.planning = false
	f.cancel()
	if m.err != nil {
		a.logErr(m.err)
		a.dropFlow(f)
		if oe, ok := errors.AsType[*fsops.OpError](m.err); ok && oe.Kind == fsops.KindNotFound && oe.Path == f.req.DestDir {
			a.setMessage(msg.DestNotFound, true)
		} else {
			a.setMessage(msg.CannotPlan(msg.Error(m.err)), true)
		}
		return
	}
	f.plan = m.plan
	// 自分自身への衝突（同じフォルダへのコピー）は、最初から自動リネームにする。既存のものを置き換えないため（U1）。
	for _, c := range f.plan.Conflicts() {
		if c.Self {
			if err := f.plan.Decide(c.ID, fsops.DecisionAutoRename); err != nil {
				a.logErr(err)
			}
		}
	}
	f.warnings = warningTexts(f.plan.Warnings())
	if f.req.Op == fsops.OpDelete {
		a.replaceOwned(f.id, &deleteComp{f: f}) // 完全削除は専用の確認で（filer §8.6。U2）
		return
	}
	a.replaceOwned(f.id, &confirmComp{f: f})
}

// ---- 実行と進捗（filer §8.4） ----

// executed は、実行の結果。flow は流れの ID。
type executed struct {
	flow int
	res  *fsops.Result
	err  error
}

// opTick は、実行中に経過時間を描き直し、応答がないかを調べる知らせ。
type opTick struct{ flow int }

// execute は、計画をその時点の決定で実行する処理を返す。Execute は作業用の goroutine で動く（filer §10）。
// 進捗は最新の値だけを progressSlot に置き、Wake でイベントループに知らせる（Execute を待たせない）。
func (a *App) execute(f *flow) []Cmd {
	a.replaceOwned(f.id, &progressComp{r: f})
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	now := a.cfg.Now()
	f.exec = &execution{slot: &progressSlot{}, started: now, changed: now, done: make(chan struct{})}
	plan, slot, wake, id, done := f.plan, f.exec.slot, a.cfg.Wake, f.id, f.exec.done
	run := func() any {
		defer close(done)
		res, err := plan.Execute(ctx, fsops.ExecOptions{Progress: func(p fsops.Progress) {
			slot.set(p)
			if wake != nil {
				wake()
			}
		}})
		return executed{flow: id, res: res, err: err}
	}
	return []Cmd{{Run: run}, a.tick(f)}
}

func (a *App) tick(f *flow) Cmd {
	id := f.id
	return Cmd{Delay: opTickInterval, Run: func() any { return opTick{flow: id} }}
}

// SetWake は、実行中の進捗が届いたことをイベントループに知らせる関数を設定する（tui が Loop.Wake を渡す）。
func (a *App) SetWake(f func()) { a.cfg.Wake = f }

// Refresh は、実行中の流れの最新の進捗を読む（tui が Wake の知らせを受けたときに呼ぶ）。
// 一番上の部品では決めない（中止の確認を出している間も読む）。
func (a *App) Refresh() {
	for _, f := range a.flows {
		if f.exec == nil {
			continue
		}
		if p := f.exec.slot.get(); p != f.exec.progress {
			f.exec.progress, f.exec.changed = p, a.cfg.Now()
		}
	}
}

func (a *App) opTick(m opTick) []Cmd {
	f := a.flowOf(m.flow)
	if f == nil || f.exec == nil {
		return nil
	}
	a.Refresh()
	if f.exec.canceling && a.cfg.Now().Sub(f.exec.cancelAt) >= unresponsiveWait {
		f.exec.unresponsive = true // 中止しても Execute が戻らない（応答しないネットワークドライブなど。filer §8.4）
	}
	return []Cmd{a.tick(f)}
}

// Abort は、実行中のファイル操作を中止し、Execute が戻るのを最大 wait だけ待つ（シグナルで終わるとき。filer §10）。
func (a *App) Abort(wait time.Duration) {
	deadline := time.After(wait)
	for _, f := range a.flows {
		if f.cancel != nil {
			f.cancel()
		}
		if f.exec != nil {
			select {
			case <-f.exec.done:
			case <-deadline:
				return
			}
		}
	}
}

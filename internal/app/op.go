package app

import (
	"context"
	"path/filepath"
	"slices"
	"time"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

// ファイル操作の流れ（filer §8.1〜§8.6）。
// コピー・移動: 覚える（y） → 貼り付け（p・P） → 計画（NewPlan） → 確認 → 衝突の決定 → 実行 → 進捗 → 結果 → 再読み込み。
// ごみ箱（d）: 計画 → 確認 → 実行 → 結果。ごみ箱に入らない項目は、利用者が選んだときだけ完全削除の確認へ進む（op_delete.go）。
// 完全削除（D）: 計画 → 完全削除の確認（y だけで確定） → 実行 → 結果。

const (
	opTickInterval   = time.Second      // 実行中に経過時間を描き直し、応答がないかを調べる間隔
	unresponsiveWait = 10 * time.Second // 中止してから、応答がないと知らせるまでの時間（filer §8.4）
	trashDialogWait  = 3 * time.Second  // ごみ箱へ移動中に進捗が変わらないとき、確認ダイアログの可能性を知らせるまでの時間（filer §8.4）
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
	ScreenDelete                  // 完全削除の確認（filer §8.6）
)

// Screen は、いまのファイル操作の画面を返す（一番上の重ねる部品から求める。計画を作っている間とダイアログは ScreenBrowse）。
func (a *App) Screen() Screen {
	switch a.topRole() {
	case RoleConfirm:
		return ScreenConfirm
	case RoleConflicts:
		return ScreenConflicts
	case RoleProgress, RoleCancelAsk:
		return ScreenProgress
	case RoleResult:
		return ScreenResult
	case RoleDelete:
		return ScreenDelete
	}
	return ScreenBrowse
}

// Planning は、計画を作っていて 0.2 秒を超えた（「計画を作成中」を出す）かを返す。
func (a *App) Planning() bool {
	if m := a.top(); m != nil {
		if c, ok := m.c.(*planningComp); ok {
			return c.f.slow
		}
	}
	return false
}

// PlanningView は、計画を作っている間の内容。
type PlanningView struct {
	Slow bool // 0.2 秒を超えた（「計画を作成中」を出す）
}

func (PlanningView) Role() Role { return RolePlanning }

// planningComp は、計画を作っている間（filer §8.1）。届いたキーは捨て、Esc だけで中止する（U2）。
// 「計画を作成中」を出している間は、キー入力でメッセージ行を消さない（filer §5.1）。
type planningComp struct {
	base
	f *flow
}

func (*planningComp) role() Role               { return RolePlanning }
func (c *planningComp) view(*App) View         { return PlanningView{Slow: c.f.slow} }
func (c *planningComp) keepsMessage(*App) bool { return c.f.slow }
func (c *planningComp) commands() commandTable {
	return commandTable{ActCancel: {GateFree, func(a *App, _ Action) []Cmd {
		a.discard(c.f)
		a.setMessage(msg.Kind(fsops.KindCanceled), false)
		return nil
	}}}
}

// Yanked は、覚えている項目の数を返す。
func (a *App) Yanked() int { return len(a.yanked) }

// ---- 覚える・貼り付け ----

// targets は、操作中のペインの対象（マークした項目。なければカーソル行の項目。.. は除く。filer §7）のパスを返す。
// パスは列挙で得た名前から作る（U4）。
func (a *App) targets() []string {
	p := a.cur()
	var out []string
	for _, i := range p.visible {
		if it := p.items[i]; !it.Parent && p.Marked(it.Name) {
			out = append(out, p.pathOf(it))
		}
	}
	if len(out) == 0 {
		if it, ok := p.current(); ok && !it.Parent {
			out = append(out, p.pathOf(it))
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
	a.yanked, a.yankDir = t, a.cur().dir
	a.setMessage(msg.Yanked(len(t)), false)
}

// paste は、覚えた項目を、操作中のペインのフォルダへコピー・移動する計画を作り始める（filer §8.1）。
func (a *App) paste(op fsops.OpKind) []Cmd {
	if len(a.yanked) == 0 {
		a.setMessage(msg.NothingYanked, false)
		return nil
	}
	p := a.cur()
	if !p.loaded {
		return nil
	}
	return a.begin(fsops.Request{Op: op, Sources: slices.Clone(a.yanked), DestDir: p.dir}, a.yankDir, false)
}

// ---- 確認画面 ----

// ItemNote は、実行されない項目とその理由。
type ItemNote struct {
	Name, Reason string
}

func (ConfirmView) Role() Role { return RoleConfirm }

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
	Untrashable       int // ごみ箱: 計画の時点でごみ箱に入らないと分かった項目の数（KindTrashUnavailable）
}

// Confirm は、確認画面の内容を返す（ScreenConfirm のとき）。
func (a *App) Confirm() ConfirmView {
	v, _ := ModalView[ConfirmView](a)
	return v
}

// confirmView は、流れ f の確認画面の内容（filer §8.2）。
func confirmView(f *flow) ConfirmView {
	op := f
	pl := op.plan
	v := ConfirmView{Op: op.req.Op, Dest: op.req.DestDir, Files: pl.TotalFiles(), Bytes: pl.TotalBytes()}
	for _, it := range pl.Items() {
		v.Count++
		switch {
		case it.Err == nil:
			v.Runnable++
		case trashUnavailable(it.Err) && op.req.Op == fsops.OpTrash:
			v.Untrashable++
			v.NotRunnable = append(v.NotRunnable, ItemNote{Name: filepath.Base(it.Src), Reason: msg.TrashUnavailableSkip})
		default:
			v.NotRunnable = append(v.NotRunnable, ItemNote{Name: filepath.Base(it.Src), Reason: msg.Error(it.Err)})
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

// confirmComp は、確認画面（filer §8.2）。Enter と D は、画面を描いた後だけ行う（U2）。
type confirmComp struct {
	base
	f *flow
}

func (*confirmComp) role() Role       { return RoleConfirm }
func (c *confirmComp) view(*App) View { return confirmView(c.f) }
func (c *confirmComp) commands() commandTable {
	return commandTable{
		ActCancel: {GateFree, func(a *App, _ Action) []Cmd { a.discard(c.f); return nil }},
		ActSubmit: {GateAfterDraw, func(a *App, _ Action) []Cmd {
			v := confirmView(c.f)
			switch {
			case v.Runnable == 0:
				// すべての項目が実行されないときは、Esc で閉じるだけにする（filer §8.2）
			case v.Conflicts > 0:
				a.replaceOwned(c.f.id, &conflictsComp{f: c.f, collapsed: map[fsops.ConflictID]bool{}})
			default:
				return a.execute(c.f)
			}
			return nil
		}},
		// ごみ箱: すべての項目が実行されず、ごみ箱に入らない項目があれば、完全削除の確認へ進める（filer §8.2。フェーズ20で決めた）。
		// 利用者が D を選んだときだけ進む。UI が自分から完全削除に切り替えない（fsops I5 の UI 側。U2）。
		ActPurge: {GateAfterDraw, func(a *App, _ Action) []Cmd {
			if v := confirmView(c.f); v.Op == fsops.OpTrash && v.Runnable == 0 && v.Untrashable > 0 {
				srcs, from := untrashableItems(c.f.plan.Items()), c.f.from
				a.discard(c.f)
				return a.begin(fsops.Request{Op: fsops.OpDelete, Sources: srcs}, from, true)
			}
			return nil
		}},
	}
}

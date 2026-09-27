package app

import (
	"slices"

	"github.com/zredjet/tana/internal/lineedit"
)

// UI の骨格（filer §4）: 部品・置き方・フォーカスの道筋・門。
//
// 画面の状態は、部品（component）の木で持つ。土台は作業場（Workspace）で、その上に重ねる部品（modals）を積む。
// キーは、フォーカスの道筋（一番上の根から、フォーカスのある子をたどった並び）の内側の部品から届け、
// 部品の操作表（commandTable）に載った操作だけを行う。
// 門（Gate）: 操作表の各行は、描く前でも行う（GateFree）か、部品が現れてから画面を描いた後だけ行う（GateAfterDraw）かを必ず宣言する（U2）。

// Role は、部品の役割。キーの割り当ての表と、描き方を決める。
type Role int

const (
	RoleNone      Role = iota
	RoleWorkspace      // 作業場（ペインの外の閲覧の操作）
	RolePane           // 操作中のペイン
	RoleHelp           // ヘルプ
	RolePath           // パスの入力（g）
	RoleRename         // 名前の変更（filer §8.7）
	RoleNewDir         // 新しいフォルダ（filer §8.7）
	RoleExec           // 実行ファイルを開く前の確認（filer §7）
	RolePlanning       // 計画を作っている間（filer §8.1）
	RoleConfirm        // 確認（filer §8.2）
	RoleConflicts      // 衝突の決定（filer §8.3）
	RoleProgress       // 進捗（filer §8.4）
	RoleCancelAsk      // 中止の確認（filer §8.4）
	RoleResult         // 結果（filer §8.5）
	RoleDelete         // 完全削除の確認（filer §8.6）
)

// AllRoles は、すべての役割を返す（テストで、役割ごとの表がそろっていることを確かめる）。
func AllRoles() []Role {
	var out []Role
	for r := RoleWorkspace; r <= RoleDelete; r++ {
		out = append(out, r)
	}
	return out
}

// Gate は、画面を描く前に届いた操作を行うか（filer §4 の門。U2）。0 は使わない（どの行も意図して選ぶ）。
type Gate int

const (
	GateFree      Gate = 1 // 描く前でも行う（やめる、移動など）
	GateAfterDraw Gate = 2 // 部品が現れてから画面を描いた後だけ行う（確定の操作）
)

// command は、操作表の 1 行。
type command struct {
	gate Gate
	run  func(a *App, act Action) []Cmd
}

// commandTable は、部品の操作表。載っていない操作は何もしない。
type commandTable map[ActionKind]command

// component は、画面の部品。
type component interface {
	role() Role
	commands() commandTable
	view(a *App) View
	keepsMessage(a *App) bool // キー入力でメッセージ行を消さない（計画を作成中の表示の間）
	focused(a *App) *mounted  // フォーカスのある子（なければ nil）
}

// mounted は、置いた部品。
type mounted struct {
	c       component
	id      int // 部品の ID
	owner   int // 持ち主の ID（ファイル操作の流れなど。0 ならなし）
	shownAt int // 道筋の一番内側になったときの a.frames。これより後に描いてから届いた AfterDraw の操作だけを行う
}

// View は、部品の内容（tui が描く）。
type View interface{ Role() Role }

// ---- 重ねる部品の出し入れ ----

// push は、部品 c を重ねる。
func (a *App) push(c component, owner int) {
	a.modals = append(a.modals, &mounted{c: c, id: a.newID(), owner: owner})
}

// pushAsync は、作業用の goroutine の結果で、作業場の上に部品 c を重ねる（filer §4。G6）。
// 利用者が閲覧で打っているキーを、急に出た部品が横取りしないように、操作がすべて AfterDraw の部品だけを重ねる。
// 重ねなければ false。
func (a *App) pushAsync(c component) bool {
	if len(a.modals) > 0 {
		return false // 作業場にフォーカスがない（ほかの部品の下に隠れて、描いた後の扱いにならないように）
	}
	for _, cmd := range c.commands() {
		if cmd.gate != GateAfterDraw {
			return false
		}
	}
	a.push(c, 0)
	return true
}

// replaceOwned は、持ち主 owner の一番上の部品を c に置き換える。なければ false。
func (a *App) replaceOwned(owner int, c component) bool {
	for i := len(a.modals) - 1; i >= 0; i-- {
		if a.modals[i].owner == owner {
			a.modals[i] = &mounted{c: c, id: a.newID(), owner: owner}
			return true
		}
	}
	return false
}

// pop は、一番上の部品を下ろす。
func (a *App) pop() {
	if n := len(a.modals); n > 0 {
		a.modals = a.modals[:n-1]
	}
}

// remove は、部品 m を下ろす。
func (a *App) remove(m *mounted) {
	a.modals = slices.DeleteFunc(a.modals, func(x *mounted) bool { return x == m })
}

// removeOwned は、持ち主 owner の部品をすべて下ろす。
func (a *App) removeOwned(owner int) {
	a.modals = slices.DeleteFunc(a.modals, func(x *mounted) bool { return x.owner == owner })
}

// top は、一番上の重ねる部品を返す（なければ nil）。
func (a *App) top() *mounted {
	if n := len(a.modals); n > 0 {
		return a.modals[n-1]
	}
	return nil
}

// topRole は、一番上の重ねる部品の役割を返す（なければ RoleNone）。
func (a *App) topRole() Role {
	if m := a.top(); m != nil {
		return m.c.role()
	}
	return RoleNone
}

// ---- フォーカスの道筋 ----

// path は、フォーカスの道筋を、内側から外側の順に返す。重ねる部品があれば、一番上のものが根になり、作業場へは渡らない。
func (a *App) path() []*mounted {
	root := a.top()
	if root == nil {
		root = &a.ws
	}
	var out []*mounted
	for m := root; m != nil; m = m.c.focused(a) {
		out = append(out, m)
	}
	slices.Reverse(out)
	return out
}

// FocusPath は、フォーカスの道筋の役割を、内側から外側の順に返す（tui がキーの表を引く順）。
func (a *App) FocusPath() []Role {
	var out []Role
	for _, m := range a.path() {
		out = append(out, m.c.role())
	}
	return out
}

// focusKey は、道筋の一番内側の部品と役割（どちらかが変われば、門を掛け直す。filer §4）。
type focusKey struct {
	id   int
	role Role
}

// settleFocus は、道筋の一番内側が変わっていれば、その時点を記録する（Do と Update の終わりに呼ぶ）。
// 重ねた、下ろして下の部品が現れた、フォーカスやモードが変わった、のどれでも、ここで記録する。
func (a *App) settleFocus() {
	m := a.path()[0]
	if k := (focusKey{m.id, m.c.role()}); k != a.inner {
		a.inner = k
		m.shownAt = a.frames
	}
}

// KeyPressed は、キー入力があったことを知らせる。メッセージ行を消す（filer §5.1）。
// 道筋の一番内側の部品が消さないとき（計画を作成中の表示の間）は消さない。tui はキー入力のたびに呼ぶ（Do も呼ぶ）。
func (a *App) KeyPressed() {
	if !a.path()[0].c.keepsMessage(a) {
		a.ClearMessage()
	}
}

// dispatch は、操作 act を道筋の部品に届ける。act.Role があればその役割の部品に、なければ内側から見て、
// 操作表にその操作を持つ最初の部品に届ける。門で捨てる操作と、届け先のない操作は何もしない。
func (a *App) dispatch(act Action) []Cmd {
	for _, m := range a.path() {
		if act.Role != RoleNone && m.c.role() != act.Role {
			continue
		}
		cmd, ok := m.c.commands()[act.Kind]
		if !ok {
			if act.Role != RoleNone {
				return nil
			}
			continue
		}
		if cmd.gate != GateFree && a.frames <= m.shownAt {
			return nil // 部品を描く前に届いた操作（先行入力。U2）
		}
		return cmd.run(a, act)
	}
	return nil
}

// ---- tui が読む状態 ----

// Modals は、作業場の上に重ねた部品の内容を、下から順に返す。
func (a *App) Modals() []View {
	var out []View
	for _, m := range a.modals {
		out = append(out, m.c.view(a))
	}
	return out
}

// SetListRows は、役割 r の一覧を描いた行数を覚える（ページ単位の移動に使う。衝突・結果）。
func (a *App) SetListRows(r Role, n int) {
	switch r {
	case RoleConflicts:
		a.SetConflictRows(n)
	case RoleResult:
		a.SetResultRows(n)
	}
}

// ModalView は、重ねた部品のうち、内容の型が T の一番上のものを返す。
func ModalView[T View](a *App) (T, bool) {
	for i := len(a.modals) - 1; i >= 0; i-- {
		if v, ok := a.modals[i].c.view(a).(T); ok {
			return v, true
		}
	}
	var zero T
	return zero, false
}

// RoleCommands は、役割 r の部品が受け付ける操作を返す（tui の keymap のテストで、表と照らし合わせる）。
func RoleCommands(r Role) []ActionKind {
	var out []ActionKind
	for k := range tableOf(r) {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// tableOf は、役割 r の部品の操作表を返す（部品の状態に依らない。空の部品から作る）。
func tableOf(r Role) commandTable {
	switch r {
	case RoleWorkspace:
		return workspaceComp{}.commands()
	case RolePane:
		return paneComp{}.commands()
	case RoleHelp:
		return helpComp{}.commands()
	case RolePath:
		return (&pathComp{}).commands()
	case RoleRename:
		return (&nameComp{rename: true}).commands()
	case RoleNewDir:
		return (&nameComp{}).commands()
	case RoleExec:
		return (&execComp{}).commands()
	case RolePlanning:
		return (&planningComp{}).commands()
	case RoleConfirm:
		return (&confirmComp{}).commands()
	case RoleConflicts:
		return (&conflictsComp{}).commands()
	case RoleProgress:
		return (&progressComp{}).commands()
	case RoleCancelAsk:
		return (&cancelAskComp{}).commands()
	case RoleResult:
		return (&resultComp{}).commands()
	case RoleDelete:
		return (&deleteComp{}).commands()
	}
	return nil
}

// ---- 部品の種類（2 つ以上の役割で使う形） ----

// editCommands は、入力欄の編集の操作表（パスの入力・名前の変更・新しいフォルダ）。編集はどれも描く前でも行う（打った文字を失わない）。
// guard が偽を返すときは何もしない（名前の変更を待っている間）。
func editCommands(e func() *lineedit.Editor, guard func() bool, after func()) commandTable {
	t := commandTable{}
	add := func(k ActionKind, f func(e *lineedit.Editor, act Action)) {
		t[k] = command{GateFree, func(_ *App, act Action) []Cmd {
			if guard != nil && !guard() {
				return nil
			}
			f(e(), act)
			if after != nil {
				after()
			}
			return nil
		}}
	}
	add(ActInsert, func(e *lineedit.Editor, act Action) { e.Insert(act.Text) })
	add(ActBackspace, func(e *lineedit.Editor, _ Action) { e.DeleteBackward() })
	add(ActDelete, func(e *lineedit.Editor, _ Action) { e.DeleteForward() })
	add(ActLeft, func(e *lineedit.Editor, _ Action) { e.Left() })
	add(ActRight, func(e *lineedit.Editor, _ Action) { e.Right() })
	add(ActLineHome, func(e *lineedit.Editor, _ Action) { e.Home() })
	add(ActLineEnd, func(e *lineedit.Editor, _ Action) { e.End() })
	return t
}

// listCommands は、一覧のカーソルの移動の操作表（衝突・結果）。門は役割が決める（衝突は AfterDraw、結果は Free）。
// move は、カーソルを操作 k で動かす（moveCursor を使う）。
func listCommands(gate Gate, move func(k ActionKind)) commandTable {
	t := commandTable{}
	for _, k := range []ActionKind{ActUp, ActDown, ActPageUp, ActPageDown, ActHome, ActEnd} {
		t[k] = command{gate, func(_ *App, act Action) []Cmd { move(act.Kind); return nil }}
	}
	return t
}

// with は、操作表 t に rows の行を足した表を返す。
func (t commandTable) with(rows commandTable) commandTable {
	for k, c := range rows {
		t[k] = c
	}
	return t
}

// base は、部品の既定（メッセージ行を消す、子を持たない）。
type base struct{}

func (base) keepsMessage(*App) bool { return false }
func (base) focused(*App) *mounted  { return nil }

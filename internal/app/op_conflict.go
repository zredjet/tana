package app

import (
	"path/filepath"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

// 衝突の決定（filer §8.3）。決定の既定は未選択（スキップと同じ扱い。U1）。

// allowed は、衝突 c で決定 d を使えるかを返す（fsops §9.1 と同じ規則。TestAllowedMatchesFsops で fsops の Decide と比べる）。
func allowed(c fsops.Conflict, d fsops.Decision) bool {
	switch d {
	case fsops.DecisionUnset, fsops.DecisionSkip, fsops.DecisionAutoRename:
		return true
	case fsops.DecisionOverwrite:
		return !c.Self && c.SrcInfo.Type == fsops.TypeFile && c.DstInfo.Type == fsops.TypeFile
	case fsops.DecisionMerge:
		return !c.Self && c.SrcInfo.Type == fsops.TypeDir && c.DstInfo.Type == fsops.TypeDir
	}
	return false
}

// ConflictRow は、衝突の一覧の 1 行。
type ConflictRow struct {
	ID       fsops.ConflictID // 0 なら、折りたたんだ内側の衝突の行（Parent の中の Inner 件）
	Parent   fsops.ConflictID
	Depth    int // 字下げの段
	Name     string
	Dir      bool // コピー元がフォルダ
	Src, Dst fsops.EntryInfo
	SrcNewer bool // コピー元の更新日時が新しい（2 秒以内の差は同じとみなす）
	DstNewer bool
	Decision fsops.Decision
	Allowed  [5]bool // 決定ごとに使えるか（fsops.Decision の値で引く）
	Inner    int     // ID が 0 の行: 内側の衝突の数
	InnerHow string  // ID が 0 の行: 表示する方法
}

func (ConflictsView) Role() Role { return RoleConflicts }

// ConflictsView は、衝突の画面の内容（filer §8.3）。
type ConflictsView struct {
	Op              fsops.OpKind
	From, To        string
	All, Top, Unset int
	Warnings        []string
	Rows            []ConflictRow
	Cursor          int
	UnsetOnly       bool
}

// Conflicts は、衝突の画面の内容を返す（ScreenConflicts のとき）。
func (a *App) Conflicts() ConflictsView {
	v, _ := ModalView[ConflictsView](a)
	return v
}

// conflictsView は、衝突の画面の内容。カーソルは、読むときに一覧の中に収める（決定で一覧が短くなったとき）。
func (c *conflictsComp) conflictsView() ConflictsView {
	f := c.f
	cs := f.plan.Conflicts()
	v := ConflictsView{Op: f.req.Op, From: f.from, To: f.req.DestDir, UnsetOnly: c.unsetOnly}
	for _, x := range cs {
		v.All++
		if x.Parent == 0 {
			v.Top++
		}
		if x.Decision == fsops.DecisionUnset {
			v.Unset++
		}
	}
	v.Warnings = f.warnings // 決定を変えたときに計算し直す（fsops §6.4）
	v.Rows = conflictRows(cs, c.collapsed, c.unsetOnly)
	c.cursor = max(min(c.cursor, len(v.Rows)-1), 0)
	v.Cursor = c.cursor
	return v
}

// conflictRows は、衝突を一覧の行に並べる。トップレベルを計画の順に並べ、内側の衝突は親の下に字下げして出す。
// 内側の衝突は、親がマージで折りたたんでいないときだけ出し、そうでなければ件数だけの 1 行にする（filer §8.3）。
// unsetOnly なら、未選択の衝突だけを出す（使われない内側の衝突は出さない）。
func conflictRows(cs []fsops.Conflict, collapsed map[fsops.ConflictID]bool, unsetOnly bool) []ConflictRow {
	children := map[fsops.ConflictID][]fsops.Conflict{}
	for _, c := range cs {
		if c.Parent != 0 {
			children[c.Parent] = append(children[c.Parent], c)
		}
	}
	var count func(id fsops.ConflictID) int
	count = func(id fsops.ConflictID) int {
		n := 0
		for _, k := range children[id] {
			n += 1 + count(k.ID)
		}
		return n
	}
	var rows []ConflictRow
	var walk func(c fsops.Conflict, depth int)
	walk = func(c fsops.Conflict, depth int) {
		if !unsetOnly || c.Decision == fsops.DecisionUnset {
			rows = append(rows, rowOf(c, depth))
		}
		kids := children[c.ID]
		switch {
		case len(kids) == 0:
		case c.Decision == fsops.DecisionMerge && !collapsed[c.ID]:
			for _, k := range kids {
				walk(k, depth+1)
			}
		case !unsetOnly:
			how := msg.InnerHidden
			if c.Decision == fsops.DecisionMerge {
				how = msg.InnerCollapsed
			}
			rows = append(rows, ConflictRow{Parent: c.ID, Depth: depth + 1, Inner: count(c.ID), InnerHow: how})
		}
	}
	for _, c := range cs {
		if c.Parent == 0 {
			walk(c, 0)
		}
	}
	return rows
}

func rowOf(c fsops.Conflict, depth int) ConflictRow {
	r := ConflictRow{ID: c.ID, Parent: c.Parent, Depth: depth, Name: filepath.Base(c.Src), Dir: c.SrcInfo.Type == fsops.TypeDir,
		Src: c.SrcInfo, Dst: c.DstInfo, Decision: c.Decision}
	d := c.SrcInfo.ModTime.Sub(c.DstInfo.ModTime)
	r.SrcNewer, r.DstNewer = d > sameTimeWindow, -d > sameTimeWindow
	for k := range r.Allowed {
		r.Allowed[k] = allowed(c, fsops.Decision(k))
	}
	return r
}

// conflictByID は、ID の衝突を返す（fsops の衝突の ID は 1 からの連番。Decide も添字で引く）。
func conflictByID(cs []fsops.Conflict, id fsops.ConflictID) (fsops.Conflict, bool) {
	if id < 1 || int(id) > len(cs) {
		return fsops.Conflict{}, false
	}
	return cs[id-1], true
}

// SetConflictRows は、衝突の一覧を描いた行数を覚える（ページ単位の移動に使う）。
func (a *App) SetConflictRows(n int) {
	for _, m := range a.modals {
		if c, ok := m.c.(*conflictsComp); ok {
			c.rows = n
		}
	}
}

// conflictsComp は、衝突の決定の画面（filer §8.3）。
// 画面を描く前に届いたキー（先行入力）では、実行しないだけでなく、決定もカーソルも変えない。見ていない衝突が上書き・マージにならないように（U1・U2）。
type conflictsComp struct {
	base
	f         *flow
	collapsed map[fsops.ConflictID]bool // 折りたたんだマージの衝突
	unsetOnly bool                      // 未選択の衝突だけを出す
	cursor    int
	rows      int // 最後に描いた一覧の行数
}

func (*conflictsComp) role() Role       { return RoleConflicts }
func (c *conflictsComp) view(*App) View { return c.conflictsView() }

func (c *conflictsComp) commands() commandTable {
	// cur は、衝突と、一覧の行と、カーソル行を返す。
	cur := func() ([]fsops.Conflict, []ConflictRow, ConflictRow) {
		cs := c.f.plan.Conflicts()
		rows := conflictRows(cs, c.collapsed, c.unsetOnly)
		var row ConflictRow
		if c.cursor >= 0 && c.cursor < len(rows) {
			row = rows[c.cursor]
		}
		return cs, rows, row
	}
	after := func(f func(a *App, act Action) []Cmd) command { return command{GateAfterDraw, f} }
	return listCommands(GateAfterDraw, func(k ActionKind) {
		_, rows, _ := cur()
		c.cursor = moveCursor(c.cursor, len(rows), c.rows, k)
	}).with(commandTable{
		ActCancel: {GateFree, func(a *App, _ Action) []Cmd { a.discard(c.f); return nil }},
		ActSubmit: after(func(a *App, _ Action) []Cmd { return a.execute(c.f) }),
		ActDecide: after(func(a *App, act Action) []Cmd {
			cs, _, row := cur()
			x, ok := conflictByID(cs, row.ID)
			switch {
			case !ok:
			case !allowed(x, act.Decision):
				a.setMessage(msg.DecisionNotAllowed(act.Decision), true)
			default:
				a.decide(c.f, x.ID, act.Decision)
			}
			return nil
		}),
		ActDecideAll: after(func(a *App, act Action) []Cmd {
			cs, _, _ := cur()
			n := 0
			for _, x := range cs {
				if allowed(x, act.Decision) {
					a.decide(c.f, x.ID, act.Decision)
				} else {
					n++
				}
			}
			if n > 0 {
				a.setMessage(msg.NotChanged(n), false)
			}
			return nil
		}),
		// ファイル同士の衝突のうち、コピー元が新しいもの（2 秒を超える差）を上書きに、それ以外をスキップにする（filer §8.3）。
		ActNewerOnly: after(func(a *App, _ Action) []Cmd {
			cs, _, _ := cur()
			n := 0
			for _, x := range cs {
				if !allowed(x, fsops.DecisionOverwrite) {
					n++
					continue
				}
				d := fsops.DecisionSkip
				if x.SrcInfo.ModTime.Sub(x.DstInfo.ModTime) > sameTimeWindow {
					d = fsops.DecisionOverwrite
				}
				a.decide(c.f, x.ID, d)
			}
			if n > 0 {
				a.setMessage(msg.NotChanged(n), false)
			}
			return nil
		}),
		ActToggle: after(func(*App, Action) []Cmd {
			cs, _, row := cur()
			switch {
			case row.ID == 0 && row.Parent != 0:
				if x, ok := conflictByID(cs, row.Parent); ok && x.Decision == fsops.DecisionMerge {
					c.collapsed[row.Parent] = false
				}
			case row.ID != 0 && row.Decision == fsops.DecisionMerge:
				c.collapsed[row.ID] = !c.collapsed[row.ID]
			}
			return nil
		}),
		ActUnsetOnly: after(func(*App, Action) []Cmd {
			c.unsetOnly = !c.unsetOnly
			c.cursor = 0
			return nil
		}),
	})
}

// decide は決定を設定し、警告（空き容量の見込みは決定で変わる）を計算し直す。使えることは呼ぶ側で確かめている。
func (a *App) decide(f *flow, id fsops.ConflictID, d fsops.Decision) {
	if err := f.plan.Decide(id, d); err != nil {
		a.logErr(err)
	}
	f.warnings = warningTexts(f.plan.Warnings())
}

// moveCursor は、n 行の一覧のカーソル cur を、操作 k で動かす（page は描いた行数）。
func moveCursor(cur, n, page int, k ActionKind) int {
	page = max(page, 1)
	switch k {
	case ActUp:
		cur--
	case ActDown:
		cur++
	case ActPageUp:
		cur -= page
	case ActPageDown:
		cur += page
	case ActHome:
		cur = 0
	case ActEnd:
		cur = n - 1
	}
	return max(min(cur, n-1), 0)
}

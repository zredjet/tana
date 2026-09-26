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
	op := a.op
	cs := op.plan.Conflicts()
	v := ConflictsView{Op: op.req.Op, From: filepath.Dir(op.req.Sources[0]), To: op.req.DestDir, UnsetOnly: op.unsetOnly}
	for _, c := range cs {
		v.All++
		if c.Parent == 0 {
			v.Top++
		}
		if c.Decision == fsops.DecisionUnset {
			v.Unset++
		}
	}
	v.Warnings = warningTexts(op.plan.Warnings()) // 決定によって計算し直される（fsops §6.4）
	v.Rows = conflictRows(cs, op.collapsed, op.unsetOnly)
	op.cursor = max(min(op.cursor, len(v.Rows)-1), 0)
	v.Cursor = op.cursor
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

// conflictByID は、ID の衝突を返す。
func conflictByID(cs []fsops.Conflict, id fsops.ConflictID) (fsops.Conflict, bool) {
	if id < 1 || int(id) > len(cs) || cs[id-1].ID != id {
		for _, c := range cs {
			if c.ID == id {
				return c, true
			}
		}
		return fsops.Conflict{}, false
	}
	return cs[id-1], true
}

// SetConflictRows は、衝突の一覧を描いた行数を覚える（ページ単位の移動に使う）。
func (a *App) SetConflictRows(n int) {
	if a.op != nil {
		a.op.rows = n
	}
}

func (a *App) doConflicts(act Action) []Cmd {
	op := a.op
	cs := op.plan.Conflicts()
	rows := conflictRows(cs, op.collapsed, op.unsetOnly)
	var row ConflictRow
	if op.cursor >= 0 && op.cursor < len(rows) {
		row = rows[op.cursor]
	}
	switch act.Kind {
	case ActCancel:
		a.discard()
	case ActSubmit:
		if !a.armed() {
			return nil // 衝突の画面を描く前に届いた Enter（先行入力。filer U2）
		}
		return a.execute()
	case ActUp, ActDown, ActPageUp, ActPageDown, ActHome, ActEnd:
		op.cursor = moveCursor(op.cursor, len(rows), op.rows, act.Kind)
	case ActDecide:
		c, ok := conflictByID(cs, row.ID)
		switch {
		case !ok:
		case !allowed(c, act.Decision):
			a.setMessage(msg.DecisionNotAllowed(act.Decision), true)
		default:
			a.decide(c.ID, act.Decision)
		}
	case ActDecideAll:
		n := 0
		for _, c := range cs {
			if allowed(c, act.Decision) {
				a.decide(c.ID, act.Decision)
			} else {
				n++
			}
		}
		if n > 0 {
			a.setMessage(msg.NotChanged(n), false)
		}
	case ActNewerOnly:
		// ファイル同士の衝突のうち、コピー元が新しいもの（2 秒を超える差）を上書きに、それ以外をスキップにする（filer §8.3）。
		n := 0
		for _, c := range cs {
			if !allowed(c, fsops.DecisionOverwrite) {
				n++
				continue
			}
			d := fsops.DecisionSkip
			if c.SrcInfo.ModTime.Sub(c.DstInfo.ModTime) > sameTimeWindow {
				d = fsops.DecisionOverwrite
			}
			a.decide(c.ID, d)
		}
		if n > 0 {
			a.setMessage(msg.NotChanged(n), false)
		}
	case ActToggle:
		switch {
		case row.ID == 0 && row.Parent != 0:
			if c, ok := conflictByID(cs, row.Parent); ok && c.Decision == fsops.DecisionMerge {
				op.collapsed[row.Parent] = false
			}
		case row.ID != 0 && row.Decision == fsops.DecisionMerge:
			op.collapsed[row.ID] = !op.collapsed[row.ID]
		}
	case ActUnsetOnly:
		op.unsetOnly = !op.unsetOnly
		op.cursor = 0
	}
	return nil
}

// decide は決定を設定する。使えることは呼ぶ側で確かめている。
func (a *App) decide(id fsops.ConflictID, d fsops.Decision) {
	if err := a.op.plan.Decide(id, d); err != nil {
		a.logErr(err)
	}
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

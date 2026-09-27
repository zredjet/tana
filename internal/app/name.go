package app

import (
	"path/filepath"
	"strings"

	"github.com/zredjet/tana/internal/lineedit"
	"github.com/zredjet/tana/internal/msg"
)

// 名前の変更と新しいフォルダ（filer §8.7）。どちらも同じ形の入力欄の画面を使う。
// 変更・作成は作業用の goroutine で行う（応答しないネットワークドライブで UI を止めない。filer U5）。

// named は、名前の変更・フォルダの作成の結果。
type named struct {
	gen    int
	rename bool   // 名前の変更（偽ならフォルダの作成）
	pane   int    // 始めたペインの ID
	dir    string // 項目のあるフォルダ、フォルダを作った場所
	path   string // 名前の変更: 変えた項目の元のパス
	name   string // 新しい名前
	err    error
}

// NameView は、名前の変更・新しいフォルダの画面の内容（filer §8.7）。
type NameView struct {
	Edit *lineedit.Editor // 入力欄（元のバイト列を持つ。表示する形への置き換えは描くときに行う。U4）
	Name string           // 名前の変更: 今の名前（列挙で得たもの）
	Dir  string           // 項目のあるフォルダ、フォルダを作る場所
	Err  string           // fsops のエラーの文言（入力欄の下に出す）
	Busy bool             // 変更・作成を待っている
	role Role
}

func (v NameView) Role() Role { return v.role }

// nameComp は、名前の変更・新しいフォルダの入力欄。
type nameComp struct {
	base
	rename bool             // 名前の変更（偽なら新しいフォルダ）
	edit   *lineedit.Editor // 入力欄
	path   string           // 名前の変更: 変える項目のパス（列挙で得た名前から作る。U4）
	name   string           // 名前の変更: 今の名前
	dir    string           // 項目のあるフォルダ、フォルダを作る場所
	pane   int              // 始めたペインの ID
	err    string           // fsops のエラーの文言（入力欄の下に出す）
	busy   int              // 変更・作成を待っている処理の ID（0 なら待っていない）
}

func (c *nameComp) role() Role {
	if c.rename {
		return RoleRename
	}
	return RoleNewDir
}

func (c *nameComp) view(*App) View {
	return NameView{Edit: c.edit, Name: c.name, Dir: c.dir, Err: c.err, Busy: c.busy != 0, role: c.role()}
}

// commands: 変更・作成を待っている間は、入力を受け付けない（結果が別の名前のものにならないように）。
// Esc で待つのをやめられる。結果は届いたときに反映する（filer U5）。
func (c *nameComp) commands() commandTable {
	idle := func() bool { return c.busy == 0 }
	return editCommands(func() *lineedit.Editor { return c.edit }, idle, func() { c.err = "" /* 名前を直し始めたら、前のエラーを消す */ }).with(commandTable{
		ActCancel: {GateFree, func(a *App, _ Action) []Cmd { a.pop(); return nil }},
		ActSubmit: {GateFree, func(a *App, _ Action) []Cmd {
			if !idle() {
				return nil
			}
			return a.submitName(c)
		}},
	})
}

// rename は、ペイン p のカーソル行の項目の名前の変更を始める。入力欄の初期値は今の名前（列挙で得たバイト列）で、カーソルは拡張子の前に置く（filer §8.7）。
func (a *App) rename(p *Pane) {
	it, ok := p.current()
	if !ok || it.Parent {
		return
	}
	cursor := len(it.Name)
	if ext := filepath.Ext(it.Name); !it.IsDir() && ext != it.Name { // .bashrc のように . で始まる名前は、全体が名前
		cursor -= len(ext)
	}
	a.push(&nameComp{rename: true, edit: lineedit.New(it.Name, cursor), path: p.pathOf(it), name: it.Name, dir: p.dir, pane: p.id}, 0)
}

// newDir は、ペイン p のフォルダに新しいフォルダを作る入力欄を開く。
func (a *App) newDir(p *Pane) {
	if !p.loaded {
		return
	}
	a.push(&nameComp{edit: lineedit.New("", 0), dir: p.dir, pane: p.id}, 0)
}

// submitName は、入力した名前で変更・作成を始める。
// 名前の変更は、入力があったかどうかで変更したかを判断する。表示用の文字列と比べない（filer §8.7。U4）。
func (a *App) submitName(c *nameComp) []Cmd {
	if c.rename && !c.edit.Changed() {
		a.pop()
		return nil
	}
	c.busy = a.newID()
	m := named{gen: c.busy, rename: c.rename, pane: c.pane, dir: c.dir, path: c.path, name: c.edit.Text()}
	if m.rename {
		rename := a.cfg.Rename
		return []Cmd{{Run: func() any { m.err = rename(m.path, m.name); return m }}}
	}
	mkdir := a.cfg.Mkdir
	return []Cmd{{Run: func() any { m.err = mkdir(m.dir, m.name); return m }}}
}

// named は、名前の変更・フォルダの作成の結果を反映する。
// エラーは入力欄の下に出し、入力欄は閉じない。待つのをやめていれば、メッセージ行に出す（filer §8.7）。
// 成功しても失敗しても、項目のあるフォルダを表示しているペインを読み直す（失敗した場合も、途中の状態を見せるため）。
// 名前を変えたフォルダそのもの（とその中）を表示しているペインも読み直す（元のパスは消えたので、開ける祖先を表示する。filer §6）。
func (a *App) named(m named) []Cmd {
	var waiting *mounted // 結果を待っている入力欄（待つのをやめていれば nil）
	for _, x := range a.modals {
		if c, ok := x.c.(*nameComp); ok && c.busy == m.gen {
			waiting = x
		}
	}
	focus := m.name
	if m.err != nil {
		a.logErr(m.err)
		focus = ""
		if waiting != nil {
			c := waiting.c.(*nameComp)
			c.busy, c.err = 0, msg.NameError(m.err)
		} else {
			a.setMessage(msg.NameFailed(m.rename, msg.NameError(m.err)), true)
		}
	} else if waiting != nil {
		a.remove(waiting)
	}
	var cmds []Cmd
	dir, old := filepath.Clean(m.dir), filepath.Clean(m.path)
	for _, p := range a.panes {
		pd := filepath.Clean(p.dir)
		inside := m.rename && (pd == old || strings.HasPrefix(pd, old+string(filepath.Separator))) // 名前を変えたフォルダそのものか、その中
		if !p.loaded || pd != dir && !inside {
			continue
		}
		f := ""
		if p.id == m.pane && pd == dir {
			f = focus // 始めたペインでは、カーソルを新しい名前に置く
		}
		cmds = append(cmds, a.load(p, p.dir, loadReload, f)...)
	}
	return cmds
}

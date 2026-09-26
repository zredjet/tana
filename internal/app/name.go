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
	pane   int    // 始めたペイン
	dir    string // 項目のあるフォルダ、フォルダを作った場所
	path   string // 名前の変更: 変えた項目の元のパス
	name   string // 新しい名前
	err    error
}

// rename は、カーソル行の項目の名前の変更を始める。入力欄の初期値は今の名前（列挙で得たバイト列）で、カーソルは拡張子の前に置く（filer §8.7）。
func (a *App) rename() {
	p := a.panes[a.active]
	it, ok := p.current()
	if !ok || it.Parent {
		return
	}
	cursor := len(it.Name)
	if ext := filepath.Ext(it.Name); !it.IsDir() && ext != it.Name { // .bashrc のように . で始まる名前は、全体が名前
		cursor -= len(ext)
	}
	a.dialog = dialog{kind: DialogRename, edit: lineedit.New(it.Name, cursor), path: filepath.Join(p.dir, it.Name), name: it.Name,
		dir: p.dir, pane: a.active}
}

// newDir は、操作中のペインのフォルダに新しいフォルダを作る入力欄を開く。
func (a *App) newDir() {
	p := a.panes[a.active]
	if !p.loaded {
		return
	}
	a.dialog = dialog{kind: DialogNewDir, edit: lineedit.New("", 0), dir: p.dir, pane: a.active}
}

// doName は、名前の変更・新しいフォルダの入力欄の操作を行う。
func (a *App) doName(act Action) []Cmd {
	d := &a.dialog
	if d.busy != 0 {
		// 変更・作成を待っている間は、入力を受け付けない（結果が別の名前のものにならないように）。
		// Esc で待つのをやめられる。結果は届いたときに反映する（filer U5）。
		if act.Kind == ActCancel {
			a.dialog = dialog{}
		}
		return nil
	}
	if edit(d.edit, act) {
		d.err = "" // 名前を直し始めたら、前のエラーを消す
		return nil
	}
	switch act.Kind {
	case ActCancel:
		a.dialog = dialog{}
	case ActSubmit:
		return a.submitName()
	}
	return nil
}

// submitName は、入力した名前で変更・作成を始める。
// 名前の変更は、入力があったかどうかで変更したかを判断する。表示用の文字列と比べない（filer §8.7。U4）。
func (a *App) submitName() []Cmd {
	d := &a.dialog
	if d.kind == DialogRename && !d.edit.Changed() {
		a.dialog = dialog{}
		return nil
	}
	a.gen++
	d.busy = a.gen
	m := named{gen: a.gen, rename: d.kind == DialogRename, pane: d.pane, dir: d.dir, path: d.path, name: d.edit.Text()}
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
func (a *App) named(m named) []Cmd {
	d := &a.dialog
	waiting := (d.kind == DialogRename || d.kind == DialogNewDir) && d.busy == m.gen
	focus := m.name
	if m.err != nil {
		a.logErr(m.err)
		focus = ""
		if waiting {
			d.busy, d.err = 0, msg.NameError(m.err)
		} else {
			a.setMessage(msg.NameFailed(m.rename, msg.NameError(m.err)), true)
		}
	} else if waiting {
		a.dialog = dialog{}
	}
	var cmds []Cmd
	dir := filepath.Clean(m.dir)
	for i, p := range a.panes {
		pd := filepath.Clean(p.dir)
		inside := m.rename && strings.HasPrefix(pd, filepath.Clean(m.path)+string(filepath.Separator)) // 名前を変えたフォルダの中
		if !p.loaded || pd != dir && !inside {
			continue
		}
		f := ""
		if i == m.pane && pd == dir {
			f = focus // 始めたペインでは、カーソルを新しい名前に置く
		}
		cmds = append(cmds, a.load(i, p.dir, loadReload, f)...)
	}
	return cmds
}

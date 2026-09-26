package app

import "github.com/zredjet/tana/internal/listing"

// Pane は、1 つのフォルダの表示（フォルダ、項目、カーソル、マーク。filer §4）。
type Pane struct {
	dir     string
	loaded  bool           // 一覧を読み込んだ（最初の読み込みに失敗すれば false のまま）
	items   []listing.Item // すべての項目（隠しファイルを含む）
	visible []int          // 表示する項目の items の添字
	cursor  int            // visible の添字
	top     int            // 表示する最初の行（visible の添字）
	rows    int            // 最後に描いた一覧の行数（ページ単位の移動に使う）
	marks   map[string]struct{}
	load    *loading

	targets map[string]string   // リンク先（名前 → リンク先）
	pending map[string]struct{} // 読み取り中のリンク先
	listGen int                 // 一覧を置き換えた回数（古いリンク先の読み取りの結果を捨てる）
	parent  *parentList         // 親フォルダの一覧（表示形式が求めたときだけ読む）
}

// Dir は、表示しているフォルダ（リンクに入ったときはリンクのパスのまま。filer §6）を返す。
// 最初の読み込みが終わる前は、読み込もうとしているフォルダ。
func (p *Pane) Dir() string { return p.dir }

// Loaded は、一覧を読み込んだかを返す。
func (p *Pane) Loaded() bool { return p.loaded }

// Loading は、読み込み中の表示を出すか（読み込みが slowLoad を超えた）を返す。
func (p *Pane) Loading() bool { return p.load != nil && p.load.slow }

// Len は、表示する項目の数（.. を含む）を返す。
func (p *Pane) Len() int { return len(p.visible) }

// Item は、表示する i 番目の項目を返す。
func (p *Pane) Item(i int) listing.Item { return p.items[p.visible[i]] }

// Cursor は、カーソルの位置（表示する項目の番号）を返す。
func (p *Pane) Cursor() int { return p.cursor }

// Marked は、名前 name の項目がマークされているかを返す。
func (p *Pane) Marked(name string) bool {
	_, ok := p.marks[name]
	return ok
}

// Counts は、表示する項目の数（.. を除く）、マークの数、隠している項目の数を返す。
// .. は並びの先頭にあり、隠れないので、数えずに求める（描くたびに呼ぶ）。
func (p *Pane) Counts() (items, marks, hidden int) {
	items = len(p.visible)
	if items > 0 && p.items[p.visible[0]].Parent {
		items--
	}
	return items, len(p.marks), len(p.items) - len(p.visible)
}

// LinkTarget は、名前 name のリンクのリンク先を返す（まだ読んでいなければ false）。
func (p *Pane) LinkTarget(name string) (string, bool) {
	t, ok := p.targets[name]
	return t, ok
}

// Window は、一覧を rows 行で描くときの最初の行を返す。カーソルが見えるように動かす。
func (p *Pane) Window(rows int) int {
	p.rows = rows
	if rows <= 0 {
		return p.cursor
	}
	if p.cursor < p.top {
		p.top = p.cursor
	}
	if p.cursor >= p.top+rows {
		p.top = p.cursor - rows + 1
	}
	p.top = max(min(p.top, len(p.visible)-rows), 0)
	return p.top
}

// current は、カーソル行の項目を返す。
func (p *Pane) current() (listing.Item, bool) {
	if p.cursor < 0 || p.cursor >= len(p.visible) {
		return listing.Item{}, false
	}
	return p.Item(p.cursor), true
}

// move は、カーソルを n 行動かす（端で止める）。
func (p *Pane) move(n int) {
	p.cursor = max(min(p.cursor+n, len(p.visible)-1), 0)
}

func (p *Pane) toggleMark(name string) {
	if p.Marked(name) {
		delete(p.marks, name)
	} else {
		p.marks[name] = struct{}{}
	}
}

// markAll は、表示している項目（.. を除く）をすべてマークする。すでにすべてマークしていれば、マークをすべて外す。
func (p *Pane) markAll() {
	all := true
	for _, i := range p.visible {
		if it := p.items[i]; !it.Parent && !p.Marked(it.Name) {
			all = false
			break
		}
	}
	if all {
		clear(p.marks)
		return
	}
	for _, i := range p.visible {
		if it := p.items[i]; !it.Parent {
			p.marks[it.Name] = struct{}{}
		}
	}
}

// set は、読み込んだ一覧を置く。
// keep（同じフォルダの再読み込み）なら、マークを名前で保ち、カーソルを同じ名前の項目（focus があればその名前の項目。名前を変えた後など）に置く。
// なければ同じ行の位置に置く（filer §6）。
// そうでなければ、マークを外し、カーソルを focus の名前の項目（なければ先頭）に置く。
func (p *Pane) set(dir string, items []listing.Item, showHidden, keep bool, focus string) {
	oldCursor := p.cursor
	if keep {
		if it, ok := p.current(); ok && focus == "" {
			focus = it.Name
		}
	} else {
		clear(p.marks)
		p.top = 0
		oldCursor = 0
	}
	p.dir, p.items, p.loaded = dir, items, true
	p.listGen++
	clear(p.targets)
	p.pending = map[string]struct{}{}
	names := make(map[string]bool, len(items))
	for _, it := range items {
		names[it.Name] = true
	}
	for name := range p.marks {
		if !names[name] {
			delete(p.marks, name)
		}
	}
	p.visible = p.visible[:0] // 古い一覧の添字を残さない（filter はカーソルを行の位置のまま収める）
	p.cursor = oldCursor
	p.filter(showHidden)
	if focus != "" {
		for vi, i := range p.visible {
			if it := p.items[i]; it.Name == focus && !it.Parent {
				p.cursor = vi
				break
			}
		}
	}
	p.move(0)
}

// filter は、表示する項目を決め直す。隠しファイルを隠すときは、隠れた項目のマークを外す（見えない項目を操作の対象にしない。filer §6）。
// カーソルは同じ項目に置き、隠れたときは次に見える項目（なければ前）に置く。
func (p *Pane) filter(showHidden bool) {
	at := -1 // カーソルの項目の items の添字
	if p.cursor >= 0 && p.cursor < len(p.visible) {
		at = p.visible[p.cursor]
	}
	p.visible = p.visible[:0]
	for i, it := range p.items {
		if showHidden || !it.Hidden || it.Parent {
			p.visible = append(p.visible, i)
		} else {
			delete(p.marks, it.Name)
		}
	}
	if at < 0 {
		p.move(0)
		return
	}
	p.cursor = len(p.visible) - 1
	for vi, i := range p.visible {
		if i >= at {
			p.cursor = vi
			break
		}
	}
	p.move(0)
}

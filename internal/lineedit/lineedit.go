package lineedit

import (
	"strings"

	"github.com/zredjet/tana/internal/textwidth"
)

// Editor は、1 行の入力欄の状態（tui §7）。
// 文字列は元のバイト列のまま持ち、カーソルは常に書記素クラスタの境界にある。表示用の置き換えは描画のときに行う（filer U4）。
type Editor struct {
	text    string
	cursor  int  // バイトの位置（書記素クラスタの境界）
	changed bool // 文字列を変えた操作があった
	offset  int  // 表示の範囲の始め（View が使う）
}

// New は、text を持ち、カーソルを cursor（バイトの位置）に置いた入力欄を作る。
// cursor は文字列の中に収め、書記素クラスタの途中なら、その書記素クラスタの終わりに動かす。
func New(text string, cursor int) *Editor {
	e := &Editor{text: text}
	e.cursor = e.snap(min(max(cursor, 0), len(text)))
	return e
}

// Text は文字列（元のバイト列）を返す。
func (e *Editor) Text() string { return e.text }

// Cursor はカーソルのバイトの位置を返す。
func (e *Editor) Cursor() int { return e.cursor }

// Changed は、文字列を変えた操作があったかを返す。文字列の比較では判断しない（入力して消した場合も true。filer §8.7）。
func (e *Editor) Changed() bool { return e.changed }

// next は、pos（境界）の次の境界を返す。pos が末尾なら pos。
func (e *Editor) next(pos int) int {
	c, _ := textwidth.Next(e.text[pos:])
	return pos + len(c.Text)
}

// prev は、pos より前の境界のうち、いちばん後ろのものを返す。pos が 0 なら 0。
func (e *Editor) prev(pos int) int {
	p := 0
	for i := 0; i < pos; i = e.next(i) {
		p = i
	}
	return p
}

// snap は、pos を含む書記素クラスタの終わり（pos 以上の境界のうち、いちばん前のもの）を返す。
func (e *Editor) snap(pos int) int {
	i := 0
	for i < pos {
		i = e.next(i)
	}
	return i
}

// Insert は、カーソルの位置に s を入れ、カーソルをその後ろに動かす。
// 改行（CR と LF）と、端末に出してはいけない文字（制御文字、双方向の制御文字、不正な UTF-8 のバイト）は取り除く（tui §7）。
// 入れた文字が前後と 1 つの書記素クラスタになる場合は、カーソルをその書記素クラスタの終わりに動かす。
func (e *Editor) Insert(s string) {
	var b strings.Builder
	for c := range textwidth.All(s) {
		if c.Class != textwidth.Forbidden && c.Class != textwidth.InvalidByte {
			b.WriteString(c.Text)
		}
	}
	if b.Len() == 0 {
		return
	}
	e.text = e.text[:e.cursor] + b.String() + e.text[e.cursor:]
	e.cursor = e.snap(e.cursor + b.Len())
	e.changed = true
}

// DeleteBackward は、カーソルの前の書記素クラスタを消す。
func (e *Editor) DeleteBackward() {
	if e.cursor == 0 {
		return
	}
	p := e.prev(e.cursor)
	e.text = e.text[:p] + e.text[e.cursor:]
	e.cursor = e.snap(p) // 消した結果、前後が 1 つの書記素クラスタになることがある
	e.changed = true
}

// DeleteForward は、カーソルの後ろの書記素クラスタを消す。
func (e *Editor) DeleteForward() {
	if e.cursor == len(e.text) {
		return
	}
	n := e.next(e.cursor)
	e.text = e.text[:e.cursor] + e.text[n:]
	e.cursor = e.snap(e.cursor)
	e.changed = true
}

// Left は、カーソルを 1 つ前の書記素クラスタの境界に動かす。
func (e *Editor) Left() { e.cursor = e.prev(e.cursor) }

// Right は、カーソルを 1 つ後ろの書記素クラスタの境界に動かす。
func (e *Editor) Right() { e.cursor = e.next(e.cursor) }

// Home は、カーソルを先頭に動かす。
func (e *Editor) Home() { e.cursor = 0 }

// End は、カーソルを末尾に動かす。
func (e *Editor) End() { e.cursor = len(e.text) }

// View は、幅 width の欄に表示する範囲。
type View struct {
	Start, End int // 表示する文字列の範囲（Text() のバイトの位置。書記素クラスタの境界）
	CursorCol  int // 欄の左端からのカーソルの桁
}

// View は、幅 width の欄に表示する範囲を決める。カーソルが常に見えるように横に動かす（カーソルのための 1 桁を残す）。
// 幅は textwidth の表示する形の幅で数える。右端にかかる幅 2 以上の書記素クラスタは含めない。
// width が 1 より小さければ、カーソルの位置の空の範囲を返す。
func (e *Editor) View(width int) View {
	if width < 1 {
		return View{Start: e.cursor, End: e.cursor}
	}
	// 編集で範囲の始めが書記素クラスタの途中になっていれば、その書記素クラスタの始めに戻す。
	e.offset = min(e.offset, e.cursor)
	if e.offset > 0 && e.snap(e.offset) != e.offset {
		e.offset = e.prev(e.offset)
	}
	for textwidth.Width(e.text[e.offset:e.cursor]) > width-1 {
		e.offset = e.next(e.offset)
	}
	// 左に余裕があれば戻して欄を埋める（消して短くなった場合）。
	for e.offset > 0 {
		p := e.prev(e.offset)
		if textwidth.Width(e.text[p:]) > width-1 {
			break
		}
		e.offset = p
	}
	end, w := e.offset, 0
	for end < len(e.text) {
		c, _ := textwidth.Next(e.text[end:])
		if w+c.Width > width {
			break
		}
		w += c.Width
		end += len(c.Text)
	}
	return View{Start: e.offset, End: max(end, e.cursor), CursorCol: textwidth.Width(e.text[e.offset:e.cursor])}
}

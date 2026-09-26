package screen

import (
	"slices"
	"strings"

	"github.com/zredjet/tana/internal/textwidth"
)

// Color は 16 色と既定の色（tui §1）。
type Color uint8

const (
	ColorDefault Color = iota
	ColorBlack
	ColorRed
	ColorGreen
	ColorYellow
	ColorBlue
	ColorMagenta
	ColorCyan
	ColorWhite
	ColorBrightBlack
	ColorBrightRed
	ColorBrightGreen
	ColorBrightYellow
	ColorBrightBlue
	ColorBrightMagenta
	ColorBrightCyan
	ColorBrightWhite
)

// Attr は色以外の属性。NO_COLOR でも残す（filer §9.5）。
type Attr uint8

const (
	AttrBold Attr = 1 << iota
	AttrDim
	AttrUnderline
	AttrReverse
)

// Style はセルの見た目。範囲外の色（ColorBrightWhite より大きい値）は既定の色に、未知の属性は無いものにして格子に置く。
type Style struct {
	FG, BG Color
	Attr   Attr
}

// valid は、範囲外の色を既定の色にし、未知の属性を捨てた見た目を返す（格子と、端末に出すものを合わせる。T6）。
func (st Style) valid() Style {
	if st.FG > ColorBrightWhite {
		st.FG = ColorDefault
	}
	if st.BG > ColorBrightWhite {
		st.BG = ColorDefault
	}
	st.Attr &= AttrBold | AttrDim | AttrUnderline | AttrReverse
	return st
}

// Region は画面の矩形（0 から数える）。画面の外にはみ出してもよい（画面の中だけに描く）。
type Region struct{ X, Y, W, H int }

// Cell は、格子の 1 つのセル。幅 N（2 以上）の書記素クラスタは、先頭のセル（Width が N）と、N−1 個の続きのセル（Width が 0、Text が空）。
// Text は textwidth の表示する形。
type Cell struct {
	Text  string
	Width int
	Style Style
}

type cell struct {
	text   string
	width  int
	style  Style
	varies bool // 幅が端末によって違いうる（textwidth の印）。この後で位置を指定し直す
	anchor bool // 欄の始め（Put で置き始めた位置、Fill の領域の左端）。ここで位置を指定し直す
}

var blankCell = cell{text: " ", width: 1}

func (c cell) same(d cell) bool { return c.text == d.text && c.width == d.width && c.style == d.style }

// Screen はセルの格子。描画の操作で格子を変え、Flush で前回の出力からの差分を端末に書く（tui §6）。
// 1 つの goroutine から使うこと。
type Screen struct {
	// NoColor なら、色を出力しない。太字・暗く・下線・反転は残す（NO_COLOR。filer §9.5）。
	NoColor bool

	cols, rows int
	back       []cell // 描いた内容
	front      []cell // 端末に出力した内容
	frontValid bool   // front が端末の内容を表しているか（New・Resize・Invalidate の後は false）

	cursorX, cursorY int
	cursorVisible    bool

	termCursorVisible, termCursorKnown bool  // 端末のカーソルの表示・非表示（出力したもの）
	termCursorX, termCursorY           int   // 端末のカーソルを最後に置いた位置（表示しているときだけ意味を持つ）
	termStyle                          Style // 端末に最後に設定した見た目
	termStyleKnown                     bool
}

// New は、空白で埋めた cols×rows の画面を作る。カーソルは隠す。最初の Flush で全体を書く。
func New(cols, rows int) *Screen {
	s := &Screen{}
	s.Resize(cols, rows)
	return s
}

// Size は画面の大きさを返す。
func (s *Screen) Size() (cols, rows int) { return s.cols, s.rows }

// Resize は画面の大きさを変える。内容は空白に戻り、次の Flush で全体を書く（端末の大きさが変わったとき）。
func (s *Screen) Resize(cols, rows int) {
	s.cols, s.rows = max(cols, 0), max(rows, 0)
	s.back = make([]cell, s.cols*s.rows)
	for i := range s.back {
		s.back[i] = blankCell
	}
	s.front = make([]cell, len(s.back))
	s.Invalidate()
}

// Invalidate は、次の Flush で全体を書かせる（端末の内容が分からなくなったとき）。
func (s *Screen) Invalidate() {
	s.frontValid = false
	s.termCursorKnown = false
	s.termStyleKnown = false
}

// SetCursor は、本物のカーソルの位置と表示・非表示を設定する（IME の変換中の文字はここに出る。filer VU4）。
// 位置は画面の中に収める。
func (s *Screen) SetCursor(x, y int, visible bool) {
	s.cursorX, s.cursorY, s.cursorVisible = x, y, visible
}

// Cell は (x, y) のセルを返す。画面の外なら空のセル。
func (s *Screen) Cell(x, y int) Cell {
	if x < 0 || x >= s.cols || y < 0 || y >= s.rows {
		return Cell{}
	}
	c := s.back[y*s.cols+x]
	return Cell{Text: c.text, Width: c.width, Style: c.style}
}

// Row は、y 行目の文字列（セルの表示する形をつないだもの）を返す。ゴールデンファイルのテスト用。
func (s *Screen) Row(y int) string {
	if y < 0 || y >= s.rows {
		return ""
	}
	var b strings.Builder
	for _, c := range s.back[y*s.cols : (y+1)*s.cols] {
		b.WriteString(c.text)
	}
	return b.String()
}

func (s *Screen) cell(x, y int) *cell { return &s.back[y*s.cols+x] }

// clip は、領域 r の行 y の、画面の中の列の範囲 [left, right) を返す。行が領域か画面の外なら ok は false。
func (s *Screen) clip(r Region, y int) (left, right int, ok bool) {
	if y < r.Y || y >= r.Y+r.H || y < 0 || y >= s.rows {
		return 0, 0, false
	}
	left, right = max(r.X, 0), min(r.X+r.W, s.cols)
	return left, right, left < right
}

// breakAt は、行 y の x のセルを含む幅 2 以上の書記素クラスタを、空白にする（そのクラスタの見た目のまま）。
// 描く範囲 [left, right) の外に残る部分も空白にする（T5 の例外。端末も、幅 2 以上の文字の一部に書くと残りを消す）。
func (s *Screen) breakAt(x, y int) {
	row := s.back[y*s.cols : (y+1)*s.cols]
	h := x
	for h > 0 && row[h].width == 0 {
		h--
	}
	if row[h].width <= 1 {
		return
	}
	st, w := row[h].style, row[h].width // row[h] を書き換える前に読む
	for i := h; i < h+w && i < s.cols; i++ {
		row[i] = cell{text: " ", width: 1, style: st}
	}
}

// set は、行 y の x から幅 w の書記素クラスタ c を置く（呼び出し側が、画面と領域の中に収まることを確かめる）。
func (s *Screen) set(x, y int, text string, w int, st Style, varies, anchor bool) {
	for i := x; i < x+w; i++ {
		s.breakAt(i, y)
	}
	row := s.back[y*s.cols:]
	row[x] = cell{text: text, width: w, style: st, varies: varies, anchor: anchor}
	for i := x + 1; i < x+w; i++ {
		row[i] = cell{style: st}
	}
}

// Put は、領域 r の中の (x, y)（領域の左上からの位置）から text を置き、領域の中で使った桁数を返す（T5）。
// 書記素クラスタは textwidth の表示する形で置く（端末に出してはいけない文字は ? など。T2）。
// 領域からはみ出す部分は切る。書記素クラスタの途中では切らず、領域の端にかかる幅 2 以上のものは、領域の中の部分を空白で埋める。
func (s *Screen) Put(r Region, x, y int, text string, st Style) int {
	row := r.Y + y
	left, right, ok := s.clip(r, row)
	if !ok {
		return 0
	}
	st = st.valid()
	col := r.X + x
	used, first := 0, true
	var buf [4]textwidth.Cluster
	for c := range textwidth.All(text) {
		// 通常の書記素クラスタは、表示する形が元の文字列と同じ（textwidth）なので、そのまま置く。
		// それ以外は、表示する形を通常の文字の書記素クラスタに分けて置く（国旗の ?? は ? が 2 つ）。
		parts := append(buf[:0], c)
		if c.Class != textwidth.Normal {
			parts = slices.AppendSeq(buf[:0], textwidth.All(c.Display))
		}
		for _, d := range parts {
			w := d.Width
			switch {
			case col+w <= left:
				// 領域の左の外
			case col >= right:
				return used
			case col < left || col+w > right:
				// 領域の端にかかる。領域の中の部分を空白で埋める。
				for i := max(col, left); i < min(col+w, right); i++ {
					s.set(i, row, " ", 1, st, false, first)
					first = false
					used++
				}
			default:
				s.set(col, row, d.Display, w, st, d.Varies, first)
				first = false
				used += w
			}
			col += w
		}
	}
	return used
}

// Fill は、領域 r を空白で塗る（T5）。領域の左端は欄の始めになる。
func (s *Screen) Fill(r Region, st Style) {
	st = st.valid()
	for y := max(r.Y, 0); y < min(r.Y+r.H, s.rows); y++ {
		left, right, ok := s.clip(r, y)
		if !ok {
			continue
		}
		for x := left; x < right; x++ {
			s.set(x, y, " ", 1, st, false, x == left)
		}
	}
}

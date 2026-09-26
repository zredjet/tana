// Package emu は、screen のテストのための小さな端末エミュレータ（docs/SPEC-tui.md §6）。
//
// 解釈するのは、screen が出力するシーケンスだけ: カーソルの位置（ESC[行;桁H）、SGR（ESC[…m）、
// 画面の消去（ESC[2J）、カーソルの表示・非表示（ESC[?25h・ESC[?25l）と、文字。
// それ以外の制御文字・シーケンスと、端末に出してはいけない文字は Errors に記録する（T2 の確認）。
// 自動改行はない（DECAWM を切った状態）。幅は textwidth で数える。Width を設定すると、幅の計算が合わない端末を模擬できる。
//
// 文字は、制御シーケンスで区切られた並びごとに書記素クラスタに分ける。
// 並びの中で隣り合う文字は結合しうるが、カーソルの位置の指定をはさめば結合しない、と仮定する。
package emu

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/zredjet/tana/internal/textwidth"
)

// Style は SGR で設定する属性。
type Style struct {
	FG, BG                        int // 0 は既定の色。30〜37・90〜97（前景）、40〜47・100〜107（背景）
	Bold, Dim, Underline, Reverse bool
}

// Cell は 1 つのセル。幅 2 以上の書記素クラスタの続きのセルは Width が 0 で、Text が空。
type Cell struct {
	Text  string
	Width int
	Style Style
}

var blank = Cell{Text: " ", Width: 1}

// Terminal は端末の状態。
type Terminal struct {
	Cols, Rows    int
	Cells         []Cell // 行ごとに Cols 個
	X, Y          int    // カーソル（0 から数える）
	CursorVisible bool
	Style         Style
	Errors        []string
	// Width は書記素クラスタの幅（nil なら textwidth の表示幅）。幅の計算が合わない端末の模擬に使う。
	Width func(cluster string) int
}

// New は、空白で埋めた端末を作る。カーソルは表示している。
func New(cols, rows int) *Terminal {
	t := &Terminal{Cols: cols, Rows: rows, Cells: make([]Cell, cols*rows), CursorVisible: true}
	t.clear()
	return t
}

func (t *Terminal) clear() {
	for i := range t.Cells {
		t.Cells[i] = blank
	}
}

// Row は、y 行目の文字列（続きのセルを除いて、セルの文字をつないだもの）を返す。
func (t *Terminal) Row(y int) string {
	var b strings.Builder
	for x := range t.Cols {
		b.WriteString(t.Cells[y*t.Cols+x].Text)
	}
	return b.String()
}

// Write は、出力を解釈する。
func (t *Terminal) Write(p []byte) (int, error) {
	s := string(p)
	for len(s) > 0 {
		if s[0] == 0x1b {
			n := t.escape(s)
			s = s[n:]
			continue
		}
		// 次の制御文字（ESC を含む）までを、文字の並びとして書く。
		end := strings.IndexFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
		if end == 0 {
			t.error("control byte %#x", s[0])
			s = s[1:]
			continue
		}
		if end < 0 {
			end = len(s)
		}
		t.text(s[:end])
		s = s[end:]
	}
	return len(p), nil
}

func (t *Terminal) error(format string, a ...any) {
	t.Errors = append(t.Errors, fmt.Sprintf(format, a...))
}

// escape は、ESC で始まる s を解釈し、使ったバイト数を返す。
func (t *Terminal) escape(s string) int {
	if len(s) < 2 || s[1] != '[' {
		t.error("unknown escape %q", s[:min(len(s), 2)])
		return 1
	}
	i := 2
	for i < len(s) && (s[i] >= 0x30 && s[i] <= 0x3f) {
		i++
	}
	if i >= len(s) || s[i] < 0x40 || s[i] > 0x7e {
		t.error("incomplete CSI %q", s[:min(len(s), i+1)])
		return i
	}
	params, final, seq := s[2:i], s[i], s[:i+1]
	switch {
	case final == 'H':
		row, col, ok := twoParams(params)
		if !ok {
			t.error("bad CUP %q", seq)
			break
		}
		t.X, t.Y = col-1, row-1
		if t.X < 0 || t.X >= t.Cols || t.Y < 0 || t.Y >= t.Rows {
			t.error("CUP outside the screen %q", seq)
		}
	case final == 'm':
		t.sgr(params, seq)
	case final == 'J' && params == "2":
		t.clear()
	case final == 'h' && params == "?25":
		t.CursorVisible = true
	case final == 'l' && params == "?25":
		t.CursorVisible = false
	default:
		t.error("unknown CSI %q", seq)
	}
	return i + 1
}

func twoParams(p string) (int, int, bool) {
	if p == "" {
		return 1, 1, true
	}
	a, b, ok := strings.Cut(p, ";")
	if !ok {
		return 0, 0, false
	}
	x, err1 := strconv.Atoi(a)
	y, err2 := strconv.Atoi(b)
	return x, y, err1 == nil && err2 == nil && x >= 1 && y >= 1
}

func (t *Terminal) sgr(params, seq string) {
	if params == "" {
		t.Style = Style{}
		return
	}
	for p := range strings.SplitSeq(params, ";") {
		n, err := strconv.Atoi(p)
		switch {
		case err != nil:
			t.error("bad SGR %q", seq)
		case n == 0:
			t.Style = Style{}
		case n == 1:
			t.Style.Bold = true
		case n == 2:
			t.Style.Dim = true
		case n == 4:
			t.Style.Underline = true
		case n == 7:
			t.Style.Reverse = true
		case 30 <= n && n <= 37, 90 <= n && n <= 97:
			t.Style.FG = n
		case n == 39:
			t.Style.FG = 0
		case 40 <= n && n <= 47, 100 <= n && n <= 107:
			t.Style.BG = n
		case n == 49:
			t.Style.BG = 0
		default:
			t.error("unknown SGR %q", seq)
		}
	}
}

// text は、制御文字を含まない文字の並びを、書記素クラスタごとにカーソルの位置に書く。
func (t *Terminal) text(s string) {
	for c := range textwidth.All(s) {
		if c.Class != textwidth.Normal {
			// screen は表示する形（通常の文字だけ）を出力する。それ以外が届いたら T2 を破っている。
			t.error("%v cluster %q written", c.Class, c.Text)
		}
		w := c.Width
		if t.Width != nil {
			w = t.Width(c.Text)
		}
		t.put(c.Text, w)
	}
}

// put は、カーソルの位置に幅 w のクラスタを置き、カーソルを進める。右端を越える部分は捨てる（自動改行なし）。
func (t *Terminal) put(text string, w int) {
	if t.Y < 0 || t.Y >= t.Rows || t.X >= t.Cols {
		return
	}
	w = max(w, 1)
	end := min(t.X+w, t.Cols)
	for x := t.X; x < end; x++ {
		t.breakCluster(x)
	}
	row := t.Cells[t.Y*t.Cols:]
	row[t.X] = Cell{Text: text, Width: end - t.X, Style: t.Style}
	for x := t.X + 1; x < end; x++ {
		row[x] = Cell{Style: t.Style}
	}
	t.X += w
}

// breakCluster は、x のセルを含む幅 2 以上のクラスタを空白にする（端末は、幅 2 以上の文字の一部に書くと残りも消す）。
func (t *Terminal) breakCluster(x int) {
	row := t.Cells[t.Y*t.Cols : (t.Y+1)*t.Cols]
	h := x
	for h > 0 && row[h].Width == 0 {
		h--
	}
	if row[h].Width <= 1 {
		return
	}
	st, w := row[h].Style, row[h].Width // row[h] を書き換える前に読む
	for i := h; i < h+w && i < t.Cols; i++ {
		row[i] = Cell{Text: " ", Width: 1, Style: st}
	}
}

package screen

import (
	"bytes"
	"io"
	"strconv"
	"strings"

	"github.com/zredjet/tana/internal/textwidth"
)

// Flush は、前回の出力からの差分を、エスケープシーケンス（カーソルの位置、SGR、カーソルの表示・非表示）と文字にして w に書く（tui §6）。
// 何も変わっていなければ、何も書かない。書き込みに失敗したら、次の Flush で全体を書く。
//
// 次の位置では、カーソルの位置を指定し直す（T6）: 行の始め、欄の始め（Put で置き始めた位置と Fill の左端）、
// 幅が端末によって違いうる書記素クラスタの後、左のセルと結合しうる書記素クラスタの前と後ろ
// （前で指定し直すと分かれて描く端末と、それでも左のセルにまとめて桁を進めない端末がある。tui §9 の VT7）。
// 幅が違いうる書記素クラスタは、幅 2 以上なら先にその幅を空白で消してから書き（端末が狭く描いても前の内容が残らない）、
// 後ろの 1 つを、変わっていなくても書き直す（端末が広く描いてはみ出した分を直す）。
// 左のセルと結合しうる書記素クラスタも、先にその幅を空白で消してから書く（左のセルにまとめる端末で、前の内容が残らない）。
// 1 回の出力は、同期出力（mode 2026）の開始と終わりで囲む（対応しない端末は無視する。VT5）。
func (s *Screen) Flush(w io.Writer) error {
	full := !s.frontValid
	changed := full
	for i := 0; !changed && i < len(s.back); i++ {
		changed = !s.back[i].same(s.front[i])
	}
	cx, cy := min(max(s.cursorX, 0), s.cols-1), min(max(s.cursorY, 0), s.rows-1)
	showCursor := s.cursorVisible && s.cols > 0 && s.rows > 0
	cursorChanged := !s.termCursorKnown || s.termCursorVisible != showCursor || showCursor && (s.termCursorX != cx || s.termCursorY != cy)
	if !changed && !cursorChanged {
		return nil
	}

	o := output{s: s}
	if full {
		o.b.WriteString("\x1b[0m")
		o.style, o.styleKnown = Style{}, true
	} else {
		o.style, o.styleKnown = s.termStyle, s.termStyleKnown
	}
	// 描いている間はカーソルを隠す（ちらつかないように）。
	if changed && (!s.termCursorKnown || s.termCursorVisible) {
		o.b.WriteString("\x1b[?25l")
		s.termCursorVisible, s.termCursorKnown = false, true
	}
	if changed {
		for y := range s.rows {
			o.row(y, full)
		}
	}
	if showCursor {
		o.cup(cx, cy)
		if !s.termCursorKnown || !s.termCursorVisible {
			o.b.WriteString("\x1b[?25h")
		}
		s.termCursorX, s.termCursorY = cx, cy
	} else if !s.termCursorKnown || s.termCursorVisible {
		o.b.WriteString("\x1b[?25l")
	}
	s.termCursorVisible, s.termCursorKnown = showCursor, true
	s.termStyle, s.termStyleKnown = o.style, o.styleKnown

	copy(s.front, s.back)
	s.frontValid = true
	out := make([]byte, 0, len(syncStart)+o.b.Len()+len(syncEnd))
	out = append(append(append(out, syncStart...), o.b.Bytes()...), syncEnd...)
	if _, err := w.Write(out); err != nil {
		s.Invalidate()
		return err
	}
	return nil
}

// 同期出力（mode 2026）の開始と終わり。対応する端末は、終わりを受けてからまとめて表示する（tui §6。VT5）。
const (
	syncStart = "\x1b[?2026h"
	syncEnd   = "\x1b[?2026l"
)

// output は、1 回の Flush の出力と、端末の状態（カーソルの位置、SGR）の見込み。
type output struct {
	s          *Screen
	b          bytes.Buffer
	x, y       int  // 端末のカーソルの位置
	known      bool // x, y が確か（幅が違いうる書記素クラスタを書いた後は不確か）
	style      Style
	styleKnown bool
}

func (o *output) cup(x, y int) {
	o.b.WriteString("\x1b[")
	o.b.WriteString(strconv.Itoa(y + 1))
	o.b.WriteByte(';')
	o.b.WriteString(strconv.Itoa(x + 1))
	o.b.WriteByte('H')
	o.x, o.y, o.known = x, y, true
}

// sgr は、SGR で見た目を st にする（NO_COLOR なら色を捨てる）。すでにその見た目なら何も書かない。
func (o *output) sgr(st Style) {
	if o.s.NoColor {
		st.FG, st.BG = ColorDefault, ColorDefault
	}
	if o.styleKnown && o.style == st {
		return
	}
	o.b.WriteString("\x1b[0")
	for _, a := range []struct {
		a    Attr
		code string
	}{{AttrBold, ";1"}, {AttrDim, ";2"}, {AttrUnderline, ";4"}, {AttrReverse, ";7"}} {
		if st.Attr&a.a != 0 {
			o.b.WriteString(a.code)
		}
	}
	if st.FG != ColorDefault {
		o.b.WriteString(";" + strconv.Itoa(colorCode(st.FG, 30)))
	}
	if st.BG != ColorDefault {
		o.b.WriteString(";" + strconv.Itoa(colorCode(st.BG, 40)))
	}
	o.b.WriteByte('m')
	o.style, o.styleKnown = st, true
}

// colorCode は、色の SGR の番号を返す（base は前景 30、背景 40。明るい色は 90・100 から）。
func colorCode(c Color, base int) int {
	if c <= ColorWhite {
		return base + int(c-ColorBlack)
	}
	return base + 60 + int(c-ColorBrightBlack)
}

// row は、y 行目の、変わったセルを書く（full ならすべて）。
func (o *output) row(y int, full bool) {
	s := o.s
	force := false // 幅が違いうる書記素クラスタの後の 1 つは、変わっていなくても書く
	for x := 0; x < s.cols; {
		i := y*s.cols + x
		c := s.back[i]
		if c.width == 0 {
			x++ // 続きのセル（先頭のセルと一緒に書く）
			continue
		}
		if !full && !force && c.same(s.front[i]) {
			x += c.width
			continue
		}
		force = false
		// 左のセルと結合しうる書記素クラスタ（VT7）。左のセルにまとめて、自分のセルに書かない端末があるので、
		// 前の内容が残って次の書記素クラスタと結合しないように、先に空白で消す（clearJoining）。
		joinsLeft := x > 0 && joins(s.headText(x-1, y), c.text)
		move := !o.known || o.x != x || o.y != y || c.anchor
		switch {
		case joinsLeft:
			o.clearJoining(x, y, c)
			move = true
		case c.varies && c.width >= 2:
			o.cup(x, y)
			o.sgr(c.style)
			o.b.WriteString(strings.Repeat(" ", c.width))
			move = true
		}
		if move {
			o.cup(x, y)
		}
		o.sgr(c.style)
		o.b.WriteString(c.text)
		o.x += c.width
		if c.varies {
			o.known, force = false, true
		}
		if joinsLeft {
			o.known = false // 桁を進めない端末があるので、次の書記素クラスタの前で位置を指定し直す
		}
		x += c.width
	}
}

// clearJoining は、左のセルと結合しうる書記素クラスタ c（行 y の x）のセルを、書く前に空白で消す。
// 空白も、プリペンドで終わる書記素クラスタには結合する（GB9b）。そのため、左に向かって、空白と結合しうる書記素クラスタを消す範囲に含め、
// 消した後で、それらを格子の内容で書き直す。こうして、左のセルにまとめる端末でも、c のセルに前の内容が残らない。
func (o *output) clearJoining(x, y int, c cell) {
	s := o.s
	start := x
	for start > 0 {
		h := s.headIndex(start-1, y)
		if !joins(s.back[y*s.cols+h].text, " ") {
			break
		}
		start = h
	}
	o.cup(start, y)
	o.sgr(c.style)
	o.b.WriteString(strings.Repeat(" ", x+c.width-start))
	for i := start; i < x; {
		l := s.back[y*s.cols+i]
		o.cup(i, y)
		o.sgr(l.style)
		o.b.WriteString(l.text)
		i += max(l.width, 1)
	}
}

// headIndex は、行 y の x のセルを含む書記素クラスタの先頭のセルの桁を返す。
func (s *Screen) headIndex(x, y int) int {
	row := s.back[y*s.cols : (y+1)*s.cols]
	for x > 0 && row[x].width == 0 {
		x--
	}
	return x
}

// headText は、行 y の x のセルを含む書記素クラスタ（格子の内容）の文字を返す。
func (s *Screen) headText(x, y int) string { return s.back[y*s.cols+s.headIndex(x, y)].text }

// joins は、a の後に b を続けて書くと、端末が 1 つの書記素クラスタにまとめうるかを返す
// （ハングルの字母、プリペンド、ヴィラーマ、ZWJ など。欄の境目で起きうる）。
func joins(a, b string) bool {
	if a == "" || a[len(a)-1] < 0x80 && b[0] < 0x80 {
		return false // ASCII どうしは結合しない（CR LF は格子に置かない）
	}
	c, _ := textwidth.Next(a + b)
	return len(c.Text) != len(a)
}

package screen

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/screen/internal/emu"
	"github.com/zredjet/tana/internal/textwidth"
)

// emuDefaultWidth は、エミュレータの既定の幅（textwidth の表示幅）。
func emuDefaultWidth(c string) int { return textwidth.Width(c) }

var full = Region{X: 0, Y: 0, W: 1000, H: 1000}

// emuStyle は、Style を、エミュレータが SGR から読んだ属性の形にする。
func emuStyle(st Style, noColor bool) emu.Style {
	e := emu.Style{Bold: st.Attr&AttrBold != 0, Dim: st.Attr&AttrDim != 0, Underline: st.Attr&AttrUnderline != 0, Reverse: st.Attr&AttrReverse != 0}
	if !noColor {
		e.FG, e.BG = sgrColor(st.FG, 30), sgrColor(st.BG, 40)
	}
	return e
}

// sgrColor は、色の SGR の番号を返す（base は前景 30、背景 40。既定の色は 0）。
func sgrColor(c Color, base int) int {
	switch {
	case c == ColorDefault:
		return 0
	case c <= ColorWhite:
		return base + int(c-ColorBlack)
	}
	return base + 60 + int(c-ColorBrightBlack)
}

// diff は、画面の格子とエミュレータの画面の違いを返す（T6）。cols の範囲だけを比べる（nil ならすべて）。
func diff(s *Screen, e *emu.Terminal, cols func(x int) bool) []string {
	var out []string
	w, h := s.Size()
	for y := range h {
		for x := range w {
			if cols != nil && !cols(x) {
				continue
			}
			c := s.Cell(x, y)
			want := emu.Cell{Text: c.Text, Width: c.Width, Style: emuStyle(c.Style, s.NoColor)}
			if got := e.Cells[y*e.Cols+x]; got != want {
				out = append(out, fmt.Sprintf("(%d,%d): terminal %+v, grid %+v", x, y, got, want))
			}
		}
	}
	if cols == nil {
		// カーソルの位置は、画面の中に収めたもの（SetCursor）。
		cx, cy := min(max(s.cursorX, 0), w-1), min(max(s.cursorY, 0), h-1)
		visible := s.cursorVisible && w > 0 && h > 0
		if e.CursorVisible != visible || visible && (e.X != cx || e.Y != cy) {
			out = append(out, fmt.Sprintf("cursor: terminal (%d,%d) visible %v, screen (%d,%d) visible %v", e.X, e.Y, e.CursorVisible, cx, cy, visible))
		}
	}
	return out
}

// flushTo は、s を e に出力し、エミュレータが記録した誤り（T2）があれば失敗にする。
func flushTo(t *testing.T, s *Screen, e *emu.Terminal) string {
	t.Helper()
	var b bytes.Buffer
	if err := s.Flush(&b); err != nil {
		t.Fatal(err)
	}
	e.Write(b.Bytes())
	if len(e.Errors) > 0 {
		t.Fatalf("terminal errors (T2): %q in %q", e.Errors, b.String())
	}
	return b.String()
}

func TestPutClipsToRegion(t *testing.T) {
	t.Parallel()
	s := New(8, 2)
	if n := s.Put(Region{X: 2, Y: 1, W: 3, H: 1}, 0, 0, "abcdef", Style{}); n != 3 {
		t.Errorf("Put returned %d, want 3", n)
	}
	if s.Row(0) != "        " || s.Row(1) != "  abc   " {
		t.Errorf("rows %q %q", s.Row(0), s.Row(1))
	}
	// 領域の外の行には描かない。
	if n := s.Put(Region{X: 2, Y: 1, W: 3, H: 1}, 0, 1, "zz", Style{}); n != 0 || s.Row(0) != "        " {
		t.Errorf("Put below the region: %d, %q", n, s.Row(0))
	}
}

func TestPutWideClusters(t *testing.T) {
	t.Parallel()
	s := New(6, 1)
	// 幅 2 の文字が右端にかかるときは、置かずに空白で埋める（T5）。
	if n := s.Put(Region{X: 0, Y: 0, W: 3, H: 1}, 0, 0, "abあ", Style{}); n != 3 || s.Row(0) != "ab    " {
		t.Errorf("wide at the right edge: %d %q", n, s.Row(0))
	}
	// 左端にかかる（x が負）ときも、領域の中の部分を空白にする。
	s = New(6, 1)
	s.Put(Region{X: 1, Y: 0, W: 5, H: 1}, -1, 0, "あい", Style{})
	if s.Row(0) != "  い  " {
		t.Errorf("wide at the left edge: %q", s.Row(0))
	}
	if c := s.Cell(1, 0); c.Text != " " || c.Width != 1 {
		t.Errorf("cell at the left edge = %+v", c)
	}
	// 幅 3 の書記素クラスタは、先頭と 2 つの続きのセル。
	s = New(6, 1)
	s.Put(full, 0, 0, "क\u094dषिx", Style{})
	if c := s.Cell(0, 0); c.Width != 3 || c.Text != "क\u094dषि" || s.Cell(1, 0).Width != 0 || s.Cell(2, 0).Width != 0 || s.Cell(3, 0).Text != "x" {
		t.Errorf("width-3 cluster: %+v %+v %+v %+v", c, s.Cell(1, 0), s.Cell(2, 0), s.Cell(3, 0))
	}
}

// TestPutDisplayForms は、表示する形で置くこと（T2・T6。tui §4）を確かめる。
func TestPutDisplayForms(t *testing.T) {
	t.Parallel()
	s := New(10, 1)
	s.Put(full, 0, 0, "a\x1bb\U0001f1ef\U0001f1f5👨\u200d👩\u200d👧", Style{})
	if s.Row(0) != "a?b??👨   " {
		t.Errorf("row %q", s.Row(0))
	}
	if c := s.Cell(3, 0); c.Text != "?" || c.Width != 1 {
		t.Errorf("flag is not split into ? cells: %+v", c)
	}
}

// TestStraddlingCluster は、領域の端にまたがる幅 2 の文字の、領域の外の部分を空白にすることを確かめる（T5 の例外）。
func TestStraddlingCluster(t *testing.T) {
	t.Parallel()
	red := Style{FG: ColorRed}
	s := New(12, 1)
	s.Put(full, 9, 0, "あ", red)
	s.Put(Region{X: 10, Y: 0, W: 2, H: 1}, 0, 0, "x", Style{})
	if c := s.Cell(9, 0); c.Text != " " || c.Width != 1 || c.Style != red {
		t.Errorf("outside half = %+v, want a blank in the old style", c)
	}
	if c := s.Cell(10, 0); c.Text != "x" {
		t.Errorf("inside = %+v", c)
	}
	// 塗る場合も同じ。右端にまたがるもの。
	s = New(12, 1)
	s.Put(full, 4, 0, "い", red)
	s.Fill(Region{X: 0, Y: 0, W: 5, H: 1}, Style{BG: ColorBlue})
	if c := s.Cell(5, 0); c.Text != " " || c.Width != 1 || c.Style != red {
		t.Errorf("outside half after Fill = %+v", c)
	}
	if c := s.Cell(4, 0); c.Style.BG != ColorBlue {
		t.Errorf("inside after Fill = %+v", c)
	}
}

func TestFillAndAnchors(t *testing.T) {
	t.Parallel()
	s := New(6, 2)
	s.Fill(Region{X: 1, Y: 0, W: 3, H: 2}, Style{BG: ColorBlue})
	for y := range 2 {
		for x := range 6 {
			in := x >= 1 && x < 4
			if got := s.Cell(x, y).Style.BG == ColorBlue; got != in {
				t.Errorf("(%d,%d) filled = %v", x, y, got)
			}
			if got := s.cell(x, y).anchor; got != (x == 1) {
				t.Errorf("(%d,%d) anchor = %v", x, y, got)
			}
		}
	}
	s.Put(full, 2, 0, "ab", Style{})
	if !s.cell(2, 0).anchor || s.cell(3, 0).anchor {
		t.Error("Put: anchor only at the start")
	}
	// 画面の左右の外にある領域、幅が 0 の領域には塗らない（T5）。
	before := snapshot(s)
	for _, r := range []Region{{X: -3, Y: 0, W: 3, H: 2}, {X: 6, Y: 0, W: 2, H: 2}, {X: 2, Y: 0, W: 0, H: 2}} {
		s.Fill(r, Style{BG: ColorRed})
		if after := snapshot(s); fmt.Sprint(after) != fmt.Sprint(before) {
			t.Errorf("Fill(%+v) changed the screen", r)
		}
	}
}

// TestOutside は、画面の外のセルと行を読んだときに、空のものを返すことを確かめる。
func TestOutside(t *testing.T) {
	t.Parallel()
	s := New(2, 1)
	for _, p := range [][2]int{{-1, 0}, {2, 0}, {0, -1}, {0, 1}} {
		if c := s.Cell(p[0], p[1]); c != (Cell{}) {
			t.Errorf("Cell(%d, %d) = %+v", p[0], p[1], c)
		}
	}
	if s.Row(-1) != "" || s.Row(1) != "" {
		t.Errorf("Row outside: %q %q", s.Row(-1), s.Row(1))
	}
}

func TestResize(t *testing.T) {
	t.Parallel()
	s := New(4, 1)
	s.Put(full, 0, 0, "abcd", Style{})
	s.Resize(3, 2)
	if w, h := s.Size(); w != 3 || h != 2 || s.Row(0) != "   " || s.Row(1) != "   " {
		t.Errorf("after Resize: %dx%d %q %q", w, h, s.Row(0), s.Row(1))
	}
	s.Resize(-1, -1)
	if w, h := s.Size(); w != 0 || h != 0 {
		t.Errorf("negative size: %dx%d", w, h)
	}
}

// TestFlushDiff は、2 回目以降の出力が差分だけであることを確かめる。
func TestFlushDiff(t *testing.T) {
	t.Parallel()
	s := New(5, 2)
	e := emu.New(5, 2)
	s.Put(full, 0, 0, "hello", Style{})
	flushTo(t, s, e)
	if d := diff(s, e, nil); len(d) > 0 {
		t.Fatalf("after the first Flush: %q", d)
	}
	if out := flushTo(t, s, e); out != "" {
		t.Errorf("Flush without changes wrote %q", out)
	}
	s.Put(full, 1, 1, "x", Style{Attr: AttrBold})
	if out := flushTo(t, s, e); out != "\x1b[2;2H\x1b[0;1mx" {
		t.Errorf("one change: %q", out)
	}
	if d := diff(s, e, nil); len(d) > 0 {
		t.Errorf("after the change: %q", d)
	}
}

// TestFlushPositioning は、カーソルの位置を指定し直す場所（tui §6）を確かめる。
func TestFlushPositioning(t *testing.T) {
	t.Parallel()
	s := New(8, 1)
	e := emu.New(8, 1)
	s.Put(full, 0, 0, "ab", Style{})
	s.Put(full, 2, 0, "cd", Style{}) // 欄の始め
	s.Put(full, 4, 0, "○e", Style{}) // 幅が端末によって違いうる文字の後
	s.SetCursor(0, 0, false)
	out := flushTo(t, s, e)
	want := "\x1b[0m\x1b[?25l" + "\x1b[1;1Hab" + "\x1b[1;3Hcd" + "\x1b[1;5H○" + "\x1b[1;6He  "
	if out != want {
		t.Errorf("output\n got  %q\n want %q", out, want)
	}
	// 幅 2 以上の、違いうる文字は、先に空白で消してから書く（端末が狭く描いても前の内容が残らない）。
	// 違いうる文字の後の 1 つは、変わっていなくても書き直す（端末が広く描いてはみ出した分を直す）。
	s.Put(full, 0, 0, "\U0001faf9", Style{})
	out = flushTo(t, s, e)
	if want := "\x1b[1;1H  \x1b[1;1H\U0001faf9\x1b[1;3Hc"; out != want {
		t.Errorf("new emoji\n got  %q\n want %q", out, want)
	}
}

// TestFlushSeparatesJoiningClusters は、隣の欄の文字と結合しうるとき、位置を指定し直して分けることを確かめる。
func TestFlushSeparatesJoiningClusters(t *testing.T) {
	t.Parallel()
	s := New(6, 1)
	e := emu.New(6, 1)
	s.Put(full, 0, 0, "\u1100", Style{}) // ハングルの初声字母 L
	s.Put(Region{X: 2, Y: 0, W: 4, H: 1}, 0, 0, "가", Style{})
	s.cell(2, 0).anchor = false // 欄の始めでなくても分ける
	out := flushTo(t, s, e)
	if !strings.Contains(out, "\u1100\x1b[1;3H가") {
		t.Errorf("L jamo and LV syllable not separated: %q", out)
	}
	if d := diff(s, e, nil); len(d) > 0 {
		t.Errorf("%q", d)
	}
}

func TestFlushStylesAndNoColor(t *testing.T) {
	t.Parallel()
	for _, noColor := range []bool{false, true} {
		s := New(4, 1)
		s.NoColor = noColor
		e := emu.New(4, 1)
		s.Put(full, 0, 0, "ab", Style{FG: ColorRed, BG: ColorBrightWhite, Attr: AttrReverse | AttrUnderline | AttrDim})
		out := flushTo(t, s, e)
		if strings.Contains(out, "31") != !noColor || strings.Contains(out, "107") != !noColor {
			t.Errorf("NoColor=%v: %q", noColor, out)
		}
		if d := diff(s, e, nil); len(d) > 0 {
			t.Errorf("NoColor=%v: %q", noColor, d)
		}
	}
}

func TestFlushCursor(t *testing.T) {
	t.Parallel()
	s := New(4, 2)
	e := emu.New(4, 2)
	s.SetCursor(3, 1, true)
	out := flushTo(t, s, e)
	if !strings.HasSuffix(out, "\x1b[2;4H\x1b[?25h") {
		t.Errorf("visible cursor: %q", out)
	}
	if out := flushTo(t, s, e); out != "" {
		t.Errorf("unchanged cursor: %q", out)
	}
	s.SetCursor(0, 0, false)
	if out := flushTo(t, s, e); out != "\x1b[?25l" {
		t.Errorf("hide cursor: %q", out)
	}
	// 画面の外のカーソルは、画面の中に収める。
	s.SetCursor(10, -3, true)
	flushTo(t, s, e)
	if e.X != 3 || e.Y != 0 || !e.CursorVisible {
		t.Errorf("clamped cursor: (%d,%d) %v", e.X, e.Y, e.CursorVisible)
	}
}

// TestInvalidate は、Invalidate の後の出力が、全体を描き直すことを確かめる（端末の内容が分からなくなったとき）。
func TestInvalidate(t *testing.T) {
	t.Parallel()
	s := New(3, 1)
	e := emu.New(3, 1)
	s.Put(full, 0, 0, "abc", Style{})
	flushTo(t, s, e)
	e2 := emu.New(3, 1) // 別の端末（内容が消えた）
	s.Invalidate()
	flushTo(t, s, e2)
	if d := diff(s, e2, nil); len(d) > 0 {
		t.Errorf("after Invalidate: %q", d)
	}
}

// failingWriter は、書き込みに失敗する端末。出力の前半（半分より前の、最後のシーケンスの手前まで）だけが届いた場合を模擬する。
// エミュレータは Write ごとに文字を区切るので、UTF-8 やシーケンスの途中では切らない。
type failingWriter struct{ e *emu.Terminal }

func (f failingWriter) Write(p []byte) (int, error) {
	n := max(bytes.LastIndexByte(p[:len(p)/2], 0x1b), 0)
	f.e.Write(p[:n])
	return n, errors.New("write failed")
}

// TestFlushWriteError は、書き込みに失敗したら、次の Flush で全体を書き直すことを確かめる（T6。端末の内容が分からない）。
func TestFlushWriteError(t *testing.T) {
	t.Parallel()
	s := New(4, 2)
	e := emu.New(4, 2)
	s.Put(full, 0, 0, "ab", Style{FG: ColorRed})
	flushTo(t, s, e)
	s.Put(full, 0, 1, "\u3042x", Style{Attr: AttrBold})
	s.SetCursor(1, 1, true)
	if err := s.Flush(failingWriter{e}); err == nil {
		t.Fatal("Flush did not return the write error")
	}
	// 何も変えずに Flush しても、全体を書き直す。
	flushTo(t, s, e)
	if d := diff(s, e, nil); len(d) > 0 {
		t.Errorf("after a failed write: %q", d)
	}
}

// TestWidthMismatch は、幅の計算が合わない端末で、ずれがその行のその領域の中で止まることを確かめる（T6）。
// 左のペインに幅が曖昧な文字と新しい絵文字を置き、端末はそれを 2 桁と 1 桁で描く。
func TestWidthMismatch(t *testing.T) {
	t.Parallel()
	s := New(20, 3)
	e := emu.New(20, 3)
	e.Width = mismatchedWidth
	left, right := Region{X: 0, Y: 0, W: 10, H: 3}, Region{X: 10, Y: 0, W: 10, H: 3}
	s.Put(left, 0, 0, "○○○○○○○○○○", Style{})
	s.Put(left, 0, 1, "\U0001faf9\U0001faf9ab", Style{})
	s.Put(left, 0, 2, "plain", Style{})
	for y := range 3 {
		s.Put(right, 0, y, "right pane", Style{})
	}
	flushTo(t, s, e)
	if d := diff(s, e, func(x int) bool { return x >= 10 }); len(d) > 0 {
		t.Errorf("right pane: %q", d)
	}
	// 右のペインを変えずに左のペインだけを描き直しても、右のペインは崩れない。
	s.Put(left, 0, 0, "○○○○○○○○○○", Style{Attr: AttrBold})
	flushTo(t, s, e)
	if d := diff(s, e, func(x int) bool { return x >= 10 }); len(d) > 0 {
		t.Errorf("right pane after redrawing the left: %q", d)
	}
	if e.Row(2) != "plain     right pane" {
		t.Errorf("row without mismatches: %q", e.Row(2))
	}
}

// mismatchedWidth は、幅の計算が合わない端末の模擬: 幅が曖昧な文字を 2 桁、Unicode 16.0 より新しい絵文字を 1 桁にする。
func mismatchedWidth(c string) int {
	switch c {
	case "○":
		return 2
	case "\U0001faf9":
		return 1
	}
	return emuDefaultWidth(c)
}

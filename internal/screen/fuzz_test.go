package screen

import (
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/screen/internal/emu"
	"github.com/zredjet/tana/internal/textwidth"
)

// pool は、ファジングで描く文字列の部品（幅 2・幅 3、幅が曖昧な文字、NFD、絵文字の並び、国旗、制御文字、
// 双方向の制御文字、結合しうるもの、不正なバイトなど）。
var pool = []string{
	"a", "bc", " ", "あ", "漢字", "○", "é", "か\u3099", "ｶﾞ", "👨\u200d👩\u200d👧", "\U0001f1ef\U0001f1f5", "\x1b[2J", "\x00", "\u202e",
	"क\u094dषि", "\u1100", "가", "😀\u200d", "\u0600", "\ufe0f", "\xff", "\U0001faf9", "ि", "\t", "\u200b", "\r\n",
}

// opReader は、ファジングの入力のバイト列を、描画の操作の引数として順に読む。
type opReader struct {
	b []byte
	i int
}

func (r *opReader) next() byte {
	if r.i >= len(r.b) {
		return 0
	}
	r.i++
	return r.b[r.i-1]
}

func (r *opReader) done() bool { return r.i >= len(r.b) }

// in は、-1 から n までの値を返す（範囲の外も含める）。
func (r *opReader) in(n int) int { return int(r.next())%(n+2) - 1 }

func (r *opReader) text(from []string) string {
	var b strings.Builder
	for range r.next() % 5 {
		b.WriteString(from[int(r.next())%len(from)])
	}
	return b.String()
}

func (r *opReader) style() Style {
	return Style{FG: Color(r.next() % 17), BG: Color(r.next() % 17), Attr: Attr(r.next() % 16)}
}

func (r *opReader) region(cols, rows int) Region {
	return Region{X: r.in(cols), Y: r.in(rows), W: r.in(cols + 1), H: r.in(rows + 1)}
}

func snapshot(s *Screen) []Cell {
	w, h := s.Size()
	out := make([]Cell, 0, w*h)
	for y := range h {
		for x := range w {
			out = append(out, s.Cell(x, y))
		}
	}
	return out
}

// checkT5 は、領域 reg に描いた操作が、領域の外のセルを変えていないことを確かめる。
// 例外は、前に描いた幅 2 以上の書記素クラスタが領域の端にまたがっていた場合の、領域の外に残る部分（空白になる。T5）。
func checkT5(t *testing.T, before []Cell, s *Screen, reg Region, op string) {
	t.Helper()
	cols, rows := s.Size()
	inRegion := func(x, y int) bool { return x >= reg.X && x < reg.X+reg.W && y >= reg.Y && y < reg.Y+reg.H }
	for y := range rows {
		for x := range cols {
			if inRegion(x, y) {
				continue
			}
			old, now := before[y*cols+x], s.Cell(x, y)
			if old == now {
				continue
			}
			h := x
			for h > 0 && before[y*cols+h].Width == 0 {
				h--
			}
			head := before[y*cols+h]
			straddles := head.Width > 1 && y >= reg.Y && y < reg.Y+reg.H && h < reg.X+reg.W && h+head.Width > reg.X
			if !straddles || now != (Cell{Text: " ", Width: 1, Style: head.Style}) {
				t.Fatalf("T5: %s in %+v changed (%d,%d) outside: %+v -> %+v", op, reg, x, y, old, now)
			}
		}
	}
}

// FuzzScreen は、任意の描画の操作と出力を繰り返し、次の性質を確かめる（tui §6）。
//   - T5: 領域に描いても、領域の外のセルが変わらない（領域の端にまたがる幅 2 以上の文字の、外の部分を除く）。
//   - T6: 出力するたびに、エミュレータの画面（セル、属性、カーソル）が格子と一致する。
//   - T2: 出力に含まれる制御文字は、screen が作るシーケンスのものだけ（エミュレータが知らないシーケンスや文字は誤り）。
func FuzzScreen(f *testing.F) {
	f.Add([]byte{8, 2, 0, 0, 0, 0, 9, 3, 0, 0, 3, 1, 2, 3, 3})
	f.Add([]byte{12, 3, 1, 0, 1, 1, 10, 4, 0, 0, 4, 3, 5, 9, 11, 13, 1, 2, 7, 3, 4, 3, 6, 5, 3})
	f.Add([]byte{5, 1, 0, 0, 0, 0, 7, 3, 2, 0, 2, 14, 15, 1, 0, 0, 0, 4, 3, 0, 5, 20, 21, 3})
	f.Fuzz(func(t *testing.T, ops []byte) {
		r := &opReader{b: ops}
		cols, rows := 1+int(r.next()%16), 1+int(r.next()%4)
		s := New(cols, rows)
		s.NoColor = r.next()%2 == 1
		e := emu.New(cols, rows)
		flush := func(op string) {
			t.Helper()
			flushTo(t, s, e)
			if d := diff(s, e, nil); len(d) > 0 {
				t.Fatalf("T6 after %s: %q", op, d)
			}
		}
		for !r.done() {
			switch r.next() % 7 {
			case 0, 6:
				reg := r.region(cols, rows)
				x, y := r.in(cols), r.in(rows)
				text, st := r.text(pool), r.style()
				before := snapshot(s)
				s.Put(reg, x, y, text, st)
				checkT5(t, before, s, reg, "Put")
			case 1:
				reg := r.region(cols, rows)
				before := snapshot(s)
				s.Fill(reg, r.style())
				checkT5(t, before, s, reg, "Fill")
			case 2:
				s.SetCursor(r.in(cols), r.in(rows), r.next()%2 == 0)
			case 3:
				flush("Flush")
			case 4:
				// 端末の内容が分からなくなった（別の端末に出力する）。
				s.Invalidate()
				e = emu.New(cols, rows)
				flush("Invalidate")
			case 5:
				cols, rows = 1+int(r.next()%16), 1+int(r.next()%4)
				s.Resize(cols, rows)
				e = emu.New(cols, rows)
			}
		}
		flush("the last Flush")
	})
}

// mismatchAll は、幅の計算が合わない端末の模擬: 幅が端末によって違いうる書記素クラスタのうち、
// 幅 1 のもの（幅が曖昧な文字）を 2 桁に、幅 2 以上のもの（新しい絵文字、幅 3 以上）を 1 桁狭く描く。
func mismatchAll(c string) int {
	x, _ := textwidth.Next(c)
	switch {
	case !x.Varies:
		return x.Width
	case x.Width == 1:
		return 2
	}
	return x.Width - 1
}

// FuzzWidthMismatch は、幅の計算が合わない端末でも、ずれがその行のその領域（ペイン）の中で止まることを確かめる（T6）。
// 左右 2 つのペインに描き直しを繰り返し、右のペイン（違いうる文字を置かない）と、違いうる文字のない行が、いつも格子と一致する。
func FuzzWidthMismatch(f *testing.F) {
	f.Add([]byte{0, 4, 5, 5, 5, 5, 1, 2, 0, 1, 2, 3, 0, 0, 3, 21, 21, 3, 0})
	f.Add([]byte{2, 4, 14, 14, 5, 0, 1, 1, 4, 0, 1, 2, 3, 0})
	f.Fuzz(func(t *testing.T, ops []byte) {
		const cols, rows = 20, 3
		left, right := Region{X: 0, Y: 0, W: 10, H: rows}, Region{X: 10, Y: 0, W: 10, H: rows}
		var plain []string // 右のペインに置く、違いうる文字のない部品
		for _, p := range pool {
			varies := false
			for c := range textwidth.All(p) {
				varies = varies || c.Varies
			}
			if !varies {
				plain = append(plain, p)
			}
		}
		r := &opReader{b: ops}
		s := New(cols, rows)
		e := emu.New(cols, rows)
		e.Width = mismatchAll
		leftVaries := make([]bool, rows)
		for !r.done() {
			y := int(r.next()) % rows
			if r.next()%2 == 0 {
				text := r.text(pool)
				s.Fill(Region{X: 0, Y: y, W: 10, H: 1}, Style{})
				s.Put(left, 0, y, text, r.style())
				leftVaries[y] = false
				for c := range textwidth.All(text) {
					leftVaries[y] = leftVaries[y] || c.Varies
				}
			} else {
				// 部品が違いうるものでなくても、つなぐと違いうるもの（ハングルの字母 L＋L など）になることがあるので、つないだ後で調べる。
				text := r.text(plain)
				varies := false
				for c := range textwidth.All(text) {
					varies = varies || c.Varies
				}
				if varies {
					continue
				}
				s.Fill(Region{X: 10, Y: y, W: 10, H: 1}, Style{})
				s.Put(right, 0, y, text, r.style())
			}
			if r.next()%3 == 0 {
				continue // 出力せずに次の描画へ
			}
			flushTo(t, s, e)
			if d := diff(s, e, func(x int) bool { return x >= 10 }); len(d) > 0 {
				t.Fatalf("right pane: %q", d)
			}
			for row := range rows {
				if leftVaries[row] {
					continue
				}
				for x := range cols {
					c := s.Cell(x, row)
					if got, want := e.Cells[row*cols+x], (emu.Cell{Text: c.Text, Width: c.Width, Style: emuStyle(c.Style, false)}); got != want {
						t.Fatalf("row %d without varying clusters differs at %d: terminal %+v, grid %+v", row, x, got, want)
					}
				}
			}
		}
	})
}

// joinPool は、左のセルと結合しうる書記素クラスタを多く含む部品（ハングルの字母、ZWJ で終わる絵文字、ヴィラーマ、母音記号、プリペンド）。
var joinPool = []string{
	"\u1100", "가", "😀\u200d", "😀", "\u0600", "a", "क\u094d", "ष", "ि", " ", "あ", "\u1100\u1100", "b",
}

// joinPairs は、格子の中で、左のセルと結合しうる書記素クラスタ（とその左のセル）が占めるセルを taint に加える。
func joinPairs(s *Screen, taint map[[2]int]bool) {
	cols, rows := s.Size()
	for y := range rows {
		for x := 1; x < cols; x++ {
			c := s.Cell(x, y)
			if c.Width == 0 {
				continue
			}
			h := x - 1
			for h > 0 && s.Cell(h, y).Width == 0 {
				h--
			}
			if !joins(s.Cell(h, y).Text, c.Text) {
				continue
			}
			for i := h; i < x+c.Width; i++ {
				taint[[2]int{i, y}] = true
			}
		}
	}
}

// FuzzJoinLeft は、左のセルと結合する端末（Windows Terminal・iTerm2。tui §9 の VT7）でも、ずれが、左のセルと結合しうる書記素クラスタと
// その左のセルの中で止まり、ほかのセルに広がらないことを確かめる（T6）。
// 結合しうる組は、全体を描き直してから出力のたびに数える（前のフレームで組だったセルには、端末が前の内容を残しうる）。
func FuzzJoinLeft(f *testing.F) {
	f.Add([]byte{0, 1, 1, 13, 2, 0, 0, 0, 0, 0, 0, 0, 1, 3, 13, 2, 0, 0, 1, 0, 0, 0, 3, 1})
	f.Add([]byte{0, 1, 1, 13, 2, 0, 0, 2, 2, 0, 0, 0, 5, 1, 13, 2, 0, 0, 1, 3, 0, 0, 0, 3, 1, 0, 1, 1, 13, 2, 0, 0, 1, 12, 0, 0, 0, 3, 1})
	f.Fuzz(func(t *testing.T, ops []byte) {
		const cols, rows = 12, 2
		r := &opReader{b: ops}
		s := New(cols, rows)
		newTerm := func() *emu.Terminal {
			e := emu.New(cols, rows)
			e.JoinLeft = true
			return e
		}
		e := newTerm()
		taint := map[[2]int]bool{}
		for !r.done() {
			switch r.next() % 4 {
			case 0, 1:
				reg := r.region(cols, rows)
				s.Put(reg, r.in(cols), r.in(rows), r.text(joinPool), r.style())
			case 2:
				s.Fill(r.region(cols, rows), r.style())
			case 3:
				if r.next()%4 == 0 {
					// 全体を描き直す（別の端末に出す）。前のフレームの名残はなくなる。
					s.Invalidate()
					e = newTerm()
					taint = map[[2]int]bool{}
				}
				flushTo(t, s, e)
				joinPairs(s, taint)
				for y := range rows {
					for x := range cols {
						if taint[[2]int{x, y}] {
							continue
						}
						c := s.Cell(x, y)
						want := emu.Cell{Text: c.Text, Width: c.Width, Style: emuStyle(c.Style, false)}
						if got := e.Cells[y*cols+x]; got != want {
							t.Fatalf("(%d,%d) outside joining pairs: terminal %+v, grid %+v; rows %q %q / %q %q",
								x, y, got, want, s.Row(0), s.Row(1), e.Row(0), e.Row(1))
						}
					}
				}
			}
		}
	})
}

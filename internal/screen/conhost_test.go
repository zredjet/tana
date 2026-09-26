package screen

import (
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/screen/internal/emu"
	"github.com/zredjet/tana/internal/textwidth"
)

// conhostWidth は、conhost（日本語の環境）の幅の数え方を模擬する。結合文字を、基底の文字とは別の桁に描く
// （実測: が（NFD）は 4 桁、e＋結合アクセントは 2 桁。docs/probe-results/conhost-2026-09-26.json）。
func conhostWidth(cluster string) int {
	w := 0
	for c := range textwidth.All(cluster) {
		w = c.Width
	}
	for _, r := range cluster[len(string([]rune(cluster)[0])):] {
		switch {
		case r == 0x3099 || r == 0x309a:
			w += 2
		case 0x0300 <= r && r <= 0x036f:
			w++
		}
	}
	return w
}

// TestConhostCombiningResidue は、conhost で結合文字を広く描いたはみ出しが、その行を描き直した後に残らないことを確かめる（T6）。
// フェーズ18の手動の確認で、Yazi 風の表示のプレビューの列に、NFD の名前の末尾（txt）が残った。
func TestConhostCombiningResidue(t *testing.T) {
	t.Parallel()
	const cols, rows = 30, 2
	s := New(cols, rows)
	e := emu.New(cols, rows)
	e.Width = conhostWidth
	r := Region{X: 0, Y: 0, W: 25, H: rows}
	flushTo(t, s, e)
	s.Put(r, 1, 0, "がぎ（NFD）.txt", Style{})
	s.Put(Region{X: 25, Y: 0, W: 5, H: rows}, 0, 0, "|", Style{})
	flushTo(t, s, e)
	s.Fill(Region{W: cols, H: rows}, Style{})
	s.Put(r, 1, 0, "テキストではない", Style{})
	s.Put(Region{X: 25, Y: 0, W: 5, H: rows}, 0, 0, "|", Style{})
	flushTo(t, s, e)
	if got, want := strings.TrimRight(e.Row(0), " "), strings.TrimRight(s.Row(0), " "); got != want {
		t.Errorf("row after redrawing:\nterminal %q\ngrid     %q", got, want)
	}
}

// conhostMark は、conhost が結合文字を別の桁に描くときの幅（実測: 濁点・半濁点と異体字セレクタは 2 桁、ほかは 1 桁）。
func conhostMark(r rune) int {
	if r == 0x3099 || r == 0x309a || 0xe0100 <= r && r <= 0xe01ef {
		return 2
	}
	return 1
}

// TestConhostCombiningResidueAtEnd は、結合文字を含む書記素クラスタで終わる名前（後ろが空白）のはみ出しが、描き直した後に残らないことを確かめる。
// conhost は結合文字を 1 つずつ別の桁に描くので、アクセントを重ねた文字は 3 桁はみ出し、直後の 1 つを書き直すだけでは残る。
func TestConhostCombiningResidueAtEnd(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"\u3071\u304b\u3099", "e\u0301\u0302\u0303", "\u845b\U000e0100"} {
		const cols, rows = 30, 2
		s := New(cols, rows)
		e := emu.New(cols, rows)
		e.SplitMarks = conhostMark
		r := Region{X: 0, Y: 0, W: 25, H: rows}
		flushTo(t, s, e) // 最初の出力は全体を書くので、差分だけを書く 2 回目以降にする
		s.Put(r, 1, 0, name, Style{})
		s.Put(Region{X: 25, Y: 0, W: 5, H: rows}, 0, 0, "|", Style{})
		flushTo(t, s, e)
		s.Fill(Region{W: cols, H: rows}, Style{})
		s.Put(r, 1, 0, "x", Style{})
		s.Put(Region{X: 25, Y: 0, W: 5, H: rows}, 0, 0, "|", Style{})
		flushTo(t, s, e)
		if got, want := strings.TrimRight(e.Row(0), " "), strings.TrimRight(s.Row(0), " "); got != want {
			t.Errorf("%+q: row after redrawing:\nterminal %q\ngrid     %q", name, got, want)
		}
	}
}

// markPool は、結合文字を含む部品（conhost が別の桁に描くもの）を加えた部品。
var markPool = append(append([]string(nil), pool...),
	"が", "é̂̃", "葛\U000e0100", "กิ", "\U0001f600︎", "ä", "ぱ゙")

// FuzzConhostMarks は、結合文字を別の桁に描く端末（conhost）でも、描き直しを繰り返した後に、
// 違いうる書記素クラスタのない行が格子と一致し、ずれのある行でもほかの領域（右のペイン）に広がらないことを確かめる（T6）。
func FuzzConhostMarks(f *testing.F) {
	f.Add([]byte{0, 0, 29, 1, 0, 0, 0, 1, 0})
	f.Add([]byte{1, 0, 30, 30, 2, 1, 0, 0, 1, 1, 2, 3, 0})
	f.Fuzz(func(t *testing.T, ops []byte) {
		const cols, rows = 20, 3
		left := Region{X: 0, Y: 0, W: 10, H: rows}
		r := &opReader{b: ops}
		s := New(cols, rows)
		e := emu.New(cols, rows)
		e.SplitMarks = conhostMark
		leftVaries := make([]bool, rows)
		for !r.done() {
			y := int(r.next()) % rows
			text := r.text(markPool)
			s.Fill(Region{X: 0, Y: y, W: 10, H: 1}, Style{})
			s.Put(left, 0, y, text, r.style())
			s.Put(Region{X: 10, Y: y, W: 10, H: 1}, 0, 0, "|right", Style{})
			leftVaries[y] = false
			for c := range textwidth.All(text) {
				leftVaries[y] = leftVaries[y] || c.Varies
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
				if got, want := e.Row(row), s.Row(row); got != want {
					t.Fatalf("row %d without varying clusters:\nterminal %q\ngrid     %q", row, got, want)
				}
			}
		}
	})
}

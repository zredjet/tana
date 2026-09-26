package lineedit

import (
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/textwidth"
)

// isBoundary は、pos が s の書記素クラスタの境界かを返す。
func isBoundary(s string, pos int) bool {
	if pos == 0 || pos == len(s) {
		return true
	}
	for i := 0; i < len(s); {
		c, _ := textwidth.Next(s[i:])
		i += len(c.Text)
		if i == pos {
			return true
		}
		if i > pos {
			return false
		}
	}
	return false
}

// filtered は、挿入する文字列から、改行と端末に出してはいけない文字（不正なバイトを含む）を取り除いたもの（tui §7）。
func filtered(s string) string {
	var b strings.Builder
	for c := range textwidth.All(s) {
		if c.Class != textwidth.Forbidden && c.Class != textwidth.InvalidByte {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

// FuzzEditor は、任意の操作の列で、次の性質を確かめる（tui §7 のファジング）。
//   - カーソルは常に書記素クラスタの境界にある。
//   - 変更する操作をしなければ、文字列は元のバイト列のまま。Changed は、文字列を変えた操作があったときだけ true。
//   - 文字列は、元の文字列と挿入した文字列の部分からだけできている
//     （挿入はカーソルの位置に取り除いた後の文字列を入れるだけ、削除はカーソルの前か後ろの一続きを消すだけ）。
//   - 表示の範囲は、書記素クラスタの境界で、欄の幅に収まり、カーソルを含む。
func FuzzEditor(f *testing.F) {
	f.Add("報告書.docx", 3, []byte{0, 1, 2, 3, 4, 5, 6, 7, 5}, "_最終\r\n")
	f.Add("か\u3099き\u3099\xff", 1, []byte{4, 4, 1, 0, 7, 3, 2}, "\u3099e\u0301")
	f.Add("a👨\u200d👩\u200d👧b", 2, []byte{6, 0, 1, 1, 7, 12}, "\U0001f1ef\U0001f1f5\x1b")
	f.Fuzz(func(t *testing.T, initial string, cursor int, ops []byte, ins string) {
		e := New(initial, cursor)
		changed := false
		check := func(op string) {
			t.Helper()
			if !isBoundary(e.Text(), e.Cursor()) {
				t.Fatalf("%s: cursor %d is not a cluster boundary of %q", op, e.Cursor(), e.Text())
			}
			if e.Changed() != changed {
				t.Fatalf("%s: Changed() = %v, want %v", op, e.Changed(), changed)
			}
			if !changed && e.Text() != initial {
				t.Fatalf("%s: text %q changed without a modifying operation (was %q)", op, e.Text(), initial)
			}
		}
		check("New")
		for i := 0; i < len(ops); i++ {
			old, c := e.Text(), e.Cursor()
			switch ops[i] % 8 {
			case 0:
				// 挿入する文字列は ins の一部（次の 2 バイトで範囲を決める）。
				a, b := 0, len(ins)
				if i+2 < len(ops) {
					a, b = int(ops[i+1])%(len(ins)+1), int(ops[i+2])%(len(ins)+1)
					i += 2
				}
				s := ins[min(a, b):max(a, b)]
				e.Insert(s)
				f := filtered(s)
				if e.Text() != old[:c]+f+old[c:] {
					t.Fatalf("Insert(%q) into %q at %d: %q, want %q", s, old, c, e.Text(), old[:c]+f+old[c:])
				}
				changed = changed || f != ""
				check("Insert")
			case 1:
				e.DeleteBackward()
				n := e.Text()
				if !strings.HasSuffix(n, old[c:]) || !strings.HasPrefix(old, n[:len(n)-len(old[c:])]) {
					t.Fatalf("DeleteBackward on %q at %d: %q is not a removal before the cursor", old, c, n)
				}
				changed = changed || n != old
				check("DeleteBackward")
			case 2:
				e.DeleteForward()
				n := e.Text()
				if !strings.HasPrefix(n, old[:c]) || !strings.HasSuffix(old, n[c:]) {
					t.Fatalf("DeleteForward on %q at %d: %q is not a removal after the cursor", old, c, n)
				}
				changed = changed || n != old
				check("DeleteForward")
			case 3:
				e.Left()
				check("Left")
			case 4:
				e.Right()
				check("Right")
			case 5:
				e.Home()
				check("Home")
			case 6:
				e.End()
				check("End")
			case 7:
				w := 0
				if i+1 < len(ops) {
					w = int(ops[i+1]) % 12
					i++
				}
				v := e.View(w)
				text := e.Text()
				if v.Start > e.Cursor() || e.Cursor() > v.End || v.End > len(text) || !isBoundary(text, v.Start) || !isBoundary(text, v.End) {
					t.Fatalf("View(%d) of %q (cursor %d) = %+v", w, text, e.Cursor(), v)
				}
				if w >= 1 && (textwidth.Width(text[v.Start:v.End]) > w || v.CursorCol != textwidth.Width(text[v.Start:e.Cursor()]) || v.CursorCol > w-1) {
					t.Fatalf("View(%d) of %q (cursor %d) = %+v does not fit", w, text, e.Cursor(), v)
				}
				if e.Text() != old || e.Cursor() != c {
					t.Fatalf("View changed the editor")
				}
				check("View")
			}
		}
	})
}

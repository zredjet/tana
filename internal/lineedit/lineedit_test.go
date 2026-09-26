package lineedit

import (
	"strings"
	"testing"
)

func TestNewSnapsCursor(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		text   string
		cursor int
		want   int
	}{
		{"e\u0301x", 1, 3}, // 結合文字の途中なら、書記素クラスタの終わりへ
		{"abc", -5, 0},
		{"abc", 99, 3},
		{"", 0, 0},
	} {
		if got := New(tt.text, tt.cursor).Cursor(); got != tt.want {
			t.Errorf("New(%q, %d).Cursor() = %d, want %d", tt.text, tt.cursor, got, tt.want)
		}
	}
}

// TestMoveByClusters は、カーソルを書記素クラスタの単位で動かすことを確かめる（NFD の名前、絵文字、不正なバイト）。
func TestMoveByClusters(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		text  string
		stops []int // 先頭から Right を押したときのカーソルの位置
	}{
		{"nfd", "か\u3099き\u3099.txt", []int{0, 6, 12, 13, 14, 15, 16}},
		{"emoji family", "a👨\u200d👩\u200d👧b", []int{0, 1, 19, 20}},
		{"invalid bytes", "a\xff\xfeb", []int{0, 1, 2, 3, 4}},
		{"crlf in a name", "a\r\nb", []int{0, 1, 3, 4}},
	} {
		e := New(tt.text, 0)
		for i, want := range tt.stops {
			if e.Cursor() != want {
				t.Errorf("%s: after %d Right: cursor %d, want %d", tt.name, i, e.Cursor(), want)
			}
			e.Right()
		}
		if e.Cursor() != len(tt.text) {
			t.Errorf("%s: Right at the end moved to %d", tt.name, e.Cursor())
		}
		for i := len(tt.stops) - 2; i >= 0; i-- {
			e.Left()
			if e.Cursor() != tt.stops[i] {
				t.Errorf("%s: Left: cursor %d, want %d", tt.name, e.Cursor(), tt.stops[i])
			}
		}
		e.Left()
		if e.Cursor() != 0 {
			t.Errorf("%s: Left at the start moved to %d", tt.name, e.Cursor())
		}
		e.End()
		if e.Cursor() != len(tt.text) {
			t.Errorf("%s: End: %d", tt.name, e.Cursor())
		}
		e.Home()
		if e.Cursor() != 0 {
			t.Errorf("%s: Home: %d", tt.name, e.Cursor())
		}
		if e.Changed() || e.Text() != tt.text {
			t.Errorf("%s: moving changed the text: %q changed=%v", tt.name, e.Text(), e.Changed())
		}
	}
}

func TestInsert(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name         string
		text         string
		cursor       int
		insert       string
		want         string
		wantCursor   int
		wantModified bool
	}{
		{"plain", "報告書.docx", 9, "_最終", "報告書_最終.docx", 16, true},
		// 改行（CR・LF）と、端末に出してはいけない文字（制御文字、双方向の制御文字、不正なバイト）を取り除く（tui §7）。
		{"filtered", "ab", 1, "x\r\ny\tz\x1b[1m\u202e\xff", "axyz[1mb", 7, true},
		{"only filtered", "ab", 1, "\r\n\x00", "ab", 1, false},
		{"empty", "ab", 1, "", "ab", 1, false},
		// 見えない文字や絵文字の並びは、表示では置き換えるが、名前には入れる。
		{"invisible kept", "ab", 1, "\u200b👨\u200d👩", "a\u200b👨\u200d👩b", 15, true},
		// 前の文字と 1 つの書記素クラスタになる文字（結合文字）を入れた後も、カーソルは境界にある。
		{"combining after", "ex", 1, "\u0301", "e\u0301x", 3, true},
		{"base before a leading combining mark", "\u0301x", 0, "e", "e\u0301x", 3, true},
	} {
		e := New(tt.text, tt.cursor)
		e.Insert(tt.insert)
		if e.Text() != tt.want || e.Cursor() != tt.wantCursor || e.Changed() != tt.wantModified {
			t.Errorf("%s: Insert(%q) into %q at %d: %q cursor %d changed %v, want %q %d %v",
				tt.name, tt.insert, tt.text, tt.cursor, e.Text(), e.Cursor(), e.Changed(), tt.want, tt.wantCursor, tt.wantModified)
		}
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	e := New("か\u3099き\u3099", 6)
	e.DeleteBackward() // NFD の「が」を 1 つとして消す
	if e.Text() != "き\u3099" || e.Cursor() != 0 || !e.Changed() {
		t.Errorf("DeleteBackward: %q %d %v", e.Text(), e.Cursor(), e.Changed())
	}
	e = New("a👨\u200d👩\u200d👧b", 1)
	e.DeleteForward()
	if e.Text() != "ab" || e.Cursor() != 1 {
		t.Errorf("DeleteForward: %q %d", e.Text(), e.Cursor())
	}
	// 何も消えない操作は、変更にしない。
	e = New("ab", 0)
	e.DeleteBackward()
	e.End()
	e.DeleteForward()
	if e.Changed() || e.Text() != "ab" {
		t.Errorf("no-op deletes: %q changed=%v", e.Text(), e.Changed())
	}
	// 消した結果、前後が 1 つの書記素クラスタになる場合も、カーソルは境界にある
	// （デーヴァナーガリーの KA＋VIRAMA と SSA の間の a を消すと、GB9c で KA＋VIRAMA＋SSA が 1 つになる）。
	e = New("\u0915\u094da\u0937", len("\u0915\u094da"))
	e.DeleteBackward()
	if e.Text() != "\u0915\u094d\u0937" || e.Cursor() != len("\u0915\u094d\u0937") {
		t.Errorf("delete that joins neighbours: %q cursor %d", e.Text(), e.Cursor())
	}
}

// TestChangedByOperation は、変更したかどうかを、文字列の比較ではなく、変更する操作があったかどうかで返すことを確かめる（filer §8.7）。
func TestChangedByOperation(t *testing.T) {
	t.Parallel()
	e := New("name", 4)
	e.Insert("x")
	e.DeleteBackward()
	if e.Text() != "name" || !e.Changed() {
		t.Errorf("type and delete back: %q changed=%v, want the same text and changed=true", e.Text(), e.Changed())
	}
}

func TestView(t *testing.T) {
	t.Parallel()
	check := func(name string, e *Editor, width, start, end, col int) {
		t.Helper()
		v := e.View(width)
		if v.Start != start || v.End != end || v.CursorCol != col {
			t.Errorf("%s: View(%d) = %+v, want start %d end %d col %d", name, width, v, start, end, col)
		}
	}
	e := New("abcdefgh", 8)
	check("cursor at the end", e, 5, 4, 8, 4) // カーソルのための 1 桁を残す
	e.Home()
	check("home", e, 5, 0, 5, 0)
	e.Right()
	e.Right()
	check("move within the view", e, 5, 0, 5, 2)
	// 幅 2 の文字。右端にかかる文字は出さない。
	e = New("あいうえお", len("あいうえお"))
	check("wide at the end", e, 5, len("あいう"), len("あいうえお"), 4)
	e = New("abあ", 0)
	check("wide at the right edge", e, 3, 0, 2, 0)
	// 消して短くなったら、左に戻して欄を埋める。
	e = New("abcdefgh", 8)
	e.View(5)
	for range 5 {
		e.DeleteBackward()
	}
	check("after deleting", e, 5, 0, 3, 3)
	// 全体が欄に収まるなら、カーソルが末尾になくても、先頭から表示する（カーソルの 1 桁は、後ろの文字の上にある）。
	e = New("abcde", 5)
	check("end of a text as wide as the field", e, 5, 1, 5, 4)
	e.Left()
	check("the whole text fits", e, 5, 0, 5, 4)
	// 表示する形の幅で数える（制御文字は ? で 1 桁）。
	e = New("a\x1bb", 3)
	check("display forms", e, 10, 0, 3, 3)
	check("zero width", e, 0, 3, 3, 0)
}

// TestViewLongText は、長い文字列（大きな貼り付け）でも、表示の範囲を文字列の長さに比例する時間で決めることを確かめる（filer U5）。
// 長さの 2 乗の時間がかかると、この長さでは数分かかる。
func TestViewLongText(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 1<<17)
	e := New(long, len(long))
	for _, w := range []int{10, 80} {
		if v := e.View(w); v.Start != len(long)-(w-1) || v.End != len(long) || v.CursorCol != w-1 {
			t.Errorf("View(%d) at the end = %+v", w, v)
		}
	}
	e.Home()
	if v := e.View(80); v.Start != 0 || v.End != 80 || v.CursorCol != 0 {
		t.Errorf("View(80) at the start = %+v", v)
	}
}

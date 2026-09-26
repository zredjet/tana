package textwidth

import (
	"strings"
	"testing"
)

// c は、表のテストの期待値を短く書くためのもの。
func c(text string, class Class, display string, width int, varies bool) Cluster {
	return Cluster{Text: text, Class: class, Display: display, Width: width, Varies: varies}
}

func n(text string, width int) Cluster { return c(text, Normal, text, width, false) }

// TestClusters は、書記素クラスタの分類・表示する形・表示幅・「幅が端末によって違いうる」印を確かめる（tui §4）。
func TestClusters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []Cluster
	}{
		{"empty", "", nil},
		{"ascii", "ab", []Cluster{n("a", 1), n("b", 1)}},
		{"wide", "あ漢Ａ𠮷", []Cluster{n("あ", 2), n("漢", 2), n("Ａ", 2), n("𠮷", 2)}},
		{"halfwidth kana with voiced mark", "ｶﾞ", []Cluster{n("ｶﾞ", 2)}},
		{"nfd kana", "か\u3099き\u3099", []Cluster{n("か\u3099", 2), n("き\u3099", 2)}},
		{"nfd latin (combining mark is not ambiguous-width)", "e\u0301", []Cluster{n("e\u0301", 1)}},
		{"ivs", "葛\U000e0100", []Cluster{n("葛\U000e0100", 2)}},
		{"thai sara am", "กำ", []Cluster{n("กำ", 2)}},
		{"hangul L+V", "\u1100\u1161", []Cluster{n("\u1100\u1161", 2)}},
		{"emoji vs15", "😀\ufe0e", []Cluster{n("😀\ufe0e", 2)}},
		{"skin tone alone", "\U0001f3fd", []Cluster{n("\U0001f3fd", 2)}},
		{"unicode 16.0 emoji", "\U0001fabe", []Cluster{n("\U0001fabe", 2)}},
		{"text-default emoji", "❤", []Cluster{n("❤", 1)}},

		// 幅が端末によって違いうる印: 幅が曖昧な文字、Unicode 16.0 より新しい絵文字、幅が 3 以上のもの
		{"ambiguous", "○é─", []Cluster{c("○", Normal, "○", 1, true), c("é", Normal, "é", 1, true), c("─", Normal, "─", 1, true)}},
		{"unicode 17.0 emoji", "\U0001fac8", []Cluster{c("\U0001fac8", Normal, "\U0001fac8", 2, true)}},
		{"unicode 18.0 emoji", "\U0001faf9", []Cluster{c("\U0001faf9", Normal, "\U0001faf9", 2, true)}},
		{"devanagari conjunct (width 3)", "क\u094dषि", []Cluster{c("क\u094dषि", Normal, "क\u094dषि", 3, true)}},
		{"hangul L+L (width 4)", "\u1100\u1100", []Cluster{c("\u1100\u1100", Normal, "\u1100\u1100", 4, true)}},

		// 端末に出してはいけない文字
		{"escape", "\x1b[31m", []Cluster{c("\x1b", Forbidden, "?", 1, false), n("[", 1), n("3", 1), n("1", 1), n("m", 1)}},
		{"crlf is one cluster", "\r\n", []Cluster{c("\r\n", Forbidden, "?", 1, false)}},
		{"del and c1", "\x7f\u0085", []Cluster{c("\x7f", Forbidden, "?", 1, false), c("\u0085", Forbidden, "?", 1, false)}},
		{"bidi override", "a\u202etxt", []Cluster{n("a", 1), c("\u202e", Forbidden, "?", 1, false), n("t", 1), n("x", 1), n("t", 1)}},
		{"direction marks and isolates", "\u200e\u200f\u061c\u2066", []Cluster{
			c("\u200e", Forbidden, "?", 1, false), c("\u200f", Forbidden, "?", 1, false),
			c("\u061c", Forbidden, "?", 1, false), c("\u2066", Forbidden, "?", 1, false)}},
		{"line and paragraph separators", "\u2028\u2029", []Cluster{c("\u2028", Forbidden, "?", 1, false), c("\u2029", Forbidden, "?", 1, false)}},

		// 不正な UTF-8 のバイト（1 バイトずつ）
		{"invalid byte", "a\xffb", []Cluster{n("a", 1), c("\xff", InvalidByte, "?", 1, false), n("b", 1)}},
		{"truncated", "\xe3\x81", []Cluster{c("\xe3", InvalidByte, "?", 1, false), c("\x81", InvalidByte, "?", 1, false)}},
		{"wtf-8 surrogate", "\xed\xa0\x80", []Cluster{c("\xed", InvalidByte, "?", 1, false), c("\xa0", InvalidByte, "?", 1, false), c("\x80", InvalidByte, "?", 1, false)}},
		{"combining mark after invalid byte", "\xff\u0301", []Cluster{c("\xff", InvalidByte, "?", 1, false), c("\u0301", Invisible, "?", 1, false)}},

		// 見えない書記素クラスタ
		{"zwsp", "a\u200bb", []Cluster{n("a", 1), c("\u200b", Invisible, "?", 1, false), n("b", 1)}},
		{"lone zwj", "\u200d", []Cluster{c("\u200d", Invisible, "?", 1, false)}},
		{"bom, word joiner, soft hyphen, cgj", "\ufeff\u2060\u00ad\u034f", []Cluster{
			c("\ufeff", Invisible, "?", 1, false), c("\u2060", Invisible, "?", 1, false),
			c("\u00ad", Invisible, "?", 1, false), c("\u034f", Invisible, "?", 1, false)}},
		{"hangul filler", "\u3164", []Cluster{c("\u3164", Invisible, "?", 1, false)}},
		{"leading combining mark", "\u3099か", []Cluster{c("\u3099", Invisible, "?", 1, false), n("か", 2)}},
		{"lone vs16", "\ufe0f", []Cluster{c("\ufe0f", Invisible, "?", 1, false)}},
		{"lone hangul V jamo (width 0)", "\u1161", []Cluster{c("\u1161", Invisible, "?", 1, false)}},
		{"lone keycap mark", "\u20e3", []Cluster{c("\u20e3", Invisible, "?", 1, false)}},

		// 不安定な書記素クラスタ（表示を簡略な形にする）
		{"vs16", "❤\ufe0f", []Cluster{c("❤\ufe0f", Unstable, "❤", 1, false)}},
		{"vs16 on ambiguous", "↔\ufe0f", []Cluster{c("↔\ufe0f", Unstable, "↔", 1, true)}},
		{"vs16 on kanji", "漢\ufe0f", []Cluster{c("漢\ufe0f", Unstable, "漢", 2, false)}},
		{"skin tone", "👍\U0001f3fd", []Cluster{c("👍\U0001f3fd", Unstable, "👍", 2, false)}},
		{"zwj family", "👨\u200d👩\u200d👧", []Cluster{c("👨\u200d👩\u200d👧", Unstable, "👨", 2, false)}},
		{"zwj with skin tone", "👩\U0001f3fd\u200d💻", []Cluster{c("👩\U0001f3fd\u200d💻", Unstable, "👩", 2, false)}},
		{"rainbow flag", "🏳\ufe0f\u200d🌈", []Cluster{c("🏳\ufe0f\u200d🌈", Unstable, "🏳", 1, false)}},
		{"keycap", "#\ufe0f\u20e3", []Cluster{c("#\ufe0f\u20e3", Unstable, "#", 1, false)}},
		{"tag sequence", "🏴\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f", []Cluster{
			c("🏴\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f", Unstable, "🏴", 2, false)}},
		{"flag", "\U0001f1ef\U0001f1f5", []Cluster{c("\U0001f1ef\U0001f1f5", Unstable, "??", 2, false)}},
		{"two flags", "\U0001f1ef\U0001f1f5\U0001f1fa\U0001f1f8", []Cluster{
			c("\U0001f1ef\U0001f1f5", Unstable, "??", 2, false), c("\U0001f1fa\U0001f1f8", Unstable, "??", 2, false)}},
		{"lone regional indicator", "\U0001f1ef", []Cluster{c("\U0001f1ef", Unstable, "?", 1, false)}},
		{"three regional indicators", "\U0001f1ef\U0001f1f5\U0001f1fa", []Cluster{
			c("\U0001f1ef\U0001f1f5", Unstable, "??", 2, false), c("\U0001f1fa", Unstable, "?", 1, false)}},
	}
	for _, tt := range tests {
		var got []Cluster
		for c := range All(tt.in) {
			got = append(got, c)
		}
		if len(got) != len(tt.want) {
			t.Errorf("%s: All(%q) = %+v, want %+v", tt.name, tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%s: All(%q)[%d] = %+v, want %+v", tt.name, tt.in, i, got[i], tt.want[i])
			}
		}
		want := 0
		for _, c := range tt.want {
			want += c.Width
		}
		if w := Width(tt.in); w != want {
			t.Errorf("%s: Width(%q) = %d, want %d", tt.name, tt.in, w, want)
		}
	}
}

// TestNext は、Next が All と同じ区切りを返し、空の文字列で空の Cluster を返すことを確かめる。
func TestNext(t *testing.T) {
	t.Parallel()
	c, rest := Next("")
	if c != (Cluster{}) || rest != "" {
		t.Errorf("Next(\"\") = %+v, %q", c, rest)
	}
	s := "a👨\u200d👩\u200d👧\xffか\u3099"
	var viaNext []string
	for rest := s; rest != ""; {
		var c Cluster
		c, rest = Next(rest)
		viaNext = append(viaNext, c.Text)
	}
	var viaAll []string
	for c := range All(s) {
		viaAll = append(viaAll, c.Text)
	}
	if strings.Join(viaNext, "|") != strings.Join(viaAll, "|") || len(viaAll) != 4 {
		t.Errorf("Next: %q, All: %q", viaNext, viaAll)
	}
}

// TestAllStopsEarly は、All の繰り返しを途中でやめられることを確かめる。
func TestAllStopsEarly(t *testing.T) {
	t.Parallel()
	count := 0
	for range All("abc") {
		count++
		break
	}
	if count != 1 {
		t.Errorf("count = %d", count)
	}
}

// TestLongCluster は、とても長い書記素クラスタ（結合文字が続くもの）を 1 つとして扱えることを確かめる。
func TestLongCluster(t *testing.T) {
	t.Parallel()
	s := "e" + strings.Repeat("\u0301", 10000)
	c, rest := Next(s)
	if c.Text != s || rest != "" || c.Width != 1 || c.Class != Normal {
		t.Errorf("Next(long) = class %v width %d len %d, rest %d", c.Class, c.Width, len(c.Text), len(rest))
	}
}

func TestClassString(t *testing.T) {
	t.Parallel()
	for cl, want := range map[Class]string{Normal: "Normal", Forbidden: "Forbidden", InvalidByte: "InvalidByte", Invisible: "Invisible", Unstable: "Unstable", Class(99): "Class(99)"} {
		if got := cl.String(); got != want {
			t.Errorf("Class(%d).String() = %q, want %q", uint8(cl), got, want)
		}
	}
}

package textfmt

import (
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"

	"github.com/zredjet/tana/internal/textwidth"
)

func sjis(t testing.TB, s string) []byte {
	b, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func utf16le(t testing.TB, s string) []byte {
	b, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestDecodeText は、プレビューの文字コードの判定を確かめる（filer §6）。UTF-8（BOM の有無を問わない）、BOM 付きの UTF-16、Shift_JIS。
func TestDecodeText(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		data      []byte
		truncated bool
		text, enc string
		ok        bool
	}{
		{"utf8", []byte("hello\n世界\n"), false, "hello\n世界\n", "UTF-8", true},
		{"utf8 bom", append([]byte("\xef\xbb\xbf"), "日本"...), false, "日本", "UTF-8", true},
		{"utf8 cut in a character", []byte("abc日")[:5], true, "abc", "UTF-8", true},
		{"sjis", sjis(t, "日本語のテキスト\r\n２行目"), false, "日本語のテキスト\r\n２行目", "Shift_JIS", true},
		{"sjis cut after a lead byte", sjis(t, "テキスト")[:7], true, "テキス", "Shift_JIS", true},
		{"utf16le bom", utf16le(t, "ab日本"), false, "ab日本", "UTF-16", true},
		{"empty", nil, false, "", "UTF-8", true},
		{"nul", []byte("a\x00b"), false, "", "", false},
		{"not text", []byte("\xff\xfe\xfd\xfc\x80\x80"[2:]), false, "", "", false},
		{"many controls", []byte("\x01\x02\x03\x04abc\x05\x06"), false, "", "", false},
		{"ansi colors are text", []byte("\x1b[31mred\x1b[0m\n"), false, "\x1b[31mred\x1b[0m\n", "UTF-8", true},
		{"utf8 invalid in the middle", []byte("abc\xffdef"), false, "", "", false},
	} {
		text, enc, ok := DecodeText(tt.data, tt.truncated)
		if ok != tt.ok || ok && (text != tt.text || enc != tt.enc) {
			t.Errorf("%s: DecodeText = %q, %q, %v, want %q, %q, %v", tt.name, text, enc, ok, tt.text, tt.enc, tt.ok)
		}
	}
}

// TestExpandTabs は、タブを 4 桁ごとの空白に広げることを確かめる。桁は表示幅（textwidth）で数える。
func TestExpandTabs(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"a\tb":     "a   b",
		"\tx":      "    x",
		"abcd\te":  "abcd    e",
		"日\tx":     "日  x", // 全角は 2 桁
		"no tabs":  "no tabs",
		"a\t\tb":   "a       b",
		"が\t": "が  ",
	} {
		if got := ExpandTabs(in, 4); got != want {
			t.Errorf("ExpandTabs(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPreviewLines は、テキストを行に分け（\r\n・\n）、タブを広げ、n 行までにすることを確かめる。
func TestPreviewLines(t *testing.T) {
	t.Parallel()
	got := PreviewLines("a\tb\r\nsecond\n\nfourth\nfifth", 4)
	want := []string{"a   b", "second", "", "fourth"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("PreviewLines = %q, want %q", got, want)
	}
	if got := PreviewLines("", 3); len(got) != 0 {
		t.Errorf("PreviewLines(\"\") = %q", got)
	}
	if got := PreviewLines("x\n", 3); len(got) != 1 {
		t.Errorf("PreviewLines(x\\n) = %q, want one line", got)
	}
}

// FuzzDecodeText は、どんな入力でも止まらず、テキストと判定したものは正しい UTF-8 で、行にタブが残らないことを確かめる。
func FuzzDecodeText(f *testing.F) {
	for _, s := range []string{"hello", "\xef\xbb\xbfa", "\xff\xfea\x00", "\x93\xfa\x96\x7b", "a\x00", "\x1b[31m", "\t\t", "abc\xe6"} {
		f.Add([]byte(s), false)
		f.Add([]byte(s), true)
	}
	f.Fuzz(func(t *testing.T, data []byte, truncated bool) {
		text, _, ok := DecodeText(data, truncated)
		if !ok {
			return
		}
		if !utf8.ValidString(text) {
			t.Fatalf("DecodeText(%q) = %q is not valid UTF-8", data, text)
		}
		for _, l := range PreviewLines(text, 50) {
			if strings.ContainsAny(l, "\t\n") {
				t.Fatalf("line %q has a tab or a newline", l)
			}
			_ = textwidth.Width(l)
		}
	})
}

// TestWrap は、表示幅ごとに行を分けることを確かめる（全角は 2 桁、書記素クラスタの途中で分けない）。
func TestWrap(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		s     string
		width int
		want  []string
	}{
		{"abcdef", 4, []string{"abcd", "ef"}},
		{"あいうえ", 5, []string{"あい", "うえ"}},
		{"がぎ", 2, []string{"が", "ぎ"}},
		{"ab", 10, []string{"ab"}},
		{"", 3, nil},
		{"あ", 1, []string{"あ"}}, // 入らない 1 文字はそれだけで 1 行
	} {
		got := Wrap(tt.s, tt.width)
		if strings.Join(got, "|") != strings.Join(tt.want, "|") || len(got) != len(tt.want) {
			t.Errorf("Wrap(%q, %d) = %q, want %q", tt.s, tt.width, got, tt.want)
		}
	}
}

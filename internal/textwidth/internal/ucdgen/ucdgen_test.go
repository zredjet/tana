package ucdgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyUCD は、textwidth の ucd のファイルを一時フォルダに写す。
func copyUCD(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range append(Files, "GraphemeBreakTest.txt") {
		data, err := os.ReadFile(filepath.Join("..", "..", "ucd", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// edit は、dir の name の中の old を new に置き換える。
func edit(t *testing.T, dir, name, old, new string) {
	t.Helper()
	p := filepath.Join(dir, name)
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s does not contain %q", name, old)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(data), old, new, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerate(t *testing.T) {
	t.Parallel()
	src, err := Generate(filepath.Join("..", "..", "ucd"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `const UnicodeVersion = "18.0.0"`) || !strings.Contains(string(src), "DO NOT EDIT") {
		t.Errorf("generated source lacks the version or the header:\n%.300s", src)
	}
}

func TestProps(t *testing.T) {
	t.Parallel()
	p, _, err := Props(filepath.Join("..", "..", "ucd"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		r    rune
		mask uint16
		want uint16
		what string
	}{
		{0x0d, GCBMask, GCBCR, "CR"},
		{0x0301, WidthMask | Mark, WidthZero<<WidthShift | Mark, "combining acute: mark, width 0"},
		{0x3042, WidthMask, WidthTwo << WidthShift, "hiragana a: width 2"},
		{0x25cb, Ambiguous | WidthMask, Ambiguous, "white circle: ambiguous, width 1"},
		{0x200b, DefaultIgnorable | WidthMask, DefaultIgnorable | WidthZero<<WidthShift, "zwsp: ignorable, width 0"},
		{0x1161, WidthMask | GCBMask, WidthZero<<WidthShift | GCBV, "hangul V jamo: width 0"},
		{0x094d, InCBMask, InCBLinker << InCBShift, "devanagari virama: InCB Linker"},
		{0x0915, InCBMask, InCBConsonant << InCBShift, "devanagari ka: InCB Consonant"},
		{0x1f3fd, EmojiModifier, EmojiModifier, "skin tone"},
		{0x1fabe, NewEmoji | ExtPict, ExtPict, "Unicode 16.0 emoji is not new"},
		{0x1fac8, NewEmoji | ExtPict, NewEmoji | ExtPict, "Unicode 17.0 emoji is new"},
		{0x1fffd, NewEmoji | ExtPict, NewEmoji | ExtPict, "unassigned Extended_Pictographic is new"},
	}
	for _, tt := range tests {
		if got := p[tt.r] & tt.mask; got != tt.want {
			t.Errorf("%U (%s): %#x, want %#x", tt.r, tt.what, got, tt.want)
		}
	}
}

func TestGenerateErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, file, old, new, want string
	}{
		{"version mismatch", "EastAsianWidth.txt", "EastAsianWidth-18.0.0.txt", "EastAsianWidth-17.0.0.txt", "Unicode 17.0.0"},
		{"unknown GCB", "GraphemeBreakProperty.txt", "; LF", "; Bogus", "unknown Grapheme_Cluster_Break"},
		{"unknown InCB", "DerivedCoreProperties.txt", "; InCB; Linker", "; InCB; Bogus", "unknown InCB"},
		{"bad range", "DerivedAge.txt", "0000..001F", "001F..0000", "bad range"},
		{"bad hex", "emoji-data.txt", "0023          ; Emoji", "00G3          ; Emoji", "invalid syntax"},
		{"no field", "DerivedGeneralCategory.txt", "0000..001F    ; Cc", "0000..001F", "no field"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := copyUCD(t)
			edit(t, dir, tt.file, tt.old, tt.new)
			if _, err := Generate(dir); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want one containing %q", err, tt.want)
			}
		})
	}
	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		dir := copyUCD(t)
		if err := os.Remove(filepath.Join(dir, "DerivedAge.txt")); err != nil {
			t.Fatal(err)
		}
		if _, err := Generate(dir); err == nil {
			t.Error("err = nil")
		}
	})
	t.Run("no version line", func(t *testing.T) {
		t.Parallel()
		dir := copyUCD(t)
		edit(t, dir, "emoji-data.txt", "# Version: 18.0.0", "# (no version)")
		if _, err := Generate(dir); err == nil || !strings.Contains(err.Error(), "no version line") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestNewerThan(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		a, b string
		want bool
	}{{"17.0", "16.0", true}, {"16.0", "16.0", false}, {"15.1", "16.0", false}, {"16.1", "16.0", true}, {"2.0", "16.0", false}} {
		if got := newerThan(tt.a, tt.b); got != tt.want {
			t.Errorf("newerThan(%s, %s) = %v", tt.a, tt.b, got)
		}
	}
}

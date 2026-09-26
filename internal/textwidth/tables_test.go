package textwidth

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/textwidth/internal/ucdgen"
)

// TestTablesUpToDate は、tables.go が ucd のデータから作ったものと同じであることを確かめる（go generate の実行忘れ）。
func TestTablesUpToDate(t *testing.T) {
	t.Parallel()
	want, err := ucdgen.Generate("ucd")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("tables.go")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("tables.go is out of date; run go generate in internal/textwidth")
	}
}

// TestPropBits は、props.go のビットの割り当てが、表を作る ucdgen と同じであることを確かめる。
func TestPropBits(t *testing.T) {
	t.Parallel()
	pairs := []struct {
		name      string
		got, want int
	}{
		{"gcbOther", gcbOther, ucdgen.GCBOther}, {"gcbCR", gcbCR, ucdgen.GCBCR}, {"gcbLF", gcbLF, ucdgen.GCBLF},
		{"gcbControl", gcbControl, ucdgen.GCBControl}, {"gcbExtend", gcbExtend, ucdgen.GCBExtend}, {"gcbZWJ", gcbZWJ, ucdgen.GCBZWJ},
		{"gcbRI", gcbRI, ucdgen.GCBRegionalIndicator}, {"gcbPrepend", gcbPrepend, ucdgen.GCBPrepend},
		{"gcbSpacingMark", gcbSpacingMark, ucdgen.GCBSpacingMark}, {"gcbL", gcbL, ucdgen.GCBL}, {"gcbV", gcbV, ucdgen.GCBV},
		{"gcbT", gcbT, ucdgen.GCBT}, {"gcbLV", gcbLV, ucdgen.GCBLV}, {"gcbLVT", gcbLVT, ucdgen.GCBLVT},
		{"gcbMask", gcbMask, ucdgen.GCBMask},
		{"incbShift", incbShift, ucdgen.InCBShift}, {"incbMask", incbMask, ucdgen.InCBMask},
		{"incbConsonant", incbConsonant, ucdgen.InCBConsonant}, {"incbExtend", incbExtend, ucdgen.InCBExtend}, {"incbLinker", incbLinker, ucdgen.InCBLinker},
		{"extPict", extPict, ucdgen.ExtPict}, {"emojiModifier", emojiModifier, ucdgen.EmojiModifier},
		{"widthShift", widthShift, ucdgen.WidthShift}, {"widthMask", widthMask, ucdgen.WidthMask},
		{"widthOne", widthOne, ucdgen.WidthOne}, {"widthZero", widthZero, ucdgen.WidthZero}, {"widthTwo", widthTwo, ucdgen.WidthTwo},
		{"ambiguous", ambiguous, ucdgen.Ambiguous}, {"defaultIgnorable", defaultIgnorable, ucdgen.DefaultIgnorable},
		{"mark", mark, ucdgen.Mark}, {"newEmoji", newEmoji, ucdgen.NewEmoji},
	}
	for _, p := range pairs {
		if p.got != p.want {
			t.Errorf("%s = %#x, ucdgen has %#x", p.name, p.got, p.want)
		}
	}
}

// TestDocVersion は、doc.go に書いた Unicode の版が、表の版と同じであることを確かめる（tui §10）。
func TestDocVersion(t *testing.T) {
	t.Parallel()
	doc, err := os.ReadFile("doc.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "Unicode "+UnicodeVersion) {
		t.Errorf("doc.go does not mention Unicode %s", UnicodeVersion)
	}
}

// TestLookupEdges は、表の端の符号位置と、範囲の外の値で lookup が壊れないことを確かめる。
func TestLookupEdges(t *testing.T) {
	t.Parallel()
	for _, r := range []rune{0, 0x10ffff, -1, 0x110000, 0x7fffffff} {
		_ = lookup(r)
	}
	if lookup(0x0a)&gcbMask != gcbLF || lookup(0x0d)&gcbMask != gcbCR {
		t.Error("lookup of LF or CR is wrong")
	}
	if lookup(0x3042)&widthMask>>widthShift != widthTwo {
		t.Error("U+3042 is not wide")
	}
}

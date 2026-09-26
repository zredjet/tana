package textwidth

import (
	"bufio"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestGraphemeBreakTest は、Unicode の GraphemeBreakTest.txt のすべての行で、書記素クラスタの分け方が一致することを確かめる（tui §4）。
func TestGraphemeBreakTest(t *testing.T) {
	t.Parallel()
	f, err := os.Open("ucd/GraphemeBreakTest.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lines, skipped := 0, 0
	for n := 1; sc.Scan(); n++ {
		line, comment, _ := strings.Cut(sc.Text(), "#")
		if strings.TrimSpace(line) == "" {
			continue
		}
		// 「÷ 0020 × 0308 ÷」の形。÷ は区切り、× は区切りなし。
		var want []string
		var cur strings.Builder
		surrogate := false
		for _, tok := range strings.Fields(line) {
			switch tok {
			case "÷":
				if cur.Len() > 0 {
					want = append(want, cur.String())
					cur.Reset()
				}
			case "×":
			default:
				v, err := strconv.ParseUint(tok, 16, 32)
				if err != nil {
					t.Fatalf("line %d: %v", n, err)
				}
				if 0xd800 <= v && v <= 0xdfff {
					surrogate = true
				}
				cur.WriteRune(rune(v))
			}
		}
		if surrogate {
			// Go の文字列ではサロゲートを表せない（UTF-8 にすると不正なバイトになる）。
			skipped++
			continue
		}
		lines++
		s := strings.Join(want, "")
		var got []string
		for c := range All(s) {
			got = append(got, c.Text)
		}
		if !slices.Equal(got, want) {
			t.Errorf("line %d: %q\n got  %q\n want %q\n %s", n, s, got, want, comment)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if lines < 500 {
		t.Errorf("only %d test lines read", lines)
	}
	t.Logf("GraphemeBreakTest.txt: %d lines checked, %d lines with surrogates skipped", lines, skipped)
}

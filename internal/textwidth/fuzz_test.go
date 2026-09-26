package textwidth

import (
	"strings"
	"testing"
)

// FuzzClusters は、任意のバイト列で、次の性質を確かめる（tui §4 のファジング）。
//   - panic しない。
//   - 書記素クラスタをつなぐと元のバイト列に戻る。どのクラスタも空でない。
//   - 表示幅は 1 以上で、表示する形を数え直した幅と一致する。
//   - 表示する形は、通常の文字の書記素クラスタだけからなる
//     （端末に出してはいけない文字・不正なバイト・見えない書記素クラスタ・不安定な書記素クラスタを含まない。T2・T6）。
//   - 通常の文字は、表示する形が元のバイト列と同じ。
//   - Next で順にたどった区切りは All と同じで、Width は表示幅の和。
func FuzzClusters(f *testing.F) {
	for _, s := range []string{
		"", "abc", "あ漢", "か\u3099", "ｶﾞ", "\r\n", "\x1b[31m", "a\xffb", "\xed\xa0\x80",
		"👨\u200d👩\u200d👧", "👍\U0001f3fd", "#\ufe0f\u20e3", "\U0001f1ef\U0001f1f5\U0001f1fa",
		"🏴\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f", "क\u094dषि", "\u1100\u1100\u1161",
		"\u200b\u200d\ufeff", "\u3099\u0301", "葛\U000e0100", "❤\ufe0f\u200d", "\ufe0f\U0001f3fd",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		var joined strings.Builder
		var texts []string
		total := 0
		for c := range All(s) {
			if c.Text == "" {
				t.Fatalf("empty cluster in %q", s)
			}
			joined.WriteString(c.Text)
			texts = append(texts, c.Text)
			if c.Width < 1 {
				t.Errorf("%q: cluster %q has width %d", s, c.Text, c.Width)
			}
			if c.Class == Normal && c.Display != c.Text {
				t.Errorf("%q: normal cluster %q displayed as %q", s, c.Text, c.Display)
			}
			w := 0
			for d := range All(c.Display) {
				if d.Class != Normal {
					t.Errorf("%q: display %q of %q contains a %v cluster %q", s, c.Display, c.Text, d.Class, d.Text)
				}
				w += d.Width
			}
			if w != c.Width {
				t.Errorf("%q: cluster %q (display %q) has width %d, display counts %d", s, c.Text, c.Display, c.Width, w)
			}
			total += c.Width
		}
		if joined.String() != s {
			t.Errorf("clusters of %q join to %q", s, joined.String())
		}
		if got := Width(s); got != total {
			t.Errorf("Width(%q) = %d, sum of clusters %d", s, got, total)
		}
		i := 0
		for rest := s; rest != ""; i++ {
			var c Cluster
			c, rest = Next(rest)
			if i >= len(texts) || c.Text != texts[i] {
				t.Fatalf("%q: Next and All disagree at cluster %d", s, i)
			}
		}
		if i != len(texts) {
			t.Errorf("%q: Next gave %d clusters, All gave %d", s, i, len(texts))
		}
	})
}

package textwidth

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// mainTerminals は、幅の期待値に使う主な 3 端末の実測（docs/probe-results。filer §12.5）。
// conhost は多くの文字で違う（filer VU9）ので、期待値に使わない。
var mainTerminals = []string{"terminal-app", "iterm2", "windows-terminal"}

type probeCase struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Hex      string `json:"hex"`
	Advance  *int   `json:"advance"`
}

// loadProbe は、端末の width の実測を読む。
func loadProbe(t *testing.T, terminal string) map[string]probeCase {
	t.Helper()
	data, err := os.ReadFile("../../docs/probe-results/" + terminal + "-2026-09-26.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Width struct {
			Cases []probeCase `json:"cases"`
		} `json:"width"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	m := map[string]probeCase{}
	for _, c := range f.Width.Cases {
		m[c.ID] = c
	}
	return m
}

// replacedCases は、実測した文字列のうち、表示で置き換える（見えない書記素クラスタ・不安定な書記素クラスタ）ものと、その理由。
// split は、元の形の幅が主な 3 端末で割れていたこと（置き換える理由）。false のものは、規則（tui §4）で置き換える。
var replacedCases = map[string]struct {
	class Class
	split bool
}{
	"combining-dakuten-alone": {Invisible, true}, // 0／1／1
	"combining-acute-alone":   {Invisible, true}, // 0／1／1
	"zwsp":                    {Invisible, false},
	"zwj":                     {Invisible, false},
	"word-joiner":             {Invisible, true}, // 1／0／1
	"bom":                     {Invisible, true}, // 1／0／1
	"soft-hyphen":             {Invisible, true}, // 1／0／1
	"cgj":                     {Invisible, true}, // 0／1／1
	"a-zwsp-b":                {Invisible, true}, // 3／3／2
	"jamo-v-alone":            {Invisible, true}, // 0／1／1
	"emoji-thumbs-skin":       {Unstable, true},  // 4／4／2
	"emoji-family":            {Unstable, true},  // 8／2／2
	"emoji-technologist":      {Unstable, true},  // 5／2／2
	"emoji-tech-skin":         {Unstable, true},  // 7／4／2
	"emoji-rainbow-flag":      {Unstable, true},  // 4／1／2
	"heart-vs16":              {Unstable, true},  // 1／1／2
	"smile-vs16":              {Unstable, true},
	"scissors-vs16":           {Unstable, true},
	"sun-vs16":                {Unstable, true},
	"arrow-lr-vs16":           {Unstable, true},
	"play-vs16":               {Unstable, true},
	"copyright-vs16":          {Unstable, true},
	"keycap-hash":             {Unstable, true}, // 2／1／2
	"keycap-1":                {Unstable, true},
	"flag-jp":                 {Unstable, true}, // 2／4／2
	"ri-alone":                {Unstable, true}, // 1／2／1
	"flags-two":               {Unstable, true}, // 4／8／4
	"flag-england":            {Unstable, true}, // 8／2／2
	"vs16-on-kanji":           {Unstable, false},
}

// adoptedWidth は、主な 3 端末の実測がそろっているのに、textwidth がほかの値を採るものと、その理由。
var adoptedWidth = map[string]string{
	"emoji-u18":  "Unicode 18.0 の絵文字。Unicode の表では W（2）。どの端末も表が追いついておらず 1。「違いうる」印を付ける",
	"emoji-u18b": "同上",
}

// TestAgainstProbeResults は、フェーズ12・13の実測（docs/probe-results）を期待値として、幅の規則を確かめる（tui §4）。
//   - 主な 3 端末の実測がそろっていて、置き換えないものは、表示幅が実測と一致する（adoptedWidth を除く）。
//   - 実測が割れているものは、置き換えるか、「幅が端末によって違いうる」印を付ける。
//   - 置き換えるものは replacedCases にあり、表示する形の幅は、その形の実測と一致する。
func TestAgainstProbeResults(t *testing.T) {
	t.Parallel()
	probes := map[string]map[string]probeCase{}
	for _, term := range mainTerminals {
		probes[term] = loadProbe(t, term)
	}
	// 表示する形の実測を引くため、文字列から実測の ID を引けるようにする。
	byText := map[string]string{}
	for id, pc := range probes["terminal-app"] {
		b, _ := hex.DecodeString(pc.Hex)
		byText[string(b)] = id
	}
	measured := func(id string) (vals []int, agree bool) {
		for _, term := range mainTerminals {
			pc, ok := probes[term][id]
			if !ok || pc.Advance == nil {
				t.Fatalf("%s: no measurement on %s", id, term)
			}
			vals = append(vals, *pc.Advance)
		}
		return vals, vals[0] == vals[1] && vals[1] == vals[2]
	}

	seen := map[string]bool{}
	for id, pc := range probes["terminal-app"] {
		b, err := hex.DecodeString(pc.Hex)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		var classes []Class
		var display strings.Builder
		width, varies, invalid := 0, false, false
		for c := range All(text) {
			if c.Class == InvalidByte {
				invalid = true
			}
			if c.Class != Normal {
				classes = append(classes, c.Class)
			}
			display.WriteString(c.Display)
			width += c.Width
			varies = varies || c.Varies
		}
		if invalid {
			// 不正なバイトは 1 バイトずつ ? にする。端末の数え方（UTF-8 の復号）は端末ごとに違い、比べる意味がない。
			continue
		}
		vals, agree := measured(id)
		want, isReplaced := replacedCases[id]
		switch {
		case len(classes) > 0:
			seen[id] = true
			if !isReplaced {
				t.Errorf("%s: replaced as %v but not listed in replacedCases (measured %v)", id, classes, vals)
				break
			}
			if classes[0] != want.class {
				t.Errorf("%s: class %v, want %v", id, classes[0], want.class)
			}
			if want.split == agree {
				t.Errorf("%s: measured %v; replacedCases says split=%v", id, vals, want.split)
			}
			// 表示する形の幅が、その形の実測と一致すること。ASCII だけの形は 1 文字 1 桁。
			d := display.String()
			if isASCII(d) {
				if width != len(d) {
					t.Errorf("%s: display %q has width %d, want %d", id, d, width, len(d))
				}
				break
			}
			did, ok := byText[d]
			if !ok {
				t.Errorf("%s: display form %q was not measured", id, d)
				break
			}
			dvals, dagree := measured(did)
			if !dagree || dvals[0] != width {
				t.Errorf("%s: display form %q has width %d, measured as %s: %v", id, d, width, did, dvals)
			}
		case isReplaced:
			t.Errorf("%s: listed in replacedCases but not replaced", id)
		case !agree && !varies:
			t.Errorf("%s: measured %v differ, but neither replaced nor marked as varying", id, vals)
		case agree && width != vals[0]:
			if _, ok := adoptedWidth[id]; !ok || !varies {
				t.Errorf("%s: width %d, all main terminals measured %d (varies=%v)", id, width, vals[0], varies)
			}
			seen[id] = true
		}
	}
	for id := range replacedCases {
		if !seen[id] {
			t.Errorf("replacedCases: %s not found or not replaced in the measurements", id)
		}
	}
	for id := range adoptedWidth {
		if !seen[id] {
			t.Errorf("adoptedWidth: %s not found or agrees with the measurements", id)
		}
	}
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

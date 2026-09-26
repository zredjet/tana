package keys

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// recorded は、docs/probe-results の keys の記録（Unix のバイト列か、Windows の VT の入力モードのレコード）。
type recorded struct {
	Steps []struct {
		ID     string      `json:"id"`
		Result string      `json:"result"`
		Reads  []probeRead `json:"reads"`
	} `json:"steps"`
}

// probeRead は、1 回の読み取りの記録。
type probeRead struct {
	TMs     float64       `json:"t_ms"`
	Hex     string        `json:"hex"`
	Records []probeRecord `json:"records"`
}

// probeRecord は、Windows の入力のレコードの記録。
type probeRecord struct {
	Kind string `json:"kind"`
	Down *bool  `json:"down"`
	VK   uint16 `json:"vk"`
	Char uint16 `json:"char"`
	Rep  uint16 `json:"repeat"`
}

// chunk は、1 回の読み取りで届いたバイト列と、その時刻（手順の最初の入力からのミリ秒）。
type chunk struct {
	ms float64
	b  []byte
}

// stepChunks は、記録の 1 つの手順を、keys に渡すバイト列の並びにする。
// Windows のレコードは、tui §8 の規則でバイト列に変える: キーを押したレコードの文字と、Alt を離したレコードの文字（conhost の Alt＋テンキー）を、
// 読み取りをまたいで UTF-16 から UTF-8 にする。文字のないレコード（キーを離したもの、Shift・Ctrl など）は捨てる。
func stepChunks(t *testing.T, reads []probeRead) []chunk {
	t.Helper()
	var out []chunk
	var high uint16
	for _, r := range reads {
		if r.Records == nil {
			b, err := hex.DecodeString(r.Hex)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, chunk{r.TMs, b})
			continue
		}
		var u []uint16
		if high != 0 {
			u, high = append(u, high), 0
		}
		for _, rec := range r.Records {
			if rec.Kind != "key" || rec.Char == 0 {
				continue
			}
			switch {
			case rec.Down != nil && *rec.Down:
				for range max(rec.Rep, 1) {
					u = append(u, rec.Char)
				}
			case rec.VK == 0x12: // VK_MENU（Alt）を離したレコード
				u = append(u, rec.Char)
			}
		}
		if n := len(u); n > 0 && 0xd800 <= u[n-1] && u[n-1] < 0xdc00 {
			high, u = u[n-1], u[:n-1]
		}
		if len(u) > 0 {
			out = append(out, chunk{r.TMs, []byte(string(utf16.Decode(u)))})
		}
	}
	return out
}

// probeSections は、keys の表のテストに使う記録（Windows は VT の入力モードのもの。tui §5・§8）。
var probeSections = []struct{ file, section string }{
	{"terminal-app", "keys"},
	{"iterm2", "keys"},
	{"windows-terminal", "keys_vtinput"},
	{"conhost", "keys_vtinput"},
}

const pasteText = "tana 貼り付け\r2行目\tタブ🍣" // 改行は CR で届く

// expectedEvents は、手順 id の期待するイベント（String() の形）。端末によって違うものは file で分ける。
func expectedEvents(id, file string) []string {
	switch id {
	case "esc":
		return []string{"Esc"}
	case "esc-x":
		return []string{"Esc", "'x'"} // 人が続けて押した間隔は、待ち時間（50 ミリ秒）より長い
	case "shift-a":
		return []string{"'A'"}
	case "ctrl-a":
		return []string{"Ctrl+'a'"}
	case "ctrl-c":
		return []string{"Ctrl+'c'"}
	case "ctrl-z":
		return []string{"Ctrl+'z'"}
	case "alt-a":
		switch file {
		case "terminal-app", "iterm2":
			return []string{"'å'"} // Mac の Option は文字を送る
		case "conhost":
			return []string{"Ctrl+'a'"} // VM が Option を Ctrl に変えた（filer §12.5。未確認）
		}
		return []string{"Alt+'a'"}
	case "backspace":
		return []string{"Backspace"}
	case "delete":
		return []string{"Delete"}
	case "up", "down", "left", "right", "home", "end":
		return []string{strings.ToUpper(id[:1]) + id[1:]}
	case "pgup":
		return []string{"PageUp"}
	case "pgdn":
		return []string{"PageDown"}
	case "tab", "ctrl-i":
		return []string{"Tab"}
	case "shift-tab":
		return []string{"Shift+Tab"}
	case "enter", "ctrl-m":
		return []string{"Enter"}
	case "ctrl-h":
		return []string{"Ctrl+'h'"}
	case "ctrl-bracket":
		return []string{"Esc"}
	case "f11":
		return nil // どの端末でも届かなかった
	case "ime":
		return []string{"'日'", "'本'", "'語'"}
	case "paste-bracketed":
		return []string{fmt.Sprintf("Paste(%q)", pasteText)}
	case "paste-plain":
		var out []string
		for _, r := range pasteText {
			switch r {
			case '\r':
				out = append(out, "Enter")
			case '\t':
				out = append(out, "Tab")
			default:
				out = append(out, Event{Kind: KeyEvent, Key: KeyRune, Rune: r}.String())
			}
		}
		return out
	}
	if len(id) >= 2 && id[0] == 'f' {
		return []string{"F" + id[1:]}
	}
	return []string{"?unknown step " + id}
}

// TestProbeRecordings は、フェーズ12の 4 端末の keys の記録を、届いた時刻のとおりに渡して、期待するイベントになることを確かめる（tui §5）。
// イベントの元の入力をつなぐと、記録と一致する（T3）。解釈できない入力は一覧にしてログに出す。
func TestProbeRecordings(t *testing.T) {
	t.Parallel()
	var unknown []string
	for _, ps := range probeSections {
		data, err := os.ReadFile("../../docs/probe-results/" + ps.file + "-2026-09-26.json")
		if err != nil {
			t.Fatal(err)
		}
		var f map[string]json.RawMessage
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatal(err)
		}
		var rec recorded
		if err := json.Unmarshal(f[ps.section], &rec); err != nil {
			t.Fatal(err)
		}
		if len(rec.Steps) != 39 {
			t.Errorf("%s %s: %d steps, want 39", ps.file, ps.section, len(rec.Steps))
		}
		for _, st := range rec.Steps {
			var d Decoder
			var ev []Event
			var input strings.Builder
			for _, c := range stepChunks(t, st.Reads) {
				ev = append(ev, d.Feed(c.b, t0.Add(time.Duration(c.ms*float64(time.Millisecond))))...)
				input.Write(c.b)
			}
			ev = append(ev, d.Tick(far)...)
			got := strs(ev)
			want := expectedEvents(st.ID, ps.file)
			if strings.Join(got, " | ") != strings.Join(want, " | ") {
				t.Errorf("%s %s %s: got %s\nwant %s", ps.file, ps.section, st.ID, strings.Join(got, " | "), strings.Join(want, " | "))
			}
			if raws(ev) != input.String() {
				t.Errorf("%s %s %s: raw of events %q, input %q (T3)", ps.file, ps.section, st.ID, raws(ev), input.String())
			}
			for _, e := range ev {
				if e.Kind == UnknownEvent {
					unknown = append(unknown, ps.file+" "+st.ID+": "+e.String())
				}
			}
		}
	}
	t.Logf("unknown inputs in the recordings: %d %q", len(unknown), unknown)
}

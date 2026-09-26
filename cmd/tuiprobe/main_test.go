package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zredjet/tana/internal/term"
)

// fakeConsole は、書かれたものを覚え、カーソル位置の問い合わせに respond の結果を入力として返す。
type fakeConsole struct {
	buf     bytes.Buffer
	written bytes.Buffer
	in      chan term.Input
	respond func(written string) (reply string, ok bool)
	paste   []bool
}

func newFake(respond func(string) (string, bool)) *fakeConsole {
	return &fakeConsole{in: make(chan term.Input, 1000), respond: respond}
}

func (f *fakeConsole) Write(p []byte) (int, error) {
	f.buf.Write(p)
	f.written.Write(p)
	return len(p), nil
}

func (f *fakeConsole) SetBracketedPaste(on bool) error {
	f.paste = append(f.paste, on)
	return nil
}

func (f *fakeConsole) QueryCursorPosition() error {
	w := f.buf.String()
	f.buf.Reset()
	if reply, ok := f.respond(w); ok {
		f.send([]byte(reply))
	}
	return nil
}

func (f *fakeConsole) send(b []byte) { f.in <- term.Input{Time: time.Now(), Bytes: b} }

func (f *fakeConsole) box() *inbox { return &inbox{ctx: context.Background(), in: f.in} }

// measuredText は、measureCases が測る行に書いた文字列を取り出す。
func measuredText(w string) string {
	prefix := "\x1b[" + strconv.Itoa(measureRow) + ";1H\x1b[2K"
	return w[strings.LastIndex(w, prefix)+len(prefix):]
}

// TestWidthCasesAreSafe は、測る文字列に端末に出してはいけない文字（tui §4）が入っていないこと、
// ID が重ならないこと、ずれの画面の ID が存在することを確かめる。
func TestWidthCasesAreSafe(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, wc := range widthCases {
		if seen[wc.id] {
			t.Errorf("duplicate id %q", wc.id)
		}
		seen[wc.id] = true
		if wc.text == "" {
			t.Errorf("%s: empty text", wc.id)
		}
		if !utf8.ValidString(wc.text) != (wc.category == "invalid") {
			t.Errorf("%s: valid UTF-8 = %v, category %q", wc.id, utf8.ValidString(wc.text), wc.category)
		}
		for _, r := range wc.text {
			if r < 0x20 || 0x7f <= r && r <= 0x9f || unicode.Is(unicode.Bidi_Control, r) || r == 0x2028 || r == 0x2029 {
				t.Errorf("%s: contains %U, which must not be written to the terminal", wc.id, r)
			}
		}
	}
	for _, id := range driftIDs {
		if !seen[id] {
			t.Errorf("driftIDs: unknown id %q", id)
		}
	}
}

func TestMeasureCases(t *testing.T) {
	t.Parallel()
	cases := []widthCase{
		{"a", "ascii", "abc", ""},
		{"b", "wide", "漢字", ""},
		{"c", "invalid", "\xff", ""},
	}
	f := newFake(func(w string) (string, bool) {
		text := measuredText(w)
		reply := ""
		if text == "漢字" {
			reply = "\x1b[?1;2c" // 報告の前に、ほかの応答が届く
		}
		return reply + "\x1b[" + strconv.Itoa(measureRow) + ";" + strconv.Itoa(1+2*utf8.RuneCountInString(text)) + "R", true
	})
	got, err := measureCases(f, &cprReader{box: f.box()}, cases, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		adv   int
		stray string
		text  string
	}{{6, "", "abc"}, {4, "1b5b3f313b3263", "漢字"}, {2, "", ""}}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Advance == nil || *g.Advance != w.adv || g.Stray != w.stray || g.Text != w.text || g.Row != measureRow {
			t.Errorf("result %d = %+v (advance %v), want advance %d stray %q text %q", i, g, g.Advance, w.adv, w.stray, w.text)
		}
	}
	if got[2].Hex != "ff" || got[2].CodePoints != "0xFF" {
		t.Errorf("invalid case: hex %q codepoints %q", got[2].Hex, got[2].CodePoints)
	}
}

// TestMeasureCasesNoResponse は、報告が届かない端末で、3 回続けて届かなければ止めることを確かめる。
func TestMeasureCasesNoResponse(t *testing.T) {
	t.Parallel()
	f := newFake(func(string) (string, bool) { return "", false })
	got, err := measureCases(f, &cprReader{box: f.box()}, widthCases[:5], 10*time.Millisecond)
	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}
	for _, g := range got {
		if g.Advance != nil || g.Error == "" {
			t.Errorf("result %+v: want no advance and an error", g)
		}
	}
}

// TestCPRReader は、報告が読み取りの途中で分かれて届く場合と、Windows のレコードで届く場合を確かめる。
func TestCPRReader(t *testing.T) {
	t.Parallel()
	ch := make(chan term.Input, 10)
	r := &cprReader{box: &inbox{ctx: context.Background(), in: ch}}
	ch <- term.Input{Bytes: []byte("x\x1b[1")}
	ch <- term.Input{Bytes: []byte("2;3")}
	ch <- term.Input{Bytes: []byte("4Ry")}
	row, col, before, report, err := r.read(time.Second)
	if err != nil || row != 12 || col != 34 || string(before) != "x" || string(report) != "\x1b[12;34R" {
		t.Errorf("read = %d, %d, %q, %q, %v", row, col, before, report, err)
	}
	// Windows: キーを押したレコードの文字だけを使う。繰り返しの回数も数える。
	var recs []term.Record
	for _, c := range "\x1b[5;6R" {
		recs = append(recs, term.Record{Kind: term.KeyRecord, KeyDown: true, RepeatCount: 1, Char: uint16(c)},
			term.Record{Kind: term.KeyRecord, KeyDown: false, RepeatCount: 1, Char: uint16(c)})
	}
	ch <- term.Input{Records: recs}
	row, col, before, _, err = r.read(time.Second)
	if err != nil || row != 5 || col != 6 || string(before) != "y" {
		t.Errorf("read (records) = %d, %d, %q, %v", row, col, before, err)
	}
	if _, _, _, _, err := r.read(10 * time.Millisecond); !errors.Is(err, errTimeout) {
		t.Errorf("read with nothing pending: err = %v, want errTimeout", err)
	}
}

func TestInputBytes(t *testing.T) {
	t.Parallel()
	key := func(down bool, rep uint16, c uint16) term.Record {
		return term.Record{Kind: term.KeyRecord, KeyDown: down, RepeatCount: rep, Char: c}
	}
	recs := []term.Record{
		key(true, 1, 0xd83c), key(true, 1, 0xdf63), // 🍣 を 2 つのレコードで
		key(false, 1, 'x'), // キーを離したレコードは使わない
		key(true, 3, 'a'),  // 繰り返し
		key(true, 1, 0),    // 文字のないキー（Shift など）
		{Kind: term.WindowSizeRecord, Width: 80, Height: 24},
		key(true, 1, 0xd800), // 対にならないサロゲート
	}
	if got, want := string(inputBytes(term.Input{Records: recs})), "🍣aaa�"; got != want {
		t.Errorf("inputBytes = %q, want %q", got, want)
	}
	if got := inputBytes(term.Input{Bytes: []byte("\x1b[A")}); string(got) != "\x1b[A" {
		t.Errorf("inputBytes(bytes) = %q", got)
	}
}

var testTiming = keyTiming{quiet: 30 * time.Millisecond, longQuiet: 60 * time.Millisecond, noInput: 100 * time.Millisecond, drain: 10 * time.Millisecond}

// TestRunKeys は、記録・やり直し・届かない場合・中断の流れを確かめる。
func TestRunKeys(t *testing.T) {
	t.Parallel()
	f := newFake(nil)
	go func() {
		// 1 つ目（esc）: 1 回目は間違えてやり直し、2 回目で次へ。
		time.Sleep(20 * time.Millisecond)
		f.send([]byte("x"))
		time.Sleep(80 * time.Millisecond)
		f.send([]byte("r"))
		time.Sleep(40 * time.Millisecond)
		f.send([]byte("\x1b"))
		time.Sleep(80 * time.Millisecond)
		f.send([]byte("\r"))
		// 2 つ目（esc-x）: 2 回に分かれて届く。
		time.Sleep(40 * time.Millisecond)
		f.send([]byte("\x1b"))
		time.Sleep(10 * time.Millisecond)
		f.send([]byte("x"))
		time.Sleep(80 * time.Millisecond)
		f.send([]byte("\r"))
		// 3 つ目（shift-a）: 何も届かないまま次へ。
		time.Sleep(200 * time.Millisecond)
		f.send([]byte("\r"))
		// 4 つ目: 中断。
		time.Sleep(200 * time.Millisecond)
		f.send([]byte("q"))
	}()
	res, err := runKeys(f, f.box(), sectionHeader{}, testTiming)
	if err != nil {
		t.Fatal(err)
	}
	if res.Completed {
		t.Error("Completed = true after q")
	}
	if len(res.Steps) != 3 {
		t.Fatalf("got %d steps, want 3: %+v", len(res.Steps), res.Steps)
	}
	s0, s1, s2 := res.Steps[0], res.Steps[1], res.Steps[2]
	if s0.ID != "esc" || s0.Attempts != 2 || s0.Hex != "1b" || s0.Result != "received" || !s0.BracketedPaste {
		t.Errorf("step 0 = %+v", s0)
	}
	if s1.ID != "esc-x" || s1.Hex != "1b78" || len(s1.Reads) != 2 || s1.Reads[0].TMs != 0 || s1.Reads[1].TMs <= 0 {
		t.Errorf("step 1 = %+v", s1)
	}
	if s2.ID != "shift-a" || s2.Result != "no_input" || len(s2.Reads) != 0 {
		t.Errorf("step 2 = %+v", s2)
	}
}

// TestKeyStepsPasteMode は、bracketed paste を無効にするのが paste-plain の手順だけであることを確かめる。
func TestKeyStepsPasteMode(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, st := range keySteps {
		if seen[st.id] {
			t.Errorf("duplicate id %q", st.id)
		}
		seen[st.id] = true
		if st.plainPaste != (st.id == "paste-plain") {
			t.Errorf("%s: plainPaste = %v", st.id, st.plainPaste)
		}
		for _, r := range st.label + st.hint {
			// 案内の文に、幅が曖昧な文字を使わない（filer §9.1）。代表的なものだけを調べる。
			if strings.ContainsRune("→←↑↓…×○①─│", r) {
				t.Errorf("%s: label or hint contains an ambiguous-width character %q", st.id, r)
			}
		}
	}
}

func TestSaveSection(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "r.json")
	if err := saveSection(path, fileHeader{Terminal: "T", Font: "F", Date: "2026-09-26"}, "width", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	// 2 回目: ほかの節と、空で渡した項目を残す。
	if err := saveSection(path, fileHeader{Terminal: "T", TerminalVersion: "1.0", Date: "2026-09-27"}, "keys", []string{"<x>"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["font"] != "F" || m["terminal_version"] != "1.0" || m["date"] != "2026-09-27" || m["width"] == nil || m["keys"] == nil {
		t.Errorf("merged file = %v", m)
	}
	if !bytes.Contains(data, []byte(`"<x>"`)) {
		t.Errorf("HTML-escaped output: %s", data)
	}
}

func TestSlugAndDetect(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"Terminal.app": "terminal-app", "iTerm2": "iterm2", "Windows Terminal": "windows-terminal", "conhost": "conhost", "..": "terminal"} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := detectTerminal(env(map[string]string{"TERM_PROGRAM": "Apple_Terminal"})); got != "Terminal.app" {
		t.Errorf("detect Apple_Terminal = %q", got)
	}
	if got := detectTerminal(env(map[string]string{"TERM_PROGRAM": "iTerm.app"})); got != "iTerm2" {
		t.Errorf("detect iTerm.app = %q", got)
	}
	if got := detectTerminal(env(map[string]string{"WT_SESSION": "x"})); got != "Windows Terminal" {
		t.Errorf("detect WT_SESSION = %q", got)
	}
}

func TestVisible(t *testing.T) {
	t.Parallel()
	in := "a\x1b[A\x7f\xffあ\u202e\u2028"
	want := `a^[[A^?\xffあ\u{202e}\u{2028}`
	if got := visible([]byte(in)); got != want {
		t.Errorf("visible(%q) = %q, want %q", in, got, want)
	}
	if got := codePoints("か\u3099\xff"); got != "U+304B U+3099 0xFF" {
		t.Errorf("codePoints = %q", got)
	}
	if got := spaced("1b5b41"); got != "1b 5b 41" {
		t.Errorf("spaced = %q", got)
	}
}

func TestSectionNames(t *testing.T) {
	t.Parallel()
	o := options{output: term.OutputWriteConsoleW, vtInput: true}
	if got := widthSectionName(o); got != "width" {
		t.Errorf("widthSectionName(default) = %q", got)
	}
	o.vtInput = false
	if got := keysSectionName(o); got != "keys" {
		t.Errorf("keysSectionName(default) = %q", got)
	}
	if !slices.Contains([]string{"width", "width_utf8cp_novtinput"}, widthSectionName(options{output: term.OutputUTF8CodePage})) {
		t.Errorf("widthSectionName(utf8cp, no vt input) = %q", widthSectionName(options{output: term.OutputUTF8CodePage}))
	}
}

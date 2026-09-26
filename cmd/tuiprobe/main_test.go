package main

import (
	"bytes"
	"context"
	"encoding/hex"
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
// onWrite があれば、書かれるたびに（書いた goroutine で）呼ぶ。画面に応じた入力を、時間に頼らずに届けるのに使う。
type fakeConsole struct {
	buf     bytes.Buffer
	written bytes.Buffer
	in      chan term.Input
	respond func(written string) (reply string, ok bool)
	onWrite func(written string)
	paste   []bool
}

func newFake(respond func(string) (string, bool)) *fakeConsole {
	return &fakeConsole{in: make(chan term.Input, 1000), respond: respond}
}

func (f *fakeConsole) Write(p []byte) (int, error) {
	f.buf.Write(p)
	f.written.Write(p)
	if f.onWrite != nil {
		f.onWrite(string(p))
	}
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
	// Windows: term がレコードから作ったバイト列を使う（レコードそのものは読まない）。
	var recs []term.Record
	for _, c := range "\x1b[5;6R" {
		recs = append(recs, term.Record{Kind: term.KeyRecord, KeyDown: true, RepeatCount: 1, Char: uint16(c)},
			term.Record{Kind: term.KeyRecord, KeyDown: false, RepeatCount: 1, Char: uint16(c)})
	}
	ch <- term.Input{Records: recs, Bytes: []byte("\x1b[5;6R")}
	row, col, before, _, err = r.read(time.Second)
	if err != nil || row != 5 || col != 6 || string(before) != "y" {
		t.Errorf("read (records) = %d, %d, %q, %v", row, col, before, err)
	}
	if _, _, _, _, err := r.read(10 * time.Millisecond); !errors.Is(err, errTimeout) {
		t.Errorf("read with nothing pending: err = %v, want errTimeout", err)
	}
}

var testTiming = keyTiming{quiet: 30 * time.Millisecond, textQuiet: 60 * time.Millisecond, noInput: 100 * time.Millisecond, textNoInput: 150 * time.Millisecond, drain: 10 * time.Millisecond}

// TestRunKeys は、記録・やり直し・届かない場合・中断の流れを確かめる。
// 入力は、手順の画面と結果の画面が書かれたときに（runKeys の goroutine で）入れる。
// スリープで間を空けると、遅いランナーで手順の区切りがずれる（macOS の CI の -race で起きた）。
func TestRunKeys(t *testing.T) {
	t.Parallel()
	f := newFake(nil)
	type input struct {
		b     string
		after time.Duration // 前の入力からの時間（Input.Time に書く）
	}
	script := []struct {
		screen string // 書かれるはずの画面に含まれる文字列
		inputs []input
	}{
		// 1 つ目（esc）: 1 回目は間違えてやり直し、2 回目で次へ。
		{"keys  1/", []input{{"x", 0}}},
		{"届いたもの", []input{{"r", 0}}},
		{"（2 回目）", []input{{"\x1b", 0}}},
		{"届いたもの", []input{{"\r", 0}}},
		// 2 つ目（esc-x）: 2 回に分かれて届く。
		{"keys  2/", []input{{"\x1b", 0}, {"x", 10 * time.Millisecond}}},
		{"届いたもの", []input{{"\r", 0}}},
		// 3 つ目（shift-a）: 何も届かないまま次へ。
		{"keys  3/", nil},
		{"何も届きませんでした", []input{{"\r", 0}}},
		// 4 つ目: 中断。
		{"keys  4/", nil},
		{"何も届きませんでした", []input{{"q", 0}}},
	}
	f.onWrite = func(w string) {
		if len(script) == 0 {
			t.Errorf("unexpected screen %q", w)
			return
		}
		sc := script[0]
		script = script[1:]
		if !strings.Contains(w, sc.screen) {
			t.Errorf("screen %q does not contain %q", w, sc.screen)
		}
		at := time.Now()
		for _, in := range sc.inputs {
			at = at.Add(in.after)
			f.in <- term.Input{Time: at, Bytes: []byte(in.b)}
		}
	}
	res, err := runKeys(f, f.box(), sectionHeader{}, keySteps, testTiming)
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
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "r.json")
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
	// 一時ファイルに書いてから名前を変えるので、一時ファイルが残らない。
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 {
		t.Errorf("files in the folder: %v, %v; want only r.json", entries, err)
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
	if got := detectTerminal(env(map[string]string{})); got != "unknown" {
		t.Errorf("detect with no variables = %q", got)
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
	if got := widthSectionName(options{output: term.OutputUTF8CodePage}); got != "width_utf8cp_novtinput" {
		t.Errorf("widthSectionName(utf8cp, no vt input) = %q", got)
	}
	if got := keysSectionName(options{output: term.OutputWriteConsoleW, vtInput: true}); got != "keys_vtinput" {
		t.Errorf("keysSectionName(vt input) = %q", got)
	}
}

// TestRecordStepIgnoresNonKeyRecords は、キーの入力より前に届いた大きさの変更のレコードでは記録を始めず、
// キーが届かなければ「届かない」とすることを確かめる（Windows で起動直後に届く WINDOW_BUFFER_SIZE_EVENT）。
func TestRecordStepIgnoresNonKeyRecords(t *testing.T) {
	t.Parallel()
	ch := make(chan term.Input, 10)
	box := &inbox{ctx: context.Background(), in: ch}
	size := term.Input{Time: time.Now(), Records: []term.Record{{Kind: term.WindowSizeRecord, Width: 120, Height: 30}}}
	ch <- size
	rec, err := recordStep(box, keySteps[0], testTiming)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Result != "no_input" || len(rec.Reads) != 1 || rec.Reads[0].Records[0].Kind != "size" {
		t.Errorf("size record only: %+v", rec)
	}

	// キーが届くまで待つ時間は長くする（遅いランナーでスリープが延びても、キーの前に待ちが終わらないように）。
	waitLong := testTiming
	waitLong.noInput = time.Hour
	ch <- size
	go func() {
		time.Sleep(20 * time.Millisecond)
		ch <- term.Input{Time: time.Now(), Records: []term.Record{{Kind: term.KeyRecord, KeyDown: true, RepeatCount: 1, VirtualKey: 0x1b, Char: 0x1b}}, Bytes: []byte{0x1b}}
	}()
	rec, err = recordStep(box, keySteps[0], waitLong)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Result != "received" || len(rec.Reads) != 2 || rec.Reads[0].TMs >= 0 || rec.Reads[1].TMs != 0 || rec.Hex != "1b" || rec.WaitMs <= 0 {
		t.Errorf("size then key: %+v", rec)
	}
}

// TestRecordStepTextStartsOnText は、IME の手順が、IME の切り替えのキー（文字のないレコード）では記録を始めず、
// 確定した文字が届いてから始めることを確かめる。
func TestRecordStepTextStartsOnText(t *testing.T) {
	t.Parallel()
	ime := keySteps[slices.IndexFunc(keySteps, func(st keyStep) bool { return st.id == "ime" })]
	ch := make(chan term.Input, 10)
	box := &inbox{ctx: context.Background(), in: ch}
	key := func(down bool, vk, c uint16) term.Record {
		return term.Record{Kind: term.KeyRecord, KeyDown: down, RepeatCount: 1, VirtualKey: vk, Char: c}
	}
	// 文字が届くまで待つ時間は長くする（遅いランナーでスリープが延びても、文字の前に待ちが終わらないように）。
	// スリープは textQuiet より長ければよい（延びる向きには崩れない）。
	waitLong := testTiming
	waitLong.textNoInput = time.Hour
	go func() {
		ch <- term.Input{Time: time.Now(), Records: []term.Record{key(false, 0xf0, 0)}} // IME の切り替え
		time.Sleep(100 * time.Millisecond)                                              // textQuiet より長く変換している
		ch <- term.Input{Time: time.Now(), Records: []term.Record{key(true, 0xe7, 0x65e5), key(true, 0xe7, 0x672c), key(true, 0xe7, 0x8a9e)}, Bytes: []byte("日本語")}
	}()
	rec, err := recordStep(box, ime, waitLong)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Result != "received" || string(mustHex(t, rec.Hex)) != "日本語" || len(rec.Reads) != 2 || rec.Reads[0].TMs >= 0 {
		t.Errorf("ime step = %+v", rec)
	}
}

func mustHex(t *testing.T, h string) []byte {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSelectAndMergeSteps(t *testing.T) {
	t.Parallel()
	if got, err := selectSteps(""); err != nil || len(got) != len(keySteps) {
		t.Errorf("selectSteps(\"\") = %d steps, %v", len(got), err)
	}
	got, err := selectSteps("ime, esc")
	if err != nil || len(got) != 2 || got[0].id != "esc" || got[1].id != "ime" {
		t.Errorf("selectSteps(ime, esc) = %+v, %v", got, err)
	}
	if _, err := selectSteps("esc,bogus"); err == nil {
		t.Error("selectSteps(bogus): err = nil")
	}

	var old keysSection
	old.Cols = 120
	for _, st := range keySteps {
		old.Steps = append(old.Steps, stepResult{ID: st.id, Result: "received", Hex: "00"})
	}
	old.Steps = slices.DeleteFunc(old.Steps, func(s stepResult) bool { return s.ID == "f11" })
	redo := &keysSection{Steps: []stepResult{{ID: "ime", Result: "received", Hex: "e697a5"}, {ID: "f11", Result: "no_input"}}}
	m := mergeKeys(&old, redo)
	if m.Cols != 120 || !m.Completed || len(m.Steps) != len(keySteps) {
		t.Fatalf("merged: cols %d completed %v steps %d", m.Cols, m.Completed, len(m.Steps))
	}
	for i, st := range keySteps {
		s := m.Steps[i]
		want := "00"
		switch st.id {
		case "ime":
			want = "e697a5"
		case "f11":
			want = ""
		}
		if s.ID != st.id || s.Hex != want {
			t.Errorf("merged step %d = %+v, want id %s hex %q", i, s, st.id, want)
		}
	}
	if old.Completed || coversAllSteps(old.Steps) {
		t.Error("old section without f11 counted as complete")
	}
}

// TestRecordStepIgnoresKeyUp は、キーを離したレコードだけでは手順の記録を始めないことを確かめる
// （確かめの Enter を長く押したとき、離したレコードが drain の後に届く）。
func TestRecordStepIgnoresKeyUp(t *testing.T) {
	t.Parallel()
	ch := make(chan term.Input, 10)
	box := &inbox{ctx: context.Background(), in: ch}
	ch <- term.Input{Time: time.Now(), Records: []term.Record{{Kind: term.KeyRecord, KeyDown: false, RepeatCount: 1, VirtualKey: 0x0d, Char: '\r'}}}
	rec, err := recordStep(box, keySteps[0], testTiming)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Result != "no_input" {
		t.Errorf("key-up only: result %q, want no_input: %+v", rec.Result, rec)
	}
}

// TestRecordStepJoinsSplitSurrogates は、サロゲートの対が 2 回の読み取りに分かれて届いたとき
// （term は、上位サロゲートの読み取りのバイト列を空にし、後の読み取りに文字を含める）、1 つの文字として記録することを確かめる。
func TestRecordStepJoinsSplitSurrogates(t *testing.T) {
	t.Parallel()
	ch := make(chan term.Input, 10)
	box := &inbox{ctx: context.Background(), in: ch}
	key := func(c uint16) []term.Record {
		return []term.Record{{Kind: term.KeyRecord, KeyDown: true, RepeatCount: 1, Char: c}}
	}
	ime := keySteps[slices.IndexFunc(keySteps, func(st keyStep) bool { return st.id == "ime" })]
	ch <- term.Input{Time: time.Now(), Records: key(0xd83c)}
	ch <- term.Input{Time: time.Now(), Records: key(0xdf63), Bytes: []byte("🍣")}
	rec, err := recordStep(box, ime, testTiming)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(mustHex(t, rec.Hex)); got != "🍣" || len(rec.Reads) != 2 {
		t.Errorf("hex = %q, reads %d, want %q and 2 reads", got, len(rec.Reads), "🍣")
	}
}

// TestShowDriftWaitsForKey は、ずれの画面の撮影の待ちが、キー以外のレコード（フォーカス・大きさの変更・キーを離したもの）では終わらず、
// キーで終わることを確かめる。
func TestShowDriftWaitsForKey(t *testing.T) {
	t.Parallel()
	f := newFake(nil)
	r := &cprReader{box: f.box()}
	f.in <- term.Input{Time: time.Now(), Records: []term.Record{{Kind: term.FocusRecord, Focus: true}, {Kind: term.WindowSizeRecord, Width: 80, Height: 24}}}
	f.in <- term.Input{Time: time.Now(), Records: []term.Record{{Kind: term.KeyRecord, KeyDown: false, RepeatCount: 1, VirtualKey: 0x0d}}}
	const hold = 100 * time.Millisecond
	start := time.Now()
	if err := showDrift(f, r, hold); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < hold {
		t.Errorf("showDrift returned after %v on non-key input, want at least %v", d, hold)
	}
	f.send([]byte("x"))
	start = time.Now()
	if err := showDrift(f, r, time.Hour); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Minute {
		t.Errorf("showDrift did not end on a key: %v", d)
	}
}

// TestAppendRun は、確認用の画面の記録を、前の記録を残して配列の後ろに加えることを確かめる（1 つの記録だった節は、配列の最初の要素にする）。
func TestAppendRun(t *testing.T) {
	t.Parallel()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "r.json")
	h := fileHeader{Terminal: "t", Date: "2026-09-26"}
	if err := saveSection(path, h, "screen", map[string]string{"exit": "first"}); err != nil {
		t.Fatal(err)
	}
	for _, exit := range []string{"second", "third"} {
		runs, err := appendRun(path, "screen", map[string]string{"exit": exit})
		if err != nil {
			t.Fatal(err)
		}
		if err := saveSection(path, h, "screen", runs); err != nil {
			t.Fatal(err)
		}
	}
	var got []map[string]string
	if ok, err := loadSection(path, "screen", &got); !ok || err != nil {
		t.Fatalf("loadSection: %v %v", ok, err)
	}
	if len(got) != 3 || got[0]["exit"] != "first" || got[2]["exit"] != "third" {
		t.Errorf("runs = %v", got)
	}
	// 節がなければ、1 つだけの配列にする。
	runs, err := appendRun(filepath.Join(dir, "none.json"), "screen", map[string]string{"exit": "only"})
	if err != nil || len(runs) != 1 {
		t.Errorf("appendRun on a new file = %v, %v", runs, err)
	}
}

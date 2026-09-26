package keys

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

// far は、どの待ち時間よりも後の時刻。
var far = t0.Add(time.Hour)

// decodeAll は、s を t0 に一度に渡し、far で確定したイベントを返す。
func decodeAll(s string, expectCPR bool) []Event {
	var d Decoder
	d.SetCursorPositionExpected(expectCPR)
	ev := d.Feed([]byte(s), t0)
	return append(ev, d.Tick(far)...)
}

func strs(ev []Event) []string {
	var out []string
	for _, e := range ev {
		out = append(out, e.String())
	}
	return out
}

func raws(ev []Event) string {
	var b strings.Builder
	for _, e := range ev {
		b.WriteString(e.Raw)
	}
	return b.String()
}

// TestDecode は、バイト列の解釈を表で確かめる（tui §5）。どの行も、イベントの元の入力をつなぐと入力と一致する（T3）。
func TestDecode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		cpr  bool
		want string // イベントの String() を " | " でつないだもの
	}{
		{"runes", "aB あ", false, `'a' | 'B' | ' ' | 'あ'`},
		{"ctrl letters", "\x01\x03\x1a", false, `Ctrl+'a' | Ctrl+'c' | Ctrl+'z'`},
		{"ctrl space", "\x00", false, `Ctrl+' '`},
		{"ctrl h is not backspace", "\x08\x7f", false, `Ctrl+'h' | Backspace`},
		{"tab enter esc", "\x09\x0d", false, `Tab | Enter`},
		{"line feed", "\x0a", false, `Ctrl+'j'`},
		{"ctrl symbols", "\x1c\x1d\x1e\x1f", false, `Ctrl+'\\' | Ctrl+']' | Ctrl+'^' | Ctrl+'_'`},
		{"esc alone", "\x1b", false, `Esc`},
		{"alt letter", "\x1ba\x1bA", false, `Alt+'a' | Alt+'A'`},
		{"alt multibyte", "\x1bあ", false, `Alt+'あ'`},
		{"alt controls", "\x1b\x7f\x1b\x0d\x1b\x01", false, `Alt+Backspace | Alt+Enter | Ctrl+Alt+'a'`},
		{"alt esc", "\x1b\x1b", false, `Alt+Esc`},
		{"alt arrow", "\x1b\x1b[A", false, `Alt+Up`},
		{"esc before an unknown sequence", "\x1b\x1b[?1;2c", false, `Esc | Unknown("\x1b[?1;2c")`},
		{"esc before a paste", "\x1b\x1b[200~x\x1b[201~", false, `Esc | Paste("x")`},
		{"alt O before a control byte", "\x1bO\x01", false, `Alt+'O' | Ctrl+'a'`},
		{"empty first parameter", "\x1b[;5A", false, `Ctrl+Up`},
		{"arrows", "\x1b[A\x1b[B\x1b[C\x1b[D", false, `Up | Down | Right | Left`},
		{"home end", "\x1b[H\x1b[F", false, `Home | End`},
		{"application mode", "\x1bOA\x1bOD\x1bOH\x1bOF", false, `Up | Left | Home | End`},
		{"f1-f4", "\x1bOP\x1bOQ\x1bOR\x1bOS", false, `F1 | F2 | F3 | F4`},
		{"modified arrows", "\x1b[1;2A\x1b[1;3D\x1b[1;5A\x1b[1;6C", false, `Shift+Up | Alt+Left | Ctrl+Up | Ctrl+Shift+Right`},
		{"meta", "\x1b[1;9A", false, `Meta+Up`},
		{"tilde keys", "\x1b[1~\x1b[2~\x1b[3~\x1b[4~\x1b[5~\x1b[6~\x1b[7~\x1b[8~", false, `Home | Insert | Delete | End | PageUp | PageDown | Home | End`},
		{"modified delete", "\x1b[3;5~", false, `Ctrl+Delete`},
		{"f5-f12", "\x1b[15~\x1b[17~\x1b[18~\x1b[19~\x1b[20~\x1b[21~\x1b[23~\x1b[24~", false, `F5 | F6 | F7 | F8 | F9 | F10 | F11 | F12`},
		{"f13-f20", "\x1b[25~\x1b[26~\x1b[28~\x1b[29~\x1b[31~\x1b[32~\x1b[33~\x1b[34~", false, `F13 | F14 | F15 | F16 | F17 | F18 | F19 | F20`},
		{"modified function keys", "\x1b[15;2~\x1b[1;2P\x1b[1;5S", false, `Shift+F5 | Shift+F1 | Ctrl+F4`},
		{"shift tab", "\x1b[Z", false, `Shift+Tab`},
		{"shift f3 when not waiting", "\x1b[1;2R", false, `Shift+F3`},
		{"cursor position when waiting", "\x1b[12;34R", true, `CPR(12,34)`},
		{"cursor position like shift f3", "\x1b[1;2R", true, `CPR(1,2)`},
		{"late cursor position report", "\x1b[12;34R", false, `Unknown("\x1b[12;34R")`},
		{"da1 reply", "\x1b[?1;2c", false, `Unknown("\x1b[?1;2c")`},
		{"decrqm reply", "\x1b[?2026;2$y", false, `Unknown("\x1b[?2026;2$y")`},
		{"unknown tilde", "\x1b[99~", false, `Unknown("\x1b[99~")`},
		{"sub-parameters", "\x1b[1:2A", false, `Unknown("\x1b[1:2A")`},
		{"kitty key", "\x1b[97;5u", false, `Unknown("\x1b[97;5u")`},
		{"unknown ss3", "\x1bOx", false, `Unknown("\x1bOx")`},
		{"bad modifier", "\x1b[1;0A\x1b[2;5A", false, `Unknown("\x1b[1;0A") | Unknown("\x1b[2;5A")`},
		{"huge parameter", "\x1b[99999999999999999999~", false, `Unknown("\x1b[99999999999999999999~")`},
		{"alt bracket on timeout", "\x1b[", false, `Alt+'['`},
		{"alt O on timeout", "\x1bO", false, `Alt+'O'`},
		{"incomplete csi on timeout", "\x1b[1;", false, `Unknown("\x1b[1;")`},
		{"csi broken by esc", "\x1b[1\x1b[A", false, `Unknown("\x1b[1") | Up`},
		{"csi broken by control", "\x1b[1\x0d", false, `Unknown("\x1b[1") | Enter`},
		{"invalid utf-8", "a\xffb", false, `'a' | Unknown("\xff") | 'b'`},
		{"truncated utf-8", "\xe3\x81", false, `Unknown("\xe3") | Unknown("\x81")`},
		{"esc then invalid byte", "\x1b\xff", false, `Esc | Unknown("\xff")`},
		{"esc then truncated utf-8", "\x1b\xe3\x81", false, `Esc | Unknown("\xe3") | Unknown("\x81")`},
		{"paste", "\x1b[200~hello\x1b[201~", false, `Paste("hello")`},
		{"paste keeps escapes and newlines", "\x1b[200~a\x1b[Ab\rc\x1b[201~", false, `Paste("a\x1b[Ab\rc")`},
		{"paste then key", "\x1b[200~x\x1b[201~y", false, `Paste("x") | 'y'`},
		{"empty paste", "\x1b[200~\x1b[201~", false, `Paste("")`},
		{"paste with invalid utf-8", "\x1b[200~\xff\x1b[201~", false, `Paste("\xff")`},
		{"stray paste end", "\x1b[201~", false, `Unknown("\x1b[201~")`},
		{"unterminated paste", "\x1b[200~ab", false, `Paste("ab")`},
	}
	for _, tt := range tests {
		ev := decodeAll(tt.in, tt.cpr)
		if got := strings.Join(strs(ev), " | "); got != tt.want {
			t.Errorf("%s: decode(%q) = %s, want %s", tt.name, tt.in, got, tt.want)
		}
		if raws(ev) != tt.in {
			t.Errorf("%s: raw of events %q, want %q (T3)", tt.name, raws(ev), tt.in)
		}
	}
}

// TestEscWait は、Esc の待ち時間を、時刻を渡して確かめる（スリープしない）。
func TestEscWait(t *testing.T) {
	t.Parallel()
	var d Decoder
	if ev := d.Feed([]byte("\x1b"), t0); len(ev) != 0 {
		t.Fatalf("Feed(ESC) = %v, want nothing yet", strs(ev))
	}
	if dl, ok := d.Deadline(); !ok || !dl.Equal(t0.Add(DefaultEscWait)) {
		t.Errorf("Deadline = %v, %v, want %v", dl, ok, t0.Add(DefaultEscWait))
	}
	if ev := d.Tick(t0.Add(DefaultEscWait - time.Millisecond)); len(ev) != 0 {
		t.Errorf("Tick before the wait = %v", strs(ev))
	}
	if ev := d.Tick(t0.Add(DefaultEscWait)); strings.Join(strs(ev), " | ") != "Esc" {
		t.Errorf("Tick at the wait = %v, want Esc", strs(ev))
	}
	if _, ok := d.Deadline(); ok {
		t.Error("Deadline after Esc: ok = true")
	}

	// 待ち時間の中に続きが届けば Alt。
	d = Decoder{}
	d.Feed([]byte("\x1b"), t0)
	if ev := d.Feed([]byte("x"), t0.Add(10*time.Millisecond)); strings.Join(strs(ev), " | ") != "Alt+'x'" {
		t.Errorf("ESC then x within the wait = %v, want Alt+'x'", strs(ev))
	}
	// 待ち時間の後に届けば、Tick を呼んでいなくても、Esc と x に分ける（人が続けて押した場合）。
	d = Decoder{}
	d.Feed([]byte("\x1b"), t0)
	if ev := d.Feed([]byte("x"), t0.Add(282*time.Millisecond)); strings.Join(strs(ev), " | ") != "Esc | 'x'" {
		t.Errorf("ESC then x after the wait = %v, want Esc | 'x'", strs(ev))
	}
	// シーケンスが読み取りの途中で分かれても、待ち時間の中なら 1 つのキー。
	d = Decoder{}
	d.Feed([]byte("\x1b["), t0)
	if ev := d.Feed([]byte("A"), t0.Add(40*time.Millisecond)); strings.Join(strs(ev), " | ") != "Up" {
		t.Errorf("split CSI = %v, want Up", strs(ev))
	}
	// 待ち時間は変えられる。待ち時間は最後の入力から数える。
	d = Decoder{EscWait: 10 * time.Millisecond}
	d.Feed([]byte("\x1b["), t0)
	d.Feed([]byte("1"), t0.Add(8*time.Millisecond))
	if dl, _ := d.Deadline(); !dl.Equal(t0.Add(18 * time.Millisecond)) {
		t.Errorf("Deadline with EscWait 10ms after input at 8ms = %v", dl.Sub(t0))
	}
	if ev := d.Tick(t0.Add(18 * time.Millisecond)); strings.Join(strs(ev), " | ") != `Unknown("\x1b[1")` {
		t.Errorf("Tick = %v", strs(ev))
	}
}

// TestPasteWait は、終わりの印が届かない貼り付けの扱い（tui §5）を確かめる。
func TestPasteWait(t *testing.T) {
	t.Parallel()
	sec := func(s float64) time.Time { return t0.Add(time.Duration(s * float64(time.Second))) }

	// 1 秒より短い間隔で分かれて届いた貼り付けは、1 つのイベント。
	var d Decoder
	d.Feed([]byte("\x1b[200~ab"), t0)
	if dl, _ := d.Deadline(); !dl.Equal(t0.Add(DefaultPasteWait)) {
		t.Errorf("Deadline in a paste = %v, want %v", dl.Sub(t0), DefaultPasteWait)
	}
	if ev := d.Feed([]byte("cd\x1b[201~"), sec(0.5)); strings.Join(strs(ev), " | ") != `Paste("abcd")` {
		t.Errorf("split paste = %v", strs(ev))
	}

	// 1 秒たつと、そこまでを貼り付けにする。その後も終わりの印までは貼り付け（キーとして解釈しない）。
	d = Decoder{}
	d.Feed([]byte("\x1b[200~ab"), t0)
	if ev := d.Tick(sec(1)); strings.Join(strs(ev), " | ") != `Paste("ab")` {
		t.Errorf("Tick after 1s = %v, want Paste(\"ab\")", strs(ev))
	}
	if ev := d.Feed([]byte("q"), sec(1.5)); len(ev) != 0 {
		t.Errorf("input after a partial paste = %v, want nothing yet (still pasting)", strs(ev))
	}
	all := d.Feed([]byte("\x1b[201~y"), sec(1.6))
	if got := strings.Join(strs(all), " | "); got != `Paste("q") | 'y'` {
		t.Errorf("rest of the paste = %v, want Paste(\"q\") | 'y'", got)
	}

	// 貼り付けにした後、さらに 1 秒なにも届かなければ、貼り付けを終える。
	d = Decoder{}
	d.Feed([]byte("\x1b[200~ab"), t0)
	d.Tick(sec(1))
	if ev := d.Tick(sec(2)); len(ev) != 0 {
		t.Errorf("Tick after another quiet second = %v", strs(ev))
	}
	if _, ok := d.Deadline(); ok {
		t.Error("Deadline after the paste ended: ok = true")
	}
	if ev := d.Feed([]byte("x"), sec(2.1)); strings.Join(strs(ev), " | ") != "'x'" {
		t.Errorf("input after the paste ended = %v, want 'x'", strs(ev))
	}

	// Tick を呼ばずに、1 秒を超えてから次の入力が届いた場合も同じ（時刻で決まる）。
	d = Decoder{}
	d.Feed([]byte("\x1b[200~ab"), t0)
	if ev := d.Feed([]byte("x"), sec(2.5)); strings.Join(strs(ev), " | ") != `Paste("ab") | 'x'` {
		t.Errorf("input 2.5s after an unterminated paste = %v, want Paste(\"ab\") | 'x'", strs(ev))
	}
	d = Decoder{}
	d.Feed([]byte("\x1b[200~ab"), t0)
	ev := d.Feed([]byte("x"), sec(1.5))
	ev = append(ev, d.Feed([]byte("\x1b[201~"), sec(1.6))...)
	if got := strings.Join(strs(ev), " | "); got != `Paste("ab") | Paste("x")` {
		t.Errorf("input 1.5s after an unterminated paste = %v, want Paste(\"ab\") | Paste(\"x\")", got)
	}

	// 待ち時間は変えられる。
	d = Decoder{PasteWait: 100 * time.Millisecond}
	d.Feed([]byte("\x1b[200~ab"), t0)
	if ev := d.Tick(t0.Add(100 * time.Millisecond)); strings.Join(strs(ev), " | ") != `Paste("ab")` {
		t.Errorf("Tick with PasteWait 100ms = %v", strs(ev))
	}

	// 開始の印だけが届いて終わった場合も、印をイベントに含める（T3）。
	d = Decoder{}
	d.Feed([]byte("\x1b[200~"), t0)
	ev = d.Tick(far)
	if got := strings.Join(strs(ev), " | "); got != `Paste("")` || raws(ev) != "\x1b[200~" {
		t.Errorf("marker only = %v (raw %q)", got, raws(ev))
	}
}

// TestCursorPositionExpected は、問い合わせを待っている間だけ、カーソル位置の報告として解釈することを確かめる。
func TestCursorPositionExpected(t *testing.T) {
	t.Parallel()
	var d Decoder
	d.SetCursorPositionExpected(true)
	ev := d.Feed([]byte("\x1b[3;7R"), t0)
	if len(ev) != 1 || ev[0].Kind != CursorPositionEvent || ev[0].Row != 3 || ev[0].Col != 7 {
		t.Errorf("CPR while waiting = %+v", ev)
	}
	d.SetCursorPositionExpected(false)
	if ev := d.Feed([]byte("\x1b[1;2R"), t0); len(ev) != 1 || ev[0].Kind != KeyEvent || ev[0].Key != KeyF3 || ev[0].Mod != ModShift {
		t.Errorf("after waiting ended = %+v", ev)
	}
}

func TestStringers(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		got, want string
	}{
		{Key(200).String(), "Key(200)"},
		{Kind(9).String(), "Kind(9)"},
		{KeyEnter.String(), "Enter"},
		{PasteEvent.String(), "Paste"},
		{CursorPositionEvent.String(), "CursorPosition"},
		{UnknownEvent.String(), "Unknown"},
		{KeyEvent.String(), "Key"},
		{Event{Kind: Kind(9), Raw: "x"}.String(), `Kind(9)("x")`},
		{(ModCtrl | ModAlt | ModShift | ModMeta).String(), "Ctrl+Alt+Shift+Meta+"},
	} {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
}

package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zredjet/tana/internal/term"
)

// keyStep は、キーの記録の 1 つの手順（filer §12.1）。
// 案内の文には、幅が曖昧な文字（矢印など）を使わない（filer §9.1）。
type keyStep struct {
	id, label, hint string
	// text は IME・貼り付けの手順。文字が届くまで記録を始めず（IME の切り替えのキーで始めない）、
	// 最初の文字を待つ時間と、最後の入力から記録を終えるまでの時間を長くする。
	text       bool
	plainPaste bool // bracketed paste を無効にして記録する（VT6）
}

// pasteText は、貼り付けの手順で貼り付けてもらう文字列（改行・タブ・日本語・サロゲートの対になる絵文字を含む）。
const pasteText = "tana 貼り付け\n2行目\tタブ🍣"

var keySteps = []keyStep{
	{id: "esc", label: "Esc を 1 回押す"},
	{id: "esc-x", label: "Esc を押して、すぐに x を押す（できるだけ速く続けて）", hint: "Esc の待ち時間を決めるため、人が続けて押したときの間隔を測ります"},
	{id: "shift-a", label: "Shift＋A"},
	{id: "ctrl-a", label: "Ctrl＋A"},
	{id: "ctrl-c", label: "Ctrl＋C", hint: "このプローブは Ctrl＋C では止まりません"},
	{id: "ctrl-z", label: "Ctrl＋Z"},
	{id: "alt-a", label: "Alt＋A", hint: "Mac では option＋A"},
	{id: "backspace", label: "Backspace", hint: "Mac では delete"},
	{id: "delete", label: "Delete（前方削除）", hint: "Mac では fn＋delete"},
	{id: "up", label: "上矢印"},
	{id: "down", label: "下矢印"},
	{id: "left", label: "左矢印"},
	{id: "right", label: "右矢印"},
	{id: "pgup", label: "PageUp", hint: "Mac では fn＋上矢印。端末に取られて届かないときは、何もせずに待つ"},
	{id: "pgdn", label: "PageDown", hint: "Mac では fn＋下矢印。届かないときは、何もせずに待つ"},
	{id: "home", label: "Home", hint: "Mac では fn＋左矢印。届かないときは、何もせずに待つ"},
	{id: "end", label: "End", hint: "Mac では fn＋右矢印。届かないときは、何もせずに待つ"},
	{id: "tab", label: "Tab"},
	{id: "shift-tab", label: "Shift＋Tab"},
	{id: "enter", label: "Enter", hint: "Mac では return"},
	{id: "f1", label: "F1", hint: fkeyHint},
	{id: "f2", label: "F2", hint: fkeyHint},
	{id: "f3", label: "F3", hint: fkeyHint},
	{id: "f4", label: "F4", hint: fkeyHint},
	{id: "f5", label: "F5", hint: fkeyHint},
	{id: "f6", label: "F6", hint: fkeyHint},
	{id: "f7", label: "F7", hint: fkeyHint},
	{id: "f8", label: "F8", hint: fkeyHint},
	{id: "f9", label: "F9", hint: fkeyHint},
	{id: "f10", label: "F10", hint: fkeyHint},
	{id: "f11", label: "F11", hint: fkeyHint},
	{id: "f12", label: "F12", hint: fkeyHint},
	{id: "ctrl-h", label: "Ctrl＋H"},
	{id: "ctrl-i", label: "Ctrl＋I"},
	{id: "ctrl-m", label: "Ctrl＋M"},
	{id: "ctrl-bracket", label: "Ctrl＋[（左角かっこ）"},
	{id: "ime", label: "日本語入力に切り替えて「にほんご」と打ち、「日本語」に変換して確定する", hint: "確定したら何も押さずに待つ。記録が終わったら英数の入力に戻す", text: true},
	{id: "paste-bracketed", label: "クリップボードの文字列を貼り付ける", hint: pasteHint, text: true},
	{id: "paste-plain", label: "もう一度、同じ文字列を貼り付ける（bracketed paste を無効にした状態）", hint: pasteHint, text: true, plainPaste: true},
}

const (
	fkeyHint  = "Mac では fn＋F キー。OS や端末に取られて届かないときは、何もせずに待つ"
	pasteHint = "Mac は command＋V、Windows Terminal と conhost は Ctrl＋V。何も押さずに待つ"
)

// keyTiming は、記録の区切りの時間。
type keyTiming struct {
	quiet       time.Duration // 最後の入力からこの時間だけ何も届かなければ、記録を終える
	textQuiet   time.Duration // 同じ（IME・貼り付け）
	noInput     time.Duration // 案内を出してからこの時間だけ何も届かなければ、「届かない」とする
	textNoInput time.Duration // 同じ（IME・貼り付け）
	drain       time.Duration // 確かめのキーの後に捨てる時間（Windows のキーを離したレコード）
}

var defaultKeyTiming = keyTiming{quiet: time.Second, textQuiet: 3 * time.Second, noInput: 15 * time.Second, textNoInput: 60 * time.Second, drain: 300 * time.Millisecond}

// recordJSON は、Windows の入力のレコード 1 つの記録。
type recordJSON struct {
	Kind   string `json:"kind"` // key・size・focus・menu・mouse
	Down   *bool  `json:"down,omitempty"`
	Repeat uint16 `json:"repeat,omitempty"`
	VK     uint16 `json:"vk,omitempty"`
	Scan   uint16 `json:"scan,omitempty"`
	Char   uint16 `json:"char,omitempty"`
	Ctrl   uint32 `json:"ctrl,omitempty"`
	W      int    `json:"w,omitempty"`
	H      int    `json:"h,omitempty"`
	Focus  *bool  `json:"focus,omitempty"`
	Raw    string `json:"raw,omitempty"`
}

// readJSON は、1 回の読み取りの記録。
type readJSON struct {
	TMs     float64      `json:"t_ms"` // その手順の最初の入力からの時間
	Hex     string       `json:"hex,omitempty"`
	Records []recordJSON `json:"records,omitempty"`
}

type stepResult struct {
	ID             string     `json:"id"`
	Label          string     `json:"label"`
	Result         string     `json:"result"` // received・no_input
	Attempts       int        `json:"attempts"`
	BracketedPaste bool       `json:"bracketed_paste"`
	WaitMs         float64    `json:"wait_ms,omitempty"` // 案内を出してから最初の入力まで
	Hex            string     `json:"hex"`               // 入力をつないだもの（Windows は、キーを押したレコードの文字を UTF-8 にしたもの）
	Reads          []readJSON `json:"reads"`
	RecordedAt     string     `json:"recorded_at"`
}

type keysSection struct {
	sectionHeader
	PasteText   string       `json:"paste_text"`
	QuietMs     int64        `json:"quiet_ms"`
	TextQuietMs int64        `json:"text_quiet_ms"`
	Steps       []stepResult `json:"steps"`
	Completed   bool         `json:"completed"` // keySteps のすべての手順の記録がある
}

// runKeys は、steps を順に案内して、届いた入力を記録する。途中で終わっても、そこまでの結果を返す。
func runKeys(c console, box *inbox, sec sectionHeader, steps []keyStep, tm keyTiming) (*keysSection, error) {
	res := &keysSection{sectionHeader: sec, PasteText: pasteText, QuietMs: tm.quiet.Milliseconds(), TextQuietMs: tm.textQuiet.Milliseconds()}
	// 起動したときに届くもの（Windows の大きさの変更のレコードなど）を、最初の手順に混ぜない。
	if err := drain(box, tm.drain); err != nil {
		return res, err
	}
	attempts := 1
	for i := 0; i < len(steps); {
		st := steps[i]
		if err := c.SetBracketedPaste(!st.plainPaste); err != nil {
			return res, err
		}
		if _, err := c.Write([]byte(stepScreen(i, len(steps), st, attempts))); err != nil {
			return res, err
		}
		rec, err := recordStep(box, st, tm)
		if err != nil {
			return res, err
		}
		rec.BracketedPaste = !st.plainPaste
		if _, err := c.Write([]byte(summaryScreen(rec))); err != nil {
			return res, err
		}
		action, err := waitConfirm(box)
		if err != nil {
			return res, err
		}
		if err := drain(box, tm.drain); err != nil {
			return res, err
		}
		switch action {
		case 'n':
			rec.Attempts = attempts
			res.Steps = append(res.Steps, rec)
			attempts = 1
			i++
		case 'r':
			attempts++
		case 'q':
			return res, nil
		}
	}
	res.Completed = coversAllSteps(res.Steps)
	return res, nil
}

// selectSteps は、コンマで区切った ID の手順を keySteps の順に返す（空なら keySteps のすべて）。
func selectSteps(ids string) ([]keyStep, error) {
	if ids == "" {
		return keySteps, nil
	}
	want := map[string]bool{}
	for id := range strings.SplitSeq(ids, ",") {
		id = strings.TrimSpace(id)
		if !slices.ContainsFunc(keySteps, func(st keyStep) bool { return st.id == id }) {
			return nil, fmt.Errorf("unknown step %q", id)
		}
		want[id] = true
	}
	var out []keyStep
	for _, st := range keySteps {
		if want[st.id] {
			out = append(out, st)
		}
	}
	return out, nil
}

// mergeKeys は、以前の記録 old の手順を、撮り直した記録 redo の同じ ID の手順で置き換える（-steps）。
// 節の項目（端末の情報など）は old のものを残す。手順は keySteps の順に並べる。
func mergeKeys(old, redo *keysSection) *keysSection {
	merged := *old
	byID := map[string]stepResult{}
	for _, s := range old.Steps {
		byID[s.ID] = s
	}
	for _, s := range redo.Steps {
		byID[s.ID] = s
	}
	merged.Steps = nil
	for _, st := range keySteps {
		if s, ok := byID[st.id]; ok {
			merged.Steps = append(merged.Steps, s)
		}
	}
	merged.Completed = coversAllSteps(merged.Steps)
	return &merged
}

// coversAllSteps は、steps に keySteps のすべての手順があるかを返す。
func coversAllSteps(steps []stepResult) bool {
	for _, st := range keySteps {
		if !slices.ContainsFunc(steps, func(s stepResult) bool { return s.ID == st.id }) {
			return false
		}
	}
	return true
}

// recordStep は、キーの入力（Unix のバイト列、Windows のキーのレコード）を tm.noInput まで待ち、
// その後は tm.quiet の間なにも届かなくなるまで記録する。
// キーの入力より前に届いたほかのレコード（大きさの変更、フォーカス）も記録するが、それだけでは記録を始めない。
// t_ms は最初のキーの入力からの時間（キーの入力が届かなければ、案内を出した時からの時間）。
func recordStep(box *inbox, st keyStep, tm keyTiming) (stepResult, error) {
	start := time.Now()
	res := stepResult{ID: st.id, Label: st.label, Result: "no_input", Reads: []readJSON{}, RecordedAt: start.Format(time.RFC3339)}
	quiet, noInput, starts := tm.quiet, tm.noInput, hasKey
	if st.text {
		quiet, noInput, starts = tm.textQuiet, tm.textNoInput, hasText
	}
	var got []term.Input
	var first time.Time
	for {
		wait := quiet
		if first.IsZero() {
			if wait = time.Until(start.Add(noInput)); wait <= 0 {
				break
			}
		}
		x, err := box.next(wait)
		if errors.Is(err, errTimeout) {
			break
		}
		if err != nil {
			return res, err
		}
		got = append(got, x)
		if first.IsZero() && starts(x) {
			first = x.Time
			res.Result = "received"
			res.WaitMs = ms(first.Sub(start))
		}
	}
	base := first
	if base.IsZero() {
		base = start
	}
	var all []byte
	for _, x := range got {
		r := readJSON{TMs: ms(x.Time.Sub(base))}
		if x.Records == nil {
			r.Hex = hex.EncodeToString(x.Bytes)
		}
		for _, rec := range x.Records {
			r.Records = append(r.Records, toRecordJSON(rec))
		}
		res.Reads = append(res.Reads, r)
		all = append(all, x.Bytes...) // Windows のレコードも、term がバイト列にしている（T3）
	}
	res.Hex = hex.EncodeToString(all)
	return res, nil
}

// hasText は、x が文字（Unix のバイト列か、Windows のキーを押したレコードの文字。term がバイト列にしたもの）を含むかを返す。
func hasText(x term.Input) bool { return len(x.Bytes) > 0 }

// hasKey は、x がキーの入力（Unix のバイト列か、Windows のキーを押したレコード）を含むかを返す。
// キーを離したレコードは含めない（前の手順で押したキーを離したものが、後から届くことがある）。
func hasKey(x term.Input) bool {
	if len(x.Bytes) > 0 {
		return true
	}
	for _, r := range x.Records {
		if r.Kind == term.KeyRecord && r.KeyDown {
			return true
		}
	}
	return false
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

func toRecordJSON(r term.Record) recordJSON {
	switch r.Kind {
	case term.KeyRecord:
		down := r.KeyDown
		return recordJSON{Kind: "key", Down: &down, Repeat: r.RepeatCount, VK: r.VirtualKey, Scan: r.ScanCode, Char: r.Char, Ctrl: r.ControlKeys}
	case term.WindowSizeRecord:
		return recordJSON{Kind: "size", W: r.Width, H: r.Height}
	case term.FocusRecord:
		f := r.Focus
		return recordJSON{Kind: "focus", Focus: &f}
	case term.MenuRecord:
		return recordJSON{Kind: "menu", Raw: hex.EncodeToString(r.Raw[:])}
	case term.MouseRecord:
		return recordJSON{Kind: "mouse", Raw: hex.EncodeToString(r.Raw[:])}
	}
	return recordJSON{Kind: fmt.Sprintf("%#x", uint16(r.Kind)), Raw: hex.EncodeToString(r.Raw[:])}
}

// waitConfirm は、確かめのキーを待つ。Enter なら 'n'（次へ）、r なら 'r'（やり直す）、q なら 'q'（中断）を返す。
func waitConfirm(box *inbox) (byte, error) {
	for {
		x, err := box.next(24 * time.Hour)
		if errors.Is(err, errTimeout) {
			continue
		}
		if err != nil {
			return 0, err
		}
		keys := x.Bytes
		for _, r := range x.Records {
			if r.Kind != term.KeyRecord || !r.KeyDown {
				continue
			}
			if r.VirtualKey == 0x0d { // VK_RETURN
				keys = append(keys, '\r')
			} else if r.Char < utf8.RuneSelf {
				keys = append(keys, byte(r.Char))
			}
		}
		for _, k := range keys {
			switch k {
			case '\r', '\n':
				return 'n', nil
			case 'r', 'R':
				return 'r', nil
			case 'q', 'Q':
				return 'q', nil
			}
		}
	}
}

// drain は、d の間に届いた入力を捨てる。
func drain(box *inbox, d time.Duration) error {
	deadline := time.Now().Add(d)
	for left := d; left > 0; left = time.Until(deadline) {
		if _, err := box.next(left); err != nil && !errors.Is(err, errTimeout) {
			return err
		}
	}
	return nil
}

func stepScreen(i, n int, st keyStep, attempt int) string {
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[H")
	fmt.Fprintf(&b, "tuiprobe keys  %d/%d  %s", i+1, n, st.id)
	if attempt > 1 {
		fmt.Fprintf(&b, "（%d 回目）", attempt)
	}
	b.WriteString("\r\n\r\n")
	fmt.Fprintf(&b, "  %s\r\n", st.label)
	if st.hint != "" {
		fmt.Fprintf(&b, "  （%s）\r\n", st.hint)
	}
	if strings.HasPrefix(st.id, "paste") {
		fmt.Fprintf(&b, "\r\n  貼り付ける文字列: %s\r\n", visible([]byte(pasteText)))
	}
	b.WriteString("\r\n  押したら、そのまま待ってください。届いたものを表示します。\r\n")
	return b.String()
}

func summaryScreen(rec stepResult) string {
	var b strings.Builder
	b.WriteString("\r\n")
	if rec.Result == "no_input" {
		b.WriteString("  何も届きませんでした。\r\n")
	} else {
		fmt.Fprintf(&b, "  届いたもの（読み取り %d 回、最初の入力まで %.0f ミリ秒）:\r\n", len(rec.Reads), rec.WaitMs)
		lines := 0
		for _, r := range rec.Reads {
			if lines >= 10 {
				b.WriteString("    （以下略）\r\n")
				break
			}
			if r.Records == nil {
				raw, _ := hex.DecodeString(r.Hex)
				fmt.Fprintf(&b, "    +%.0fms  %s  %s\r\n", r.TMs, spaced(r.Hex), visible(raw))
				lines++
				continue
			}
			for _, rj := range r.Records {
				if lines >= 10 {
					break
				}
				fmt.Fprintf(&b, "    +%.0fms  %s\r\n", r.TMs, describeRecord(rj))
				lines++
			}
		}
		if rec.Hex != "" && slices.ContainsFunc(rec.Reads, func(r readJSON) bool { return r.Records != nil }) {
			raw, _ := hex.DecodeString(rec.Hex)
			fmt.Fprintf(&b, "    文字: %s\r\n", visible(raw))
		}
	}
	b.WriteString("\r\n  Enter: 次へ    r: やり直す    q: 中断して保存する\r\n")
	return b.String()
}

func describeRecord(r recordJSON) string {
	switch r.Kind {
	case "key":
		dir := "up  "
		if *r.Down {
			dir = "down"
		}
		return fmt.Sprintf("key %s vk=%#02x scan=%#02x char=%#04x ctrl=%#x rep=%d", dir, r.VK, r.Scan, r.Char, r.Ctrl, r.Repeat)
	case "size":
		return fmt.Sprintf("size %dx%d", r.W, r.H)
	case "focus":
		return fmt.Sprintf("focus %v", *r.Focus)
	}
	return r.Kind + " " + r.Raw
}

// spaced は、16 進の文字列を 2 桁ずつ空白で区切る。
func spaced(h string) string {
	var parts []string
	for i := 0; i+2 <= len(h); i += 2 {
		parts = append(parts, h[i:i+2])
	}
	return strings.Join(parts, " ")
}

// visible は、入力を画面に出せる形にする。制御文字は ^[ のような形、印字できない文字は \u{...}、不正な UTF-8 は \xNN にする。
func visible(b []byte) string {
	var s strings.Builder
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&s, `\x%02x`, b[i])
		case r < 0x20:
			s.WriteByte('^')
			s.WriteByte(byte(r) + 0x40)
		case r == 0x7f:
			s.WriteString("^?")
		case r >= utf8.RuneSelf && (!unicode.IsPrint(r) || unicode.Is(unicode.Bidi_Control, r)):
			fmt.Fprintf(&s, `\u{%x}`, r)
		default:
			s.WriteRune(r)
		}
		i += size
	}
	return s.String()
}

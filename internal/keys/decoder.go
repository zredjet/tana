package keys

import (
	"bytes"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// DefaultEscWait は Esc の待ち時間（tui §5。フェーズ12の実測）。
	DefaultEscWait = 50 * time.Millisecond
	// DefaultPasteWait は、終わりの印が届かない貼り付けの待ち時間（tui §5）。
	DefaultPasteWait = time.Second
)

var (
	pasteStart = []byte("\x1b[200~")
	pasteEnd   = []byte("\x1b[201~")
)

// maxCSI は、終わりのバイトを待つ CSI の長さの上限。xterm のシーケンスは長くても数十バイトなので、
// これを超えたら解釈できない入力にする（壊れた入力で保留が増え続けないように）。
const maxCSI = 64

// Decoder は、端末からの入力（バイト列）をイベントに変える状態機械（tui §5）。ゼロ値で使える。
// 時刻は呼び出し側が渡す。Decoder は時刻を読まず、goroutine も使わない。1 つの goroutine から使うこと。
type Decoder struct {
	// EscWait は Esc の後に続きを待つ時間。0 なら DefaultEscWait。
	EscWait time.Duration
	// PasteWait は、終わりの印が届かない貼り付けを区切るまでの時間。0 なら DefaultPasteWait。
	PasteWait time.Duration

	expectCPR bool

	buf  []byte    // 保留中の入力（貼り付けの外）
	last time.Time // 保留中の入力、または貼り付けの中の入力が最後に届いた時刻

	paste     bool   // 貼り付けの中（開始の印を受け取り、終わりの印をまだ受け取っていない）
	pasteRaw  []byte // 貼り付けの、まだイベントにしていない元の入力（開始の印から始まることがある）
	pasteHead int    // pasteRaw の先頭の、開始の印の長さ（印をイベントにした後は 0）
	scanned   int    // pasteRaw の中で、終わりの印を探し終えた位置
}

// SetCursorPositionExpected は、カーソル位置の問い合わせの応答を待っているかを設定する。
// 待っている間だけ、ESC[行;桁R をカーソル位置の報告として解釈する（修飾キー付きの F3 と同じ形のため。tui §5）。
func (d *Decoder) SetCursorPositionExpected(on bool) { d.expectCPR = on }

func (d *Decoder) escWait() time.Duration {
	if d.EscWait > 0 {
		return d.EscWait
	}
	return DefaultEscWait
}

func (d *Decoder) pasteWait() time.Duration {
	if d.PasteWait > 0 {
		return d.PasteWait
	}
	return DefaultPasteWait
}

// Feed は、時刻 now に届いた入力 b を加え、確定したイベントを返す。
// 待ち時間が過ぎた保留中の入力は、b より前に確定する（Tick を呼んでいなくても、時刻で決まる）。
func (d *Decoder) Feed(b []byte, now time.Time) []Event {
	ev := d.expire(now, nil)
	if len(b) == 0 {
		return ev
	}
	d.last = now
	if d.paste {
		d.pasteRaw = append(d.pasteRaw, b...)
	} else {
		d.buf = append(d.buf, b...)
	}
	return d.decode(ev, false)
}

// Tick は、時刻 now までに待ち時間が過ぎた保留中の入力を確定し、そのイベントを返す。
func (d *Decoder) Tick(now time.Time) []Event {
	return d.expire(now, nil)
}

// Deadline は、次に Tick を呼ぶべき時刻を返す。保留中の入力がなく、貼り付けの中でもなければ ok は false。
func (d *Decoder) Deadline() (deadline time.Time, ok bool) {
	switch {
	case d.paste:
		return d.last.Add(d.pasteWait()), true
	case len(d.buf) > 0:
		return d.last.Add(d.escWait()), true
	}
	return time.Time{}, false
}

// expire は、now までに待ち時間が過ぎたものを確定して ev に加える。
func (d *Decoder) expire(now time.Time, ev []Event) []Event {
	for {
		dl, ok := d.Deadline()
		if !ok || now.Before(dl) {
			return ev
		}
		if !d.paste {
			ev = d.decode(ev, true)
			continue
		}
		if len(d.pasteRaw) == 0 {
			// 貼り付けのイベントにした後、さらに待ち時間の間なにも届かなかった。貼り付けを終える（tui §5）。
			d.paste = false
			continue
		}
		// 終わりの印が届かないまま待ち時間が過ぎた。そこまでを貼り付けのイベントにし、貼り付けは続ける（filer U2）。
		ev = append(ev, Event{Kind: PasteEvent, Text: string(d.pasteRaw[d.pasteHead:]), Raw: string(d.pasteRaw)})
		d.pasteRaw, d.pasteHead, d.scanned = nil, 0, 0
		d.last = dl
	}
}

type status uint8

const (
	parsed     status = iota // イベントを 1 つ解釈した
	needMore                 // 続きを待つ
	startPaste               // 先頭が貼り付けの開始の印
)

// decode は、保留中の入力を解釈できるだけ解釈して ev に加える。final なら、続きを待たない（待ち時間が過ぎた）。
func (d *Decoder) decode(ev []Event, final bool) []Event {
	for {
		if d.paste {
			from := max(d.pasteHead, d.scanned-len(pasteEnd)+1)
			i := bytes.Index(d.pasteRaw[from:], pasteEnd)
			if i < 0 {
				d.scanned = len(d.pasteRaw)
				return ev
			}
			end := from + i
			ev = append(ev, Event{Kind: PasteEvent, Text: string(d.pasteRaw[d.pasteHead:end]), Raw: string(d.pasteRaw[:end+len(pasteEnd)])})
			d.buf = append(d.buf, d.pasteRaw[end+len(pasteEnd):]...)
			d.paste, d.pasteRaw, d.pasteHead, d.scanned = false, nil, 0, 0
			continue
		}
		if len(d.buf) == 0 {
			d.buf = nil
			return ev
		}
		e, n, st := d.parse(d.buf, final)
		switch st {
		case needMore:
			return ev
		case startPaste:
			d.paste = true
			d.pasteRaw = append([]byte(nil), d.buf...)
			d.pasteHead, d.scanned = len(pasteStart), len(pasteStart)
			d.buf = nil
			continue
		}
		ev = append(ev, e)
		d.buf = d.buf[n:]
	}
}

func keyEvent(k Key, m Mod, raw []byte) Event {
	return Event{Kind: KeyEvent, Key: k, Mod: m, Raw: string(raw)}
}

func runeEvent(r rune, m Mod, raw []byte) Event {
	return Event{Kind: KeyEvent, Key: KeyRune, Rune: r, Mod: m, Raw: string(raw)}
}

func unknown(raw []byte) Event { return Event{Kind: UnknownEvent, Raw: string(raw)} }

// controlKey は、ESC 以外の制御文字 1 バイト c（0x00〜0x1A、0x1C〜0x1F、0x7F）のキーを返す（tui §5 の「同じバイトで届くキー」）。
func controlKey(c byte, m Mod, raw []byte) Event {
	switch c {
	case 0x00:
		return runeEvent(' ', m|ModCtrl, raw)
	case 0x09:
		return keyEvent(KeyTab, m, raw)
	case 0x0d:
		return keyEvent(KeyEnter, m, raw)
	case 0x7f:
		return keyEvent(KeyBackspace, m, raw)
	}
	if c <= 0x1a {
		return runeEvent(rune('a'+c-1), m|ModCtrl, raw) // 0x08 は Ctrl＋H（Backspace は 0x7F）
	}
	return runeEvent(rune(`\]^_`[c-0x1c]), m|ModCtrl, raw)
}

// parse は、buf（空でない）の先頭の 1 つのイベントを解釈する。final なら、続きを待たない。
func (d *Decoder) parse(buf []byte, final bool) (Event, int, status) {
	b0 := buf[0]
	switch {
	case b0 == 0x1b:
		return d.parseEsc(buf, final)
	case b0 < 0x20 || b0 == 0x7f:
		return controlKey(b0, 0, buf[:1]), 1, parsed
	}
	if !utf8.FullRune(buf) {
		if !final {
			return Event{}, 0, needMore
		}
		return unknown(buf[:1]), 1, parsed
	}
	r, size := utf8.DecodeRune(buf)
	if r == utf8.RuneError && size == 1 {
		return unknown(buf[:1]), 1, parsed
	}
	return runeEvent(r, 0, buf[:size]), size, parsed
}

// parseEsc は、ESC で始まる入力を解釈する。
func (d *Decoder) parseEsc(buf []byte, final bool) (Event, int, status) {
	if len(buf) == 1 {
		if !final {
			return Event{}, 0, needMore
		}
		return keyEvent(KeyEsc, 0, buf), 1, parsed
	}
	switch b1 := buf[1]; {
	case b1 == '[':
		return d.parseCSI(buf, final)
	case b1 == 'O':
		return parseSS3(buf, final)
	case b1 == 0x1b:
		// ESC に続くシーケンスを Alt 付きにする（Alt＋Esc、Alt＋上矢印など）。キーでなければ、Esc だけを確定する。
		// Alt は 1 段だけ: ESC がさらに続くなら、先頭の ESC は Esc（押し続けた Esc を 1 つのイベントにまとめない）。
		if len(buf) >= 3 && buf[2] == 0x1b {
			return keyEvent(KeyEsc, 0, buf[:1]), 1, parsed
		}
		e, n, st := d.parseEsc(buf[1:], final)
		switch {
		case st == needMore:
			return Event{}, 0, needMore
		case st == parsed && e.Kind == KeyEvent:
			e.Mod |= ModAlt
			e.Raw = string(buf[:1+n])
			return e, 1 + n, parsed
		}
		return keyEvent(KeyEsc, 0, buf[:1]), 1, parsed
	case b1 < 0x20 || b1 == 0x7f:
		return controlKey(b1, ModAlt, buf[:2]), 2, parsed
	}
	if !utf8.FullRune(buf[1:]) {
		if !final {
			return Event{}, 0, needMore
		}
		return keyEvent(KeyEsc, 0, buf[:1]), 1, parsed
	}
	r, size := utf8.DecodeRune(buf[1:])
	if r == utf8.RuneError && size == 1 {
		return keyEvent(KeyEsc, 0, buf[:1]), 1, parsed
	}
	return runeEvent(r, ModAlt, buf[:1+size]), 1 + size, parsed
}

// parseSS3 は、ESC O で始まる入力（アプリケーションモードの矢印、F1〜F4）を解釈する。
func parseSS3(buf []byte, final bool) (Event, int, status) {
	if len(buf) == 2 {
		if !final {
			return Event{}, 0, needMore
		}
		return runeEvent('O', ModAlt, buf), 2, parsed
	}
	c := buf[2]
	if c < 0x40 || c > 0x7e {
		return runeEvent('O', ModAlt, buf[:2]), 2, parsed
	}
	k, ok := ss3Keys[c]
	if !ok {
		return unknown(buf[:3]), 3, parsed
	}
	return keyEvent(k, 0, buf[:3]), 3, parsed
}

// parseCSI は、ESC [ で始まる入力を解釈する（ECMA-48: 引数のバイト 0x30〜0x3F、中間のバイト 0x20〜0x2F、終わりのバイト 0x40〜0x7E）。
func (d *Decoder) parseCSI(buf []byte, final bool) (Event, int, status) {
	for i := 2; i < len(buf); i++ {
		c := buf[i]
		switch {
		case i >= maxCSI:
			return unknown(buf[:i]), i, parsed
		case 0x20 <= c && c <= 0x3f:
			continue
		case 0x40 <= c && c <= 0x7e:
			seq := buf[:i+1]
			if bytes.Equal(seq, pasteStart) {
				return Event{}, 0, startPaste
			}
			return d.csiEvent(seq), i + 1, parsed
		}
		// 制御文字などで途切れた。ESC [ の直後なら Alt＋[（待ち時間が過ぎた場合と同じ）、そうでなければ、そこまでを解釈できない入力にする。
		if i == 2 {
			return runeEvent('[', ModAlt, buf[:2]), 2, parsed
		}
		return unknown(buf[:i]), i, parsed
	}
	if !final {
		return Event{}, 0, needMore
	}
	if len(buf) == 2 {
		return runeEvent('[', ModAlt, buf), 2, parsed
	}
	return unknown(buf), len(buf), parsed
}

// ss3Keys は、ESC O の後の 1 バイトで決まるキー。
var ss3Keys = map[byte]Key{'A': KeyUp, 'B': KeyDown, 'C': KeyRight, 'D': KeyLeft, 'H': KeyHome, 'F': KeyEnd,
	'P': KeyF1, 'Q': KeyF2, 'R': KeyF3, 'S': KeyF4}

// csiKeys は、ESC [ の後に修飾キーの引数と、この終わりのバイトが続くキー。
var csiKeys = map[byte]Key{'A': KeyUp, 'B': KeyDown, 'C': KeyRight, 'D': KeyLeft, 'H': KeyHome, 'F': KeyEnd,
	'P': KeyF1, 'Q': KeyF2, 'R': KeyF3, 'S': KeyF4, 'Z': KeyTab}

// tildeKeys は、ESC [ 数 ~ のキー（xterm）。
var tildeKeys = map[int]Key{1: KeyHome, 2: KeyInsert, 3: KeyDelete, 4: KeyEnd, 5: KeyPageUp, 6: KeyPageDown, 7: KeyHome, 8: KeyEnd,
	11: KeyF1, 12: KeyF2, 13: KeyF3, 14: KeyF4, 15: KeyF5, 17: KeyF6, 18: KeyF7, 19: KeyF8, 20: KeyF9, 21: KeyF10,
	23: KeyF11, 24: KeyF12, 25: KeyF13, 26: KeyF14, 28: KeyF15, 29: KeyF16, 31: KeyF17, 32: KeyF18, 33: KeyF19, 34: KeyF20}

// csiEvent は、完全な CSI のシーケンス seq を解釈する。
func (d *Decoder) csiEvent(seq []byte) Event {
	body := string(seq[2 : len(seq)-1])
	fin := seq[len(seq)-1]
	var params []int
	if body != "" {
		for p := range strings.SplitSeq(body, ";") {
			if p == "" {
				params = append(params, 0)
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 || strings.ContainsAny(p, "+-") {
				return unknown(seq) // 中間のバイト、非公開の引数（? など）、下位の引数（:）、大きすぎる数
			}
			params = append(params, n)
		}
	}
	// mod は、[1, 修飾キー] の形の引数から修飾キーを得る。引数がなければ 0。
	mod := func(ps []int) (Mod, bool) {
		switch {
		case len(ps) == 0:
			return 0, true
		case len(ps) == 1 && ps[0] <= 1:
			return 0, true
		case len(ps) == 2 && ps[0] <= 1 && ps[1] >= 1:
			return Mod(ps[1]-1) & (ModShift | ModAlt | ModCtrl | ModMeta), true
		}
		return 0, false
	}
	switch k, ok := csiKeys[fin]; {
	case fin == 'R' && d.expectCPR && len(params) == 2 && params[0] >= 1 && params[1] >= 1:
		return Event{Kind: CursorPositionEvent, Row: params[0], Col: params[1], Raw: string(seq)}
	case ok:
		m, ok := mod(params)
		if !ok {
			return unknown(seq)
		}
		if fin == 'Z' {
			m |= ModShift
		}
		return keyEvent(k, m, seq)
	case fin == '~' && len(params) >= 1 && len(params) <= 2:
		k, ok := tildeKeys[params[0]]
		m, mok := mod(append([]int{1}, params[1:]...))
		if !ok || !mok {
			return unknown(seq)
		}
		return keyEvent(k, m, seq)
	}
	return unknown(seq)
}

package term

import (
	"encoding/binary"
	"unicode/utf16"
	"unicode/utf8"
)

// RecordKind は Windows の入力のレコードの種類（INPUT_RECORD の EventType と同じ値）。
type RecordKind uint16

const (
	KeyRecord        RecordKind = 0x0001 // KEY_EVENT
	MouseRecord      RecordKind = 0x0002 // MOUSE_EVENT
	WindowSizeRecord RecordKind = 0x0004 // WINDOW_BUFFER_SIZE_EVENT
	MenuRecord       RecordKind = 0x0008 // MENU_EVENT
	FocusRecord      RecordKind = 0x0010 // FOCUS_EVENT
)

// Record は Windows の入力のレコード（INPUT_RECORD）を、OS に依存しない型に写したもの（tui §5）。
type Record struct {
	Kind RecordKind

	// KeyRecord のとき（KEY_EVENT_RECORD）。
	KeyDown     bool
	RepeatCount uint16
	VirtualKey  uint16
	ScanCode    uint16
	Char        uint16 // UTF-16 の 1 単位。サロゲートの半分のこともある
	ControlKeys uint32 // dwControlKeyState

	// WindowSizeRecord のとき: 画面バッファの大きさ。
	Width, Height int

	// FocusRecord のとき。
	Focus bool

	// Raw は INPUT_RECORD の Event の 16 バイト（種類によらず、元のまま）。
	Raw [16]byte
}

// inputRecordSize は INPUT_RECORD の大きさ（WORD EventType、2 バイトの詰め物、16 バイトの Event）。
const inputRecordSize = 20

// decodeRecord は、INPUT_RECORD の 20 バイトを Record にする。
func decodeRecord(b []byte) Record {
	le := binary.LittleEndian
	r := Record{Kind: RecordKind(le.Uint16(b[0:]))}
	ev := b[4:inputRecordSize]
	copy(r.Raw[:], ev)
	switch r.Kind {
	case KeyRecord:
		r.KeyDown = le.Uint32(ev[0:]) != 0
		r.RepeatCount = le.Uint16(ev[4:])
		r.VirtualKey = le.Uint16(ev[6:])
		r.ScanCode = le.Uint16(ev[8:])
		r.Char = le.Uint16(ev[10:])
		r.ControlKeys = le.Uint32(ev[12:])
	case WindowSizeRecord:
		r.Width = int(int16(le.Uint16(ev[0:])))
		r.Height = int(int16(le.Uint16(ev[2:])))
	case FocusRecord:
		r.Focus = le.Uint32(ev[0:]) != 0
	}
	return r
}

// utf16Encoder は、WriteConsoleW に渡すために UTF-8 のバイト列を UTF-16 に変える（VT1 の候補の 1 つ）。
// 書き込みの境界で切れた UTF-8 の文字は、次の書き込みまで持っておく。
// 不正な UTF-8 のバイトは U+FFFD にする。対になっていないサロゲートをコンソールに渡さない。
type utf16Encoder struct {
	pending []byte
}

func (e *utf16Encoder) encode(p []byte) []uint16 {
	b := append(e.pending, p...)
	e.pending = nil
	out := make([]uint16, 0, len(b))
	for i := 0; i < len(b); {
		if b[i] < utf8.RuneSelf {
			out = append(out, uint16(b[i]))
			i++
			continue
		}
		if !utf8.FullRune(b[i:]) {
			e.pending = append([]byte(nil), b[i:]...)
			break
		}
		r, size := utf8.DecodeRune(b[i:])
		out = utf16.AppendRune(out, r)
		i += size
	}
	return out
}

// vkMenu は Alt キーの仮想キーコード（VK_MENU）。
const vkMenu = 0x12

// recordDecoder は、Windows の入力のレコードを、keys に渡すバイト列にする（T3。tui §8）。
//   - キーを押したレコードの文字（UTF-16）を UTF-8 にする。繰り返しの回数だけ繰り返す。
//   - conhost が Alt＋テンキーの並びの最後に送る、Alt を離したレコードの文字も含める（BMP の外の文字を貼り付けたとき）。
//   - サロゲートの対は、2 回の読み取りにまたがっても組み立てる。対にならない半分は U+FFFD にする。
//   - 文字を持たないレコード（キーを離したレコード、Shift・Ctrl のレコード、フォーカス、メニュー）は捨てる。入力を表さない。
//   - 大きさの変更のレコードは、大きさが変わった印にする。
type recordDecoder struct {
	high uint16 // 前の読み取りの最後に残った上位サロゲート（0 ならなし）
}

func (d *recordDecoder) decode(recs []Record) (text []byte, resize bool) {
	var u []uint16
	if d.high != 0 {
		u = append(u, d.high)
		d.high = 0
	}
	for _, r := range recs {
		switch {
		case r.Kind == WindowSizeRecord:
			resize = true
		case r.Kind != KeyRecord || r.Char == 0:
		case r.KeyDown:
			for range max(r.RepeatCount, 1) {
				u = append(u, r.Char)
			}
		case r.VirtualKey == vkMenu:
			u = append(u, r.Char)
		}
	}
	// 最後の上位サロゲートは、次の読み取りの下位サロゲートと組むかもしれないので持っておく。
	if n := len(u); n > 0 && utf16.IsSurrogate(rune(u[n-1])) && u[n-1] < 0xdc00 {
		d.high, u = u[n-1], u[:n-1]
	}
	return []byte(string(utf16.Decode(u))), resize
}

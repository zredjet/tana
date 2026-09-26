package main

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strconv"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/zredjet/tana/internal/term"
)

// console は、プローブが使う端末の操作（*term.Term。テストでは偽物）。
type console interface {
	io.Writer
	SetBracketedPaste(on bool) error
	QueryCursorPosition() error
}

var (
	errTimeout     = errors.New("timeout")
	errInputClosed = errors.New("input closed")
)

// inbox は、端末からの入力を、期限を付けて受け取る。
type inbox struct {
	ctx context.Context
	in  <-chan term.Input
}

// next は、d の間に届いた入力を返す。届かなければ errTimeout を返す。
func (b *inbox) next(d time.Duration) (term.Input, error) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case x, ok := <-b.in:
		if !ok {
			return term.Input{}, errInputClosed
		}
		if x.Err != nil {
			return term.Input{}, x.Err
		}
		return x, nil
	case <-timer.C:
		return term.Input{}, errTimeout
	case <-b.ctx.Done():
		return term.Input{}, b.ctx.Err()
	}
}

// vkMenu は Alt キーの仮想キーコード（VK_MENU）。
const vkMenu = 0x12

// inputBytes は、1 回の読み取りの入力をバイト列にする（textDecoder を 1 回だけ使う）。
func inputBytes(x term.Input) []byte {
	var d textDecoder
	return append(d.decode(x), d.flush()...)
}

// textDecoder は、入力を順にバイト列にする。Unix のバイト列はそのまま。
// Windows のレコードは、キーを押したレコードの文字（UTF-16）を UTF-8 にする（VT の入力モードとカーソル位置の報告のため）。
// Alt キーを離したレコードの文字も含める。conhost は、BMP の外の文字（絵文字など）を貼り付けると、
// Alt＋テンキーの並びの最後の、Alt を離したレコードに文字（サロゲートの半分）を載せて届ける。
// サロゲートの対が 2 回の読み取りに分かれて届いても組み立てる。対にならないサロゲートは U+FFFD になる。
type textDecoder struct {
	high uint16 // 前の読み取りの最後に残った上位サロゲート（0 ならなし）
}

func (d *textDecoder) decode(x term.Input) []byte {
	if x.Records == nil {
		return x.Bytes
	}
	var u []uint16
	if d.high != 0 {
		u = append(u, d.high)
		d.high = 0
	}
	for _, r := range x.Records {
		if r.Kind != term.KeyRecord || r.Char == 0 {
			continue
		}
		switch {
		case r.KeyDown:
			for range max(r.RepeatCount, 1) {
				u = append(u, r.Char)
			}
		case r.VirtualKey == vkMenu:
			u = append(u, r.Char)
		}
	}
	if n := len(u); n > 0 && 0xd800 <= u[n-1] && u[n-1] < 0xdc00 {
		d.high, u = u[n-1], u[:n-1]
	}
	return []byte(string(utf16.Decode(u)))
}

// flush は、残っている上位サロゲートを U+FFFD にして返す。
func (d *textDecoder) flush() []byte {
	if d.high == 0 {
		return nil
	}
	d.high = 0
	return []byte(string(utf8.RuneError))
}

// cprPattern はカーソル位置の報告（ESC[行;桁R）。
var cprPattern = regexp.MustCompile(`\x1b\[(\d+);(\d+)R`)

// cprReader は、カーソル位置の報告を待つ。報告の前に届いたもの（ほかの問い合わせの応答など）も返す。
type cprReader struct {
	box *inbox
	dec textDecoder
	buf []byte
}

// read は、timeout までにカーソル位置の報告を待つ。
func (r *cprReader) read(timeout time.Duration) (row, col int, before, report []byte, err error) {
	deadline := time.Now().Add(timeout)
	for {
		if m := cprPattern.FindSubmatchIndex(r.buf); m != nil {
			before = append([]byte(nil), r.buf[:m[0]]...)
			report = append([]byte(nil), r.buf[m[0]:m[1]]...)
			row, _ = strconv.Atoi(string(r.buf[m[2]:m[3]]))
			col, _ = strconv.Atoi(string(r.buf[m[4]:m[5]]))
			r.buf = append([]byte(nil), r.buf[m[1]:]...)
			return row, col, before, report, nil
		}
		left := time.Until(deadline)
		if left <= 0 {
			before, r.buf = r.buf, nil
			return 0, 0, before, nil, errTimeout
		}
		x, err := r.box.next(left)
		if err != nil && !errors.Is(err, errTimeout) {
			return 0, 0, nil, nil, err
		}
		r.buf = append(r.buf, r.dec.decode(x)...)
	}
}

// drain は、d の間に届いた入力と、読んで残っていたものを捨てる。
func (r *cprReader) drain(d time.Duration) error {
	r.buf = nil
	return drain(r.box, d)
}

package main

import (
	"context"
	"errors"
	"io"
	"regexp"
	"strconv"
	"time"

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

// cprPattern はカーソル位置の報告（ESC[行;桁R）。
var cprPattern = regexp.MustCompile(`\x1b\[(\d+);(\d+)R`)

// cprReader は、カーソル位置の報告を待つ。報告の前に届いたもの（ほかの問い合わせの応答など）も返す。
type cprReader struct {
	box *inbox
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
		r.buf = append(r.buf, x.Bytes...) // Windows のレコードも、term がバイト列にしている
	}
}

// drain は、d の間に届いた入力と、読んで残っていたものを捨てる。
func (r *cprReader) drain(d time.Duration) error {
	r.buf = nil
	return drain(r.box, d)
}

package tui

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/zredjet/tana/internal/keys"
	"github.com/zredjet/tana/internal/screen"
	"github.com/zredjet/tana/internal/term"
)

// Terminal は、Loop が使う端末。*term.Term が満たす。
type Terminal interface {
	Write(p []byte) (int, error)
	Size() (cols, rows int, err error)
	StartInput() <-chan term.Input
	Restore() error
}

// EventKind はイベントの種類。
type EventKind int

const (
	// KindKey は、keys のイベント（キー、貼り付け、カーソル位置の報告、解釈できない入力）。Event.Key に入る。
	KindKey EventKind = iota + 1
	// KindResize は、端末の大きさが変わったこと。画面はもう新しい大きさで、内容は空白に戻っている。
	KindResize
	// KindMessage は、Post で送られたもの。Event.Msg に入る。
	KindMessage
	// KindWake は、Wake の知らせ（進捗など、最新の値を読み直す）。何回 Wake しても、まとめて 1 回になることがある。
	KindWake
	// KindSignal は、シグナル（SIGINT・SIGTERM・SIGHUP、Windows のコンソールを閉じる通知）。Event.Signal に入る。
	// ハンドラがどう返しても、この後 Run は SignalError を返して終わる。
	KindSignal
)

// Event は、ハンドラに渡すもの。
type Event struct {
	Kind   EventKind
	Key    keys.Event // KindKey のとき
	Msg    any        // KindMessage のとき
	Signal os.Signal  // KindSignal のとき
}

// Handler は、イベントを受けて状態を変え、画面を描く。どちらもイベントループの goroutine から呼ぶ。
type Handler interface {
	// Handle はイベントを処理する。false を返すと Run を終える。
	Handle(l *Loop, ev Event) bool
	// Draw は画面の全体を描く。画面は描く前に空白に戻す（前のフレームの内容は残らない）。
	// Run は、たまっているイベントをすべて処理した後で 1 回呼び、差分を端末に書く。
	Draw(s *screen.Screen)
}

// PanicError は、イベントループか、Go で始めた goroutine の panic。Run は、端末を戻してからこれを返す。
type PanicError struct {
	Value any
	Stack []byte // panic した goroutine のスタック
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v\n\n%s", e.Value, e.Stack) }

// SignalError は、シグナルを受けて Run が終わったこと。
type SignalError struct{ Signal os.Signal }

func (e *SignalError) Error() string { return "tui: received signal " + e.Signal.String() }

// ErrInputClosed は、端末の入力が閉じられた（読み取りの失敗を知らせずに）こと。
var ErrInputClosed = errors.New("tui: terminal input closed")

// Stats は、最後のフレームを描いて書くのにかかった時間など（確認用。filer VU1 の「スクロールが遅れない」）。
type Stats struct {
	Frames    int           // 描いたフレームの数
	Draw      time.Duration // 最後のフレームの Draw の時間
	Flush     time.Duration // 最後のフレームの Flush（差分を作って端末に書く）の時間
	Bytes     int           // 最後のフレームで端末に書いたバイト数
	Coalesced int           // 最後のフレームの前にまとめて処理したイベントの数
}

// maxBatch は、描く前にまとめて処理する時間の上限。イベントが途切れずに届き続けても、この間隔では描く。
const maxBatch = 50 * time.Millisecond

// Loop は、端末・keys・screen を組み合わせたイベントループ（filer §10）。
type Loop struct {
	t     Terminal
	scr   *screen.Screen
	dec   keys.Decoder
	stats Stats

	mu      sync.Mutex
	msgs    []any
	woken   bool
	failure *PanicError   // Go で始めた goroutine の最初の panic
	done    bool          // Run が終わった（Post・Wake は捨てる）
	notify  chan struct{} // 容量 1。Post・Wake・panic の知らせ
}

// New は、端末 t のイベントループを作る。
func New(t Terminal) *Loop {
	return &Loop{t: t, scr: screen.New(0, 0), notify: make(chan struct{}, 1)}
}

// SetNoColor は、色を出力しないようにする（NO_COLOR。filer §9.5）。Run の前に呼ぶ。
func (l *Loop) SetNoColor(on bool) { l.scr.NoColor = on }

// Stats は、最後のフレームの統計を返す。イベントループの goroutine（Handle・Draw の中）から呼ぶ。
func (l *Loop) Stats() Stats { return l.stats }

// Size は、画面の大きさを返す。イベントループの goroutine（Handle・Draw の中）から呼ぶ。
func (l *Loop) Size() (cols, rows int) { return l.scr.Size() }

// Post は、msg をイベントループに送る（KindMessage）。どの goroutine から呼んでもよく、待たない。送った順に届く。
// Run が終わった後に送ったものは捨てる。
func (l *Loop) Post(msg any) {
	l.mu.Lock()
	if !l.done {
		l.msgs = append(l.msgs, msg)
	}
	l.mu.Unlock()
	l.signal()
}

// Wake は、イベントループに知らせる（KindWake）。どの goroutine から呼んでもよく、待たない。
// 進捗のように最新の値だけが要るものは、値を置き場所に書いてから Wake を呼ぶ（filer §10）。
func (l *Loop) Wake() {
	l.mu.Lock()
	if !l.done {
		l.woken = true
	}
	l.mu.Unlock()
	l.signal()
}

func (l *Loop) signal() {
	select {
	case l.notify <- struct{}{}:
	default:
	}
}

// Go は、作業用の goroutine で f を動かす。f の panic は回収して、Run を PanicError で終わらせる（端末を戻すため。tui T1）。
// Run が終わった後の panic は、端末がもう戻っているので、回収せずにそのまま panic させる（隠さない）。
// ファイラーの作業用の goroutine は、すべてこれで始める（filer §10）。
func (l *Loop) Go(f func()) {
	go func() {
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			pe := &PanicError{Value: r, Stack: debug.Stack()}
			l.mu.Lock()
			done := l.done
			if !done && l.failure == nil {
				l.failure = pe
			}
			l.mu.Unlock()
			if done {
				panic(r)
			}
			l.signal()
		}()
		f()
	}()
}

// Run は、入力を読んでハンドラに渡し、画面を描くことを、ハンドラが false を返すまで繰り返す。
// たまっているイベントはまとめて処理してから描く（イベントが届き続けるときは、maxBatch ごとに描く）。
// どの終わり方でも（ハンドラが終える、シグナル、読み取りの失敗、書き込みの失敗、Handle・Draw・Go の panic）、
// 返る前に端末を戻す（tui T1）。panic は PanicError、シグナルは SignalError として返す。
func (l *Loop) Run(h Handler) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &PanicError{Value: r, Stack: debug.Stack()}
		}
		l.mu.Lock()
		l.done = true
		l.msgs = nil
		failure := l.failure
		l.mu.Unlock()
		// 終わる直前に Go で始めた goroutine が panic していれば、それも返す（知らせを処理する前に終わった場合）。
		if failure != nil && !errors.Is(err, failure) {
			err = errors.Join(err, failure)
		}
		err = errors.Join(err, l.t.Restore())
	}()
	in := l.t.StartInput()
	if err := l.resize(); err != nil {
		return err
	}
	for {
		if err := l.draw(h); err != nil {
			return err
		}
		l.stats.Coalesced = 0
		// 1 つ待ち、その後はたまっているものを描く前に処理する。ただし、イベントが途切れずに届き続けても描画が止まらないように、
		// 最初のイベントから maxBatch が過ぎたら描く（filer U5）。
		var batchStart time.Time
		for block := true; ; block = false {
			if !block && time.Since(batchStart) >= maxBatch {
				break
			}
			handled, quit, err := l.step(h, in, block)
			if err != nil || quit {
				return err
			}
			if !handled {
				break
			}
			if block {
				batchStart = time.Now()
			}
			l.stats.Coalesced++
		}
	}
}

// step は、入力・待ち時間・知らせのどれか 1 つを処理する。block が false なら待たず、何もなければ handled は false。
func (l *Loop) step(h Handler, in <-chan term.Input, block bool) (handled, quit bool, err error) {
	var timeout <-chan time.Time
	if deadline, ok := l.dec.Deadline(); ok {
		wait := time.Until(deadline)
		if wait <= 0 {
			return true, !l.keys(h, l.dec.Tick(time.Now())), nil
		}
		if block {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			timeout = timer.C
		}
	}
	if block {
		select {
		case x, ok := <-in:
			quit, err := l.input(h, x, ok)
			return true, quit, err
		case <-timeout:
			return true, !l.keys(h, l.dec.Tick(time.Now())), nil
		case <-l.notify:
			quit, err := l.notified(h)
			return true, quit, err
		}
	}
	select {
	case x, ok := <-in:
		quit, err := l.input(h, x, ok)
		return true, quit, err
	case <-l.notify:
		quit, err := l.notified(h)
		return true, quit, err
	default:
		return false, false, nil
	}
}

// input は、端末からの 1 つの入力を処理する。
func (l *Loop) input(h Handler, x term.Input, ok bool) (quit bool, err error) {
	switch {
	case !ok:
		return true, ErrInputClosed
	case x.Err != nil:
		return true, x.Err
	case x.Signal != nil:
		h.Handle(l, Event{Kind: KindSignal, Signal: x.Signal})
		return true, &SignalError{Signal: x.Signal}
	}
	if x.Resize {
		if err := l.resize(); err != nil {
			return true, err
		}
		if !h.Handle(l, Event{Kind: KindResize}) {
			return true, nil
		}
	}
	if len(x.Bytes) > 0 {
		// 入力が届いた時刻で渡す（Esc の待ち時間は、ループが読むのが遅れても、届いた時刻から数える）。
		now := x.Time
		if now.IsZero() {
			now = time.Now()
		}
		if !l.keys(h, l.dec.Feed(x.Bytes, now)) {
			return true, nil
		}
	}
	return false, nil
}

// keys は、keys のイベントを順にハンドラに渡す。ハンドラが false を返したら、そこで止めて false を返す。
func (l *Loop) keys(h Handler, evs []keys.Event) bool {
	for _, ev := range evs {
		if !h.Handle(l, Event{Kind: KindKey, Key: ev}) {
			return false
		}
	}
	return true
}

// notified は、Post・Wake・Go の panic の知らせを処理する。
func (l *Loop) notified(h Handler) (quit bool, err error) {
	l.mu.Lock()
	failure, msgs, woken := l.failure, l.msgs, l.woken
	l.msgs, l.woken = nil, false
	l.mu.Unlock()
	if failure != nil {
		return true, failure
	}
	if woken && !h.Handle(l, Event{Kind: KindWake}) {
		return true, nil
	}
	for _, m := range msgs {
		if !h.Handle(l, Event{Kind: KindMessage, Msg: m}) {
			return true, nil
		}
	}
	return false, nil
}

// resize は、端末の大きさを読み直して、画面の大きさを変える。
func (l *Loop) resize() error {
	cols, rows, err := l.t.Size()
	if err != nil {
		return err
	}
	l.scr.Resize(cols, rows)
	return nil
}

// draw は、画面を空白に戻してハンドラに描かせ、差分を端末に書く。
func (l *Loop) draw(h Handler) error {
	cols, rows := l.scr.Size()
	start := time.Now()
	l.scr.Fill(screen.Region{W: cols, H: rows}, screen.Style{})
	h.Draw(l.scr)
	drawn := time.Now()
	w := &countingWriter{w: l.t}
	err := l.scr.Flush(w)
	l.stats.Frames++
	l.stats.Draw, l.stats.Flush, l.stats.Bytes = drawn.Sub(start), time.Since(drawn), w.n
	return err
}

type countingWriter struct {
	w Terminal
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

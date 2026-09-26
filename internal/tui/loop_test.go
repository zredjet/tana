package tui

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/keys"
	"github.com/zredjet/tana/internal/screen"
	"github.com/zredjet/tana/internal/term"
)

// fakeTerm は、テスト用の端末。入力はチャネルに入れ、書かれたものと Restore の回数を覚える。
type fakeTerm struct {
	in chan term.Input

	mu         sync.Mutex
	cols, rows int
	out        bytes.Buffer
	restored   int
	writeErr   error
}

func newFakeTerm(cols, rows int) *fakeTerm {
	return &fakeTerm{in: make(chan term.Input, 1000), cols: cols, rows: rows}
}

func (f *fakeTerm) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.out.Write(p)
}

func (f *fakeTerm) Size() (int, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cols, f.rows, nil
}

func (f *fakeTerm) StartInput() <-chan term.Input { return f.in }

func (f *fakeTerm) Restore() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restored++
	return nil
}

func (f *fakeTerm) setSize(cols, rows int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cols, f.rows = cols, rows
}

func (f *fakeTerm) restoreCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restored
}

func (f *fakeTerm) send(b string) { f.in <- term.Input{Time: time.Now(), Bytes: []byte(b)} }

// recorder は、受け取ったイベントを覚え、onEvent で振る舞いを決めるハンドラ。
type recorder struct {
	events  []Event
	draws   int
	sizes   [][2]int
	onEvent func(l *Loop, ev Event) bool
	onDraw  func(s *screen.Screen)
}

func (r *recorder) Handle(l *Loop, ev Event) bool {
	r.events = append(r.events, ev)
	if r.onEvent != nil {
		return r.onEvent(l, ev)
	}
	return true
}

func (r *recorder) Draw(s *screen.Screen) {
	r.draws++
	c, rw := s.Size()
	r.sizes = append(r.sizes, [2]int{c, rw})
	if r.onDraw != nil {
		r.onDraw(s)
	}
}

// quitOn は、キー q で終わるハンドラの振る舞い。
func quitOn(l *Loop, ev Event) bool {
	return !(ev.Kind == KindKey && ev.Key.Kind == keys.KeyEvent && ev.Key.Key == keys.KeyRune && ev.Key.Rune == 'q')
}

func keyStrings(evs []Event) []string {
	var out []string
	for _, ev := range evs {
		if ev.Kind == KindKey {
			out = append(out, ev.Key.String())
		}
	}
	return out
}

// TestRunKeysAndQuit は、入力がキーのイベントとしてハンドラに届き、ハンドラが false を返すと Run が終わって端末を戻すことを確かめる。
func TestRunKeysAndQuit(t *testing.T) {
	t.Parallel()
	f := newFakeTerm(20, 5)
	r := &recorder{onEvent: quitOn, onDraw: func(s *screen.Screen) { s.Put(screen.Region{W: 20, H: 5}, 0, 0, "hello", screen.Style{}) }}
	f.send("a\x1b[A")
	f.send("\x1b") // Esc は待ち時間の後で確定する
	go func() {
		time.Sleep(2 * keys.DefaultEscWait) // Esc の後に続きが来ないことを示す（待つのは Esc の確定のため。短くても長くても結果は同じ）
		f.send("q")
	}()
	if err := New(f).Run(r); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := strings.Join(keyStrings(r.events), " "), "'a' Up Esc 'q'"; got != want {
		t.Errorf("keys = %s, want %s", got, want)
	}
	if f.restoreCount() != 1 {
		t.Errorf("Restore called %d times, want 1", f.restoreCount())
	}
	if !strings.Contains(f.out.String(), "hello") {
		t.Errorf("output %q does not contain the drawn text", f.out.String())
	}
}

// TestRunDrawsOncePerBatch は、たまっている入力をすべて処理してから 1 回だけ描くことを確かめる（10 万行の一覧でスクロールが遅れない。VU1）。
func TestRunDrawsOncePerBatch(t *testing.T) {
	t.Parallel()
	f := newFakeTerm(20, 5)
	for range 100 {
		f.send("j")
	}
	f.send("q")
	r := &recorder{onEvent: quitOn}
	if err := New(f).Run(r); err != nil {
		t.Fatal(err)
	}
	if n := len(keyStrings(r.events)); n != 101 {
		t.Errorf("got %d key events, want 101", n)
	}
	if r.draws > 2 {
		t.Errorf("drew %d times for one batch of input, want at most 2 (the first frame and the batch)", r.draws)
	}
}

// TestRunResize は、大きさの変更で画面の大きさを変え、ハンドラに知らせてから描くことを確かめる。
func TestRunResize(t *testing.T) {
	t.Parallel()
	f := newFakeTerm(20, 5)
	r := &recorder{onEvent: quitOn}
	r.onEvent = func(l *Loop, ev Event) bool {
		if ev.Kind == KindResize {
			f.send("q")
		}
		return quitOn(l, ev)
	}
	f.setSize(30, 8)
	f.in <- term.Input{Time: time.Now(), Resize: true}
	if err := New(f).Run(r); err != nil {
		t.Fatal(err)
	}
	if len(r.sizes) == 0 || r.sizes[0] != [2]int{30, 8} {
		t.Errorf("draw sizes = %v, want the first frame at 30x8", r.sizes)
	}
	resized := false
	for _, ev := range r.events {
		resized = resized || ev.Kind == KindResize
	}
	if !resized {
		t.Error("no resize event")
	}
}

// TestRunSignal は、シグナルをハンドラに知らせてから Run を終え、端末を戻すことを確かめる（T1）。
func TestRunSignal(t *testing.T) {
	t.Parallel()
	f := newFakeTerm(20, 5)
	r := &recorder{onEvent: func(*Loop, Event) bool { return true }}
	f.in <- term.Input{Time: time.Now(), Signal: syscall.SIGTERM}
	err := New(f).Run(r)
	var se *SignalError
	if !errors.As(err, &se) || se.Signal != syscall.SIGTERM {
		t.Fatalf("Run = %v, want a SignalError for SIGTERM", err)
	}
	if last := r.events[len(r.events)-1]; last.Kind != KindSignal || last.Signal != syscall.SIGTERM {
		t.Errorf("last event = %+v, want the signal", last)
	}
	if f.restoreCount() != 1 {
		t.Errorf("Restore called %d times, want 1", f.restoreCount())
	}
}

// TestRunPanicInHandler は、ハンドラ・描画の panic を回収し、端末を戻して PanicError を返すことを確かめる（T1）。
func TestRunPanicInHandler(t *testing.T) {
	t.Parallel()
	for _, where := range []string{"handle", "draw"} {
		f := newFakeTerm(20, 5)
		r := &recorder{}
		if where == "handle" {
			r.onEvent = func(*Loop, Event) bool { panic("boom in handle") }
			f.send("x")
		} else {
			r.onDraw = func(*screen.Screen) { panic("boom in draw") }
		}
		err := New(f).Run(r)
		var pe *PanicError
		if !errors.As(err, &pe) || pe.Value != "boom in "+where || !strings.Contains(string(pe.Stack), "loop_test.go") {
			t.Errorf("%s: Run = %v, want a PanicError with the stack", where, err)
		}
		if f.restoreCount() != 1 {
			t.Errorf("%s: Restore called %d times, want 1", where, f.restoreCount())
		}
	}
}

// TestRunPanicInWorker は、Go で始めた作業用の goroutine の panic を回収し、Run を終えて端末を戻すことを確かめる（T1。filer §10）。
func TestRunPanicInWorker(t *testing.T) {
	t.Parallel()
	f := newFakeTerm(20, 5)
	l := New(f)
	r := &recorder{onEvent: func(l *Loop, ev Event) bool {
		if ev.Kind == KindKey {
			l.Go(func() { panic("boom in worker") })
		}
		return true
	}}
	f.send("x")
	err := l.Run(r)
	var pe *PanicError
	if !errors.As(err, &pe) || pe.Value != "boom in worker" {
		t.Fatalf("Run = %v, want a PanicError from the worker", err)
	}
	if f.restoreCount() != 1 {
		t.Errorf("Restore called %d times, want 1", f.restoreCount())
	}
}

// TestPostAndWake は、ほかの goroutine から Post したメッセージが順に届き、Wake が知らせとして届くことを確かめる。
func TestPostAndWake(t *testing.T) {
	t.Parallel()
	f := newFakeTerm(20, 5)
	l := New(f)
	var got []any
	wakes := 0
	r := &recorder{onEvent: func(l *Loop, ev Event) bool {
		switch ev.Kind {
		case KindMessage:
			got = append(got, ev.Msg)
			return ev.Msg != "last"
		case KindWake:
			wakes++
		case KindKey:
			l.Go(func() {
				l.Wake()
				for i := range 50 {
					l.Post(i)
				}
				l.Post("last")
			})
		}
		return true
	}}
	f.send("x")
	if err := l.Run(r); err != nil {
		t.Fatal(err)
	}
	if len(got) != 51 || got[50] != "last" {
		t.Fatalf("messages = %v", got)
	}
	for i := range 50 {
		if got[i] != i {
			t.Fatalf("message %d = %v, want in order: %v", i, got[i], got)
		}
	}
	if wakes == 0 {
		t.Error("no wake event")
	}
	// Run が終わった後の Post・Wake は、止まらずに捨てられる。
	l.Post("after")
	l.Wake()
}

// TestRunInputErrors は、入力の読み取りの失敗と、入力のチャネルが閉じたときに、Run が終わって端末を戻すことを確かめる。
func TestRunInputErrors(t *testing.T) {
	t.Parallel()
	readErr := errors.New("read failed")
	f := newFakeTerm(20, 5)
	f.in <- term.Input{Time: time.Now(), Err: readErr}
	if err := New(f).Run(&recorder{}); !errors.Is(err, readErr) {
		t.Errorf("Run with a read error = %v", err)
	}
	f = newFakeTerm(20, 5)
	close(f.in)
	if err := New(f).Run(&recorder{}); !errors.Is(err, ErrInputClosed) {
		t.Errorf("Run with closed input = %v", err)
	}
	if f.restoreCount() != 1 {
		t.Errorf("Restore called %d times, want 1", f.restoreCount())
	}
}

// TestRunWriteError は、端末に書けなくなったら Run が終わることを確かめる。
func TestRunWriteError(t *testing.T) {
	t.Parallel()
	f := newFakeTerm(20, 5)
	f.writeErr = errors.New("write failed")
	if err := New(f).Run(&recorder{}); !errors.Is(err, f.writeErr) {
		t.Errorf("Run = %v, want the write error", err)
	}
}

// TestRunPasteIsNotKeys は、貼り付けの中身をキーとして渡さないことを確かめる（filer U2）。
func TestRunPasteIsNotKeys(t *testing.T) {
	t.Parallel()
	f := newFakeTerm(20, 5)
	f.send("\x1b[200~yq\x1b[201~")
	f.send("q")
	r := &recorder{onEvent: quitOn}
	if err := New(f).Run(r); err != nil {
		t.Fatal(err)
	}
	if len(r.events) != 2 || r.events[0].Key.Kind != keys.PasteEvent || r.events[0].Key.Text != "yq" {
		t.Errorf("events = %+v, want the paste and then q", r.events)
	}
}

// TestRunEscDeadlinePassed は、入力が届いた時刻で Esc の待ち時間を数えることを確かめる（ループが読むのが遅れても、Esc と次のキーを分ける）。
func TestRunEscDeadlinePassed(t *testing.T) {
	t.Parallel()
	f := newFakeTerm(20, 5)
	f.in <- term.Input{Time: time.Now().Add(-time.Second), Bytes: []byte("\x1b")}
	f.in <- term.Input{Bytes: []byte("q")} // 時刻のない入力は、読んだ時刻で数える
	r := &recorder{onEvent: quitOn}
	if err := New(f).Run(r); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(keyStrings(r.events), " "); got != "Esc 'q'" {
		t.Errorf("keys = %s, want Esc 'q'", got)
	}
}

// TestRunHandlerStops は、ハンドラが大きさの変更・知らせ・メッセージで false を返すと、Run が終わることを確かめる。
func TestRunHandlerStops(t *testing.T) {
	t.Parallel()
	for _, kind := range []EventKind{KindResize, KindWake, KindMessage} {
		f := newFakeTerm(20, 5)
		l := New(f)
		switch kind {
		case KindResize:
			f.in <- term.Input{Time: time.Now(), Resize: true}
		case KindWake:
			l.Wake()
		case KindMessage:
			l.Post("m")
		}
		r := &recorder{onEvent: func(_ *Loop, ev Event) bool { return ev.Kind != kind }}
		if err := l.Run(r); err != nil {
			t.Errorf("kind %d: Run = %v", kind, err)
		}
		if f.restoreCount() != 1 {
			t.Errorf("kind %d: Restore called %d times", kind, f.restoreCount())
		}
	}
}

// sizeErrTerm は、大きさを得られない端末。
type sizeErrTerm struct {
	*fakeTerm
	err error
}

func (s sizeErrTerm) Size() (int, int, error) { return 0, 0, s.err }

// TestRunSizeError は、大きさを得られなければ Run が終わって端末を戻すことを確かめる。
func TestRunSizeError(t *testing.T) {
	t.Parallel()
	f := sizeErrTerm{newFakeTerm(20, 5), errors.New("no size")}
	if err := New(f).Run(&recorder{}); !errors.Is(err, f.err) {
		t.Errorf("Run = %v, want the size error", err)
	}
	if f.restoreCount() != 1 {
		t.Errorf("Restore called %d times", f.restoreCount())
	}
	// 大きさの変更のときに得られない場合も同じ。
	g := &flakySize{fakeTerm: newFakeTerm(20, 5), err: errors.New("no size later")}
	g.in <- term.Input{Time: time.Now(), Resize: true}
	if err := New(g).Run(&recorder{}); !errors.Is(err, g.err) {
		t.Errorf("Run with a failing resize = %v", err)
	}
}

// flakySize は、2 回目から大きさを得られない端末。
type flakySize struct {
	*fakeTerm
	calls int
	err   error
}

func (s *flakySize) Size() (int, int, error) {
	s.calls++
	if s.calls > 1 {
		return 0, 0, s.err
	}
	return s.fakeTerm.Size()
}

// TestStatsAndNoColor は、フレームの統計と NO_COLOR を確かめる。
func TestStatsAndNoColor(t *testing.T) {
	t.Parallel()
	f := newFakeTerm(20, 5)
	l := New(f)
	l.SetNoColor(true)
	var st Stats
	r := &recorder{
		onDraw: func(s *screen.Screen) {
			s.Put(screen.Region{W: 20, H: 5}, 0, 0, "red", screen.Style{FG: screen.ColorRed})
		},
		onEvent: func(l *Loop, ev Event) bool {
			st = l.Stats()
			return false
		},
	}
	f.send("x")
	if err := l.Run(r); err != nil {
		t.Fatal(err)
	}
	if st.Frames != 1 || st.Bytes == 0 {
		t.Errorf("stats = %+v, want 1 frame with bytes", st)
	}
	if strings.Contains(f.out.String(), "31") {
		t.Errorf("NO_COLOR output has a color: %q", f.out.String())
	}
	if !strings.Contains((&PanicError{Value: "v", Stack: []byte("stack")}).Error(), "panic: v") ||
		!strings.Contains((&SignalError{Signal: syscall.SIGTERM}).Error(), "terminated") {
		t.Error("error strings")
	}
}

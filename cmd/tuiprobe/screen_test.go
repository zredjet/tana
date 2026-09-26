package main

import (
	"errors"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/term"
	"github.com/zredjet/tana/internal/tui"
)

// screenTerm は、確認用の画面のテストの端末（tui.Terminal）。
type screenTerm struct {
	in chan term.Input

	mu         sync.Mutex
	cols, rows int
	out        strings.Builder
	restored   int
}

func newScreenTerm(cols, rows int) *screenTerm {
	return &screenTerm{in: make(chan term.Input, 100), cols: cols, rows: rows}
}

func (s *screenTerm) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.out.Write(p)
}

func (s *screenTerm) Size() (int, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cols, s.rows, nil
}

func (s *screenTerm) setSize(c, r int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cols, s.rows = c, r
}

func (s *screenTerm) StartInput() <-chan term.Input { return s.in }

func (s *screenTerm) Restore() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restored++
	return nil
}

func (s *screenTerm) send(keys ...string) {
	for _, k := range keys {
		s.in <- term.Input{Time: time.Now(), Bytes: []byte(k)}
	}
}

func newScreenSection() *screenSection {
	return &screenSection{sectionHeader: sectionHeader{Cols: 80, Rows: 24}}
}

// TestScreenNavigateAndEdit は、一覧の移動・ペインの切り替え・入力欄（lineedit）の確定と取り消しを確かめる。
func TestScreenNavigateAndEdit(t *testing.T) {
	t.Parallel()
	f := newScreenTerm(80, 24)
	res := newScreenSection()
	l := tui.New(f)
	h := newProbeScreen(res, l)
	f.send("\x1b[B", "\x1b[B", "\x1b[B", "\x1b[6~", "\x1b[A") // Down×3、PgDn、Up
	f.send("\t", "i", "x", "\x7f", "あ", "\r")                 // 右のペインで入力欄を開き、x を消して あ を入れて確定
	f.send("i", "\x1b[200~貼り付け\x1b[201~", "\x1b")             // 貼り付けてから Esc で取り消す（Esc は待ち時間の後で確定する）
	go func() {
		time.Sleep(2 * 50 * time.Millisecond) // Esc の待ち時間より後に q を送る（短くても長くても結果は同じ）
		f.send("q")
	}()
	if err := l.Run(h); err != nil {
		t.Fatal(err)
	}
	if res.Exit != "quit" {
		t.Errorf("exit = %q", res.Exit)
	}
	listH := 24 - 5
	if got, want := h.panes[0].cursor, 3+listH-1; got != want {
		t.Errorf("left cursor = %d, want %d", got, want)
	}
	if h.active != 1 {
		t.Errorf("active pane = %d, want 1", h.active)
	}
	if len(res.Inputs) != 2 || res.Inputs[0].Result != "confirmed" || res.Inputs[0].Text != "報告書_2026年度あ.docx" ||
		res.Inputs[1].Result != "cancelled" || res.Inputs[1].Text != "報告書_2026年度貼り付け.docx" {
		t.Errorf("inputs = %+v", res.Inputs)
	}
	if len(res.Keys) == 0 || res.Keys[0].Event != "Down" || res.Keys[0].Raw != "1b5b42" {
		t.Errorf("keys = %+v", res.Keys)
	}
	if f.restored != 1 {
		t.Errorf("Restore called %d times", f.restored)
	}
}

// TestScreenPanics は、イベントループと作業用の goroutine の panic で、端末を戻して panic として記録することを確かめる（T1。VU1）。
func TestScreenPanics(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"!", "@"} {
		f := newScreenTerm(80, 24)
		f.send(key)
		res, err := runScreen(f, newScreenSection().sectionHeader, selfTest{})
		var pe *tui.PanicError
		if !errors.As(err, &pe) || res.Exit != "panic" || !strings.Contains(res.Error, "(test)") {
			t.Errorf("%s: err = %v, result exit %q error %q", key, err, res.Exit, res.Error)
		}
		if f.restored != 1 {
			t.Errorf("%s: Restore called %d times", key, f.restored)
		}
	}
}

// TestScreenSignal は、シグナルで端末を戻して終わり、signal として記録することを確かめる（T1）。
func TestScreenSignal(t *testing.T) {
	t.Parallel()
	f := newScreenTerm(80, 24)
	f.in <- term.Input{Time: time.Now(), Signal: syscall.SIGTERM}
	res, err := runScreen(f, newScreenSection().sectionHeader, selfTest{})
	var se *tui.SignalError
	if !errors.As(err, &se) || res.Exit != "signal" || f.restored != 1 {
		t.Errorf("err = %v, exit %q, restored %d", err, res.Exit, f.restored)
	}
}

// stopWhen は、cond が満たされたら終わるハンドラ。
type stopWhen struct {
	*probeScreen
	cond func() bool
}

func (s stopWhen) Handle(l *tui.Loop, ev tui.Event) bool {
	return s.probeScreen.Handle(l, ev) && !s.cond()
}

// TestScreenResizeAndPoll は、大きさの変更のイベントと、見回りで見つけた変化の両方を記録することを確かめる（VT4）。
func TestScreenResizeAndPoll(t *testing.T) {
	t.Parallel()
	f := newScreenTerm(80, 24)
	res := newScreenSection()
	l := tui.New(f)
	h := newProbeScreen(res, l)
	stop := make(chan struct{})
	defer close(stop)
	pollSize(l, f, 80, 24, time.Millisecond, stop)
	f.setSize(100, 30)
	f.in <- term.Input{Time: time.Now(), Resize: true}
	sources := func() map[string]bool {
		m := map[string]bool{}
		for _, r := range res.Resizes {
			if r.Cols == 100 && r.Rows == 30 {
				m[r.Source] = true
			}
		}
		return m
	}
	if err := l.Run(stopWhen{h, func() bool { m := sources(); return m["event"] && m["poll"] }}); err != nil {
		t.Fatal(err)
	}
}

// TestScreenProgressAndSmall は、進捗が作業用の goroutine から届いて終わることと、小さすぎる端末で案内だけを描くことを確かめる。
func TestScreenProgressAndSmall(t *testing.T) {
	t.Parallel()
	f := newScreenTerm(30, 8)
	res := newScreenSection()
	l := tui.New(f)
	h := newProbeScreen(res, l)
	h.progressStep = 0
	f.send("p")
	started := false
	if err := l.Run(stopWhen{h, func() bool {
		started = started || h.running
		return started && !h.running
	}}); err != nil {
		t.Fatal(err)
	}
	if got := h.progress.Load(); got != 1000 {
		t.Errorf("progress = %d, want 1000", got)
	}
	if !strings.Contains(f.out.String(), "端末が小さすぎます") {
		t.Error("small terminal message not drawn")
	}
}

// TestScreenSelfTest は、-selftest の panic と作業用の goroutine の panic が、キーを押さずに起き、端末を戻して記録されることを確かめる。
func TestScreenSelfTest(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"panic", "worker-panic"} {
		f := newScreenTerm(80, 24)
		res, err := runScreen(f, newScreenSection().sectionHeader, selfTest{kind: kind, after: time.Millisecond})
		var pe *tui.PanicError
		if !errors.As(err, &pe) || res.Exit != "panic" || res.SelfTest != kind || !strings.Contains(res.Error, "self-test") {
			t.Errorf("%s: err = %v, result %+v", kind, err, res)
		}
		if f.restored != 1 {
			t.Errorf("%s: Restore called %d times", kind, f.restored)
		}
	}
}

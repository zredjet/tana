//go:build darwin || linux

package term

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openPty は疑似端末を作り、親と子の fd を返す。テストの終わりに閉じる。
func openPty(t *testing.T) (master, slave int) {
	t.Helper()
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Skipf("cannot open /dev/ptmx: %v", err)
	}
	t.Cleanup(func() { unix.Close(master) })
	name := ptsName(t, master)
	slave, err = unix.Open(name, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() { unix.Close(slave) })
	return master, slave
}

// readMaster は、疑似端末の親から n バイトを読む。timeout までに届かなければ失敗する。
func readMaster(t *testing.T, master, n int, timeout time.Duration) []byte {
	t.Helper()
	var got []byte
	deadline := time.Now().Add(timeout)
	buf := make([]byte, 1024)
	for len(got) < n {
		left := time.Until(deadline)
		if left <= 0 {
			t.Fatalf("read from pty master: got %q, want %d bytes", got, n)
		}
		var rset unix.FdSet
		rset.Set(master)
		tv := unix.NsecToTimeval(left.Nanoseconds())
		if _, err := unix.Select(master+1, &rset, nil, nil, &tv); err != nil {
			if err == unix.EINTR {
				continue
			}
			t.Fatal(err)
		}
		if !rset.IsSet(master) {
			continue
		}
		m, err := unix.Read(master, buf)
		if err != nil {
			t.Fatalf("read from pty master: %v", err)
		}
		got = append(got, buf[:m]...)
	}
	return got
}

// getTermios は fd の termios を返す。PENDIN は除く。
// PENDIN は設定ではなく状態の印で、macOS のカーネルは ICANON を付け直すたびに立てる（未処理の入力を処理し直す印）。
func getTermios(t *testing.T, fd int) unix.Termios {
	t.Helper()
	tio, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		t.Fatal(err)
	}
	tio.Lflag &^= unix.PENDIN
	return *tio
}

// startOn は、疑似端末の子 slave で Term を始める。
func startOn(t *testing.T, slave int, opts Options) *Term {
	t.Helper()
	sys, err := newSys(slave)
	if err != nil {
		t.Fatal(err)
	}
	tm, err := start(sys, opts)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}

// TestRawModeAndRestore は、開始時に raw モードと各モードの制御シーケンスを設定し、
// Restore でそれらを戻して termios が開始前と同じになることを確かめる（T1）。
func TestRawModeAndRestore(t *testing.T) {
	master, slave := openPty(t)
	before := getTermios(t, slave)
	tm := startOn(t, slave, Options{AltScreen: true, HideCursor: true, NoAutoWrap: true, BracketedPaste: true})

	raw := getTermios(t, slave)
	if raw.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG|unix.IEXTEN) != 0 {
		t.Errorf("lflag = %#x, want ICANON, ECHO, ISIG, IEXTEN off", raw.Lflag)
	}
	if raw.Iflag&(unix.IXON|unix.ICRNL) != 0 {
		t.Errorf("iflag = %#x, want IXON and ICRNL off", raw.Iflag)
	}
	if raw.Oflag&unix.OPOST != 0 {
		t.Errorf("oflag = %#x, want OPOST off", raw.Oflag)
	}
	if raw.Cc[unix.VMIN] != 1 || raw.Cc[unix.VTIME] != 0 {
		t.Errorf("VMIN, VTIME = %d, %d, want 1, 0", raw.Cc[unix.VMIN], raw.Cc[unix.VTIME])
	}
	const enter = "\x1b[?1049h\x1b[?25l\x1b[?7l\x1b[?2004h"
	if got := readMaster(t, master, len(enter), 2*time.Second); string(got) != enter {
		t.Errorf("start wrote %q, want %q", got, enter)
	}

	if err := tm.Restore(); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	const leave = "\x1b[?2004l\x1b[?7h\x1b[?25h\x1b[?1049l"
	if got := readMaster(t, master, len(leave), 2*time.Second); string(got) != leave {
		t.Errorf("Restore wrote %q, want %q", got, leave)
	}
	if after := getTermios(t, slave); after != before {
		t.Errorf("termios after Restore = %+v, want %+v", after, before)
	}

	// 2 回目以降の Restore は何もしない。戻した後は書けない。
	if err := tm.Restore(); err != nil {
		t.Errorf("second Restore: %v", err)
	}
	if _, err := tm.Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Errorf("Write after Restore: err = %v, want ErrClosed", err)
	}
	if err := tm.SetBracketedPaste(true); !errors.Is(err, ErrClosed) {
		t.Errorf("SetBracketedPaste after Restore: err = %v, want ErrClosed", err)
	}
}

// TestRestoreConcurrent は、複数の goroutine から同時に Restore を呼んでも、1 回だけ戻すことを確かめる（T1）。
func TestRestoreConcurrent(t *testing.T) {
	master, slave := openPty(t)
	before := getTermios(t, slave)
	tm := startOn(t, slave, Options{HideCursor: true})
	readMaster(t, master, len("\x1b[?25l"), 2*time.Second)
	in := tm.StartInput()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := tm.Restore(); err != nil {
				t.Errorf("Restore: %v", err)
			}
		})
	}
	wg.Wait()
	if got := readMaster(t, master, len("\x1b[?25h"), 2*time.Second); string(got) != "\x1b[?25h" {
		t.Errorf("Restore wrote %q, want one \\x1b[?25h", got)
	}
	if after := getTermios(t, slave); after != before {
		t.Errorf("termios after Restore = %+v, want %+v", after, before)
	}
	for range in {
	}
}

// TestStartFailsOnHighFd は、select で待てない大きな fd（FD_SETSIZE 以上）の端末では始めず、
// termios を変えないことを確かめる（読み取りの goroutine が FdSet で panic し、端末を戻せなくなるため。T1）。
func TestStartFailsOnHighFd(t *testing.T) {
	_, slave := openPty(t)
	before := getTermios(t, slave)
	high := unix.FD_SETSIZE + 100
	if err := unix.Dup2(slave, high); err != nil {
		t.Skipf("cannot dup the pty to fd %d: %v", high, err)
	}
	defer unix.Close(high)
	if _, err := newSys(high); err == nil {
		t.Fatalf("newSys(fd %d): err = nil, want an error", high)
	}
	if after := getTermios(t, slave); after != before {
		t.Errorf("termios changed by a failed newSys: %+v, want %+v", after, before)
	}
}

// TestStartFailsOnNonTerminal は、端末でない fd では始めないことを確かめる。
func TestStartFailsOnNonTerminal(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "file"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := newSys(int(f.Fd())); err == nil {
		t.Error("newSys on a regular file: err = nil, want an error")
	}
}

// TestInput は、端末に書いた入力が、読んだ順にそのまま届くことを確かめる。
func TestInput(t *testing.T) {
	master, slave := openPty(t)
	tm := startOn(t, slave, Options{})
	defer tm.Restore()
	in := tm.StartInput()
	if in2 := tm.StartInput(); in2 != in {
		t.Error("StartInput returned a different channel on the second call")
	}
	// Ctrl+C（0x03）はシグナルにならず、Enter（CR）は LF に変わらずに届く。
	want := []byte("a\x03\r\x1b[Aあ\xff")
	if _, err := unix.Write(master, want); err != nil {
		t.Fatal(err)
	}
	var got []byte
	timeout := time.After(2 * time.Second)
	for len(got) < len(want) {
		select {
		case x, ok := <-in:
			if !ok {
				t.Fatalf("input closed early; got %q", got)
			}
			if x.Err != nil {
				t.Fatalf("input error: %v", x.Err)
			}
			if x.Time.IsZero() || len(x.Records) != 0 {
				t.Errorf("input %+v: want Time and no Records", x)
			}
			got = append(got, x.Bytes...)
		case <-timeout:
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestRestoreStopsReader は、入力を待っている読み取りの goroutine を、Restore が止めることを確かめる。
func TestRestoreStopsReader(t *testing.T) {
	_, slave := openPty(t)
	tm := startOn(t, slave, Options{})
	in := tm.StartInput()
	done := make(chan error, 1)
	go func() { done <- tm.Restore() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Restore: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Restore did not return while the reader was waiting for input")
	}
	if _, ok := <-in; ok {
		t.Error("input channel still open after Restore")
	}
}

// TestStartInputAfterRestore は、Restore の後の StartInput が、閉じたチャネルを返すことを確かめる。
func TestStartInputAfterRestore(t *testing.T) {
	_, slave := openPty(t)
	tm := startOn(t, slave, Options{})
	if err := tm.Restore(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-tm.StartInput():
		if ok {
			t.Error("StartInput after Restore delivered input")
		}
	case <-time.After(2 * time.Second):
		t.Error("StartInput after Restore returned a channel that is not closed")
	}
}

// TestWriteAndSize は、書いたバイト列が変換されずに届くこと（OPOST なし）と、大きさを得られることを確かめる。
func TestWriteAndSize(t *testing.T) {
	master, slave := openPty(t)
	if err := unix.IoctlSetWinsize(master, unix.TIOCSWINSZ, &unix.Winsize{Row: 40, Col: 120}); err != nil {
		t.Fatal(err)
	}
	tm := startOn(t, slave, Options{})
	defer tm.Restore()
	if cols, rows, err := tm.Size(); err != nil || cols != 120 || rows != 40 {
		t.Errorf("Size = %d, %d, %v, want 120, 40", cols, rows, err)
	}
	want := "あ\n\r\x1b[1;1H\xff"
	if _, err := tm.Write([]byte(want)); err != nil {
		t.Fatal(err)
	}
	if err := tm.QueryCursorPosition(); err != nil {
		t.Fatal(err)
	}
	want += "\x1b[6n"
	if got := readMaster(t, master, len(want), 2*time.Second); string(got) != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
	if err := tm.SetBracketedPaste(true); err != nil {
		t.Fatal(err)
	}
	readMaster(t, master, len(seqBracketedPasteOn), 2*time.Second)
	// SetBracketedPaste で有効にしたものは、Restore で無効に戻す。
	if err := tm.Restore(); err != nil {
		t.Fatal(err)
	}
	if got := readMaster(t, master, len(seqBracketedPasteOff), 2*time.Second); string(got) != seqBracketedPasteOff {
		t.Errorf("Restore wrote %q, want %q", got, seqBracketedPasteOff)
	}
}

func TestInfo(t *testing.T) {
	_, slave := openPty(t)
	tm := startOn(t, slave, Options{})
	defer tm.Restore()
	info := tm.Info()
	if info["os_version"] == "" || info["termios_orig"] == "" {
		t.Errorf("Info = %v, want os_version and termios_orig", info)
	}
}

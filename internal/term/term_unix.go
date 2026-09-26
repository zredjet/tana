//go:build darwin || linux

package term

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// resizeSignals は、端末の大きさの変更を知らせるシグナル。
func resizeSignals() []os.Signal { return []os.Signal{unix.SIGWINCH} }

func isResizeSignal(sig os.Signal) bool { return sig == unix.SIGWINCH }

// exitSignals は、Options.Signals で受け取るシグナル。
func exitSignals() []os.Signal { return []os.Signal{unix.SIGINT, unix.SIGTERM, unix.SIGHUP} }

// sysTerm は Unix の端末の状態。
type sysTerm struct {
	fd           int
	ownFd        bool // fd を Open で開いた（restore で閉じる）
	orig         unix.Termios
	wakeR, wakeW int // 読み取りの goroutine を select から起こすためのパイプ
}

func openSys(Options) (*sysTerm, error) {
	fd, err := retryEINTR(func() (int, error) {
		return unix.Open("/dev/tty", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	})
	if err != nil {
		return nil, fmt.Errorf("term: open /dev/tty: %w", err)
	}
	s, err := newSys(fd)
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	s.ownFd = true
	return s, nil
}

// newSys は fd の端末を raw モードにする。fd は閉じない（呼び出し側が持つ）。
// 読み取りは select で待つので、FD_SETSIZE 以上の fd は扱えない（FdSet の外になり、読み取りの goroutine が panic する）。
func newSys(fd int) (*sysTerm, error) {
	if fd < 0 || fd >= unix.FD_SETSIZE {
		return nil, fmt.Errorf("term: fd %d cannot be used with select (FD_SETSIZE %d)", fd, unix.FD_SETSIZE)
	}
	orig, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil, fmt.Errorf("term: not a terminal: %w", err)
	}
	var p [2]int
	if err := unix.Pipe(p[:]); err != nil {
		return nil, fmt.Errorf("term: pipe: %w", err)
	}
	unix.CloseOnExec(p[0])
	unix.CloseOnExec(p[1])
	if p[0] >= unix.FD_SETSIZE || p[1] >= unix.FD_SETSIZE {
		unix.Close(p[0])
		unix.Close(p[1])
		return nil, fmt.Errorf("term: pipe fds %d, %d cannot be used with select", p[0], p[1])
	}
	s := &sysTerm{fd: fd, orig: *orig, wakeR: p[0], wakeW: p[1]}
	raw := makeRaw(*orig)
	if err := setTermios(fd, &raw); err != nil {
		unix.Close(p[0])
		unix.Close(p[1])
		return nil, fmt.Errorf("term: set raw mode: %w", err)
	}
	return s, nil
}

// makeRaw は cfmakeraw(3) と同じ設定を返す。
// Ctrl+C・Ctrl+Z・Ctrl+\ をシグナルにせず（ISIG）、Ctrl+S・Ctrl+Q で出力を止めず（IXON）、
// Enter の CR を LF に変えず（ICRNL）、出力の LF を CRLF に変えない（OPOST）。
func makeRaw(t unix.Termios) unix.Termios {
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB
	t.Cflag |= unix.CS8
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	return t
}

func setTermios(fd int, t *unix.Termios) error {
	_, err := retryEINTR(func() (int, error) { return 0, unix.IoctlSetTermios(fd, ioctlSetTermios, t) })
	return err
}

func (s *sysTerm) write(p []byte) error {
	for len(p) > 0 {
		n, err := unix.Write(s.fd, p)
		switch {
		case err == unix.EINTR:
			continue
		case err == unix.EAGAIN:
			// ノンブロッキングの fd で出力のバッファが一杯。書けるようになるまで select で待つ（空回りしない）。
			var wset unix.FdSet
			wset.Set(s.fd)
			if _, err := unix.Select(s.fd+1, nil, &wset, nil, nil); err != nil && err != unix.EINTR {
				return fmt.Errorf("term: select for write: %w", err)
			}
			continue
		case err != nil:
			return fmt.Errorf("term: write: %w", err)
		case n == 0:
			return fmt.Errorf("term: write: %w", io.ErrShortWrite)
		}
		p = p[n:]
	}
	return nil
}

func (s *sysTerm) size() (int, int, error) {
	ws, err := unix.IoctlGetWinsize(s.fd, unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, fmt.Errorf("term: TIOCGWINSZ: %w", err)
	}
	return int(ws.Col), int(ws.Row), nil
}

// readLoop は、stop が閉じられて wake が呼ばれるまで、端末から読んで send に渡す。
// ブロックする read で止められなくならないように、select で端末とパイプの両方を待つ
// （macOS の poll は端末のデバイスに使えないため select を使う）。
func (s *sysTerm) readLoop(send func(Input) bool, stop <-chan struct{}) error {
	buf := make([]byte, 4096)
	for {
		select {
		case <-stop:
			return nil
		default:
		}
		var rset unix.FdSet
		rset.Zero()
		rset.Set(s.fd)
		rset.Set(s.wakeR)
		if _, err := unix.Select(max(s.fd, s.wakeR)+1, &rset, nil, nil, nil); err != nil {
			if err == unix.EINTR {
				continue
			}
			return fmt.Errorf("term: select: %w", err)
		}
		if rset.IsSet(s.wakeR) {
			return nil
		}
		if !rset.IsSet(s.fd) {
			continue
		}
		n, err := unix.Read(s.fd, buf)
		now := time.Now()
		switch {
		case err == unix.EINTR || err == unix.EAGAIN:
			continue
		case err != nil:
			return fmt.Errorf("term: read: %w", err)
		case n == 0:
			return io.EOF // 端末が閉じられた
		}
		if !send(Input{Time: now, Bytes: append([]byte(nil), buf[:n]...)}) {
			return nil
		}
	}
}

func (s *sysTerm) wake() {
	_, _ = unix.Write(s.wakeW, []byte{0})
}

func (s *sysTerm) restore() error {
	err := setTermios(s.fd, &s.orig)
	if err != nil {
		err = fmt.Errorf("term: restore termios: %w", err)
	}
	unix.Close(s.wakeR)
	unix.Close(s.wakeW)
	if s.ownFd {
		err = errors.Join(err, unix.Close(s.fd))
	}
	return err
}

func (s *sysTerm) info() map[string]string {
	m := osInfo()
	m["termios_orig"] = fmt.Sprintf("iflag=%#x oflag=%#x cflag=%#x lflag=%#x", s.orig.Iflag, s.orig.Oflag, s.orig.Cflag, s.orig.Lflag)
	return m
}

// retryEINTR は、f が EINTR で失敗したら呼び直す。
func retryEINTR(f func() (int, error)) (int, error) {
	for {
		n, err := f()
		if err != unix.EINTR {
			return n, err
		}
	}
}

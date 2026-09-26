package term

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// resizeSignals は空。Windows では、大きさの変更はレコードで届く（VT4）。
func resizeSignals() []os.Signal { return nil }

func isResizeSignal(os.Signal) bool { return false }

// exitSignals は、Options.Signals で受け取るシグナル。Go は、Ctrl+Break を os.Interrupt として、
// コンソールを閉じる通知・ログオフ・シャットダウンを SIGTERM として届ける（Ctrl+C はキーとして届くので、シグナルにならない）。
func exitSignals() []os.Signal { return []os.Signal{os.Interrupt, syscall.SIGTERM} }

// x/sys/windows にない関数（tui §3）。
var (
	procReadConsoleInputW     = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReadConsoleInputW")
	procGetKeyboardLayoutName = windows.NewLazySystemDLL("user32.dll").NewProc("GetKeyboardLayoutNameW")
)

const cpUTF8 = 65001

// sysTerm は Windows のコンソールの状態。
type sysTerm struct {
	in, out         windows.Handle // CONIN$、CONOUT$
	origIn, origOut uint32         // 開始前のコンソールモード
	setIn, setOut   uint32         // 開始時に設定したコンソールモード
	origOutCP       uint32
	cpChanged       bool
	method          OutputMethod
	cancel          windows.Handle // 読み取りの goroutine を止めるイベント
	enc             utf16Encoder
	dec             recordDecoder // 読み取りの goroutine だけが使う
}

func openConsole(name string) (windows.Handle, error) {
	return windows.CreateFile(windows.StringToUTF16Ptr(name),
		windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
}

func openSys(opts Options) (_ *sysTerm, err error) {
	s := &sysTerm{method: opts.Output}
	// 失敗したら、それまでに変えたものを戻して閉じる。
	defer func() {
		if err != nil {
			err = errors.Join(err, s.restore())
		}
	}()
	// 開けたハンドルだけを s に入れる（CreateFile は失敗すると 0 ではなく InvalidHandle を返す。restore は 0 かどうかで判断する）。
	in, err := openConsole("CONIN$")
	if err != nil {
		return nil, fmt.Errorf("term: open CONIN$: %w", err)
	}
	s.in = in
	out, err := openConsole("CONOUT$")
	if err != nil {
		return nil, fmt.Errorf("term: open CONOUT$: %w", err)
	}
	s.out = out
	cancel, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("term: CreateEvent: %w", err)
	}
	s.cancel = cancel
	if err = windows.GetConsoleMode(s.in, &s.origIn); err != nil {
		return nil, fmt.Errorf("term: GetConsoleMode(in): %w", err)
	}
	if err = windows.GetConsoleMode(s.out, &s.origOut); err != nil {
		return nil, fmt.Errorf("term: GetConsoleMode(out): %w", err)
	}
	if s.origOutCP, err = windows.GetConsoleOutputCP(); err != nil {
		return nil, fmt.Errorf("term: GetConsoleOutputCP: %w", err)
	}

	// 入力: Ctrl+C をキーとして受け取り（ENABLE_PROCESSED_INPUT を外す）、行の入力とエコーをやめ、
	// 大きさの変更のイベントを受け取る。マウスのイベントは受け取らない（tui §1）。
	s.setIn = s.origIn | windows.ENABLE_WINDOW_INPUT
	s.setIn &^= windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_MOUSE_INPUT
	// 簡易編集モードは ENABLE_EXTENDED_FLAGS と一緒にしか設定できない。開始前のモードに ENABLE_EXTENDED_FLAGS がなければ、
	// 戻すときに簡易編集モードの状態を戻せないので、変えない。
	if s.origIn&windows.ENABLE_EXTENDED_FLAGS != 0 {
		s.setIn &^= windows.ENABLE_QUICK_EDIT_MODE
	}
	if opts.VTInput {
		s.setIn |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	} else {
		s.setIn &^= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	}
	if err = windows.SetConsoleMode(s.in, s.setIn); err != nil {
		return nil, fmt.Errorf("term: SetConsoleMode(in, %#x): %w", s.setIn, err)
	}
	// 出力: VT のシーケンスを解釈させ、LF で行の始めに戻さない（Unix の raw モードで OPOST を外すのと同じ）。
	s.setOut = s.origOut | windows.ENABLE_PROCESSED_OUTPUT | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.DISABLE_NEWLINE_AUTO_RETURN
	if err = windows.SetConsoleMode(s.out, s.setOut); err != nil {
		return nil, fmt.Errorf("term: SetConsoleMode(out, %#x) (VT not supported?): %w", s.setOut, err)
	}
	if s.method == OutputUTF8CodePage {
		if err = windows.SetConsoleOutputCP(cpUTF8); err != nil {
			return nil, fmt.Errorf("term: SetConsoleOutputCP(65001): %w", err)
		}
		s.cpChanged = true
	}
	return s, nil
}

func (s *sysTerm) write(p []byte) error {
	if s.method != OutputWriteConsoleW {
		for len(p) > 0 {
			var n uint32
			if err := windows.WriteFile(s.out, p, &n, nil); err != nil {
				return fmt.Errorf("term: WriteFile: %w", err)
			}
			p = p[n:]
		}
		return nil
	}
	u := s.enc.encode(p)
	for len(u) > 0 {
		// 大きすぎる書き込みで失敗しないように分ける。サロゲートの対は分けない。
		c := min(len(u), 8192)
		if c < len(u) && utf16IsHighSurrogate(u[c-1]) {
			c--
		}
		var n uint32
		if err := windows.WriteConsole(s.out, &u[0], uint32(c), &n, nil); err != nil {
			return fmt.Errorf("term: WriteConsoleW: %w", err)
		}
		u = u[n:]
	}
	return nil
}

func utf16IsHighSurrogate(u uint16) bool { return 0xd800 <= u && u < 0xdc00 }

func (s *sysTerm) size() (int, int, error) {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(s.out, &info); err != nil {
		return 0, 0, fmt.Errorf("term: GetConsoleScreenBufferInfo: %w", err)
	}
	return int(info.Window.Right-info.Window.Left) + 1, int(info.Window.Bottom-info.Window.Top) + 1, nil
}

// readLoop は、stop が閉じられて wake が呼ばれるまで、ReadConsoleInputW で読んで send に渡す。
// ブロックする ReadConsoleInputW で止められなくならないように、入力とイベントの両方を待ってから読む。
func (s *sysTerm) readLoop(send func(Input) bool, stop <-chan struct{}) error {
	var buf [128][inputRecordSize]byte
	for {
		select {
		case <-stop:
			return nil
		default:
		}
		ev, err := windows.WaitForMultipleObjects([]windows.Handle{s.cancel, s.in}, false, windows.INFINITE)
		if err != nil {
			return fmt.Errorf("term: WaitForMultipleObjects: %w", err)
		}
		if ev == windows.WAIT_OBJECT_0 {
			return nil
		}
		var avail uint32
		if err := windows.GetNumberOfConsoleInputEvents(s.in, &avail); err != nil {
			return fmt.Errorf("term: GetNumberOfConsoleInputEvents: %w", err)
		}
		if avail == 0 {
			continue
		}
		var n uint32
		r1, _, e := procReadConsoleInputW.Call(uintptr(s.in), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n)))
		now := time.Now()
		if r1 == 0 {
			return fmt.Errorf("term: ReadConsoleInputW: %w", e)
		}
		recs := make([]Record, n)
		for i := range recs {
			recs[i] = decodeRecord(buf[i][:])
		}
		text, resize := s.dec.decode(recs)
		if !send(Input{Time: now, Bytes: text, Records: recs, Resize: resize}) {
			return nil
		}
	}
}

func (s *sysTerm) wake() {
	if s.cancel != 0 {
		_ = windows.SetEvent(s.cancel)
	}
}

func (s *sysTerm) restore() error {
	var errs []error
	if s.in != 0 && s.setIn != 0 {
		errs = append(errs, windows.SetConsoleMode(s.in, s.origIn))
	}
	if s.out != 0 && s.setOut != 0 {
		errs = append(errs, windows.SetConsoleMode(s.out, s.origOut))
	}
	if s.cpChanged {
		errs = append(errs, windows.SetConsoleOutputCP(s.origOutCP))
	}
	for _, h := range []windows.Handle{s.in, s.out, s.cancel} {
		if h != 0 {
			errs = append(errs, windows.CloseHandle(h))
		}
	}
	return errors.Join(errs...)
}

func (s *sysTerm) info() map[string]string {
	v := windows.RtlGetVersion()
	m := map[string]string{
		"os_version":        fmt.Sprintf("Windows %d.%d build %d", v.MajorVersion, v.MinorVersion, v.BuildNumber),
		"console_in_orig":   fmt.Sprintf("%#x", s.origIn),
		"console_in_set":    fmt.Sprintf("%#x", s.setIn),
		"console_out_orig":  fmt.Sprintf("%#x", s.origOut),
		"console_out_set":   fmt.Sprintf("%#x", s.setOut),
		"output_cp_orig":    fmt.Sprint(s.origOutCP),
		"output_method":     s.method.String(),
		"keyboard_layout":   keyboardLayout(),
		"console_input_cp":  "",
		"console_output_cp": "",
	}
	if cp, err := windows.GetConsoleCP(); err == nil {
		m["console_input_cp"] = fmt.Sprint(cp)
	}
	if cp, err := windows.GetConsoleOutputCP(); err == nil {
		m["console_output_cp"] = fmt.Sprint(cp)
	}
	return m
}

func keyboardLayout() string {
	var buf [9]uint16 // KL_NAMELENGTH
	if r1, _, _ := procGetKeyboardLayoutName.Call(uintptr(unsafe.Pointer(&buf[0]))); r1 == 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:])
}

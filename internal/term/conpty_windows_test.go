package term

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// conptyChildEnv は、疑似コンソールの中で動かす子のテストに、結果のファイルのパスを渡す環境変数。
const conptyChildEnv = "TANA_TERM_CONPTY_CHILD"

// TestPseudoConsole は、Windows の疑似コンソール（CreatePseudoConsole）の中で term を動かし、次のことを確かめる（VT3。tui §8）。
//   - 開始したときのコンソールモード（VT の入力モードなど）と、終了した後にコンソールモードが開始前と同じこと（T1）
//   - 書いた入力が、keys に渡すバイト列として届くこと（Ctrl+C はキーとして届く。T3）
//   - 疑似コンソールの大きさを変えると、大きさの変更が届くこと
//
// 子のプロセス（このテストのバイナリの TestPseudoConsoleChild）を疑似コンソールに付けて動かし、結果をファイルに書かせる。
func TestPseudoConsole(t *testing.T) {
	if os.Getenv(conptyChildEnv) != "" {
		t.Skip("running as the child")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	result := filepath.Join(dir, "child.txt")
	t.Setenv(conptyChildEnv, result)

	var inR, inW, outR, outW windows.Handle
	if err := windows.CreatePipe(&inR, &inW, nil, 0); err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(inW)
	if err := windows.CreatePipe(&outR, &outW, nil, 0); err != nil {
		t.Fatal(err)
	}
	var hpc windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: 80, Y: 25}, inR, outW, 0, &hpc); err != nil {
		windows.CloseHandle(inR)
		windows.CloseHandle(outW)
		windows.CloseHandle(outR)
		t.Skipf("CreatePseudoConsole is not available: %v", err)
	}
	// 疑似コンソールは渡したハンドルを複製して持つので、こちらの分は閉じる。
	windows.CloseHandle(inR)
	windows.CloseHandle(outW)

	// 疑似コンソールの出力を読み続ける（読まないと、子の書き込みと ClosePseudoConsole が止まる）。
	var outMu sync.Mutex
	var output bytes.Buffer
	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		buf := make([]byte, 4096)
		for {
			var n uint32
			if err := windows.ReadFile(outR, buf, &n, nil); err != nil || n == 0 {
				return
			}
			outMu.Lock()
			output.Write(buf[:n])
			outMu.Unlock()
		}
	}()
	closed := false
	closeConsole := func() {
		if !closed {
			closed = true
			windows.ClosePseudoConsole(hpc)
			<-outDone
			windows.CloseHandle(outR)
		}
	}
	defer closeConsole()

	proc := startInPseudoConsole(t, hpc)
	defer windows.CloseHandle(proc)
	defer func() {
		if t.Failed() {
			windows.TerminateProcess(proc, 1)
			outMu.Lock()
			t.Logf("pseudo console output: %q", output.String())
			outMu.Unlock()
			if b, err := os.ReadFile(result); err == nil {
				t.Logf("child result:\n%s", b)
			}
		}
	}()

	lines := waitLines(t, result, 60*time.Second, func(l []string) bool { return has(l, "ready") || has(l, "error") })
	if has(lines, "error") {
		t.Fatalf("child failed to start: %q", lines)
	}

	// 入力: 文字、Ctrl+C、日本語、矢印（VT の入力モードでは、Mac の端末と同じシーケンスで届く）。
	const input = "ab\x03あ\x1b[A"
	writeAll(t, inW, []byte(input))
	lines = waitLines(t, result, 30*time.Second, func(l []string) bool { return strings.HasPrefix(string(received(l)), input) || has(l, "error") })

	// 大きさの変更。
	if err := windows.ResizePseudoConsole(hpc, windows.Coord{X: 100, Y: 30}); err != nil {
		t.Fatal(err)
	}
	lines = waitLines(t, result, 30*time.Second, func(l []string) bool { return has(l, "resize 100 30") || has(l, "error") })

	writeAll(t, inW, []byte("q"))
	lines = waitLines(t, result, 30*time.Second, func(l []string) bool { return has(l, "done") })
	if ev, err := windows.WaitForSingleObject(proc, 30*1000); err != nil || ev != windows.WAIT_OBJECT_0 {
		t.Errorf("child did not exit: %v %v", ev, err)
	}
	closeConsole()

	for _, l := range lines {
		if strings.HasPrefix(l, "error") {
			t.Errorf("child: %s", l)
		}
	}
	if got := string(received(lines)); !strings.HasPrefix(got, input) {
		t.Errorf("received %q, want %q first", got, input)
	}
	before, set, after := field(lines, "before"), field(lines, "set"), field(lines, "after")
	if before == "" || before != after {
		t.Errorf("console modes: before %q, after %q (T1)", before, after)
	}
	var in, out uint32
	if _, err := fmt.Sscanf(set, "%x %x", &in, &out); err != nil {
		t.Errorf("set modes %q: %v", set, err)
	}
	if in&windows.ENABLE_VIRTUAL_TERMINAL_INPUT == 0 || in&(windows.ENABLE_PROCESSED_INPUT|windows.ENABLE_LINE_INPUT|windows.ENABLE_ECHO_INPUT) != 0 || in&windows.ENABLE_WINDOW_INPUT == 0 {
		t.Errorf("input mode while open = %#x", in)
	}
	if out&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING == 0 || out&windows.DISABLE_NEWLINE_AUTO_RETURN == 0 {
		t.Errorf("output mode while open = %#x", out)
	}
	t.Logf("child result: %q", lines)
}

// TestPseudoConsoleChild は、TestPseudoConsole が疑似コンソールの中で動かす子。結果を 1 行ずつファイルに書く。
func TestPseudoConsoleChild(t *testing.T) {
	path := os.Getenv(conptyChildEnv)
	if path == "" {
		t.Skip("run by TestPseudoConsole inside a pseudo console")
	}
	out := func(format string, a ...any) {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		fmt.Fprintf(f, format+"\n", a...)
		f.Close()
	}
	in, o, err := consoleModes()
	if err != nil {
		out("error modes before: %v", err)
		return
	}
	out("before %x %x", in, o)
	tm, err := Open(Options{AltScreen: true, HideCursor: true, NoAutoWrap: true, BracketedPaste: true, VTInput: true})
	if err != nil {
		out("error open: %v", err)
		return
	}
	var mi, mo uint32
	windows.GetConsoleMode(tm.sys.in, &mi)
	windows.GetConsoleMode(tm.sys.out, &mo)
	out("set %x %x", mi, mo)
	if c, r, err := tm.Size(); err == nil {
		out("size %d %d", c, r)
	}
	ch := tm.StartInput()
	out("ready")
	deadline := time.After(90 * time.Second)
loop:
	for {
		select {
		case x, ok := <-ch:
			if !ok {
				out("error input closed")
				break loop
			}
			if x.Err != nil {
				out("error read: %v", x.Err)
				break loop
			}
			if len(x.Bytes) > 0 {
				out("bytes %x", x.Bytes)
			}
			if x.Resize {
				c, r, _ := tm.Size()
				out("resize %d %d", c, r)
			}
			if bytes.IndexByte(x.Bytes, 'q') >= 0 {
				break loop
			}
		case <-deadline:
			out("error timeout")
			break loop
		}
	}
	if err := tm.Restore(); err != nil {
		out("error restore: %v", err)
	}
	if in, o, err := consoleModes(); err != nil {
		out("error modes after: %v", err)
	} else {
		out("after %x %x", in, o)
	}
	out("done")
}

// consoleModes は、CONIN$ と CONOUT$ のコンソールモードを返す。
func consoleModes() (in, out uint32, err error) {
	for _, c := range []struct {
		name string
		mode *uint32
	}{{"CONIN$", &in}, {"CONOUT$", &out}} {
		h, err := openConsole(c.name)
		if err != nil {
			return 0, 0, err
		}
		err = windows.GetConsoleMode(h, c.mode)
		windows.CloseHandle(h)
		if err != nil {
			return 0, 0, err
		}
	}
	return in, out, nil
}

// startInPseudoConsole は、このテストのバイナリの TestPseudoConsoleChild を、疑似コンソール hpc に付けて始める。
func startInPseudoConsole(t *testing.T, hpc windows.Handle) windows.Handle {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	al, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		t.Fatal(err)
	}
	defer al.Delete()
	// PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE の値は HPCON そのもの（ポインタの大きさ）。
	if err := al.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, *(*unsafe.Pointer)(unsafe.Pointer(&hpc)), unsafe.Sizeof(hpc)); err != nil {
		t.Fatal(err)
	}
	si := &windows.StartupInfoEx{
		StartupInfo:             windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))},
		ProcThreadAttributeList: al.List(),
	}
	cmd, err := windows.UTF16PtrFromString(windows.ComposeCommandLine([]string{exe, "-test.run=^TestPseudoConsoleChild$", "-test.count=1"}))
	if err != nil {
		t.Fatal(err)
	}
	var pi windows.ProcessInformation
	if err := windows.CreateProcess(nil, cmd, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, nil, nil, &si.StartupInfo, &pi); err != nil {
		t.Fatal(err)
	}
	windows.CloseHandle(pi.Thread)
	return pi.Process
}

func writeAll(t *testing.T, h windows.Handle, b []byte) {
	t.Helper()
	for len(b) > 0 {
		var n uint32
		if err := windows.WriteFile(h, b, &n, nil); err != nil {
			t.Fatal(err)
		}
		b = b[n:]
	}
}

// waitLines は、子の結果のファイルの行が cond を満たすまで待つ。timeout までに満たさなければ失敗する。
// 子は別のプロセスなので、ファイルを読み直して待つ。
func waitLines(t *testing.T, path string, timeout time.Duration, cond func([]string) bool) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		b, _ := os.ReadFile(path)
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		if cond(lines) {
			return lines
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out; child result: %q", lines)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func has(lines []string, prefix string) bool {
	for _, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

// field は、prefix で始まる最初の行の、prefix の後ろを返す。
func field(lines []string, prefix string) string {
	for _, l := range lines {
		if rest, ok := strings.CutPrefix(l, prefix+" "); ok {
			return rest
		}
	}
	return ""
}

// received は、子が受け取ったバイト列を、順につないで返す。
func received(lines []string) []byte {
	var b []byte
	for _, l := range lines {
		if h, ok := strings.CutPrefix(l, "bytes "); ok {
			if d, err := hex.DecodeString(h); err == nil {
				b = append(b, d...)
			}
		}
	}
	return b
}

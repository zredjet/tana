package term

import (
	"errors"
	"os"
	"os/signal"
	"sync"
	"time"
)

// OutputMethod は Windows のコンソールへの書き方（tui §9 の VT1）。Unix では使わない。
type OutputMethod int

const (
	// OutputWriteConsoleW は、UTF-8 を UTF-16 に変えて WriteConsoleW で書く。コードページを変えない。
	OutputWriteConsoleW OutputMethod = iota
	// OutputUTF8CodePage は、出力のコードページを UTF-8（65001）にして、UTF-8 のまま WriteFile で書く。
	// コードページは Restore で元に戻す。
	OutputUTF8CodePage
	// OutputWriteFile は、コードページを変えずに UTF-8 のまま WriteFile で書く（比較用）。
	OutputWriteFile
)

// String は、tuiprobe の引数と結果の記録に使う名前を返す。
func (m OutputMethod) String() string {
	switch m {
	case OutputWriteConsoleW:
		return "writeconsole"
	case OutputUTF8CodePage:
		return "utf8cp"
	case OutputWriteFile:
		return "writefile"
	}
	return "unknown"
}

// ParseOutputMethod は String の名前から OutputMethod を返す。
func ParseOutputMethod(s string) (OutputMethod, error) {
	for _, m := range []OutputMethod{OutputWriteConsoleW, OutputUTF8CodePage, OutputWriteFile} {
		if m.String() == s {
			return m, nil
		}
	}
	return 0, errors.New("term: unknown output method " + s)
}

// Options は Open の設定。どれも Restore で元に戻す。
type Options struct {
	AltScreen      bool // 代替画面に切り替える
	HideCursor     bool // カーソルを隠す
	NoAutoWrap     bool // 自動改行（DECAWM）を切る
	BracketedPaste bool // bracketed paste を有効にする（SetBracketedPaste で後から切り替えられる）

	// VTInput は、Windows で VT の入力モード（ENABLE_VIRTUAL_TERMINAL_INPUT）を付ける（VT2）。Unix では使わない。
	VTInput bool
	// Output は Windows の出力の方法（VT1）。Unix では使わない。
	Output OutputMethod

	// Signals は、SIGINT・SIGTERM・SIGHUP を受けて、入力のチャネルに送る（Input.Signal）。
	// Windows では、コンソールを閉じる通知なども SIGTERM として届く（その後、約 5 秒で OS に終了させられる）。
	// 受け取り始めるのは StartInput を呼んだときで、Restore でやめる（読む者がいないのにシグナルを止めないように）。
	Signals bool
}

// Input は、1 回の読み取りで得た入力と、端末の大きさの変更・シグナルの知らせ。
type Input struct {
	Time time.Time
	// Bytes は keys に渡すバイト列。Unix は端末から読んだまま。
	// Windows は Records の文字を UTF-8 にしたもの（T3。サロゲートの対が読み取りをまたぐときは、後の読み取りに含める）。
	Bytes []byte
	// Records は、Windows で ReadConsoleInputW で読んだレコード（記録用。VT の入力モードでも、この形で届く）。
	Records []Record
	// Resize は、端末の大きさが変わった印（Unix: SIGWINCH、Windows: 大きさの変更のレコード）。新しい大きさは Size で得る。
	Resize bool
	// Signal は、受け取ったシグナル（Options.Signals のとき）。
	Signal os.Signal
	// Err は、読み取りに失敗したこと。この後は届かない。
	Err error
}

// ErrClosed は、Restore の後に Write などを呼んだ場合のエラー。
var ErrClosed = errors.New("term: terminal already restored")

// Term は、Open で状態を変えた端末。
type Term struct {
	sys  *sysTerm
	opts Options

	mu     sync.Mutex // 出力と、次の状態を守る
	closed bool
	paste  bool // bracketed paste を有効にしている

	inputOnce sync.Once
	input     <-chan Input
	stop      chan struct{} // 閉じると、読み取りの goroutine が止まる
	done      chan struct{} // 読み取りの goroutine が終わると閉じる

	restoreOnce sync.Once
	restoreErr  error
}

// 開始・終了のときに書く制御シーケンス。
const (
	seqAltScreenOn       = "\x1b[?1049h"
	seqAltScreenOff      = "\x1b[?1049l"
	seqHideCursor        = "\x1b[?25l"
	seqShowCursor        = "\x1b[?25h"
	seqAutoWrapOff       = "\x1b[?7l"
	seqAutoWrapOn        = "\x1b[?7h"
	seqBracketedPasteOn  = "\x1b[?2004h"
	seqBracketedPasteOff = "\x1b[?2004l"
	seqCursorPosition    = "\x1b[6n"
)

// Open は端末を raw モードにして、opts の状態に変える。
// Unix では /dev/tty を、Windows では CONIN$ と CONOUT$ を使う。
// 呼び出し側は、どの終わり方でも Restore を呼ぶこと（defer と、シグナルを受けたとき）。
func Open(opts Options) (*Term, error) {
	sys, err := openSys(opts)
	if err != nil {
		return nil, err
	}
	return start(sys, opts)
}

// start は、raw モードにした sys に、開始時の制御シーケンスを書く。
func start(sys *sysTerm, opts Options) (*Term, error) {
	t := &Term{sys: sys, opts: opts, stop: make(chan struct{}), done: make(chan struct{})}
	var seq string
	if opts.AltScreen {
		seq += seqAltScreenOn
	}
	if opts.HideCursor {
		seq += seqHideCursor
	}
	if opts.NoAutoWrap {
		seq += seqAutoWrapOff
	}
	if opts.BracketedPaste {
		seq += seqBracketedPasteOn
		t.paste = true
	}
	if seq != "" {
		if err := sys.write([]byte(seq)); err != nil {
			return nil, errors.Join(err, t.Restore())
		}
	}
	return t, nil
}

// Write は p をそのまま端末に書く。Restore の後は ErrClosed を返す。
func (t *Term) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, ErrClosed
	}
	if err := t.sys.write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// SetBracketedPaste は bracketed paste を有効・無効にする。有効にしたものは Restore で無効に戻す。
func (t *Term) SetBracketedPaste(on bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	seq := seqBracketedPasteOff
	if on {
		seq = seqBracketedPasteOn
	}
	if err := t.sys.write([]byte(seq)); err != nil {
		return err
	}
	t.paste = on
	return nil
}

// QueryCursorPosition はカーソル位置の問い合わせ（ESC[6n）を書く。応答（ESC[行;桁R）は入力として届く。
func (t *Term) QueryCursorPosition() error {
	_, err := t.Write([]byte(seqCursorPosition))
	return err
}

// Size は端末の表示の大きさ（桁数と行数）を返す。
func (t *Term) Size() (cols, rows int, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return 0, 0, ErrClosed
	}
	return t.sys.size()
}

// Info は、結果の記録に使う端末と OS の情報（OS の版、Windows のコンソールモードとコードページなど）を返す。
func (t *Term) Info() map[string]string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sys.info()
}

// StartInput は、別の goroutine で入力を読み始め、読んだものを送るチャネルを返す。
// 端末の大きさの変更と、Options.Signals のときはシグナルも、同じチャネルに送る。
// 2 回目以降の呼び出しは、同じチャネルを返す。
// 読み取りに失敗したら Err を持つ Input を送って閉じる。Restore でも閉じる。
func (t *Term) StartInput() <-chan Input {
	t.inputOnce.Do(func() {
		ch := make(chan Input)
		t.input = ch
		send := func(in Input) bool {
			select {
			case ch <- in:
				return true
			case <-t.stop:
				return false
			}
		}
		sigs := resizeSignals()
		if t.opts.Signals {
			sigs = append(sigs, exitSignals()...)
		}
		sigCh := make(chan os.Signal, 8)
		if len(sigs) > 0 {
			signal.Notify(sigCh, sigs...)
		}
		readDone := make(chan struct{})
		var wg sync.WaitGroup
		wg.Go(func() {
			defer close(readDone)
			if err := t.sys.readLoop(send, t.stop); err != nil {
				send(Input{Time: time.Now(), Err: err})
			}
		})
		wg.Go(func() {
			for {
				select {
				case sig := <-sigCh:
					in := Input{Time: time.Now(), Signal: sig}
					if isResizeSignal(sig) {
						in = Input{Time: in.Time, Resize: true}
					}
					if !send(in) {
						return
					}
				case <-readDone: // 読み取りに失敗した。チャネルを閉じる
					return
				case <-t.stop:
					return
				}
			}
		})
		go func() {
			wg.Wait()
			signal.Stop(sigCh)
			close(ch)
			close(t.done)
		}()
	})
	return t.input
}

// Restore は、端末を Open の前の状態に戻す（tui T1）。
// 読み取りの goroutine を止め、シグナルを受け取るのをやめ、開始時に変えたもの（代替画面、カーソル、自動改行、bracketed paste、
// raw モード、Windows のコンソールモードとコードページ）を戻す。
// 何度呼んでも、どの goroutine から呼んでもよい。2 回目以降は 1 回目と同じエラーを返す。
func (t *Term) Restore() error {
	t.restoreOnce.Do(func() {
		// 読み取りの goroutine を先に止める。戻した後の端末（行の入力のモード）から読まないように。
		t.inputOnce.Do(func() { // 始めていなければ、閉じたチャネルを返すようにする
			ch := make(chan Input)
			close(ch)
			t.input = ch
			close(t.done)
		})
		close(t.stop)
		t.sys.wake()
		<-t.done

		t.mu.Lock()
		defer t.mu.Unlock()
		var seq string
		if t.paste {
			seq += seqBracketedPasteOff
		}
		if t.opts.NoAutoWrap {
			seq += seqAutoWrapOn
		}
		if t.opts.HideCursor {
			seq += seqShowCursor
		}
		if t.opts.AltScreen {
			seq += seqAltScreenOff
		}
		var errs []error
		if seq != "" {
			errs = append(errs, t.sys.write([]byte(seq)))
		}
		errs = append(errs, t.sys.restore())
		t.closed = true
		t.restoreErr = errors.Join(errs...)
	})
	return t.restoreErr
}

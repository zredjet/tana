// tuiprobe は、端末の文字幅とキー入力を実測し、TUI の土台を手で確かめるプログラム（docs/SPEC-filer.md §12.1、docs/SPEC-tui.md §9）。
//
//	tuiprobe width  [-terminal 名前] [-o ファイル] [-hold 秒] [-output 方法] [-vtinput=true|false]
//	tuiprobe keys   [-terminal 名前] [-o ファイル] [-vtinput] [-output 方法] [-steps ID,...]
//	tuiprobe modes  [-terminal 名前] [-o ファイル] [-hold 秒] [-decrqm=true|false]
//	tuiprobe screen [-terminal 名前] [-o ファイル]
//
// 結果は JSON のファイル（既定は <端末>-<日付>.json）に書く。ファイルがあれば、その回の節（width・keys など）だけを置き換える。
// -output（writeconsole・utf8cp・writefile）と -vtinput は Windows だけで意味を持つ（VT1・VT2）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/zredjet/tana/internal/term"
	"github.com/zredjet/tana/internal/tui"
)

const usageText = `使い方:
  tuiprobe width [-terminal 名前] [-o ファイル] [-hold 秒] [-output 方法] [-vtinput=true|false]
      文字列ごとに、端末が進めた桁数をカーソル位置の問い合わせで測る。最後に、ずれの広がりを見る画面を -hold 秒だけ出す。
  tuiprobe keys [-terminal 名前] [-o ファイル] [-vtinput] [-output 方法] [-steps ID,...]
      案内に従って押したキーを、届いたまま（Unix はバイト列、Windows は入力のレコード）記録する。
      -steps を付けると、その手順だけを記録し、ファイルにある同じ節の同じ手順を置き換える（撮り直し）。
  tuiprobe modes [-terminal 名前] [-o ファイル] [-hold 秒] [-decrqm=true|false]
      制御シーケンス（同期出力・自動改行・カーソル・bracketed paste）が使えるか（VT5）と、
      位置の指定で書記素クラスタの結合が切れるか（VT7）を、カーソル位置の問い合わせで測る。
      -decrqm は DECRQM の問い合わせを送るか（既定は Terminal.app 以外で送る）。
  tuiprobe screen [-terminal 名前] [-o ファイル]
      確認用の画面（2 つのペインの 10 万行の一覧、入力欄、進捗、panic の試験）。キー・大きさの変更・終わり方を記録する（VU1・VU4・VT4）。

共通の引数:
  -terminal 名前         端末の名前（例: Terminal.app、iTerm2、Windows Terminal、conhost）。省略すると環境変数から推測する
  -terminal-version 版   端末の版
  -font 名前             フォントの名前と大きさ
  -note 文               メモ
  -o ファイル            結果のファイル（既定は <端末>-<日付>.json）
  -output 方法           Windows の出力の方法: writeconsole（既定）、utf8cp、writefile
  -vtinput               Windows で VT の入力モードを使う（keys の既定は false、ほかは true）
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// options は、サブコマンドに共通の引数。
type options struct {
	terminal, terminalVersion, font, note, out, steps string
	output                                            term.OutputMethod
	vtInput, decrqm                                   bool
	hold                                              time.Duration
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	cmd := args[0]
	if cmd != "width" && cmd != "keys" && cmd != "modes" && cmd != "screen" {
		fmt.Fprint(stderr, usageText)
		return 2
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o options
	var output string
	fs.StringVar(&o.terminal, "terminal", "", "")
	fs.StringVar(&o.terminalVersion, "terminal-version", "", "")
	fs.StringVar(&o.font, "font", "", "")
	fs.StringVar(&o.note, "note", "", "")
	fs.StringVar(&o.out, "o", "", "")
	fs.StringVar(&output, "output", term.OutputWriteConsoleW.String(), "")
	fs.BoolVar(&o.vtInput, "vtinput", cmd != "keys", "")
	fs.BoolVar(&o.decrqm, "decrqm", os.Getenv("TERM_PROGRAM") != "Apple_Terminal", "")
	fs.DurationVar(&o.hold, "hold", 0, "")
	fs.StringVar(&o.steps, "steps", "", "")
	fs.Usage = func() { fmt.Fprint(stderr, usageText) }
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	var err error
	if o.output, err = term.ParseOutputMethod(output); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	steps, err := selectSteps(o.steps)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if o.terminal == "" {
		o.terminal = detectTerminal(os.Getenv)
	}
	if o.out == "" {
		o.out = slug(o.terminal) + "-" + time.Now().Format(time.DateOnly) + ".json"
	}

	// SIGINT・SIGTERM（Windows ではコンソールを閉じる通知も）・SIGHUP を受けたら、そこまでの結果を書いて端末を戻す。
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	opts := term.Options{AltScreen: true, HideCursor: true, NoAutoWrap: true, VTInput: o.vtInput, Output: o.output}
	switch cmd {
	case "keys":
		opts.HideCursor = false
		opts.BracketedPaste = true
	case "screen":
		// tana と同じ設定（tui §8）。シグナルは入力のチャネルに届き、tui.Loop が端末を戻して終わる。
		opts.BracketedPaste = true
		opts.Signals = true
	}
	t, err := term.Open(opts)
	if err != nil {
		fmt.Fprintf(stderr, "端末を開けません: %v\n", err)
		return 1
	}
	defer t.Restore() // panic のときも戻す
	in := t.StartInput()
	sec := sectionHeader{
		Started:  time.Now().Format(time.RFC3339),
		Revision: revision(),
		GOOS:     runtime.GOOS,
		GOARCH:   runtime.GOARCH,
		Env:      envInfo(os.Getenv),
		Info:     t.Info(),
		Note:     o.note,
		VTInput:  o.vtInput,
		Output:   o.output.String(),
	}
	sec.Cols, sec.Rows, _ = t.Size()

	var name string
	var result any
	var runErr error
	box := &inbox{ctx: ctx, in: in}
	switch cmd {
	case "width":
		name = widthSectionName(o)
		result, runErr = runWidth(t, box, sec, o.hold)
	case "keys":
		name = keysSectionName(o)
		var res *keysSection
		res, runErr = runKeys(t, box, sec, steps, defaultKeyTiming)
		result = res
		if o.steps != "" {
			// 撮り直し: ファイルにある記録の同じ手順だけを置き換える。
			var old keysSection
			if ok, err := loadSection(o.out, name, &old); err != nil {
				runErr = errors.Join(runErr, err)
				result = nil
			} else if ok {
				result = mergeKeys(&old, res)
			}
		}
	case "modes":
		name = "modes"
		result, runErr = runModes(t, box, sec, o.decrqm, o.hold, cprTimeout)
	case "screen":
		name = "screen"
		result, runErr = runScreen(t, sec)
	}
	restoreErr := t.Restore()

	if result != nil {
		if err := saveSection(o.out, fileHeader{
			Terminal:        o.terminal,
			TerminalVersion: o.terminalVersion,
			Font:            o.font,
			Date:            time.Now().Format(time.DateOnly),
		}, name, result); err != nil {
			fmt.Fprintf(stderr, "結果を書けません: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "結果を %s の %q に書きました。\n", o.out, name)
	}
	if restoreErr != nil {
		fmt.Fprintf(stderr, "端末を戻せませんでした: %v\n", restoreErr)
	}
	if runErr != nil {
		// panic は、端末を戻した後で、値とスタックを出す（tui T1）。
		if pe, ok := errors.AsType[*tui.PanicError](runErr); ok {
			fmt.Fprintf(stderr, "panic で終わりました（端末は戻しました）:\n%v\n", pe)
			return 2
		}
		fmt.Fprintf(stderr, "途中で終わりました: %v\n", runErr)
		if _, ok := errors.AsType[*tui.SignalError](runErr); ok || errors.Is(runErr, context.Canceled) {
			return 130
		}
		return 1
	}
	return 0
}

// widthSectionName は、width の結果を置く節の名前。既定と違う出力の方法・入力のモード（どちらも Windows でだけ意味を持つ）を名前に含める。
func widthSectionName(o options) string {
	name := "width"
	if o.output != term.OutputWriteConsoleW {
		name += "_" + o.output.String()
	}
	if !o.vtInput {
		name += "_novtinput"
	}
	return name
}

// keysSectionName は、keys の結果を置く節の名前（VT2 の比較のため、Windows の VT の入力モードは別の節にする）。
func keysSectionName(o options) string {
	name := "keys"
	if o.vtInput {
		name += "_vtinput"
	}
	if o.output != term.OutputWriteConsoleW {
		name += "_" + o.output.String()
	}
	return name
}

// detectTerminal は、環境変数から端末の名前を推測する。conhost は環境変数で見分けられない
// （Windows Terminal から起動すると WT_SESSION を受け継ぐ）。-terminal で指定すること。
func detectTerminal(getenv func(string) string) string {
	switch {
	case getenv("TERM_PROGRAM") == "Apple_Terminal":
		return "Terminal.app"
	case getenv("TERM_PROGRAM") == "iTerm.app":
		return "iTerm2"
	case getenv("WT_SESSION") != "":
		return "Windows Terminal"
	case getenv("TERM_PROGRAM") != "":
		return getenv("TERM_PROGRAM")
	case getenv("TERM") != "":
		return getenv("TERM")
	}
	return "unknown"
}

// slug は、端末の名前をファイル名に使える形（小文字の英数字と -）にする。
func slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if 'a' <= r && r <= 'z' || '0' <= r && r <= '9' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimSuffix(b.String(), "-")
	if s == "" {
		return "terminal"
	}
	return s
}

// envKeys は、結果に記録する環境変数。
var envKeys = []string{
	"TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLORTERM", "LANG", "LC_ALL", "LC_CTYPE",
	"ITERM_PROFILE", "WT_SESSION", "WT_PROFILE_ID", "SESSIONNAME",
}

func envInfo(getenv func(string) string) map[string]string {
	m := map[string]string{}
	for _, k := range envKeys {
		if v := getenv(k); v != "" {
			m[k] = v
		}
	}
	return m
}

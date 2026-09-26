// tana は、ターミナルのファイラー（docs/SPEC-filer.md）。
//
//	tana [フォルダ [フォルダ]]
//
// 左右のペインに表示するフォルダを指定する。省略すると、今いるフォルダを表示する（filer §6）。
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/msg"
	"github.com/zredjet/tana/internal/term"
	"github.com/zredjet/tana/internal/tui"
)

// panes はペインの数（2 ペインの構成。filer §15 で決めるまでの案）。
const panes = 2

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run は tana を動かし、終了コードを返す。
func run(args []string, stderr io.Writer) int {
	dirs, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		fmt.Fprintln(stderr, msg.Usage)
		return 2
	}
	cfg := app.DefaultConfig(dirs)
	if path := os.Getenv("TANA_LOG"); path != "" {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			fmt.Fprintln(stderr, msg.CannotOpenLog+err.Error())
			return 1
		}
		defer f.Close()
		cfg.Log = logger(f)
	}
	t, err := term.Open(term.Options{AltScreen: true, HideCursor: true, NoAutoWrap: true, BracketedPaste: true, VTInput: true, Signals: true})
	if err != nil {
		fmt.Fprintln(stderr, msg.CannotOpenTerminal+err.Error())
		return 1
	}
	defer t.Restore() // Run も戻すが、Run に入る前の panic に備える（T1。2 回目は何もしない）
	l := tui.New(t)
	l.SetNoColor(os.Getenv("NO_COLOR") != "")
	a, cmds := app.New(cfg)
	err = tui.RunFiler(l, a, cmds)
	// Run は端末を戻してから返る。ここからは普通の画面に書ける。
	if pe, ok := errors.AsType[*tui.PanicError](err); ok {
		fmt.Fprint(stderr, pe)
		return 2
	}
	if se, ok := errors.AsType[*tui.SignalError](err); ok {
		if cfg.Log != nil {
			cfg.Log(se)
		}
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// parseArgs は、引数から各ペインのフォルダ（絶対パス）を決める。省略したペインは、今いるフォルダ。
func parseArgs(args []string) ([]string, error) {
	if len(args) > panes {
		return nil, errors.New(msg.InvalidFolder + fmt.Sprint(args[panes:]))
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, errors.New(msg.InvalidFolder + err.Error())
	}
	dirs := make([]string, panes)
	for i := range dirs {
		dirs[i] = wd
		if i < len(args) {
			if args[i] == "" {
				return nil, errors.New(msg.InvalidFolder + `""`)
			}
			// 利用者が打った文字列から作る（表示用に加工したものではない。filer U4）。
			dirs[i] = filepath.Join(wd, args[i])
			if filepath.IsAbs(args[i]) {
				dirs[i] = filepath.Clean(args[i])
			}
		}
	}
	return dirs, nil
}

// logger は、エラーの英語の詳細を w に書く（filer §10）。イベントループと作業用の goroutine のどちらから呼んでもよい。
func logger(w io.Writer) func(error) {
	var mu sync.Mutex
	return func(err error) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(w, "%s %v\n", time.Now().Format("2006-01-02 15:04:05.000"), err)
	}
}

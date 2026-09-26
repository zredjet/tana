package tui

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

// TestVU7 は、応答しない場所（ネットワークドライブなど）を開いたときに、UI が固まらず Esc で中止できるかと、
// OS の列挙がどれくらい待たされるかを記録する（filer VU7、U5）。
// TANA_VU7_PATHS に、; で区切ったパスを指定したときだけ動かす（仮想マシンで、届かない UNC パスなどを指定する）。
// 画面は偽物の端末に描き、描かれたものを見てキーを送る。結果は t.Log に出す。
func TestVU7(t *testing.T) {
	paths := os.Getenv("TANA_VU7_PATHS")
	if paths == "" {
		t.Skip(`set TANA_VU7_PATHS (e.g. \\192.0.2.1\share) to measure unreachable locations`)
	}
	for _, target := range strings.Split(paths, ";") {
		vu7(t, target)
	}
}

func vu7(t *testing.T, target string) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		took time.Duration
		err  error
	}
	done := make(chan result, 1)
	if err := os.WriteFile(filepath.Join(dir, "vu7-ready.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := app.DefaultConfig([]string{dir, dir})
	cfg.Open = func(p string) error { t.Errorf("opened %s", p); return nil } // アプリを起動しない
	cfg.ReadDir = func(d string) ([]fsops.Entry, error) {
		if d != target {
			return fsops.ReadDir(d)
		}
		start := time.Now()
		e, err := fsops.ReadDir(d)
		done <- result{time.Since(start), err}
		return e, err
	}
	f := newFakeTerm(80, 24)
	var (
		mu                       sync.Mutex
		step                     int
		entered, shown, canceled time.Time
	)
	f.onWrite = func(out string) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case step == 0 && strings.Contains(out, "vu7-ready.txt"): // 一覧を読み込んだ
			step++
			// パスの入力欄を空にして、貼り付けで入れる。
			f.send("g" + strings.Repeat("\x7f", 400) + "\x1b[200~" + target + "\x1b[201~\r")
			entered = time.Now()
		case step == 1 && strings.Contains(out, msg.Loading):
			step++
			shown = time.Now()
			f.send("\x1b")
		case step == 2 && strings.Contains(out, msg.LoadCanceled):
			step++
			canceled = time.Now()
			f.send("q")
		case step == 1 && strings.Contains(out, "このフォルダを開けません"):
			step = 3 // 0.2 秒より前に失敗した
			f.send("q")
		}
	}
	a, cmds := app.New(cfg)
	start := time.Now()
	if err := RunFiler(New(f), a, cmds); err != nil {
		t.Fatalf("RunFiler: %v", err)
	}
	mu.Lock()
	t.Logf("VU7 %s: UI ended after %v; loading notice after %v; Esc to canceled %v (step %d)",
		target, time.Since(start).Round(time.Millisecond), sub(shown, entered), sub(canceled, shown), step)
	mu.Unlock()
	select {
	case r := <-done:
		t.Logf("VU7 %s: ReadDir returned after %v: %v", target, r.took.Round(time.Millisecond), r.err)
	case <-time.After(5 * time.Minute):
		t.Logf("VU7 %s: ReadDir did not return within 5 minutes", target)
	}
}

func sub(a, b time.Time) time.Duration {
	if a.IsZero() || b.IsZero() {
		return -1
	}
	return a.Sub(b).Round(time.Millisecond)
}

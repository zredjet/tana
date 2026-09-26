package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// vu10Probe は、ShellExecute に渡したパスで、どのスクリプトが動いたかを記録する（filer §12 の VU10）。
// 各スクリプトは、自分の印のファイル（marker-<id>.txt）に、cmd が見た自分のパス（%~f0）を書く。
// 印は別のプロセスが書くので、現れるまで待つ（プローブの記録で、期待値との比較ではない）。
func vu10Probe(t *testing.T, dir string) {
	ext := `\\?\` + dir
	long := filepath.Join(dir, strings.Repeat("a", 120), strings.Repeat("b", 120))
	if err := os.MkdirAll(`\\?\`+long, 0o755); err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{ // \\?\ で作る名前 → 印の id
		`probe.cmd`:  "plain",
		`probe.cmd.`: "trailing-dot",
		`probe.cmd `: "trailing-space",
		`con.cmd`:    "reserved-con",
		`nul.cmd`:    "reserved-nul",
		`nocom.cmd`:  "no-coinit",
	}
	for name, id := range scripts {
		writeScript(t, filepath.Join(ext, name), dir, id)
	}
	writeScript(t, `\\?\`+filepath.Join(long, "long.cmd"), dir, "long")
	longFile := filepath.Join(long, "long.cmd")
	t.Logf("VU10 long path: %d characters", len(longFile))

	cases := []struct {
		name, path string
		noCOM      bool
	}{
		{"plain", filepath.Join(dir, "probe.cmd"), false},
		{"trailing-dot", filepath.Join(dir, "probe.cmd."), false},
		{"trailing-space", filepath.Join(dir, "probe.cmd "), false},
		{"reserved-con", filepath.Join(dir, "con.cmd"), false},
		{"reserved-nul", filepath.Join(dir, "nul.cmd"), false},
		{"long", longFile, false},
		{"prefixed-plain", `\\?\` + filepath.Join(dir, "probe.cmd"), false},
		{"prefixed-trailing-dot", `\\?\` + filepath.Join(dir, "probe.cmd."), false},
		{"prefixed-long", `\\?\` + longFile, false},
		{"goroutine-without-coinit", filepath.Join(dir, "nocom.cmd"), true},
	}
	for _, c := range cases {
		clearMarkers(t, dir)
		var err error
		done := make(chan struct{})
		go func() {
			defer close(done)
			if c.noCOM {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				err = shellExecute(c.path)
				return
			}
			err = Open(c.path)
		}()
		<-done
		got := waitMarkers(t, dir, err == nil)
		t.Logf("VU10 case=%s CanOpen=%v ShellExecute err=%v ran=%q", c.name, CanOpen(c.path), err, got)
	}
}

// writeScript は、path に、印 marker-<id>.txt を dir に書く .cmd を作る。
func writeScript(t *testing.T, path, dir, id string) {
	t.Helper()
	body := "@echo off\r\necho %~f0> \"" + filepath.Join(dir, "marker-"+id+".txt") + "\"\r\nexit\r\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
}

func clearMarkers(t *testing.T, dir string) {
	t.Helper()
	names, _ := filepath.Glob(filepath.Join(dir, "marker-*.txt"))
	for _, n := range names {
		if err := os.Remove(n); err != nil {
			t.Fatal(err)
		}
	}
}

// waitMarkers は、印が現れるのを最大 10 秒待ち、現れたらほかの印のために 1 秒待ってから、印の id と中身を返す。
// ShellExecute が失敗したときは 2 秒だけ待つ（何も動かないことを確かめる）。
func waitMarkers(t *testing.T, dir string, started bool) []string {
	t.Helper()
	limit := 2 * time.Second
	if started {
		limit = 10 * time.Second
	}
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if names, _ := filepath.Glob(filepath.Join(dir, "marker-*.txt")); len(names) > 0 {
			time.Sleep(time.Second)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	names, _ := filepath.Glob(filepath.Join(dir, "marker-*.txt"))
	var out []string
	for _, n := range names {
		b, _ := os.ReadFile(n)
		id := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(n), "marker-"), ".txt")
		out = append(out, id+": "+strings.TrimSpace(string(b)))
	}
	slices.Sort(out)
	return out
}

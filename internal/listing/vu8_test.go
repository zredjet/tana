package listing

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops"
)

// names100k は、10 万件の名前（英数字・日本語・数字の並びを混ぜる）。
func entries100k() []fsops.Entry {
	out := make([]fsops.Entry, 100_000)
	for i := range out {
		name := fmt.Sprintf("file%d.txt", i)
		switch i % 4 {
		case 1:
			name = fmt.Sprintf("報告書_%05d.docx", i)
		case 2:
			name = fmt.Sprintf("IMG_%d.JPG", i)
		case 3:
			name = fmt.Sprintf("フォルダ%d", i)
		}
		t := fsops.TypeFile
		if i%4 == 3 {
			t = fsops.TypeDir
		}
		out[i] = fsops.Entry{Name: name, Info: fsops.EntryInfo{Type: t, Size: int64(i), ModTime: time.Unix(int64(i), 0)}}
	}
	return out
}

// BenchmarkBuild100k は、10 万件の項目を作って並べ替える時間を測る（filer VU8）。
func BenchmarkBuild100k(b *testing.B) {
	entries := entries100k()
	dir := filepath.Join(root(), "big")
	for b.Loop() {
		Build(dir, entries, true)
	}
}

// TestVU8 は、10 万件のファイルがあるフォルダの列挙・並べ替えにかかる時間とメモリを記録する（filer VU8）。
// 10 万件のファイルを作るので、TANA_VU8=1 のときだけ動かす。結果は t.Log に出す。描画の時間は tui のテストで測る。
func TestVU8(t *testing.T) {
	if os.Getenv("TANA_VU8") != "1" {
		t.Skip("set TANA_VU8=1 to measure a folder with 100,000 files")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	for _, e := range entries100k() {
		p := filepath.Join(dir, e.Name)
		if e.Info.Type == fsops.TypeDir {
			err = os.Mkdir(p, 0o755)
		} else {
			err = os.WriteFile(p, nil, 0o644)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("VU8 created 100,000 entries in %v", time.Since(start))
	for round := 1; round <= 3; round++ {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start = time.Now()
		entries, err := fsops.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		read := time.Since(start)
		start = time.Now()
		items := Build(dir, entries, true)
		built := time.Since(start)
		runtime.ReadMemStats(&after)
		runtime.KeepAlive(items)
		t.Logf("VU8 round %d: ReadDir %v, Build %v, %d items, allocated %.1f MB, heap in use %.1f MB",
			round, read, built, len(items), float64(after.TotalAlloc-before.TotalAlloc)/(1<<20), float64(after.HeapInuse)/(1<<20))
	}
}

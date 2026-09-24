package fsops

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// macOS の exFAT では、NFC の名前で作られたファイル（Windows などで作られたもの）を、列挙（ReadDir）が NFD の名前で返し、
// その名前では削除・リネームできない（ENOENT。Lstat と読み込みはできる。V17）。fsops は名前を変換して探し直さない（I6、§8.5）。
// その場合に、データを失わず、黙って成功扱いにもせず、KindNotFound の失敗として報告することを確かめる（総点検の穴 9）。

// nfcExFAT は、macOS の exFAT のフォルダを返す（ほかの OS では Skip）。
func nfcExFAT(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the NFC/NFD listing issue is specific to exFAT on macOS (V17)")
	}
	return testfs.EnvDir(t, testfs.ExFATEnv)
}

// writeNFC は、dir に NFC の名前のファイルを作る（名前はパスで渡すので NFC のまま保存される）。
func writeNFC(t *testing.T, dir, data string) string {
	t.Helper()
	testfs.MkdirAll(t, dir)
	p := filepath.Join(dir, testfs.NameNFC)
	testfs.WriteFile(t, p, data)
	// 後片付けは列挙の名前（NFD）では消せないので、作ったときの NFC の名前で消す（V17）。AppleDouble ファイルも同じ。
	t.Cleanup(func() {
		os.Remove(p)
		os.Remove(filepath.Join(dir, "._"+testfs.NameNFC))
	})
	return p
}

// TestNFCOnExFATDelete は、完全削除で、NFC の名前のファイルを消せなくても、ファイルが残り、KindNotFound の失敗として報告されることを確かめる。
func TestNFCOnExFATDelete(t *testing.T) {
	t.Parallel()
	root := nfcExFAT(t)
	nfc := writeNFC(t, filepath.Join(root, "tree"), "nfc")
	testfs.WriteFile(t, filepath.Join(root, "tree", "plain.txt"), "p")
	res := execDelete(t, context.Background(), nil, filepath.Join(root, "tree"))
	it := res.Items[0]
	t.Logf("result: %+v", it)
	if testfs.Exists(t, nfc) && testfs.ReadFile(t, nfc) != "nfc" {
		t.Fatal("the content of the NFC-named file changed")
	}
	t.Logf("NFC-named file left: %v", testfs.Exists(t, nfc))
	if left := realNames(t, filepath.Join(root, "tree")); len(left) > 0 && it.Outcome == OutcomeDone {
		t.Errorf("the item was reported Done although %+q is left", left)
	}
}

// realNames は、dir の名前のうち、macOS が作る AppleDouble ファイル（._名前）を除いたものを返す。
func realNames(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	for _, n := range testfs.ListNames(t, dir) {
		if len(n) < 2 || n[:2] != "._" {
			names = append(names, n)
		}
	}
	return names
}

// TestNFCOnExFATCopy は、exFAT からのコピーで、NFC の名前のファイルが、列挙が返した名前（バイト単位でそのまま。I6）でコピーされ、
// 内容が一致することを確かめる。
func TestNFCOnExFATCopy(t *testing.T) {
	t.Parallel()
	root := nfcExFAT(t)
	writeNFC(t, filepath.Join(root, "tree"), "nfc")
	listed := realNames(t, filepath.Join(root, "tree"))
	dest := testfs.TempDir(t)
	res := execPlan(t, context.Background(), mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "tree")}, DestDir: dest}), ExecOptions{})
	it := res.Items[0]
	t.Logf("result: %+v, listed: %+q", it, listed)
	if it.Outcome != OutcomeDone {
		t.Errorf("result = %+v, want Done", it)
	}
	copied := realNames(t, filepath.Join(dest, "tree"))
	if !slices.Equal(copied, listed) {
		t.Errorf("copied names %+q, want the listed names %+q", copied, listed)
	}
	for _, n := range copied {
		if got := testfs.ReadFile(t, filepath.Join(dest, "tree", n)); got != "nfc" {
			t.Errorf("%q: content %q", n, got)
		}
	}
}

// TestNFCOnExFATMove は、exFAT からのボリュームをまたぐ移動と、exFAT の中のマージ移動で、NFC の名前のファイルを移動元から消せなくても、
// データを失わず（ボリュームをまたぐ移動では移動先にもある）、失敗として報告されることを確かめる。
func TestNFCOnExFATMove(t *testing.T) {
	t.Parallel()
	root := nfcExFAT(t)
	t.Run("cross volume", func(t *testing.T) {
		src := filepath.Join(root, "cross")
		nfc := writeNFC(t, filepath.Join(src, "tree"), "nfc")
		dest := testfs.TempDir(t)
		plan := mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(src, "tree")}, DestDir: dest})
		res := execPlan(t, context.Background(), plan, ExecOptions{})
		it := res.Items[0]
		t.Logf("method %v, result: %+v", plan.Items()[0].Method, it)
		left := testfs.Exists(t, nfc)
		copied := len(realNames(t, filepath.Join(dest, "tree"))) > 0
		if !left && !copied {
			t.Fatal("the NFC-named file was lost")
		}
		if left && it.Outcome == OutcomeDone {
			t.Errorf("the item was reported Done although the source file is left")
		}
	})
	t.Run("merge", func(t *testing.T) {
		base := filepath.Join(root, "merge")
		nfc := writeNFC(t, filepath.Join(base, "src", "m"), "nfc")
		testfs.MkdirAll(t, filepath.Join(base, "dest", "m"))
		testfs.WriteFile(t, filepath.Join(base, "dest", "m", "old.txt"), "o")
		plan := mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(base, "src", "m")}, DestDir: filepath.Join(base, "dest")})
		decide(t, plan, filepath.Join(base, "dest", "m"), DecisionMerge)
		res := execPlan(t, context.Background(), plan, ExecOptions{})
		it := res.Items[0]
		t.Logf("result: %+v", it)
		left := testfs.Exists(t, nfc)
		moved := len(realNames(t, filepath.Join(base, "dest", "m"))) > 1
		if !left && !moved {
			t.Fatal("the NFC-named file was lost")
		}
		if left && it.Outcome == OutcomeDone {
			t.Errorf("the item was reported Done although the source file is left")
		}
	})
}

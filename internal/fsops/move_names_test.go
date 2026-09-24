package fsops

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// 移動で、名前をバイト単位でそのまま使うこと（I6、§18.4 の I6 の「移動」。総点検の穴 10）。
// 日本語・絵文字・NFD の名前を、トップレベルの項目とフォルダの中身の両方に置く。

// oddNames は、変換されやすい名前とその内容。
var oddNames = map[string]string{testfs.NameJapanese: "ja", testfs.NameEmoji: "emoji", testfs.NameNFD: "nfd"}

// oddTree は、dir の中に oddNames のファイルと、日本語の名前のフォルダ（中に NFD の名前のファイル）を作る。
func oddTree(t *testing.T, dir string) {
	t.Helper()
	tree := testfs.Tree{"日本語のフォルダ/" + testfs.NameNFD: testfs.File("nested nfd")}
	for n, d := range oddNames {
		tree[n] = testfs.File(d)
	}
	testfs.Build(t, dir, tree)
}

// checkOddTree は、dir が oddTree のとおりで、名前がバイト単位で一致することを確かめる。
func checkOddTree(t *testing.T, dir string) {
	t.Helper()
	want := []string{"日本語のフォルダ"}
	for n := range oddNames {
		want = append(want, n)
	}
	slices.Sort(want)
	if got := testfs.ListNames(t, dir); !slices.Equal(got, want) {
		t.Errorf("%s: names %+q, want %+q (byte for byte)", dir, got, want)
	}
	if got := testfs.ListNames(t, filepath.Join(dir, "日本語のフォルダ")); !slices.Equal(got, []string{testfs.NameNFD}) {
		t.Errorf("nested names %+q, want [%+q]", got, testfs.NameNFD)
	}
	files := map[string]string{"日本語のフォルダ/" + testfs.NameNFD: "nested nfd"}
	for n, d := range oddNames {
		files[n] = d
	}
	wantFiles(t, dir, files)
}

// TestMoveNamesSameVolume は、同一ボリュームの移動（トップレベルの項目のリネームと、マージの中身の移動）で、名前がそのまま使われることを確かめる。
func TestMoveNamesSameVolume(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	oddTree(t, filepath.Join(root, "src", "tree"))
	oddTree(t, filepath.Join(root, "src", "m"))
	testfs.Build(t, root, testfs.Tree{"src/" + testfs.NameNFD: testfs.File("top nfd"), "dest/m": testfs.Dir()})
	dest := filepath.Join(root, "dest")
	plan := sameMove(t, root, "tree", "m", testfs.NameNFD)
	decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone {
			t.Errorf("%s: %+v (%v), want Done", it.Src, it, it.Err)
		}
	}
	checkOddTree(t, filepath.Join(dest, "tree"))
	checkOddTree(t, filepath.Join(dest, "m"))
	if got := testfs.ListNames(t, dest); !slices.Contains(got, testfs.NameNFD) {
		t.Errorf("dest names %+q, want %+q", got, testfs.NameNFD)
	}
	wantFiles(t, dest, map[string]string{testfs.NameNFD: "top nfd"})
}

// TestMoveNamesCrossVolume は、ボリュームをまたぐ移動（新しく作るフォルダとマージ先へのコピーと、記録した名前での移動元の削除）で、
// 名前がそのまま使われ、移動元が消えることを確かめる。
func TestMoveNamesCrossVolume(t *testing.T) {
	t.Parallel()
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	oddTree(t, filepath.Join(root, "src", "tree"))
	oddTree(t, filepath.Join(root, "src", "m"))
	testfs.Build(t, root, testfs.Tree{"src/" + testfs.NameEmoji: testfs.File("top emoji")})
	testfs.MkdirAll(t, filepath.Join(dest, "m"))
	plan := crossMove(t, root, dest, "tree", "m", testfs.NameEmoji)
	decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone {
			t.Errorf("%s: %+v (%v), want Done", it.Src, it, it.Err)
		}
	}
	checkOddTree(t, filepath.Join(dest, "tree"))
	checkOddTree(t, filepath.Join(dest, "m"))
	wantFiles(t, dest, map[string]string{testfs.NameEmoji: "top emoji"})
	if got := testfs.ListNames(t, filepath.Join(root, "src")); len(got) != 0 {
		t.Errorf("left in the source: %+q", got)
	}
}

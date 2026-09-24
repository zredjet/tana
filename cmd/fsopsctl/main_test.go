package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tempDir は、t.TempDir() を EvalSymlinks で正規化したものを返す（macOS の /var → /private/var など）。
func tempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// runCLI は run を実行し、終了コードと標準出力・標準エラー出力を返す。
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// TestPlanDoesNotChange は、plan が計画と衝突を表示するだけで、何も変更しないことを確かめる。
func TestPlanDoesNotChange(t *testing.T) {
	root := tempDir(t)
	write(t, filepath.Join(root, "src", "a.txt"), "new")
	write(t, filepath.Join(root, "dest", "a.txt"), "old")
	code, out, errOut := runCLI(t, "plan", "copy", "-dest", filepath.Join(root, "dest"), filepath.Join(root, "src", "a.txt"))
	if code != exitOK {
		t.Fatalf("code = %d, stderr = %s", code, errOut)
	}
	for _, want := range []string{"計画: コピー 1 項目", "衝突 1 件", "使える決定: スキップ・上書き・名前を変えて残す"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if read(t, filepath.Join(root, "dest", "a.txt")) != "old" || !exists(filepath.Join(root, "src", "a.txt")) {
		t.Error("plan changed the file system")
	}
}

// TestCopyConflicts は、-on-conflict の一括指定と、使えない決定になる衝突がスキップのまま表示されることを確かめる。
func TestCopyConflicts(t *testing.T) {
	root := tempDir(t)
	write(t, filepath.Join(root, "src", "a.txt"), "new")
	write(t, filepath.Join(root, "src", "d", "x"), "x")
	write(t, filepath.Join(root, "dest", "a.txt"), "old")
	write(t, filepath.Join(root, "dest", "d", "y"), "y")
	dest := filepath.Join(root, "dest")
	srcs := []string{filepath.Join(root, "src", "a.txt"), filepath.Join(root, "src", "d")}

	// 指定なし: すべてスキップ。
	code, out, _ := runCLI(t, append([]string{"copy", "-dest", dest}, srcs...)...)
	if code != exitOK || !strings.Contains(out, "スキップします") || read(t, filepath.Join(dest, "a.txt")) != "old" {
		t.Errorf("default: code %d, a.txt %q\n%s", code, read(t, filepath.Join(dest, "a.txt")), out)
	}
	// overwrite: ファイルは上書きし、フォルダ同士の衝突は上書きできないのでスキップのまま。
	code, out, _ = runCLI(t, append([]string{"copy", "-dest", dest, "-on-conflict=overwrite"}, srcs...)...)
	if code != exitOK || read(t, filepath.Join(dest, "a.txt")) != "new" || exists(filepath.Join(dest, "d", "x")) {
		t.Errorf("overwrite: code %d\n%s", code, out)
	}
	if !strings.Contains(out, "「上書き」を使えないので、スキップします") {
		t.Errorf("overwrite: the skipped folder conflict is not shown:\n%s", out)
	}
	// merge: フォルダはマージする。
	code, out, _ = runCLI(t, append([]string{"copy", "-dest", dest, "-on-conflict=merge"}, srcs[1:]...)...)
	if code != exitOK || read(t, filepath.Join(dest, "d", "x")) != "x" || read(t, filepath.Join(dest, "d", "y")) != "y" {
		t.Errorf("merge: code %d\n%s", code, out)
	}
}

// TestMoveRelativePaths は、相対パスを絶対パスにして移動することを確かめる。
func TestMoveRelativePaths(t *testing.T) {
	root := tempDir(t)
	write(t, filepath.Join(root, "src", "a.txt"), "a")
	if err := os.MkdirAll(filepath.Join(root, "dest"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	code, out, errOut := runCLI(t, "move", "-dest", "dest", filepath.Join("src", "a.txt"))
	if code != exitOK || !strings.Contains(out, "結果: 完了") {
		t.Fatalf("code %d\n%s\n%s", code, out, errOut)
	}
	if exists(filepath.Join(root, "src", "a.txt")) || read(t, filepath.Join(root, "dest", "a.txt")) != "a" {
		t.Error("the file was not moved")
	}
}

// TestDeleteNeedsYes は、delete が -yes なしでは何もしないことを確かめる。
func TestDeleteNeedsYes(t *testing.T) {
	root := tempDir(t)
	p := filepath.Join(root, "a.txt")
	write(t, p, "a")
	code, _, errOut := runCLI(t, "delete", p)
	if code != exitUsage || !strings.Contains(errOut, "-yes") || !exists(p) {
		t.Errorf("without -yes: code %d, exists %v\n%s", code, exists(p), errOut)
	}
	code, out, _ := runCLI(t, "delete", "-yes", p)
	if code != exitOK || exists(p) {
		t.Errorf("with -yes: code %d, exists %v\n%s", code, exists(p), out)
	}
}

// TestTrashUnavailable は、ごみ箱が使えない環境（Linux）で、完全削除に切り替えずに表示し、ファイルが残ることを確かめる。
// 開発者のごみ箱を汚さないよう、ごみ箱が使える OS では実行しない。
func TestTrashUnavailable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the trash may be available on " + runtime.GOOS)
	}
	root := tempDir(t)
	p := filepath.Join(root, "a.txt")
	write(t, p, "a")
	code, out, _ := runCLI(t, "trash", p)
	if code != exitErrors || !strings.Contains(out, "ごみ箱に入れられません（完全削除には切り替えません）") || !exists(p) {
		t.Errorf("code %d, exists %v\n%s", code, exists(p), out)
	}
}

// TestUsage は、使い方の誤りで終了コード 2 になることを確かめる。
func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"copy", "a"}, {"plan"}, {"plan", "x"}, {"copy", "-dest", "d", "-on-conflict=bad", "a"}} {
		if code, _, _ := runCLI(t, args...); code != exitUsage {
			t.Errorf("%q: code %d, want %d", args, code, exitUsage)
		}
	}
}

package fsops

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestValidateName は、Rename の新しい名前の検査（§11.3）を確かめる。
func TestValidateName(t *testing.T) {
	t.Parallel()
	common := []string{"", ".", "..", "a/b", "/", "a\x00b"}
	valid := []string{"a", "a.txt", ".hidden", "日本語", testfs.NameEmoji, testfs.NameNFD, "a b", "..a", "a..b"}
	windowsInvalid := []string{
		`a\b`, "a:b", "a*", "a?", `a"b`, "a<b", "a>b", "a|b", "a\x01", "a\x1f",
		"a.", "a ", "a. ", "CON", "con", "Con.txt", "PRN", "AUX.log", "NUL", "nul.tar.gz",
		"COM1", "com9.txt", "LPT1", "lpt9", "CON .txt",
	}
	windowsValid := []string{"CONSOLE", "COM10", "COM0x", "LPT", "a.b.c", "NULL", "auxiliary"}
	for _, n := range common {
		if err := validateName(n); KindOf(err) != KindInvalidName {
			t.Errorf("validateName(%q) = %v, want KindInvalidName", n, err)
		}
	}
	for _, n := range valid {
		if err := validateName(n); err != nil {
			t.Errorf("validateName(%q) = %v, want nil", n, err)
		}
	}
	for _, n := range windowsInvalid {
		err := validateName(n)
		if runtime.GOOS == "windows" {
			if KindOf(err) != KindInvalidName {
				t.Errorf("validateName(%q) = %v, want KindInvalidName (Windows)", n, err)
			}
		} else if !strings.ContainsRune(n, '\x00') && err != nil {
			t.Errorf("validateName(%q) = %v, want nil (Unix allows it)", n, err)
		}
	}
	for _, n := range windowsValid {
		if err := validateName(n); err != nil {
			t.Errorf("validateName(%q) = %v, want nil", n, err)
		}
	}
}

// TestRename は Rename（§11.3）を確かめる。
func TestRename(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"a.txt":     testfs.File("a"),
		"b.txt":     testfs.File("b"),
		"dir/x":     testfs.File("x"),
		"other/y":   testfs.File("y"),
		"ja.txt":    testfs.File("ja"),
		"emoji.txt": testfs.File("emoji"),
	})
	p := func(n string) string { return filepath.Join(root, n) }

	if err := Rename(p("a.txt"), "c.txt"); err != nil {
		t.Errorf("Rename file: %v", err)
	}
	if err := Rename(p("dir"), "dir2"); err != nil {
		t.Errorf("Rename dir: %v", err)
	}
	if err := Rename(p("ja.txt"), testfs.NameJapanese); err != nil {
		t.Errorf("Rename to Japanese: %v", err)
	}
	if err := Rename(p("emoji.txt"), testfs.NameEmoji); err != nil {
		t.Errorf("Rename to emoji: %v", err)
	}
	got := names(t, root)
	for _, w := range []string{"c.txt", "dir2", testfs.NameJapanese, testfs.NameEmoji} {
		if !slices.Contains(got, w) {
			t.Errorf("names = %+q, want %+q among them (I6)", got, w)
		}
	}

	// 上書きしない（I1）。
	before := testfs.Take(t, root)
	for _, c := range []struct{ src, name string }{{"b.txt", "c.txt"}, {"b.txt", "other"}, {"dir2", "other"}} {
		err := Rename(p(c.src), c.name)
		if KindOf(err) != KindExist {
			t.Errorf("Rename(%s, %s) = %v, want KindExist", c.src, c.name, err)
		}
		var oe *OpError
		if oe, _ = err.(*OpError); oe != nil && (oe.Path != p(c.src) || oe.Dest != p(c.name)) {
			t.Errorf("OpError paths = %q, %q; want %q, %q (without \\\\?\\)", oe.Path, oe.Dest, p(c.src), p(c.name))
		}
	}
	if diff := testfs.Diff(before, testfs.Take(t, root)); diff != nil {
		t.Errorf("Rename onto existing entries changed the tree (I1): %q", diff)
	}

	// 失敗の分類。
	for _, c := range []struct {
		path, name string
		want       Kind
	}{
		{p("missing"), "x", KindNotFound},
		{"relative/path", "x", KindInvalidRequest},
		{p("b.txt"), "", KindInvalidName},
		{p("b.txt"), "a/b", KindInvalidName},
		{p("b.txt"), strings.Repeat("n", 300), KindInvalidName},
	} {
		if err := Rename(c.path, c.name); KindOf(err) != c.want {
			t.Errorf("Rename(%q, %q) = %v, want %v", c.path, c.name, err, c.want)
		}
	}
	root2 := "/"
	if runtime.GOOS == "windows" {
		root2 = filepath.VolumeName(root) + `\`
	}
	if err := Rename(root2, "x"); KindOf(err) != KindInvalidRequest {
		t.Errorf("Rename(volume root) = %v, want KindInvalidRequest", err)
	}
}

// TestRenameCaseOnly は、大文字小文字だけ・正規化だけ違う名前への Rename（ファイル・フォルダ）で、名前がバイト単位で変わることを確かめる（§18.4「I6」、V1、V2）。
func TestRenameCaseOnly(t *testing.T) {
	t.Parallel()
	testRenameCaseOnly(t, testfs.TempDir(t))
}

// TestRenameCaseOnlyOtherVolumes は、exFAT・FAT32 でも名前が実際に変わることを確かめる（Windows では MoveFileExW が名前を変えずに成功を返す。V1、§11.3）。
func TestRenameCaseOnlyOtherVolumes(t *testing.T) {
	t.Parallel()
	for _, env := range []string{testfs.ExFATEnv, testfs.FAT32Env} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			testRenameCaseOnly(t, testfs.EnvDir(t, env))
		})
	}
}

func testRenameCaseOnly(t *testing.T, root string) {
	t.Helper()
	testfs.Build(t, root, testfs.Tree{
		"f/" + testfs.NameLower: testfs.File("c"),
		"d/abc/x":               testfs.File("x"),
		"n/" + testfs.NameNFC:   testfs.File("n"),
	})
	for _, c := range []struct{ dir, src, dst string }{
		{"f", testfs.NameLower, testfs.NameUpper},
		{"d", "abc", "ABC"},
		{"n", testfs.NameNFC, testfs.NameNFD},
	} {
		d := filepath.Join(root, c.dir)
		if err := Rename(filepath.Join(d, c.src), c.dst); err != nil {
			t.Errorf("Rename(%+q -> %+q): %v", c.src, c.dst, err)
			continue
		}
		if got := names(t, d); !slices.Equal(got, []string{c.dst}) {
			t.Errorf("%s: names = %+q, want [%+q]", c.dir, got, c.dst)
		}
	}
	if got := testfs.ReadFile(t, filepath.Join(root, "d", "ABC", "x")); got != "x" {
		t.Errorf("content under the renamed dir = %q", got)
	}
}

// TestRenameReadOnly は、読み取り専用の項目の名前の変更の扱いを確かめる。macOS のロック（UF_IMMUTABLE）は KindReadOnly になる（§17）。
func TestRenameReadOnly(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"locked": testfs.File("x")})
	testfs.SetImmutable(t, filepath.Join(root, "locked"))
	err := Rename(filepath.Join(root, "locked"), "renamed")
	if KindOf(err) != KindReadOnly {
		t.Errorf("Rename(immutable) = %v, want KindReadOnly", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "locked")); err != nil {
		t.Errorf("the immutable file was moved: %v", err)
	}
}

package probe

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// renameFunc は、排他リネームの候補（MoveFileExW(0)、renamex_np(RENAME_EXCL)、renameat2(RENAME_NOREPLACE)）。
type renameFunc func(from, to string) error

// listNames は dir の中の名前を返す。
func listNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(testfs.ExtendedPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func errnoNum(err error) int {
	var e syscall.Errno
	if errors.As(err, &e) {
		return int(e)
	}
	return 0
}

// renameCases は、排他リネームの候補 rename について、dir の中で次の場合の動作を記録する（SPEC §8.4、V1・V2・V12）。
// 新しい名前へ、既存のファイル・フォルダへ（上書きされないこと）、大文字小文字だけ・NFC/NFD だけの違い（ファイル・フォルダ）。
// 既存の移動先が上書きされた場合は、I1 に関わるのでテストを失敗にする。
func renameCases(t *testing.T, v, label, dir string, rename renameFunc) {
	t.Helper()
	cases := []struct {
		name     string
		tree     testfs.Tree
		from, to string
	}{
		{"new name", testfs.Tree{"a.txt": testfs.File("a")}, "a.txt", "b.txt"},
		{"existing file", testfs.Tree{"a.txt": testfs.File("a"), "b.txt": testfs.File("b")}, "a.txt", "b.txt"},
		{"existing dir", testfs.Tree{"a/x": testfs.File("x"), "b/y": testfs.File("y")}, "a", "b"},
		{"case only (file)", testfs.Tree{testfs.NameLower: testfs.File("c")}, testfs.NameLower, testfs.NameUpper},
		{"case only (dir)", testfs.Tree{"casedir/x": testfs.File("x")}, "casedir", "CASEDIR"},
		{"NFC to NFD (file)", testfs.Tree{testfs.NameNFC: testfs.File("n")}, testfs.NameNFC, testfs.NameNFD},
		{"NFC to NFD (dir)", testfs.Tree{"dir-é/x": testfs.File("x")}, "dir-é", "dir-é"},
	}
	for _, c := range cases {
		d := filepath.Join(dir, sanitize(c.name))
		testfs.Build(t, d, c.tree)
		before := testfs.Take(t, d)
		err := rename(filepath.Join(d, c.from), filepath.Join(d, c.to))
		t.Logf("%s: %s: %s: %+q -> %+q: err=%v (errno %d); names after=%+q",
			v, label, c.name, c.from, c.to, err, errnoNum(err), listNames(t, d))
		if c.name == "existing file" || c.name == "existing dir" {
			after := testfs.Take(t, d)
			// 移動先（b.txt / b/y）の内容が変わっていないこと（I1）。
			for rel, n := range before {
				if rel == "b.txt" || rel == "b/y" {
					if after[rel] != n {
						t.Errorf("%s: %s: %s: the existing target %s was changed: %+v -> %+v", v, label, c.name, rel, n, after[rel])
					}
				}
			}
			if err == nil {
				t.Errorf("%s: %s: %s: the exclusive rename onto an existing entry succeeded", v, label, c.name)
			}
		}
		if slices.Contains(listNames(t, d), c.to) && err == nil {
			t.Logf("%s: %s: %s: the new name is stored as given (byte-exact): true", v, label, c.name)
		}
	}
}

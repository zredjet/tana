package probe

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// describe は Lstat と Stat の結果を 1 行にまとめる。
func describe(p string) string {
	s := ""
	if fi, err := os.Lstat(testfs.ExtendedPath(p)); err != nil {
		s += "Lstat error: " + err.Error()
	} else {
		a := fi.Sys().(*syscall.Win32FileAttributeData).FileAttributes
		s += fmt.Sprintf("Lstat mode=%v type=%v IsDir=%v ModeSymlink=%v ModeIrregular=%v attrs=%#08x",
			fi.Mode(), fi.Mode().Type(), fi.IsDir(), fi.Mode()&fs.ModeSymlink != 0, fi.Mode()&fs.ModeIrregular != 0, a)
	}
	if fi, err := os.Stat(testfs.ExtendedPath(p)); err != nil {
		s += "; Stat error: " + err.Error()
	} else {
		s += "; Stat mode=" + fi.Mode().String()
	}
	if target, err := os.Readlink(testfs.ExtendedPath(p)); err != nil {
		s += "; Readlink error: " + err.Error()
	} else {
		s += "; Readlink=" + target
	}
	return s
}

// TestV3 は、ジャンクションとシンボリックリンクが Lstat でどう見えるかを記録し、
// §13.2 の削除方法（フォルダ用は RemoveDirectoryW、ファイル用は DeleteFileW）で
// リンク自体だけが消え、リンク先の中身が残ることを確かめる（SPEC §20 V3）。
func TestV3(t *testing.T) {
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"target/marker.txt": testfs.File("marker"),
		"target.txt":        testfs.File("file target"),
	})
	t.Logf("V3: %s; target dir: %s", runtime.Version(), describe(filepath.Join(root, "target")))

	tests := []struct {
		name   string
		entry  testfs.Entry
		remove func(p *uint16) error
	}{
		{"junction", testfs.Junction(filepath.Join(root, "target")), windows.RemoveDirectory},
		{"dir symlink (absolute)", testfs.DirSymlink(filepath.Join(root, "target")), windows.RemoveDirectory},
		{"dir symlink (relative)", testfs.DirSymlink(`..\target`), windows.RemoveDirectory},
		{"file symlink", testfs.Symlink(`..\target.txt`), windows.DeleteFile},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(root, sanitize(tt.name))
			testfs.Build(t, dir, testfs.Tree{"link": tt.entry})
			link := filepath.Join(dir, "link")
			t.Logf("V3: %s: %s", tt.name, describe(link))

			p16, err := windows.UTF16PtrFromString(testfs.ExtendedPath(link))
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.remove(p16); err != nil {
				t.Fatalf("V3: %s: removing the link failed: %v", tt.name, err)
			}
			if testfs.Exists(t, link) {
				t.Errorf("V3: %s: the link still exists after removal", tt.name)
			}
			if got := testfs.ReadFile(t, filepath.Join(root, "target", "marker.txt")); got != "marker" {
				t.Errorf("V3: %s: marker in the target = %q", tt.name, got)
			}
			if got := testfs.ReadFile(t, filepath.Join(root, "target.txt")); got != "file target" {
				t.Errorf("V3: %s: target file = %q", tt.name, got)
			}
			t.Logf("V3: %s: removed the link itself; the target and its contents remain", tt.name)
		})
	}

	// 誤った関数で消そうとした場合（フォルダ用のリンクに DeleteFileW、ファイル用のリンクに RemoveDirectoryW）の動作も記録する。
	t.Run("mismatched removal", func(t *testing.T) {
		dir := filepath.Join(root, "mismatch")
		testfs.Build(t, dir, testfs.Tree{"junction": testfs.Junction("../target")})
		testfs.Build(t, dir, testfs.Tree{"filelink": testfs.Symlink(`..\target.txt`)})
		j16, _ := windows.UTF16PtrFromString(testfs.ExtendedPath(filepath.Join(dir, "junction")))
		f16, _ := windows.UTF16PtrFromString(testfs.ExtendedPath(filepath.Join(dir, "filelink")))
		t.Logf("V3: DeleteFileW on a junction: %v", windows.DeleteFile(j16))
		t.Logf("V3: RemoveDirectoryW on a file symlink: %v", windows.RemoveDirectory(f16))
		if got := testfs.ReadFile(t, filepath.Join(root, "target", "marker.txt")); got != "marker" {
			t.Errorf("V3: marker in the target = %q", got)
		}
	})
}

func sanitize(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !('a' <= c && c <= 'z' || '0' <= c && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

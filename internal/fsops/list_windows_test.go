package fsops

import (
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// setAttrs は、path のファイル属性に add を加える。
func setAttrs(t *testing.T, path string, add uint32) {
	t.Helper()
	p16, err := windows.UTF16PtrFromString(testfs.ExtendedPath(path))
	if err != nil {
		t.Fatal(err)
	}
	a, err := windows.GetFileAttributes(p16)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetFileAttributes(p16, a|add); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.SetFileAttributes(p16, a) })
}

// TestListAttributesWindows は、隠し属性と、ファイルの読み取り専用属性を返し、フォルダの読み取り専用属性は見ないことを確かめる（§14.3）。
func TestListAttributesWindows(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"d/hidden.txt": testfs.File("h"), "d/hidden-dir": testfs.Dir(), "d/ro-dir": testfs.Dir(), "d/system.txt": testfs.File("s")})
	dir := filepath.Join(root, "d")
	setAttrs(t, filepath.Join(dir, "hidden.txt"), windows.FILE_ATTRIBUTE_HIDDEN)
	setAttrs(t, filepath.Join(dir, "hidden-dir"), windows.FILE_ATTRIBUTE_HIDDEN|windows.FILE_ATTRIBUTE_SYSTEM)
	setAttrs(t, filepath.Join(dir, "ro-dir"), windows.FILE_ATTRIBUTE_READONLY)
	setAttrs(t, filepath.Join(dir, "system.txt"), windows.FILE_ATTRIBUTE_SYSTEM) // システム属性だけでは隠しにしない（UI の規則。fsops は属性をそのまま返す）
	want := map[string][2]bool{                                                  // Hidden, ReadOnly
		"hidden.txt": {true, false},
		"hidden-dir": {true, false},
		"ro-dir":     {false, false},
		"system.txt": {false, false},
	}
	entries, err := ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if w := want[e.Name]; e.Hidden != w[0] || e.ReadOnly != w[1] {
			t.Errorf("ReadDir: %s hidden %v readOnly %v, want %v", e.Name, e.Hidden, e.ReadOnly, w)
		}
		if l, err := Lstat(filepath.Join(dir, e.Name)); err != nil || l.Hidden != e.Hidden || l.ReadOnly != e.ReadOnly {
			t.Errorf("Lstat(%s) = %+v, %v; want the same attributes as ReadDir", e.Name, l, err)
		}
	}
	if len(entries) != len(want) {
		t.Errorf("entries = %+v", entries)
	}
}

// TestListJunction は、ジャンクションを TypeJunction として返し、ReadDir に渡すと中に入り、Readlink でリンク先を返すことを確かめる（§14.3）。
func TestListJunction(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	target := filepath.Join(root, "target")
	testfs.Build(t, root, testfs.Tree{"target/x": testfs.File("x"), "j": testfs.Junction("target")})
	j := filepath.Join(root, "j")
	if e, err := Lstat(j); err != nil || e.Info.Type != TypeJunction {
		t.Errorf("Lstat(junction) = %+v, %v", e, err)
	}
	if entries, err := ReadDir(j); err != nil || len(entries) != 1 || entries[0].Name != "x" {
		t.Errorf("ReadDir(junction) = %+v, %v, want the target's entries", entries, err)
	}
	if got, err := Readlink(j); err != nil || got != target {
		t.Errorf("Readlink(junction) = %q, %v, want %q", got, err, target)
	}
}

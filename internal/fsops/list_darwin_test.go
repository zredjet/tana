package fsops

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/unix"
)

// TestListAttributesDarwin は、UF_HIDDEN を隠し、ロック（UF_IMMUTABLE）を読み取り専用として返すことを確かめる（§14.3）。
func TestListAttributesDarwin(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"d/hidden.txt": testfs.File("h"), "d/locked.txt": testfs.File("l"), "d/.dot": testfs.File("d")})
	dir := filepath.Join(root, "d")
	if err := unix.Chflags(filepath.Join(dir, "hidden.txt"), unix.UF_HIDDEN); err != nil {
		t.Fatal(err)
	}
	testfs.SetImmutable(t, filepath.Join(dir, "locked.txt"))
	want := map[string][2]bool{ // Hidden, ReadOnly
		".dot":       {false, false}, // 名前の . による判断は UI が行う
		"hidden.txt": {true, false},
		"locked.txt": {false, true},
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

// TestReadDirAppleDoubleOtherVolumes は、exFAT・FAT32 で、拡張属性の保存先の `._名前` を含めず、孤立した `._名前` は含めることを確かめる（§8.5、§14.3）。
func TestReadDirAppleDoubleOtherVolumes(t *testing.T) {
	t.Parallel()
	for _, env := range []string{testfs.ExFATEnv, testfs.FAT32Env} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			root := testfs.EnvDir(t, env)
			testfs.Build(t, root, testfs.Tree{"m/x": testfs.File("x"), "m/._orphan": testfs.File("o")})
			if err := unix.Lsetxattr(filepath.Join(root, "m", "x"), "com.example.tana", []byte("v"), 0); err != nil {
				t.Fatal(err)
			}
			entries, err := ReadDir(filepath.Join(root, "m"))
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, e := range entries {
				names = append(names, e.Name)
			}
			t.Logf("raw names %+q", testfs.ListRawNames(t, filepath.Join(root, "m")))
			if want := []string{"._orphan", "x"}; !slices.Equal(names, want) {
				t.Errorf("names = %+q, want %+q", names, want)
			}
		})
	}
}

// TestMkdirLockedParentDarwin は、ロック（UF_IMMUTABLE）された親フォルダの中に作れないとき、KindReadOnly にすることを確かめる（§11.4）。
func TestMkdirLockedParentDarwin(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"locked": testfs.Dir()})
	locked := filepath.Join(root, "locked")
	testfs.SetImmutable(t, locked)
	wantOpError(t, Mkdir(locked, "x"), "mkdir", KindReadOnly, filepath.Join(locked, "x"))
}

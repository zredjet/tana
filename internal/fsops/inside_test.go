package fsops

import (
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestDestInside は、コピー先がコピー元の内側か（§8.3）の判定を確かめる。リンク経由の場合を含む（§18.4「計画」）。
func TestDestInside(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/sub/deeper":   testfs.Dir(),
		"src-sibling":      testfs.Dir(), // 名前の前半が同じ別のフォルダ
		"other":            testfs.Dir(),
		"link-into-src":    testfs.DirSymlink(filepath.Join(root, "src", "sub")),
		"link-to-other":    testfs.DirSymlink("other"),
		"src/sub/link-out": testfs.DirSymlink(filepath.Join(root, "other")),
	})
	src := filepath.Join(root, "src")
	tests := []struct {
		name string
		dest string
		want bool
	}{
		{"src itself", src, true},
		{"child", filepath.Join(src, "sub"), true},
		{"grandchild", filepath.Join(src, "sub", "deeper"), true},
		{"parent", root, false},
		{"sibling with the same prefix", filepath.Join(root, "src-sibling"), false},
		{"other", filepath.Join(root, "other"), false},
		{"symlink into src", filepath.Join(root, "link-into-src"), true},
		{"symlink to other", filepath.Join(root, "link-to-other"), false},
		{"symlink inside src that points outside", filepath.Join(src, "sub", "link-out"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := destInside(src, tt.dest)
			if err != nil || got != tt.want {
				t.Errorf("destInside(src, %s) = %v, %v; want %v", tt.dest, got, err, tt.want)
			}
		})
	}
}

// TestDestInsideCaseAlias は、大文字小文字の違うパスで指した内側のフォルダも内側と判定することを確かめる（文字列で比べない）。
func TestDestInsideCaseAlias(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	if !testfs.FoldsCase(t, root) {
		t.Skip("the temp volume is case-sensitive")
	}
	testfs.Build(t, root, testfs.Tree{"Src/Sub": testfs.Dir()})
	got, err := destInside(filepath.Join(root, "Src"), filepath.Join(root, "SRC", "sub"))
	if err != nil || !got {
		t.Errorf("destInside via a different case = %v, %v; want true", got, err)
	}
}

// TestDestInsideJunction は、ジャンクション経由でコピー元の内側を指すコピー先を内側と判定することを確かめる（§18.4「計画」、Windows）。
func TestDestInsideJunction(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/sub":      testfs.Dir(),
		"other":        testfs.Dir(),
		"j-into-src":   testfs.Junction("src/sub"),
		"j-to-other":   testfs.Junction("other"),
		"j-to-src-top": testfs.Junction("src"),
	})
	src := filepath.Join(root, "src")
	for _, tt := range []struct {
		dest string
		want bool
	}{
		{filepath.Join(root, "j-into-src"), true},
		{filepath.Join(root, "j-to-src-top"), true},
		{filepath.Join(root, "j-to-src-top", "sub"), true},
		{filepath.Join(root, "j-to-other"), false},
	} {
		got, err := destInside(src, tt.dest)
		if err != nil || got != tt.want {
			t.Errorf("destInside(src, %s) = %v, %v; want %v", tt.dest, got, err, tt.want)
		}
	}
}

// TestDestInsideLongPath は、260 文字を超えるパスでも判定できることを確かめる。
func TestDestInsideLongPath(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.MkdirAll(t, filepath.Join(root, "src"))
	long := testfs.LongPath(t, filepath.Join(root, "src"))
	got, err := destInside(filepath.Join(root, "src"), long)
	if err != nil || !got {
		t.Errorf("destInside with a long path = %v, %v; want true", got, err)
	}
}

// TestDestInsideOtherVolume は、別のボリュームのコピー先を内側と判定しないことを確かめる。
func TestDestInsideOtherVolume(t *testing.T) {
	t.Parallel()
	cross := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	got, err := destInside(root, cross)
	if err != nil || got {
		t.Errorf("destInside(temp, crossvol) = %v, %v; want false", got, err)
	}
}

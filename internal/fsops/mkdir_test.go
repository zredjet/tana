package fsops

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestMkdir は、フォルダを作り、名前をバイト単位でそのまま使うことを確かめる（§11.4、I6）。
func TestMkdir(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	for _, name := range []string{"new", "新しいフォルダ", "か\u3099（NFD）", "e\u0301", "with space"} {
		if err := Mkdir(root, name); err != nil {
			t.Errorf("Mkdir(%q): %v", name, err)
			continue
		}
		if e, err := Lstat(filepath.Join(root, name)); err != nil || e.Info.Type != TypeDir {
			t.Errorf("Lstat(%q) = %+v, %v, want a folder", name, e, err)
		}
	}
	names := testfs.ListRawNames(t, root)
	for _, name := range []string{"new", "新しいフォルダ", "か\u3099（NFD）", "e\u0301", "with space"} {
		if !slices.Contains(names, name) {
			t.Errorf("%+q not in %+q (the name must not be converted. I6)", name, names)
		}
	}
}

// TestMkdirExisting は、同じ名前のエントリがあれば KindExist にし、既存のものを変えないことを確かめる（I1）。
func TestMkdirExisting(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"file":     testfs.File("data"),
		"dir/x":    testfs.File("x"),
		"link":     testfs.DirSymlink("dir"),
		"dangling": testfs.DirSymlink("missing"),
	})
	before := testfs.Take(t, root)
	for _, name := range []string{"file", "dir", "link", "dangling"} {
		err := Mkdir(root, name)
		wantOpError(t, err, "mkdir", KindExist, filepath.Join(root, name))
		if oe, _ := err.(*OpError); oe == nil || !oe.OnDest {
			t.Errorf("Mkdir(%s): OnDest = false, want true", name)
		}
	}
	if d := testfs.Diff(before, testfs.Take(t, root)); len(d) > 0 {
		t.Errorf("existing entries changed (I1): %q", d)
	}
}

// TestMkdirFoldedNames は、大文字小文字・正規化だけが違う名前を同じ名前として扱うボリュームで、既存のものを変えずに KindExist にすることを確かめる。
func TestMkdirFoldedNames(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	// ボリュームが名前をまとめるかは、中にエントリを作って調べる（root の更新日時が変わる）ので、状態を記録する前に調べる。
	foldsCase, foldsNorm := testfs.FoldsCase(t, root), testfs.FoldsNormalization(t, root)
	testfs.Build(t, root, testfs.Tree{"Foo": testfs.Dir(), "が": testfs.File("nfc")})
	before := testfs.Take(t, root)
	if foldsCase {
		wantOpError(t, Mkdir(root, "foo"), "mkdir", KindExist, filepath.Join(root, "foo"))
	}
	if foldsNorm {
		wantOpError(t, Mkdir(root, "か\u3099"), "mkdir", KindExist, filepath.Join(root, "か\u3099"))
	}
	if d := testfs.Diff(before, testfs.Take(t, root)); len(d) > 0 {
		t.Errorf("existing entries changed (I1): %q", d)
	}
}

// TestMkdirInvalidName は、Rename と同じ名前の検査（§11.3）で KindInvalidName にし、何も作らないことを確かめる。
// 使えない名前からはパスを作らないので、エラーの Path は parent。
func TestMkdirInvalidName(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	names := []string{"", ".", "..", "a/b", "a\x00b"}
	if runtime.GOOS == "windows" {
		names = append(names, `a\b`, "CON", "nul.txt", "foo.", "foo ", "a:b", "a*b", "a\x01b")
	}
	for _, name := range names {
		err := Mkdir(root, name)
		wantOpError(t, err, "mkdir", KindInvalidName, root)
	}
	if got := testfs.ListRawNames(t, root); len(got) != 0 {
		t.Errorf("created %+q", got)
	}
}

// TestMkdirParent は、親フォルダの検査と分類を確かめる。
func TestMkdirParent(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"file": testfs.File("f")})
	err := Mkdir("relative", "x")
	wantOpError(t, err, "mkdir", KindInvalidRequest, "relative")
	missing := filepath.Join(root, "missing")
	wantOpError(t, Mkdir(missing, "x"), "mkdir", KindNotFound, filepath.Join(missing, "x"))
	file := filepath.Join(root, "file")
	wantOpError(t, Mkdir(file, "x"), "mkdir", KindNotFound, filepath.Join(file, "x"))
}

// TestMkdirPaths は、長いパス・Win32 の正規化で変わる名前の親と、リンクの親（途中の要素のリンクは辿る）の中に作れることを確かめる（§8.2、§11.4）。
func TestMkdirPaths(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	long := testfs.LongPath(t, root)
	if err := Mkdir(long, "x"); err != nil {
		t.Errorf("Mkdir(long): %v", err)
	}
	testfs.Build(t, root, testfs.Tree{
		testfs.NameTrailingDot + "/in": testfs.File("i"),
		testfs.NamePlain:               testfs.Dir(),
		"target":                       testfs.Dir(),
		"link":                         testfs.DirSymlink("target"),
	})
	if err := Mkdir(filepath.Join(root, testfs.NameTrailingDot), "made"); err != nil {
		t.Errorf("Mkdir(%q): %v", testfs.NameTrailingDot, err)
	}
	if names := testfs.ListRawNames(t, filepath.Join(root, testfs.NameTrailingDot)); !slices.Equal(names, []string{"in", "made"}) {
		t.Errorf("%q: %+q, want in and made", testfs.NameTrailingDot, names)
	}
	if names := testfs.ListRawNames(t, filepath.Join(root, testfs.NamePlain)); len(names) != 0 {
		t.Errorf("created in %q instead (Win32 normalization): %+q", testfs.NamePlain, names)
	}
	if err := Mkdir(filepath.Join(root, "link"), "via-link"); err != nil {
		t.Errorf("Mkdir(link): %v", err)
	}
	if !testfs.Exists(t, filepath.Join(root, "target", "via-link")) {
		t.Error("not created in the link's target")
	}
}

// TestMkdirReadOnlyParent は、書き込めない親フォルダの分類を確かめる。
// Unix は書き込み権限のない親で KindPermission。Windows のフォルダの読み取り専用属性は保護を意味しないので、作れる（§11.4）。
func TestMkdirReadOnlyParent(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"ro": testfs.Dir()})
	ro := filepath.Join(root, "ro")
	testfs.SetReadOnly(t, ro)
	err := Mkdir(ro, "x")
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Errorf("Mkdir in a folder with the read-only attribute: %v, want success", err)
		}
		return
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the write permission of folders")
	}
	wantOpError(t, err, "mkdir", KindPermission, filepath.Join(ro, "x"))
}

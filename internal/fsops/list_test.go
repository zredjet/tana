package fsops

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// wantOpError は、err が Kind k の *OpError で、Path が path であることを確かめる（§14.3。パスは \\?\ の付かない形。§8.2）。
func wantOpError(t *testing.T, err error, op string, k Kind, path string) {
	t.Helper()
	oe, ok := errors.AsType[*OpError](err)
	if !ok || oe.Op != op || oe.Kind != k || oe.Path != path {
		t.Errorf("err = %#v, want *OpError{Op: %q, Kind: %v, Path: %q}", err, op, k, path)
		return
	}
	if strings.HasPrefix(oe.Path, `\\?\`) || strings.Contains(oe.Error(), `\\?\`) {
		t.Errorf("error has a \\\\?\\ path: %v", oe)
	}
}

// TestLstatPublic は、Lstat が §14.1 の種類と、名前・サイズ・更新日時・読み取り専用を返すことを確かめる（§14.3）。
func TestLstatPublic(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	mt := time.Date(2021, 6, 7, 8, 9, 10, 0, time.UTC)
	testfs.Build(t, root, testfs.Tree{
		"file.txt":  testfs.File("hello").At(mt),
		"dir":       testfs.Dir().At(mt),
		"ro.txt":    testfs.File("ro").RO(),
		"target":    testfs.Dir(),
		"link":      testfs.DirSymlink("target"),
		"dangling":  testfs.Symlink("missing"),
		"file-link": testfs.Symlink("file.txt"),
	})
	tests := []struct {
		name     string
		typ      EntryType
		size     int64
		readOnly bool
	}{
		{"file.txt", TypeFile, 5, false},
		{"dir", TypeDir, 0, false},
		{"ro.txt", TypeFile, 2, true},
		{"link", TypeSymlink, 0, false},
		{"dangling", TypeSymlink, 0, false},
		{"file-link", TypeSymlink, 0, false},
	}
	for _, tt := range tests {
		e, err := Lstat(filepath.Join(root, tt.name))
		if err != nil {
			t.Errorf("Lstat(%s): %v", tt.name, err)
			continue
		}
		if e.Name != tt.name || e.Info.Type != tt.typ || e.Info.Size != tt.size || e.ReadOnly != tt.readOnly || e.Hidden || e.Err != nil {
			t.Errorf("Lstat(%s) = %+v, want name %q type %v size %d readOnly %v", tt.name, e, tt.name, tt.typ, tt.size, tt.readOnly)
		}
	}
	if e, err := Lstat(filepath.Join(root, "file.txt")); err != nil || !e.Info.ModTime.Equal(mt) {
		t.Errorf("Lstat(file.txt).ModTime = %v, %v; want %v", e.Info.ModTime, err, mt)
	}
}

// TestLstatErrors は、Lstat のエラーが分類された *OpError であることを確かめる。
func TestLstatErrors(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	missing := filepath.Join(root, "missing")
	_, err := Lstat(missing)
	wantOpError(t, err, "lstat", KindNotFound, missing)
	_, err = Lstat("relative")
	wantOpError(t, err, "lstat", KindInvalidRequest, "relative")
}

// TestReadDirPublic は、ReadDir が名前のバイト順に、リンクを辿らずに列挙し、Lstat と同じ値を返すことを確かめる（§14.3）。
func TestReadDirPublic(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"d/b.txt":    testfs.File("bb"),
		"d/a":        testfs.Dir(),
		"d/C.txt":    testfs.File("c").RO(),
		"d/link":     testfs.DirSymlink("a"),
		"d/dangling": testfs.Symlink("missing"),
		"d/a/inner":  testfs.File("not listed"),
	})
	dir := filepath.Join(root, "d")
	entries, err := ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
		if e.Err != nil {
			t.Errorf("%s: Err = %v", e.Name, e.Err)
		}
		// 列挙の値は、Lstat で 1 つずつ調べた値と同じ。ただし Windows のフォルダの更新日時は、列挙では親フォルダの索引に記録された値で、
		// 中身が変わった後に更新されていないことがある（NTFS。§14.3）ので比べない。
		l, err := Lstat(filepath.Join(dir, e.Name))
		if err != nil {
			t.Errorf("Lstat(%s): %v", e.Name, err)
			continue
		}
		sameTime := l.Info.ModTime.Equal(e.Info.ModTime) || runtime.GOOS == "windows" && e.Info.Type == TypeDir
		if l.Name != e.Name || l.Info.Type != e.Info.Type || l.Info.Size != e.Info.Size || !sameTime ||
			l.Hidden != e.Hidden || l.ReadOnly != e.ReadOnly {
			t.Errorf("%s: ReadDir %+v, Lstat %+v", e.Name, e, l)
		}
	}
	if want := []string{"C.txt", "a", "b.txt", "dangling", "link"}; !slices.Equal(names, want) {
		t.Errorf("names = %q, want %q (byte order)", names, want)
	}
	types := map[string]EntryType{}
	for _, e := range entries {
		types[e.Name] = e.Info.Type
	}
	if types["link"] != TypeSymlink || types["dangling"] != TypeSymlink || types["a"] != TypeDir {
		t.Errorf("types = %v, want links as TypeSymlink (not followed)", types)
	}
}

// TestReadDirEntersLinkedFolder は、ReadDir に渡したフォルダ自体がリンクなら、そのリンク先を列挙することを確かめる
// （利用者がリンクのフォルダに入った場合。§14.3）。fsops の中の走査は、今までどおりリンクに入らない（I4）。
func TestReadDirEntersLinkedFolder(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"target/x":  testfs.File("x"),
		"target/y":  testfs.DirSymlink("x"),
		"dirlink":   testfs.DirSymlink("target"),
		"file.txt":  testfs.File("f"),
		"file-link": testfs.Symlink("file.txt"),
		"dangling":  testfs.DirSymlink("missing"),
	})
	entries, err := ReadDir(filepath.Join(root, "dirlink"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "x" || entries[1].Name != "y" || entries[1].Info.Type != TypeSymlink {
		t.Errorf("ReadDir(dirlink) = %+v, want x and y (the link inside is not followed)", entries)
	}
	if _, err := readDir(filepath.Join(root, "dirlink")); err == nil {
		t.Error("the internal readDir entered a link (I4)")
	}
	for _, name := range []string{"file.txt", "file-link", "dangling", "missing"} {
		p := filepath.Join(root, name)
		_, err := ReadDir(p)
		wantOpError(t, err, "readdir", KindNotFound, p)
	}
	_, err = ReadDir("relative")
	wantOpError(t, err, "readdir", KindInvalidRequest, "relative")
}

// TestListLongPathAndUnsafeNames は、長いパスと、Win32 の正規化で変わる名前（末尾の . や空白、予約名）を、
// 取り違えずに調べ・列挙できることを確かめる（§8.2）。
func TestListLongPathAndUnsafeNames(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	long := testfs.LongPath(t, root)
	testfs.WriteFile(t, filepath.Join(long, "f"), "abc")
	if entries, err := ReadDir(long); err != nil || len(entries) != 1 || entries[0].Name != "f" || entries[0].Info.Size != 3 {
		t.Errorf("ReadDir(long) = %+v, %v", entries, err)
	}
	if e, err := Lstat(filepath.Join(long, "f")); err != nil || e.Info.Size != 3 {
		t.Errorf("Lstat(long/f) = %+v, %v", e, err)
	}

	dir := filepath.Join(root, "unsafe")
	testfs.Build(t, dir, testfs.Tree{
		testfs.NamePlain:         testfs.File("1"),
		testfs.NameTrailingDot:   testfs.File("22"),
		testfs.NameTrailingSpace: testfs.Dir(),
		testfs.NameReservedCON:   testfs.File("4444"),
	})
	entries, err := ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	sizes := map[string]int64{}
	for _, e := range entries {
		sizes[e.Name] = e.Info.Size
	}
	for name, size := range map[string]int64{testfs.NamePlain: 1, testfs.NameTrailingDot: 2, testfs.NameReservedCON: 4} {
		if got, ok := sizes[name]; !ok || got != size {
			t.Errorf("ReadDir: %q size %d (present %v), want %d", name, got, ok, size)
		}
		if e, err := Lstat(filepath.Join(dir, name)); err != nil || e.Info.Size != size {
			t.Errorf("Lstat(%q) = %+v, %v, want size %d", name, e, err, size)
		}
	}
}

// TestReadlink は、リンク先の文字列を書き換えずに返すことと、エラーの分類を確かめる（§14.3）。
func TestReadlink(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	abs := filepath.Join(root, "target")
	testfs.Build(t, root, testfs.Tree{
		"target":   testfs.Dir(),
		"file.txt": testfs.File("f"),
		"rel":      testfs.DirSymlink("target"),
		"abs":      testfs.DirSymlink(abs),
		"dangling": testfs.Symlink(filepath.Join("no", "such")),
	})
	for name, want := range map[string]string{"rel": "target", "abs": abs, "dangling": filepath.Join("no", "such")} {
		if got, err := Readlink(filepath.Join(root, name)); err != nil || got != want {
			t.Errorf("Readlink(%s) = %q, %v, want %q", name, got, err, want)
		}
	}
	missing := filepath.Join(root, "missing")
	_, err := Readlink(missing)
	wantOpError(t, err, "readlink", KindNotFound, missing)
	if _, err := Readlink(filepath.Join(root, "file.txt")); KindOf(err) == KindNotFound || err == nil {
		t.Errorf("Readlink(a file) = %v, want an error other than KindNotFound", err)
	}
	_, err = Readlink("relative")
	wantOpError(t, err, "readlink", KindInvalidRequest, "relative")
}

// TestPublicEntries は、列挙はできたが調べられなかったエントリを、Err を付けて返し、ほかのエントリを続けることを確かめる（§14.3）。
func TestPublicEntries(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(testfs.TempDir(t), "d")
	failed := &os.PathError{Op: "fstatat", Path: filepath.Join(dir, "bad"), Err: os.ErrPermission}
	got := publicEntries(dir, []dirEntry{
		{name: "bad", statErr: failed},
		{name: "ok", info: EntryInfo{Type: TypeFile, Size: 1}, hidden: true, readOnly: true},
	})
	if len(got) != 2 {
		t.Fatalf("entries = %+v", got)
	}
	if got[0].Name != "bad" || got[0].Err == nil || got[0].Err.Kind != KindPermission || got[0].Err.Path != filepath.Join(dir, "bad") {
		t.Errorf("bad entry = %+v (Err %+v)", got[0], got[0].Err)
	}
	if e := got[1]; e.Name != "ok" || e.Err != nil || e.Info.Size != 1 || !e.Hidden || !e.ReadOnly {
		t.Errorf("ok entry = %+v", e)
	}
}

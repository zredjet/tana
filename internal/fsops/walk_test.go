package fsops

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestReadDir は、リンクを辿らない列挙（§13.1）が、名前順（バイト順）で、種類・サイズ・更新日時・fileID を返すことを確かめる。
func TestReadDir(t *testing.T) {
	t.Parallel()
	testReadDir(t, testfs.TempDir(t), true)
}

// TestReadDirOtherVolumes は、exFAT・FAT32 でも列挙できることを確かめる（Windows では FileIdBothDirectoryInfo への切り替え。V14）。
func TestReadDirOtherVolumes(t *testing.T) {
	t.Parallel()
	for _, env := range []string{testfs.ExFATEnv, testfs.FAT32Env, testfs.CrossVolEnv} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			testReadDir(t, testfs.EnvDir(t, env), env == testfs.CrossVolEnv)
		})
	}
}

func testReadDir(t *testing.T, root string, links bool) {
	t.Helper()
	mt := time.Date(2022, 3, 4, 5, 6, 7, 0, time.UTC)
	tree := testfs.Tree{
		"d/b.txt":                  testfs.File("bb").At(mt),
		"d/a.txt":                  testfs.File("a"),
		"d/sub/inner":              testfs.File("i"),
		"d/sub":                    testfs.Dir().At(mt),
		"d/" + testfs.NameNFD:      testfs.File("nfd"),
		"d/" + testfs.NameJapanese: testfs.File("ja"),
		"d/B.txt2":                 testfs.File("upper"),
	}
	if links {
		tree["d/link"] = testfs.Symlink("a.txt")
		tree["d/dirlink"] = testfs.DirSymlink("sub")
	}
	testfs.Build(t, root, tree)
	dir := filepath.Join(root, "d")
	entries, err := readDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.name)
	}
	want := slices.Clone(testfs.ListNames(t, dir))
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("names = %+q, want %+q (byte order)", names, want)
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.name)
		ls, err := lstatEntry(p)
		if err != nil {
			t.Fatal(err)
		}
		if e.info.Type != ls.Type || e.info.Size != ls.Size || !e.info.ModTime.Equal(ls.ModTime) {
			t.Errorf("%s: readDir info = %+v, lstatEntry = %+v", e.name, e.info, ls)
		}
		id, err := fileIDOf(p)
		if err != nil {
			t.Fatal(err)
		}
		if e.id != id.id {
			t.Errorf("%s: readDir fileID = %+v, fileIDOf = %+v", e.name, e.id, id.id)
		}
	}
	if links {
		for _, e := range entries {
			if (e.name == "link" || e.name == "dirlink") && e.info.Type != TypeSymlink {
				t.Errorf("%s: type = %v, want TypeSymlink (not followed)", e.name, e.info.Type)
			}
		}
	}
}

// TestReadDirWin32UnsafeNames は、末尾が . や空白の名前・予約名を取り違えずに列挙することを確かめる。
func TestReadDirWin32UnsafeNames(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		testfs.NamePlain:         testfs.File("1"),
		testfs.NameTrailingDot:   testfs.File("22"),
		testfs.NameTrailingSpace: testfs.Dir(),
		testfs.NameReservedCON:   testfs.File("4444"),
	})
	entries, err := readDir(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]EntryInfo{}
	for _, e := range entries {
		got[e.name] = e.info
	}
	for name, want := range map[string]EntryInfo{
		testfs.NamePlain:         {Type: TypeFile, Size: 1},
		testfs.NameTrailingDot:   {Type: TypeFile, Size: 2},
		testfs.NameTrailingSpace: {Type: TypeDir},
		testfs.NameReservedCON:   {Type: TypeFile, Size: 4},
	} {
		if g, ok := got[name]; !ok || g.Type != want.Type || g.Size != want.Size {
			t.Errorf("%q: %+v (present=%v), want %+v", name, g, ok, want)
		}
	}
}

// TestReadDirJunction は、ジャンクションを TypeJunction として返し、入り込まないことを確かめる（Windows）。
func TestReadDirJunction(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"target/x": testfs.File("x"), "d/j": testfs.Junction("target")})
	entries, err := readDir(filepath.Join(root, "d"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].info.Type != TypeJunction {
		t.Errorf("entries = %+v, want one TypeJunction", entries)
	}
}

// TestReadDirNotADir は、フォルダでないものやリンクを列挙しないことを確かめる（I4）。
func TestReadDirNotADir(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"f": testfs.File("x"), "target/x": testfs.File("x"), "link": testfs.DirSymlink("target")})
	for _, name := range []string{"f", "link", "missing"} {
		if entries, err := readDir(filepath.Join(root, name)); err == nil {
			t.Errorf("readDir(%s) = %+v, want an error", name, entries)
		}
	}
}

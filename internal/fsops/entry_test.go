package fsops

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// checkEntry は lstatEntry(path) が want の種類・サイズを返すことを確かめる。modTime がゼロ値でなければ更新日時も比べる。
func checkEntry(t *testing.T, path string, want EntryType, size int64, modTime time.Time) {
	t.Helper()
	info, err := lstatEntry(path)
	if err != nil {
		t.Fatalf("lstatEntry(%s): %v", path, err)
	}
	if info.Type != want || info.Size != size {
		t.Errorf("lstatEntry(%s) = %v size %d, want %v size %d", path, info.Type, info.Size, want, size)
	}
	if !modTime.IsZero() && !info.ModTime.Equal(modTime) {
		t.Errorf("lstatEntry(%s).ModTime = %v, want %v", path, info.ModTime, modTime)
	}
}

func TestLstatEntryFileAndDir(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	mt := time.Date(2021, 6, 7, 8, 9, 10, 0, time.UTC)
	testfs.Build(t, root, testfs.Tree{
		"file.txt":      testfs.File("hello").At(mt),
		"empty.txt":     testfs.File(""),
		"dir":           testfs.Dir().At(mt),
		"ro.txt":        testfs.File("ro").RO(),
		"long-name.txt": testfs.File("x"),
	})
	checkEntry(t, filepath.Join(root, "file.txt"), TypeFile, 5, mt)
	checkEntry(t, filepath.Join(root, "empty.txt"), TypeFile, 0, time.Time{})
	checkEntry(t, filepath.Join(root, "dir"), TypeDir, 0, mt)
	checkEntry(t, filepath.Join(root, "ro.txt"), TypeFile, 2, time.Time{})

	long := testfs.LongPath(t, root)
	testfs.WriteFile(t, filepath.Join(long, "f"), "abc")
	checkEntry(t, long, TypeDir, 0, time.Time{})
	checkEntry(t, filepath.Join(long, "f"), TypeFile, 3, time.Time{})
}

func TestLstatEntryNotFound(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(testfs.TempDir(t), "missing")
	_, err := lstatEntry(missing)
	if got := classify(err, classifyOpts{}); got != KindNotFound {
		t.Errorf("classify(lstatEntry(missing)) = %v (%v), want %v", got, err, KindNotFound)
	}
	// エラーで返すパスは \\?\ の付かない、渡したままの形（SPEC §8.2）。
	pe, ok := err.(*fs.PathError)
	if !ok || pe.Path != missing {
		t.Errorf("lstatEntry error = %#v, want *fs.PathError with Path %q", err, missing)
	}
}

// TestLstatEntryLinks は、リンクを辿らずにリンク自体の種類を返すことを確かめる（I4 の前提）。
func TestLstatEntryLinks(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"target.txt":       testfs.File("target"),
		"targetdir/marker": testfs.File("m"),
	})
	tests := []struct {
		name  string
		entry testfs.Entry
	}{
		{"file symlink", testfs.Symlink("target.txt")},
		{"dir symlink", testfs.DirSymlink("targetdir")},
		{"dangling file symlink", testfs.Symlink("missing")},
		{"dangling dir symlink", testfs.DirSymlink("missing")},
		{"absolute symlink", testfs.DirSymlink(filepath.Join(root, "targetdir"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join(root, tt.name)
			testfs.Build(t, dir, testfs.Tree{"link": tt.entry})
			link := filepath.Join(dir, "link")
			// リンク自体の更新日時（リンク先のものではない）を返すこと。
			fi, err := os.Lstat(testfs.ExtendedPath(link))
			if err != nil {
				t.Fatal(err)
			}
			checkEntry(t, link, TypeSymlink, 0, fi.ModTime())
		})
	}
}

func TestLstatEntryJunction(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"target/marker": testfs.File("m"),
		"junction":      testfs.Junction("target"),
	})
	checkEntry(t, filepath.Join(root, "junction"), TypeJunction, 0, time.Time{})
}

func TestLstatEntryFIFO(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"fifo": testfs.FIFO()})
	checkEntry(t, filepath.Join(root, "fifo"), TypeSpecial, 0, time.Time{})
}

// TestLstatEntryWin32UnsafeNames は、末尾が . や空白の名前と予約名のエントリを、
// 同名の別エントリ（foo）と取り違えずに調べられることを確かめる（SPEC §8.2）。
// 名前ごとに種類・サイズを変えておき、取り違えれば結果が変わるようにする。
func TestLstatEntryWin32UnsafeNames(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		testfs.NamePlain:         testfs.File("1"),
		testfs.NameTrailingDot:   testfs.Dir(),
		testfs.NameTrailingSpace: testfs.File("333"),
		testfs.NameReservedCON:   testfs.File("4444"),
		testfs.NameReservedNUL:   testfs.File("55555"),
	})
	checkEntry(t, filepath.Join(root, testfs.NamePlain), TypeFile, 1, time.Time{})
	checkEntry(t, filepath.Join(root, testfs.NameTrailingDot), TypeDir, 0, time.Time{})
	checkEntry(t, filepath.Join(root, testfs.NameTrailingSpace), TypeFile, 3, time.Time{})
	checkEntry(t, filepath.Join(root, testfs.NameReservedCON), TypeFile, 4, time.Time{})
	checkEntry(t, filepath.Join(root, testfs.NameReservedNUL), TypeFile, 5, time.Time{})
}

//go:build unix

package fsops

import (
	"errors"
	"os"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/unix"
)

// TestListFDStatError は、列挙はできたが調べられなかった 1 件のエントリで、フォルダ全体の列挙を失敗にせず、
// そのエントリに statErr（エントリのパス付き）を付けて返すことを確かめる（1 件のために兄弟のエントリを処理できなくならないため）。
func TestListFDStatError(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"a": testfs.File("a"), "b": testfs.File("b"), "c": testfs.File("c")})
	f, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	stat := func(fd int, name string) (dirEntry, error) {
		if name == "b" {
			return dirEntry{}, unix.EIO
		}
		return statAt(fd, name)
	}
	entries, err := listFDWith(int(f.Fd()), root, stat)
	if err != nil {
		t.Fatalf("listFDWith = %v, want the other entries", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %+v, want 3", entries)
	}
	for _, e := range entries {
		var pe *os.PathError
		switch {
		case e.name == "b" && (!errors.As(e.statErr, &pe) || pe.Path != root+"/b" || !errors.Is(e.statErr, unix.EIO)):
			t.Errorf("b: statErr = %v, want EIO for %s/b", e.statErr, root)
		case e.name != "b" && (e.statErr != nil || e.info.Type != TypeFile):
			t.Errorf("%s: %+v, want an ordinary file", e.name, e)
		}
	}
}

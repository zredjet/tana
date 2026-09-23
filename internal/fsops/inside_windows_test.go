package fsops

import (
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// TestDestInsideShortName は、8.3 形式の短縮名で指したコピー元の内側を内側と判定することを確かめる（§8.3）。
func TestDestInsideShortName(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"long source folder name/sub": testfs.Dir()})
	long := filepath.Join(root, "long source folder name")
	l16, err := windows.UTF16PtrFromString(long)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(l16, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Skipf("GetShortPathName: %v (8.3 names may be disabled on this volume)", err)
	}
	short := windows.UTF16ToString(buf[:n])
	if short == long {
		t.Skip("no 8.3 short name on this volume")
	}
	t.Logf("short name: %s", short)
	got, err := destInside(long, filepath.Join(short, "sub"))
	if err != nil || !got {
		t.Errorf("destInside(long, short\\sub) = %v, %v; want true", got, err)
	}
	got, err = destInside(short, filepath.Join(long, "sub"))
	if err != nil || !got {
		t.Errorf("destInside(short, long\\sub) = %v, %v; want true", got, err)
	}
}

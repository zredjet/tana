package testfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetImmutable(t *testing.T) {
	t.Parallel()
	root := TempDir(t)
	p := filepath.Join(root, "locked.txt")
	WriteFile(t, p, "x")
	SetImmutable(t, p)
	if err := os.Remove(p); err == nil {
		t.Fatal("removing an immutable file succeeded")
	}
	f, err := os.OpenFile(p, os.O_WRONLY, 0)
	if err == nil {
		f.Close()
		t.Error("opening an immutable file for writing succeeded")
	}
}

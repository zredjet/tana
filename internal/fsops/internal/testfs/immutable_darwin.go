package testfs

import (
	"testing"

	"golang.org/x/sys/unix"
)

// SetImmutable は path に macOS のロック（UF_IMMUTABLE）を付ける。テストの終了時に外す。
func SetImmutable(t testing.TB, path string) {
	t.Helper()
	if err := unix.Chflags(path, unix.UF_IMMUTABLE); err != nil {
		t.Fatalf("chflags uchg %s: %v", path, err)
	}
	t.Cleanup(func() {
		if err := unix.Chflags(path, 0); err != nil {
			t.Errorf("chflags nouchg %s: %v", path, err)
		}
	})
}

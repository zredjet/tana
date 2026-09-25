package fsops

import (
	"os"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// setUnkeptMetadata は、p に Zone.Identifier 以外の代替データストリーム（fsops が保持しない）を付ける。付けられなければ偽。
func setUnkeptMetadata(t *testing.T, p string) bool {
	t.Helper()
	return os.WriteFile(testfs.ExtendedPath(p)+":fsops-test", []byte("x"), 0o644) == nil
}

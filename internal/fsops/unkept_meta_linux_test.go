package fsops

import (
	"testing"

	"golang.org/x/sys/unix"
)

// setUnkeptMetadata は、p に user.* の拡張属性（fsops が保持しない）を付ける。付けられなければ（ファイルシステムが対応しないなど）偽。
func setUnkeptMetadata(t *testing.T, p string) bool {
	t.Helper()
	return unix.Lsetxattr(p, "user.fsops-test", []byte("x"), 0) == nil
}

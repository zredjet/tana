package fsops

import (
	"testing"

	"golang.org/x/sys/unix"
)

// setUnkeptMetadata は、p に Finder のタグ（fsops が保持しない拡張属性）を付ける。付けられなければ偽。
func setUnkeptMetadata(t *testing.T, p string) bool {
	t.Helper()
	return unix.Lsetxattr(p, "com.apple.metadata:_kMDItemUserTags", []byte("bplist00\xa1\x01URed\n6\x08\x0a\x00\x00\x00\x00\x00\x00\x01\x01\x00\x00\x00\x00\x00\x00\x00\x02\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x10"), 0) == nil
}

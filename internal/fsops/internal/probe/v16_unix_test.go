//go:build unix

package probe

import (
	"fmt"
	"testing"

	"golang.org/x/sys/unix"
)

// fileIDs は、path の Dev と Ino（SPEC §8.3 の Unix の fileID）を 1 行にまとめる。
func fileIDs(t *testing.T, path string) string {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return "Lstat: " + err.Error()
	}
	return fmt.Sprintf("dev=%d ino=%d", st.Dev, st.Ino)
}

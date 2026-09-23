//go:build unix

package probe

import (
	"fmt"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// volumeInfo は path のあるボリュームの識別子（Stat_t.Dev）と説明を返す。
func volumeInfo(t *testing.T, path string) (id, desc string) {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		t.Fatal(err)
	}
	var fs unix.Statfs_t
	if err := unix.Statfs(path, &fs); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(st.Dev), fmt.Sprintf("dev=%d %s", st.Dev, fsTypeName(&fs))
}

func isCrossDevice(errno syscall.Errno) bool { return errno == unix.EXDEV }

package term

import (
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// ptsName は、疑似端末の親の fd から子の名前を得る（ptsname(3) と unlockpt(3)）。
func ptsName(t *testing.T, master int) string {
	t.Helper()
	if err := unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0); err != nil {
		t.Fatalf("TIOCSPTLCK: %v", err)
	}
	n, err := unix.IoctlGetUint32(master, unix.TIOCGPTN)
	if err != nil {
		t.Fatalf("TIOCGPTN: %v", err)
	}
	return "/dev/pts/" + strconv.FormatUint(uint64(n), 10)
}

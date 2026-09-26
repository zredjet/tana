package term

import (
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// ptsName は、疑似端末の親の fd から子の名前を得る（ptsname(3)。x/sys/unix にないので ioctl を直接呼ぶ）。
func ptsName(t *testing.T, master int) string {
	t.Helper()
	if err := unix.IoctlSetInt(master, unix.TIOCPTYGRANT, 0); err != nil {
		t.Fatalf("TIOCPTYGRANT: %v", err)
	}
	if err := unix.IoctlSetInt(master, unix.TIOCPTYUNLK, 0); err != nil {
		t.Fatalf("TIOCPTYUNLK: %v", err)
	}
	var buf [128]byte // TIOCPTYGNAME は 128 バイトを書く
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(master), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&buf[0]))); e != 0 {
		t.Fatalf("TIOCPTYGNAME: %v", e)
	}
	return unix.ByteSliceToString(buf[:])
}

package term

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)

func osInfo() map[string]string {
	m := map[string]string{}
	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		m["os_version"] = "Linux " + unix.ByteSliceToString(u.Release[:])
	}
	return m
}

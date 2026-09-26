package term

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)

func osInfo() map[string]string {
	m := map[string]string{}
	if v, err := unix.Sysctl("kern.osproductversion"); err == nil {
		m["os_version"] = "macOS " + v
	}
	if v, err := unix.Sysctl("kern.osversion"); err == nil {
		m["os_build"] = v
	}
	return m
}

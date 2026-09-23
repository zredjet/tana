package fsops

import (
	"os"

	"golang.org/x/sys/windows"
)

// freeSpace は、path のあるボリュームで使える空き容量（バイト）を返す（§6.4。GetDiskFreeSpaceEx）。
func freeSpace(path string) (uint64, error) {
	s, err := sysPath(path)
	if err != nil {
		return 0, err
	}
	if len(s) > 0 && s[len(s)-1] != '\\' {
		s += `\`
	}
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return 0, err
	}
	var avail uint64
	if err := windows.GetDiskFreeSpaceEx(s16, &avail, nil, nil); err != nil {
		return 0, &os.PathError{Op: "GetDiskFreeSpaceEx", Path: path, Err: err}
	}
	return avail, nil
}

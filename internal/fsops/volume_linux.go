package fsops

import (
	"os"

	"golang.org/x/sys/unix"
)

// freeSpace は、path のあるボリュームで使える空き容量（バイト）を返す（§6.4。statfs の Bavail × Frsize）。
func freeSpace(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := ignoringEINTR(func() error { return unix.Statfs(path, &st) }); err != nil {
		return 0, &os.PathError{Op: "statfs", Path: path, Err: err}
	}
	return st.Bavail * uint64(st.Frsize), nil
}

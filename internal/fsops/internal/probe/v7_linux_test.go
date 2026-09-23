package probe

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func fsTypeName(fs *unix.Statfs_t) string {
	return fmt.Sprintf("fstype=%#x", fs.Type)
}

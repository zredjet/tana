package fsops

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameExclusiveSys は Linux の排他リネーム（§8.4）: renameat2(..., RENAME_NOREPLACE)。
// EINVAL（ファイルシステムが対応しない）または ENOSYS（カーネルが対応しない）なら、§8.4 の代わりの手段を使う。
func renameExclusiveSys(s, d string) error {
	err := unix.Renameat2(unix.AT_FDCWD, s, unix.AT_FDCWD, d, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		return reserveThenRenameSys(s, d)
	}
	if err != nil {
		return &os.LinkError{Op: "renameat2", Old: s, New: d, Err: err}
	}
	return nil
}

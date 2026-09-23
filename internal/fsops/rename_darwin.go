package fsops

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameExclusiveSys は macOS の排他リネーム（§8.4）: renamex_np(src, dst, RENAME_EXCL)。
// RENAME_EXCL が使えないボリューム（exFAT では常に ENOTSUP。V12）では、§8.4 の代わりの手段を使う。
func renameExclusiveSys(s, d string) error {
	err := unix.RenamexNp(s, d, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) {
		return reserveThenRenameSys(s, d)
	}
	if err != nil {
		return &os.LinkError{Op: "renamex_np", Old: s, New: d, Err: err}
	}
	return nil
}

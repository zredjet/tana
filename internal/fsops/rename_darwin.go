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

// renameAtExclusiveSys は、フォルダ fd の中の name を d へ排他リネームする（§13.1 のマージ移動。renameatx_np(RENAME_EXCL)）。
func renameAtExclusiveSys(fd int, name, d string) error {
	err := unix.RenameatxNp(fd, name, unix.AT_FDCWD, d, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) {
		return reserveThenRenameAt(fd, name, d)
	}
	if err != nil {
		return &os.LinkError{Op: "renameatx_np", Old: name, New: d, Err: err}
	}
	return nil
}

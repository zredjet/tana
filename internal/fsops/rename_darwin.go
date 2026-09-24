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

// renameAtExclusiveSys は、フォルダ sfd の中の s を、フォルダ dfd の中の d へ排他リネームする（§8.4。renameatx_np(RENAME_EXCL)）。
// AT_FDCWD ならパス。§13.1 の、確かめて開いたフォルダからの相対の操作に使う。
func renameAtExclusiveSys(sfd int, s string, dfd int, d string) error {
	err := unix.RenameatxNp(sfd, s, dfd, d, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) {
		return reserveThenRenameAt(sfd, s, dfd, d)
	}
	if err != nil {
		return &os.LinkError{Op: "renameatx_np", Old: s, New: d, Err: err}
	}
	return nil
}

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

// renameAtExclusiveSys は、フォルダ sfd の中の s を、フォルダ dfd の中の d へ排他リネームする（§8.4。renameat2(RENAME_NOREPLACE)）。
// AT_FDCWD ならパス。§13.1 の、確かめて開いたフォルダからの相対の操作に使う。
func renameAtExclusiveSys(sfd int, s string, dfd int, d string) error {
	err := unix.Renameat2(sfd, s, dfd, d, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EINVAL) || errors.Is(err, unix.ENOSYS) {
		return reserveThenRenameAt(sfd, s, dfd, d)
	}
	if err != nil {
		return &os.LinkError{Op: "renameat2", Old: s, New: d, Err: err}
	}
	return nil
}

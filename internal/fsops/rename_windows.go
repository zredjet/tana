package fsops

import (
	"os"

	"golang.org/x/sys/windows"
)

// renameExclusiveSys は Windows の排他リネーム（§8.4）: MoveFileExW(src, dst, 0)。
// MOVEFILE_REPLACE_EXISTING も MOVEFILE_COPY_ALLOWED も付けない。
func renameExclusiveSys(s, d string) error {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return err
	}
	d16, err := windows.UTF16PtrFromString(d)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(s16, d16, 0); err != nil {
		return &os.LinkError{Op: "MoveFileEx", Old: s, New: d, Err: err}
	}
	return nil
}

// renamePlainSys は、同じファイルの名前変更（§8.4）に使う OS の通常のリネーム。
func renamePlainSys(s, d string) error { return os.Rename(s, d) }

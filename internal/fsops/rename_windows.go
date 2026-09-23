package fsops

import (
	"os"

	"golang.org/x/sys/windows"
)

// renameExclusiveSys は Windows の排他リネーム（§8.4）: MoveFileExW(src, dst, 0)。
// MOVEFILE_REPLACE_EXISTING も MOVEFILE_COPY_ALLOWED も付けない。
//
// MoveFileExW(src, dst, 0) は、dst が src と同じファイルへのハードリンクのとき、dst が存在するのに成功して src の名前を消す
// （2026-09-23 の CI で確認）。そのため、呼ぶ前に dst の fileID を調べ、src と同じファイルで、
// §8.4 の「同じファイルの名前変更」でなければ ERROR_ALREADY_EXISTS にする。
// dst がその後に別のファイルへ置き換えられても、MoveFileExW(0) は失敗するので上書きにはならない。
func renameExclusiveSys(s, d string) error {
	if di, err := statIDSys(d, false); err == nil {
		if si, err := statIDSys(s, false); err == nil && si.id == di.id && !sameFileRename(s, d) {
			return &os.LinkError{Op: "MoveFileEx", Old: s, New: d, Err: windows.ERROR_ALREADY_EXISTS}
		}
	}
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

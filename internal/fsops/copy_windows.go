package fsops

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// openSourceSys は、コピー元のファイル s を開く（§10.1 の手順 1）。
// 開いたハンドルで、フォルダでないことと、fileID が走査時の want と一致することを確かめる
// （リンクに置き換えられていれば、リンク先の fileID になるので一致しない）。違えば KindSourceChanged の *OpError を返す。
// ほかのプロセスが共有なしで開いていれば ERROR_SHARING_VIOLATION（KindLocked）で失敗する。
func openSourceSys(s string, want fileID) (*os.File, error) {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(s16, windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_SEQUENTIAL_SCAN, 0)
	if err != nil {
		return nil, &os.PathError{Op: "CreateFile", Path: s, Err: err}
	}
	st, err := statIDHandle(h)
	if err != nil {
		windows.CloseHandle(h)
		return nil, &os.PathError{Op: "GetFileInformationByHandle", Path: s, Err: err}
	}
	if st.isDir || st.id != want {
		windows.CloseHandle(h)
		return nil, &OpError{Op: "open", Path: s, Kind: KindSourceChanged}
	}
	return os.NewFile(uintptr(h), s), nil
}

// inUseSys は、s がほかのプロセスに削除を許さずに開かれている（置換リネームで置き換えられない）かを返す（§9.3）。
// 置換リネームは、上書き先が使用中のとき ERROR_ACCESS_DENIED で失敗することがあり、読み取り専用と区別できないため、
// 削除のアクセス権で開き直して ERROR_SHARING_VIOLATION になるかで判定する。
func inUseSys(s string) bool {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return false
	}
	h, err := windows.CreateFile(s16, windows.DELETE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return errors.Is(err, windows.ERROR_SHARING_VIOLATION)
	}
	windows.CloseHandle(h)
	return false
}

// clearReadOnlySys は、fsops の一時ファイル s の読み取り専用属性を外す（§10.1 の手順 8）。外したら真を返す。
func clearReadOnlySys(s string) bool {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return false
	}
	a, err := windows.GetFileAttributes(s16)
	if err != nil || a&windows.FILE_ATTRIBUTE_READONLY == 0 {
		return false
	}
	return windows.SetFileAttributes(s16, a&^windows.FILE_ATTRIBUTE_READONLY) == nil
}

// syncDirSys は、フォルダ s を FILE_FLAG_BACKUP_SEMANTICS で書き込み可能に開いて FlushFileBuffers する（§10.5）。
// 失敗しても処理は続け、警告にもしない（§10.5）ので、常に nil を返す。
func syncDirSys(s string) error {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	h, err := windows.CreateFile(s16, windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return nil
	}
	windows.FlushFileBuffers(h)
	windows.CloseHandle(h)
	return nil
}

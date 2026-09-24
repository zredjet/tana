package fsops

import (
	"errors"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// zoneStream は、インターネットから取得したことを示す代替データストリーム（§15。V6）。
const zoneStream = ":Zone.Identifier"

// keptAttrs は、コピーで保持するファイル属性（§15）。
const keptAttrs = windows.FILE_ATTRIBUTE_READONLY | windows.FILE_ATTRIBUTE_HIDDEN

// settableAttrs は、SetFileInformationByHandle（FileBasicInfo）で設定できるファイル属性。それ以外（フォルダ・リパースポイントなど）は渡さない。
const settableAttrs = windows.FILE_ATTRIBUTE_ARCHIVE | windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_NOT_CONTENT_INDEXED |
	windows.FILE_ATTRIBUTE_OFFLINE | windows.FILE_ATTRIBUTE_READONLY | windows.FILE_ATTRIBUTE_SYSTEM | windows.FILE_ATTRIBUTE_TEMPORARY

// fileBasicInfo は FILE_BASIC_INFO（x/sys/windows に定義がない）。時刻の 0 は「変更しない」。
type fileBasicInfo struct {
	CreationTime   int64
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
	_              uint32
}

// filetimeOf は、t を FILETIME の 64 ビット値にする。
func filetimeOf(t time.Time) int64 {
	ft := windows.NsecToFiletime(t.UnixNano())
	return int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
}

// readExtra は、コピー元 s（\\?\ 形式）の Zone.Identifier を読む（§15）。なければ nil。
// 代替データストリームを扱えないボリューム（exFAT・FAT32 の ERROR_INVALID_NAME）では、ないものとする。
func readExtra(_ *os.File, s string) ([]byte, error) {
	data, err := os.ReadFile(s + zoneStream)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) || errors.Is(err, windows.ERROR_INVALID_NAME) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

// dirMetaSys は、フォルダ s のメタデータ（更新日時・属性）をリンクを辿らずに読む（§15）。
func dirMetaSys(s string) (srcMeta, error) {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return srcMeta{}, err
	}
	var d windows.Win32FileAttributeData
	if err := windows.GetFileAttributesEx(s16, windows.GetFileExInfoStandard, (*byte)(unsafe.Pointer(&d))); err != nil {
		return srcMeta{}, &os.PathError{Op: "GetFileAttributesEx", Path: s, Err: err}
	}
	return srcMeta{mtime: time.Unix(0, d.LastWriteTime.Nanoseconds()), attrs: d.FileAttributes}, nil
}

// setMetaIn は、フォルダ d の中の、fsops が作ったファイル（一時ファイル）・フォルダ name に、メタデータ m を設定する（§15）。
// リンクを辿らずに開き、fileID が作ったときの want と一致することを確かめてから設定する。
// 設定できなかったものがあっても残りは続け、エラーをまとめて返す（呼び出し側は KindMetadata の警告にする）。
// 順序: 照合 → Zone.Identifier（書き込みが要る。書くと更新日時が変わる）→ 更新日時と属性（読み取り専用にするのは最後）。
func setMetaIn(d *secDir, name string, want fileID, m srcMeta, isDir bool, hooks *testHooks) error {
	s := d.sysJoin(name)
	var errs []error
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return err
	}
	// 共有モードに FILE_SHARE_DELETE を含めない。開いている間は名前の変更・削除ができないので、照合した後に
	// パスで書く Zone.Identifier も、照合したファイルに書かれる（名前をリンクへ置き換えられない。I4）。
	// 属性だけのアクセス権で開いたハンドルは共有モードの検査の対象にならないので、FILE_READ_DATA（フォルダでは FILE_LIST_DIRECTORY）も要求する。
	h, err := windows.CreateFile(s16, windows.FILE_READ_DATA|windows.FILE_READ_ATTRIBUTES|windows.FILE_WRITE_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return errors.Join(append(errs, &os.PathError{Op: "CreateFile", Path: s, Err: err})...)
	}
	defer windows.CloseHandle(h)
	st, err := statIDHandle(h)
	if err != nil {
		return errors.Join(append(errs, &os.PathError{Op: "GetFileInformationByHandle", Path: s, Err: err})...)
	}
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bi); err != nil {
		return errors.Join(append(errs, &os.PathError{Op: "GetFileInformationByHandle", Path: s, Err: err})...)
	}
	// ファイルは大きさも照合する（§7.3。fileID だけで判断しない）。
	if st.id != want || !isDir && int64(bi.FileSizeHigh)<<32|int64(bi.FileSizeLow) != m.size {
		return errors.Join(append(errs, &OpError{Op: "metadata", Path: s, Kind: KindSourceChanged})...)
	}
	// 照合した後に Zone.Identifier を書く（照合したハンドルを、名前の変更を許さずに開いたままなので、同じファイルに書かれる）。
	if len(m.extra) > 0 && !isDir {
		hooks.zoneWrite(d.join(name))
		if err := os.WriteFile(s+zoneStream, m.extra, 0o644); err != nil {
			errs = append(errs, err)
		}
	}
	a := (bi.FileAttributes&^keptAttrs | m.attrs&keptAttrs) & settableAttrs
	if a == 0 {
		a = windows.FILE_ATTRIBUTE_NORMAL
	}
	t := filetimeOf(m.mtime)
	info := fileBasicInfo{LastAccessTime: t, LastWriteTime: t, FileAttributes: a}
	if err := windows.SetFileInformationByHandle(h, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		errs = append(errs, &os.PathError{Op: "SetFileInformationByHandle", Path: s, Err: err})
	}
	return errors.Join(errs...)
}

package fsops

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// secDir は、§13.1 の方法で確かめてから開いたフォルダ。削除（§13.2、§13.3）と同一ボリュームのマージ移動（§11.1）で使う。
// Windows では、共有モードに FILE_SHARE_DELETE を含めずに開き、そのフォルダの処理が終わるまで閉じない
// （開いている間、そのフォルダは名前の変更・削除・リンクへの置き換えができない）。
type secDir struct {
	path string // \\?\ の付かない形のパス（結果とフックに使う）
	sys  string // \\?\ 形式のパス
	h    windows.Handle
}

// openSecDir は、フォルダ path を開き、リパースポイント（リンク・ジャンクションなど）でないこと、fileID が want と一致することを確かめる（§13.1）。
// parent・name は Unix に合わせた引数で、Windows では使わない（親のハンドルを開いたままにしているので、パスで開いてよい）。
// 確かめられなければ KindSourceChanged の *OpError を返す。
func openSecDir(parent *secDir, path, name string, want fileID) (*secDir, error) {
	s, err := sysPath(path)
	if err != nil {
		return nil, err
	}
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(s16, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, &OpError{Op: "open", Path: path, Kind: classify(err, classifyOpts{}), Err: &os.PathError{Op: "CreateFile", Path: path, Err: err}}
	}
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bi); err != nil {
		windows.CloseHandle(h)
		return nil, &OpError{Op: "open", Path: path, Kind: classify(err, classifyOpts{}), Err: err}
	}
	var tag uint32
	if bi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		var ti fileAttributeTagInfo
		if err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&ti)), uint32(unsafe.Sizeof(ti))); err != nil {
			windows.CloseHandle(h)
			return nil, &OpError{Op: "open", Path: path, Kind: classify(err, classifyOpts{}), Err: err}
		}
		tag = ti.ReparseTag
	}
	st, err := statIDHandle(h)
	if err != nil || entryTypeFromAttrs(bi.FileAttributes, tag) != TypeDir || st.id != want {
		windows.CloseHandle(h)
		return nil, &OpError{Op: "open", Path: path, Kind: KindSourceChanged, Err: err}
	}
	return &secDir{path: path, sys: s, h: h}, nil
}

func (d *secDir) close() { windows.CloseHandle(d.h) }

// list は中身を名前のバイト順で列挙する（開いたハンドルで列挙する。§13.1）。
func (d *secDir) list() ([]dirEntry, error) {
	entries, err := listHandle(d.h, d.sys)
	return entries, withUserPaths(err, d.path, "")
}

// stat は、中の name をリンクを辿らずに調べる（§13.3 の照合、削除に失敗した後の調べ直し）。
// 祖先のハンドルを開いたままにしているので、パスで調べてよい（§13.3）。
func (d *secDir) stat(name string) (dirEntry, error) {
	e, err := statEntrySys(d.sys + `\` + name)
	e.name = name
	return e, withUserPaths(err, d.path+`\`+name, "")
}

// remove は、中の e を §13.2 の方法（DeleteFileW・RemoveDirectoryW）で削除する。
func (d *secDir) remove(e dirEntry, clearReadOnly bool) error {
	return removeSys(d.sys+`\`+e.name, e.dirAttr, clearReadOnly)
}

// removeTop は、トップレベルのエントリ path を §13.2 の方法で削除する。
func removeTop(path string, e dirEntry, clearReadOnly bool) error {
	s, err := sysPath(path)
	if err != nil {
		return err
	}
	return withUserPaths(removeSys(s, e.dirAttr, clearReadOnly), path, "")
}

// statTop は、トップレベルのエントリ path をリンクを辿らずに調べる。
func statTop(path string) (dirEntry, error) {
	s, err := sysPath(path)
	if err != nil {
		return dirEntry{}, err
	}
	e, err := statEntrySys(s)
	return e, withUserPaths(err, path, "")
}

// statEntrySys は、\\?\ 形式のパス s を調べ、列挙と同じ形の dirEntry を返す（name は設定しない）。
func statEntrySys(s string) (dirEntry, error) {
	info, err := lstatEntrySys(s)
	if err != nil {
		return dirEntry{}, err
	}
	st, err := statIDSys(s, false)
	if err != nil {
		return dirEntry{}, err
	}
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return dirEntry{}, err
	}
	a, err := windows.GetFileAttributes(s16)
	if err != nil {
		return dirEntry{}, &os.PathError{Op: "GetFileAttributes", Path: s, Err: err}
	}
	return dirEntry{info: info, id: st.id, dirAttr: a&windows.FILE_ATTRIBUTE_DIRECTORY != 0}, nil
}

// removeSys は、\\?\ 形式のパス s を削除する（§13.2）。フォルダ属性のあるもの（フォルダ用のリンク・ジャンクションを含む）は
// RemoveDirectoryW、それ以外は DeleteFileW。
// フォルダの読み取り専用属性は外してから削除する（V15）。clearReadOnly が真なら、ファイルの読み取り専用属性も外す（§13.3）。
// 削除に失敗したら、外した属性を元に戻す。
func removeSys(s string, dirAttr, clearReadOnly bool) error {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return err
	}
	restore := func() {}
	if dirAttr || clearReadOnly {
		if a, err := windows.GetFileAttributes(s16); err == nil && a&windows.FILE_ATTRIBUTE_READONLY != 0 {
			if windows.SetFileAttributes(s16, a&^windows.FILE_ATTRIBUTE_READONLY) == nil {
				restore = func() { windows.SetFileAttributes(s16, a) }
			}
		}
	}
	if dirAttr {
		err = windows.RemoveDirectory(s16)
	} else {
		err = windows.DeleteFile(s16)
	}
	if err != nil {
		restore()
		return &os.PathError{Op: "remove", Path: s, Err: err}
	}
	return nil
}

// isMismatchRemoveErr は、種類に合わない方法での削除の失敗を示しうるエラーか（§13.2。ERROR_ACCESS_DENIED・ERROR_DIRECTORY）。
func isMismatchRemoveErr(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_DIRECTORY)
}

package fsops

import (
	"errors"
	"os"
	"time"
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
	return removeSys(d.sys+`\`+e.name, e, clearReadOnly)
}

// removeTop は、トップレベルのエントリ path を §13.2 の方法で削除する。
func removeTop(path string, e dirEntry, clearReadOnly bool) error {
	s, err := sysPath(path)
	if err != nil {
		return err
	}
	return withUserPaths(removeSys(s, e, clearReadOnly), path, "")
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

// statEntrySys は、\\?\ 形式のパス s を 1 つのハンドルで調べ、列挙と同じ形の dirEntry を返す（name は設定しない）。
// 種類・サイズ・更新日時・fileID を同じハンドルから求め、途中で置き換えられても別のエントリの情報が混ざらないようにする。
func statEntrySys(s string) (dirEntry, error) {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return dirEntry{}, err
	}
	h, err := windows.CreateFile(s16, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return dirEntry{}, &os.PathError{Op: "CreateFile", Path: s, Err: err}
	}
	defer windows.CloseHandle(h)
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bi); err != nil {
		return dirEntry{}, &os.PathError{Op: "GetFileInformationByHandle", Path: s, Err: err}
	}
	var tag uint32
	if bi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		var ti fileAttributeTagInfo
		if err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&ti)), uint32(unsafe.Sizeof(ti))); err != nil {
			return dirEntry{}, &os.PathError{Op: "GetFileInformationByHandleEx", Path: s, Err: err}
		}
		tag = ti.ReparseTag
	}
	st, err := statIDHandle(h)
	if err != nil {
		return dirEntry{}, &os.PathError{Op: "GetFileInformationByHandleEx", Path: s, Err: err}
	}
	t := entryTypeFromAttrs(bi.FileAttributes, tag)
	info := EntryInfo{Type: t, ModTime: time.Unix(0, bi.LastWriteTime.Nanoseconds())}
	if t == TypeFile {
		info.Size = int64(bi.FileSizeHigh)<<32 | int64(bi.FileSizeLow)
	}
	return dirEntry{info: info, id: st.id, dirAttr: bi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0}, nil
}

// removeSys は、\\?\ 形式のパス s にあるエントリ e を削除する（§13.2）。フォルダ属性のあるもの（フォルダ用のリンク・ジャンクションを含む）は
// RemoveDirectoryW、それ以外は DeleteFileW。
// フォルダの読み取り専用属性は外してから削除する（V15）。clearReadOnly が真なら、ファイルの読み取り専用属性も外す（§13.3）。
// 属性を外すのは、s にあるものが e と同じ fileID の場合だけ（確かめていないエントリの属性を変えない）。削除に失敗したら元に戻す。
func removeSys(s string, e dirEntry, clearReadOnly bool) error {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return err
	}
	restore := func() {}
	if e.dirAttr || clearReadOnly {
		if a, err := windows.GetFileAttributes(s16); err == nil && a&windows.FILE_ATTRIBUTE_READONLY != 0 {
			if now, err := statIDSys(s, false); err == nil && now.id == e.id &&
				windows.SetFileAttributes(s16, a&^windows.FILE_ATTRIBUTE_READONLY) == nil {
				restore = func() { windows.SetFileAttributes(s16, a) }
			}
		}
	}
	if e.dirAttr {
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

// renameOut は、中の name を dst（\\?\ 形式のパス）へリネームする（§13.1 のマージ移動）。
// Windows に開いたフォルダからの相対のリネームはないので、パスで行う。祖先のハンドルを共有モードに FILE_SHARE_DELETE を含めずに
// 開いたままにしているので、途中の階層を名前の変更・リンクへの置き換えで差し替えられることはない（§13.1）。
// replace が偽なら排他リネーム（§8.4）、真なら置換リネーム（ファイルの上書き）。
func (d *secDir) renameOut(name, dst string, replace bool) error {
	s := d.sys + `\` + name
	if !replace {
		return renameExclusiveSys(s, dst)
	}
	return os.Rename(s, dst)
}

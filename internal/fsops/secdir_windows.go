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
	path  string // \\?\ の付かない形のパス（結果とフックに使う）
	sys   string // \\?\ 形式のパス
	h     windows.Handle
	id    fileID // 開いたフォルダの fileID
	dirty bool   // 中の名前を変えた（作成・リネーム）。§10.5 の同期に使う
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
	return &secDir{path: path, sys: s, h: h, id: want}, nil
}

// openDirHandle は、\\?\ 形式のパス s のフォルダを、共有モードに FILE_SHARE_DELETE を含めずに開く（§13.1。開いている間、
// そのフォルダは名前の変更・削除・リンクへの置き換えができない）。follow が偽ならリパースポイントを辿らない。
// フォルダであること（follow が偽ならリパースポイントでないことも）を確かめ、fileID を返す。確かめられなければ KindSourceChanged。
func openDirHandle(path, s string, follow bool) (windows.Handle, fileID, error) {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return 0, fileID{}, err
	}
	flags := uint32(windows.FILE_FLAG_BACKUP_SEMANTICS)
	if !follow {
		flags |= windows.FILE_FLAG_OPEN_REPARSE_POINT
	}
	h, err := windows.CreateFile(s16, windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return 0, fileID{}, &OpError{Op: "open", Path: path, Kind: classify(err, classifyOpts{}), Err: &os.PathError{Op: "CreateFile", Path: path, Err: err}}
	}
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bi); err != nil {
		windows.CloseHandle(h)
		return 0, fileID{}, &OpError{Op: "open", Path: path, Kind: classify(err, classifyOpts{}), Err: err}
	}
	// 種類は §14.1 と同じ方法で判定する（クラウドファイルのフォルダはリパースポイントでも TypeDir）。
	var tag uint32
	if bi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		var ti fileAttributeTagInfo
		if err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&ti)), uint32(unsafe.Sizeof(ti))); err != nil {
			windows.CloseHandle(h)
			return 0, fileID{}, &OpError{Op: "open", Path: path, Kind: classify(err, classifyOpts{}), Err: err}
		}
		tag = ti.ReparseTag
	}
	st, err := statIDHandle(h)
	if err != nil || entryTypeFromAttrs(bi.FileAttributes, tag) != TypeDir {
		windows.CloseHandle(h)
		return 0, fileID{}, &OpError{Op: "open", Path: path, Kind: KindSourceChanged, Err: err}
	}
	return h, st.id, nil
}

// openNewSecDir は、フォルダ parent の中に作ったばかりのフォルダ name（パスは path）を、リパースポイントを辿らずに開く（§10.2）。
// fileID は、開いたものから記録する。リンク・ジャンクションに置き換えられていれば KindSourceChanged の *OpError を返す。
func openNewSecDir(parent *secDir, path, name string) (*secDir, error) {
	s := parent.sysJoin(name)
	h, id, err := openDirHandle(path, s, false)
	if err != nil {
		return nil, err
	}
	return &secDir{path: path, sys: s, h: h, id: id}, nil
}

// openDestRoot は、コピー先・移動先のフォルダ（DestDir）path を開き、fileID が計画時の want（リンクを辿った先）と一致することを確かめる（§13.1）。
// DestDir 自体はリンクでもよいので、辿って開く。違えば KindSourceChanged の *OpError を返す。
func openDestRoot(path string, want fileID) (*secDir, error) {
	s, err := sysPath(path)
	if err != nil {
		return nil, err
	}
	h, id, err := openDirHandle(path, s, true)
	if err != nil {
		return nil, err
	}
	if id != want {
		windows.CloseHandle(h)
		return nil, &OpError{Op: "open", Path: path, Kind: KindSourceChanged}
	}
	return &secDir{path: path, sys: s, h: h, id: id}, nil
}

// join は、中の name の \\?\ の付かない形のパスを返す（結果とエラーに使う）。
func (d *secDir) join(name string) string { return d.path + `\` + name }

// sysJoin は、中の name の \\?\ 形式のパスを返す。このフォルダのハンドルを開いたままなので、パスで操作してよい（§13.1）。
func (d *secDir) sysJoin(name string) string { return d.sys + `\` + name }

// createFile は、中に name を新しく作る（CREATE_NEW。§10.1 の一時ファイル）。
func (d *secDir) createFile(name string) (*os.File, error) {
	f, err := os.OpenFile(d.sysJoin(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	return f, withUserPaths(err, d.join(name), "")
}

// mkdir は、中に name のフォルダを作る（§10.2）。
func (d *secDir) mkdir(name string) error {
	return withUserPaths(os.Mkdir(d.sysJoin(name), 0o700), d.join(name), "")
}

// symlink は、中に name のシンボリックリンクを作る（§14.2）。dir ならフォルダ用のリンク。
func (d *secDir) symlink(target, name string, dir bool) error {
	return withUserPaths(createSymlinkSys(target, d.sysJoin(name), dir), target, d.join(name))
}

// unlinkTemp は、中の fsops の一時ファイル name を削除する（§10.1 の手順 8）。
// 一時ファイルは fsops が作ったものなので、削除できなければ読み取り専用属性を外してから削除し直す。
func (d *secDir) unlinkTemp(name string) error {
	s := d.sysJoin(name)
	err := os.Remove(s)
	if err != nil && clearReadOnlySys(s) {
		err = os.Remove(s)
	}
	return withUserPaths(err, d.join(name), "")
}

// openRegular は、中の name を読むために開き、フォルダでなく fileID が want であることを確かめる（§10.4 の読み直し）。
func (d *secDir) openRegular(name string, want fileID) (*os.File, srcMeta, error) {
	return openSourceSys(d.sysJoin(name), want)
}

// targetReadOnly は、中の上書き先 name が読み取り専用（§9.3。読み取り専用属性）かを返す。
func (d *secDir) targetReadOnly(name string) bool { return readOnlySys(d.sysJoin(name))() }

// sync は、このフォルダを同期する（§10.5）。Windows では失敗しても処理は続け、警告にもしない。
func (d *secDir) sync() error { return syncDirSys(d.sys) }

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

// remove は、中の e を §13.2 の方法（確かめたハンドルでの削除）で削除する。recorded は §13.3 の削除か。
func (d *secDir) remove(e dirEntry, recorded bool) error {
	return removeSys(d.sys+`\`+e.name, e, recorded)
}

// removeTop は、トップレベルのエントリ path を §13.2 の方法で削除する。
func removeTop(path string, e dirEntry, recorded bool) error {
	s, err := sysPath(path)
	if err != nil {
		return err
	}
	return withUserPaths(removeSys(s, e, recorded), path, "")
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

// removeSys は、\\?\ 形式のパス s にあるエントリ e を削除する（§13.2）。s を削除のアクセス権でリンクを辿らずに開き、
// そのハンドルで e と同じものか確かめてから、同じハンドルに削除の印を付ける（removeVerified）。
// フォルダの読み取り専用属性は外してから削除する（V15）。recorded が真なら（§13.3）、ファイルの読み取り専用属性も外す。
func removeSys(s string, e dirEntry, recorded bool) error {
	return removeVerified(s, e, recorded)
}

// fileDispositionInfo は FILE_DISPOSITION_INFO（x/sys/windows に定義がない）。
type fileDispositionInfo struct{ DeleteFile bool }

// fileDispositionInfoEx は FILE_DISPOSITION_INFO_EX（x/sys/windows に定義がない）。
type fileDispositionInfoEx struct{ Flags uint32 }

// removeVerified は、エントリ e を、s を開いたハンドルで確かめてから、同じハンドルで削除する（総点検の穴 2）。
// パスで DeleteFileW・RemoveDirectoryW を呼ぶと、確かめた後（フォルダでは中身を処理してハンドルを閉じた後）に、そのエントリが
// 別のファイルやジャンクションなどに置き換えられた場合、置き換えたものを消してしまう。リパースポイントを開かずに
// （FILE_FLAG_OPEN_REPARSE_POINT）削除のアクセス権で開き、fileID・フォルダ属性・種類（§14.1）が e と一致することを確かめる。
// recorded が真なら（§13.3）、フォルダ以外は大きさ・更新日時も e と比べる（照合の後に書き換えられた移動元を消さない。I2）。
// 一致しなければ削除せず KindSourceChanged。確かめたものと消すものが同じになる。
// 読み取り専用属性（フォルダは空でも削除に失敗する。V15）は、確かめたハンドルで外し、削除に失敗したら元に戻す。
// ファイルの読み取り専用属性は recorded のときだけ外す（§13.2 では外さず、削除が ERROR_ACCESS_DENIED で失敗する。V15、V23）。
func removeVerified(s string, e dirEntry, recorded bool) error {
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return err
	}
	access := uint32(windows.DELETE | windows.FILE_READ_ATTRIBUTES)
	clearRO := e.dirAttr || recorded
	if clearRO {
		access |= windows.FILE_WRITE_ATTRIBUTES
	}
	h, err := windows.CreateFile(s16, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return &os.PathError{Op: "remove", Path: s, Err: err}
	}
	defer windows.CloseHandle(h) // 削除の印を付けたハンドルを閉じると削除される
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &bi); err != nil {
		return &os.PathError{Op: "remove", Path: s, Err: err}
	}
	st, err := statIDHandle(h)
	if err != nil {
		return &os.PathError{Op: "remove", Path: s, Err: err}
	}
	// 種類は §14.1 と同じ方法で判定する（クラウドファイルのフォルダはリパースポイントでも TypeDir）。
	var tag uint32
	if bi.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		var ti fileAttributeTagInfo
		if err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&ti)), uint32(unsafe.Sizeof(ti))); err != nil {
			return &os.PathError{Op: "remove", Path: s, Err: err}
		}
		tag = ti.ReparseTag
	}
	t := entryTypeFromAttrs(bi.FileAttributes, tag)
	same := st.id == e.id && (bi.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) == e.dirAttr && t == e.info.Type
	if same && recorded && t != TypeDir {
		var size int64
		if t == TypeFile {
			size = int64(bi.FileSizeHigh)<<32 | int64(bi.FileSizeLow)
		}
		same = size == e.info.Size && time.Unix(0, bi.LastWriteTime.Nanoseconds()).Equal(e.info.ModTime)
	}
	if !same {
		return &OpError{Op: "remove", Kind: KindSourceChanged}
	}
	restore := func() {}
	if a := bi.FileAttributes; clearRO && a&windows.FILE_ATTRIBUTE_READONLY != 0 {
		if setAttrsHandle(h, a&^windows.FILE_ATTRIBUTE_READONLY) == nil {
			restore = func() { setAttrsHandle(h, a) }
		}
	}
	if err := markDelete(h); err != nil {
		restore()
		return &os.PathError{Op: "remove", Path: s, Err: err}
	}
	return nil
}

// markDelete は、開いたハンドル h に削除の印を付ける（閉じると削除される）。DeleteFileW・RemoveDirectoryW と同じ結果にするため、
// POSIX 形式（ほかのハンドルが開いていても名前がすぐ消える）の FileDispositionInfoEx を使い、それを受け付けないボリューム
// （exFAT・FAT32 は ERROR_INVALID_PARAMETER を返し、何もしない）では FileDispositionInfo を使う（V23）。
func markDelete(h windows.Handle) error {
	ex := fileDispositionInfoEx{Flags: windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS}
	err := windows.SetFileInformationByHandle(h, windows.FileDispositionInfoEx, (*byte)(unsafe.Pointer(&ex)), uint32(unsafe.Sizeof(ex)))
	if !errors.Is(err, windows.ERROR_INVALID_PARAMETER) && !errors.Is(err, windows.ERROR_NOT_SUPPORTED) && !errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		return err
	}
	info := fileDispositionInfo{DeleteFile: true}
	return windows.SetFileInformationByHandle(h, windows.FileDispositionInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
}

// setAttrsHandle は、開いたハンドル h のファイル属性を a にする（時刻は変えない）。
func setAttrsHandle(h windows.Handle, a uint32) error {
	a &= settableAttrs
	if a == 0 {
		a = windows.FILE_ATTRIBUTE_NORMAL
	}
	info := fileBasicInfo{FileAttributes: a}
	return windows.SetFileInformationByHandle(h, windows.FileBasicInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
}

// isMismatchRemoveErr は、種類に合わない方法での削除の失敗を示しうるエラーか（§13.2。ERROR_ACCESS_DENIED・ERROR_DIRECTORY）。
func isMismatchRemoveErr(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_DIRECTORY)
}

// renameBetween は、from の中の fromName を、to の中の toName へリネームする（§13.1）。
// from が nil なら fromName はパス（sysPath で変換したもの）。
// Windows に開いたフォルダからの相対のリネームはないので、パスで行う。移動元・移動先のフォルダのハンドルを、共有モードに
// FILE_SHARE_DELETE を含めずに開いたままにしているので、フォルダを名前の変更・リンクへの置き換えで差し替えられることはない（§13.1）。
// replace が偽なら排他リネーム（§8.4）、真なら置換リネーム（ファイルの上書き）。
func renameBetween(from *secDir, fromName string, to *secDir, toName string, replace bool) error {
	s := fromName
	if from != nil {
		s = from.sysJoin(fromName)
	}
	d := to.sysJoin(toName)
	if !replace {
		return renameExclusiveSys(s, d)
	}
	return os.Rename(s, d)
}

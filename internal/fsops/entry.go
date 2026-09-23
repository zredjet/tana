package fsops

import (
	"errors"
	"io/fs"
)

// lstatEntry は path をリンクを辿らずに調べ、種類・サイズ・更新日時を返す（SPEC §14.1）。
// Size は TypeFile のときだけ設定する。エラーは分類せずにそのまま返す。
func lstatEntry(path string) (EntryInfo, error) {
	p, err := sysPath(path)
	if err != nil {
		return EntryInfo{}, err
	}
	info, err := lstatEntrySys(p)
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		pe.Path = path // エラーで返すパスは \\?\ の付かない形にする（SPEC §8.2）
	}
	return info, err
}

// entryInfo は、種類 t と Lstat の結果 fi から EntryInfo を作る。
func entryInfo(t EntryType, fi fs.FileInfo) EntryInfo {
	info := EntryInfo{Type: t, ModTime: fi.ModTime()}
	if t == TypeFile {
		info.Size = fi.Size()
	}
	return info
}

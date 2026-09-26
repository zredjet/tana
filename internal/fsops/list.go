package fsops

import (
	"os"
	"path/filepath"
)

// Entry は、リンクを辿らずに調べた 1 つのエントリ（一覧のため。§14.3）。
type Entry struct {
	Name     string    // 列挙で得た名前（バイト列をそのまま。I6。UI はこれからパスを作る）
	Info     EntryInfo // 種類・サイズ・更新日時（§14.1 の判定）
	Hidden   bool      // Windows: FILE_ATTRIBUTE_HIDDEN。macOS: UF_HIDDEN。名前の . による判断は UI が行う
	ReadOnly bool      // Windows: ファイルの読み取り専用属性（フォルダの属性は保護を意味しないので見ない）。Unix: オーナーの書き込み権限がない。macOS はロック（UF_IMMUTABLE）も含む
	Err      *OpError  // 列挙はできたが調べられなかった（Unix の fstatat の失敗）。Info・Hidden・ReadOnly は使えない
}

// Lstat は path をリンクを辿らずに調べる（§14.3）。Name は path の最後の要素。
// エラーは §17 で分類した *OpError（Op は "lstat"）。
func Lstat(path string) (Entry, error) {
	p, err := checkPath(path)
	if err != nil {
		return Entry{}, &OpError{Op: "lstat", Path: path, Kind: KindInvalidRequest}
	}
	e, err := statTop(p)
	if err != nil {
		return Entry{}, &OpError{Op: "lstat", Path: p, Kind: classify(err, classifyOpts{}), Err: err}
	}
	e.name = filepath.Base(p)
	return publicEntry(p, e), nil
}

// ReadDir は、フォルダ dir の中身をリンクを辿らずに列挙し、名前のバイト順で返す（§13.1 と同じ方法。§14.3）。
// dir 自体がリンク・ジャンクションなら、そのリンク先を列挙する（利用者がリンクのフォルダに入った場合）。
// dir がフォルダでない（リンク先がファイルの場合を含む）ときは KindNotFound。AppleDouble の付属（§8.5）は含めない。
// エラーは §17 で分類した *OpError（Op は "readdir"）。
func ReadDir(dir string) ([]Entry, error) {
	d, err := checkPath(dir)
	if err != nil {
		return nil, &OpError{Op: "readdir", Path: dir, Kind: KindInvalidRequest}
	}
	s, err := sysPath(d)
	if err != nil {
		return nil, &OpError{Op: "readdir", Path: d, Kind: KindInvalidRequest, Err: err}
	}
	entries, err := readDirFollowSys(s)
	if err != nil {
		err = withUserPaths(err, d, "") // エラーで返すパスは \\?\ の付かない形にする（§8.2）
		return nil, &OpError{Op: "readdir", Path: d, Kind: classify(err, classifyOpts{}), Err: err}
	}
	return publicEntries(d, entries), nil
}

// publicEntries は、フォルダ dir の列挙の結果を Entry にする。調べられなかったエントリは Err を付ける。
func publicEntries(dir string, entries []dirEntry) []Entry {
	out := make([]Entry, len(entries))
	for i, e := range entries {
		out[i] = publicEntry(filepath.Join(dir, e.name), e)
	}
	return out
}

// publicEntry は、path にある e を Entry にする。
func publicEntry(path string, e dirEntry) Entry {
	if e.statErr != nil {
		err := withUserPaths(e.statErr, path, "")
		return Entry{Name: e.name, Err: &OpError{Op: "lstat", Path: path, Kind: classify(err, classifyOpts{}), Err: err}}
	}
	return Entry{Name: e.name, Info: e.info, Hidden: e.hidden, ReadOnly: e.readOnly}
}

// Readlink は、シンボリックリンク・ジャンクション path のリンク先を、書き換えずに返す（表示用。§14.3）。
// エラーは §17 で分類した *OpError（Op は "readlink"）。
func Readlink(path string) (string, error) {
	p, err := checkPath(path)
	if err != nil {
		return "", &OpError{Op: "readlink", Path: path, Kind: KindInvalidRequest}
	}
	s, err := sysPath(p)
	if err != nil {
		return "", &OpError{Op: "readlink", Path: p, Kind: KindInvalidRequest, Err: err}
	}
	target, err := os.Readlink(s)
	if err != nil {
		err = withUserPaths(err, p, "")
		return "", &OpError{Op: "readlink", Path: p, Kind: classify(err, classifyOpts{}), Err: err}
	}
	return target, nil
}

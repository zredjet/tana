package fsops

import (
	"os"
	"path/filepath"
)

// Mkdir は、フォルダ parent の中に、名前 name のフォルダを作る（§11.4）。
// OS の不可分なフォルダの作成で作るので、同じ名前のエントリがあれば（大文字小文字・正規化だけが違う名前を含む）KindExist にし、
// 上書きも既存のものへの変更もしない（I1）。名前は Rename と同じく検査し（§11.3）、変換しない（I6）。
// 途中の要素のリンクは辿る（利用者が表示しているフォルダの中に作るため）。
func Mkdir(parent, name string) error {
	p, err := checkPath(parent)
	if err != nil {
		return &OpError{Op: "mkdir", Path: parent, Kind: KindInvalidRequest}
	}
	if err := validateName(name); err != nil {
		return &OpError{Op: "mkdir", Path: p, Kind: KindInvalidName, OnDest: true} // 使えない名前からはパスを作らない
	}
	dst := filepath.Join(p, name)
	s, err := sysPath(dst)
	if err != nil {
		return &OpError{Op: "mkdir", Path: dst, Kind: KindInvalidRequest, OnDest: true, Err: err}
	}
	if err := os.Mkdir(s, 0o777); err != nil {
		err = withUserPaths(err, dst, "") // エラーで返すパスは \\?\ の付かない形にする（§8.2）
		ps, _ := sysPath(p)
		return &OpError{Op: "mkdir", Path: dst, Kind: classify(err, classifyOpts{readOnly: dirLockedSys(ps)}), OnDest: true, Err: err}
	}
	return nil
}

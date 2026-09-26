package fsops

import "errors"

// Head は ReadHead の結果（§14.4）。
type Head struct {
	Data     []byte // 先頭の max バイトまで
	Size     int64  // ファイルの大きさ
	NotLocal bool   // 中身が手元にない（読むと取得が始まる）ので読んでいない。Data は空
}

// errNotRegular は、通常のファイルでないこと（ReadHead は KindUnsupportedType にする）。
var errNotRegular = errors.New("not a regular file")

// ReadHead は、ファイル path の先頭を max バイトまで読む（プレビューのため。§14.4）。リンク・ジャンクションは辿る。
// 読むのは通常のファイルだけで、フォルダ・FIFO・デバイスなどは開かずに KindUnsupportedType にする。
// 中身が手元にないファイル（クラウドのファイルなど）は開かずに NotLocal を返す。
// エラーは §17 で分類した *OpError（Op は "readhead"）。
func ReadHead(path string, max int) (Head, error) {
	p, err := checkPath(path)
	if err != nil {
		return Head{}, &OpError{Op: "readhead", Path: path, Kind: KindInvalidRequest}
	}
	if max < 0 {
		return Head{}, &OpError{Op: "readhead", Path: p, Kind: KindInvalidRequest}
	}
	s, err := sysPath(p)
	if err != nil {
		return Head{}, &OpError{Op: "readhead", Path: p, Kind: KindInvalidRequest, Err: err}
	}
	h, err := readHeadSys(s, max)
	if err != nil {
		err = withUserPaths(err, p, "") // エラーで返すパスは \\?\ の付かない形にする（§8.2）
		kind := classify(err, classifyOpts{})
		if errors.Is(err, errNotRegular) {
			kind = KindUnsupportedType
		}
		return Head{}, &OpError{Op: "readhead", Path: p, Kind: kind, Err: err}
	}
	return h, nil
}

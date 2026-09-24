//go:build darwin && !cgo

package fsops

import "context"

// trashAvailable は macOS（cgo なし）の事前確認。cgo なしのビルドではごみ箱を使えない（§12.3、I5）。
func trashAvailable(ctx context.Context, src string, info EntryInfo) (bool, error) { return false, nil }

// trashSys は、cgo なしのビルドではごみ箱を使えないので何もせず KindTrashUnavailable を返す（§12.3、I5）。
func trashSys(src string, info EntryInfo) (string, error) {
	return "", &OpError{Op: "trash", Path: src, Kind: KindTrashUnavailable}
}

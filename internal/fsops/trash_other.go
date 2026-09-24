//go:build !windows && !darwin

package fsops

import "context"

// trashAvailable は Windows・macOS 以外の事前確認。ごみ箱は使えない（§12.4、I5）。
func trashAvailable(ctx context.Context, src string, info EntryInfo) (bool, error) { return false, nil }

// trashSys は、ごみ箱が使えないので何もせず KindTrashUnavailable を返す（§12.4、I5）。
func trashSys(src string, info EntryInfo) (string, error) {
	return "", &OpError{Op: "trash", Path: src, Kind: KindTrashUnavailable}
}

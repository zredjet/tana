//go:build darwin && !cgo

package fsops

import "context"

// trashAvailable は macOS（cgo なし）の事前確認。cgo なしのビルドではごみ箱を使えない（§12.3、I5）。
func trashAvailable(ctx context.Context, src string, info EntryInfo) (bool, error) { return false, nil }

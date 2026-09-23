//go:build darwin && cgo

package fsops

import "context"

// trashAvailable は macOS（cgo あり）の事前確認。ごみ箱に入れられるかは実行時に NSFileManager が判断する（§12.3）。
func trashAvailable(ctx context.Context, src string, info EntryInfo) (bool, error) { return true, nil }

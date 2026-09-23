//go:build !windows && !darwin

package fsops

import "context"

// trashAvailable は Windows・macOS 以外の事前確認。ごみ箱は使えない（§12.4、I5）。
func trashAvailable(ctx context.Context, src string, info EntryInfo) (bool, error) { return false, nil }

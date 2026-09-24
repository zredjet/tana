//go:build unix

package fsops

import (
	"testing"
)

// checkTrashed は、src がごみ箱の trashed に入ったことを確かめる（§18.4「ごみ箱」。macOS: TrashedPath）。
func checkTrashed(t *testing.T, src, trashed string, info EntryInfo) {
	t.Helper()
	if trashed == "" {
		t.Errorf("%s: TrashedPath is empty", src)
		return
	}
	now, err := lstatEntry(trashed)
	if err != nil {
		t.Errorf("%s: TrashedPath %s: %v", src, trashed, err)
		return
	}
	if now.Type != info.Type || info.Type == TypeFile && now.Size != info.Size {
		t.Errorf("%s: in the trash %+v, want %+v", src, now, info)
	}
}

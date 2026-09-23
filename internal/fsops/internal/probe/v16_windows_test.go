package probe

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// fileIDs は、path の FileIdInfo（SPEC §8.3 の Windows の fileID）、GetFileInformationByHandle のファイルインデックス、
// FileIdBothDirectoryInfo の列挙で得たファイル ID を 1 行にまとめる。
func fileIDs(t *testing.T, path string) string {
	t.Helper()
	s := ""
	id, _, _, err := idAndTag(t, path)
	if err != nil {
		s += "FileIdInfo: " + err.Error()
	} else {
		s += fmt.Sprintf("FileIdInfo serial=%#x id=%s", id.VolumeSerialNumber, hex.EncodeToString(id.FileID[:]))
	}
	h, err := windows.CreateFile(u16(t, `\\?\`+path), windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err == nil {
		var bi windows.ByHandleFileInformation
		err = windows.GetFileInformationByHandle(h, &bi)
		windows.CloseHandle(h)
		if err == nil {
			s += fmt.Sprintf(" | ByHandle serial=%#x index=%08x%08x", bi.VolumeSerialNumber, bi.FileIndexHigh, bi.FileIndexLow)
		}
	}
	if err != nil {
		s += " | ByHandle: " + err.Error()
	}
	entries, err := enumerate(t, filepath.Dir(path), windows.FileIdBothDirectoryRestartInfo, windows.FileIdBothDirectoryInfo, false)
	for _, e := range entries {
		if e.Name == filepath.Base(path) {
			s += " | FileIdBothDirectoryInfo id=" + e.FileID
		}
	}
	if err != nil {
		s += " | FileIdBothDirectoryInfo: " + err.Error()
	}
	return s
}

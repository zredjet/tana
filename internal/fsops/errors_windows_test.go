package fsops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

func TestClassifyErrnoWindows(t *testing.T) {
	t.Parallel()
	cases := []errnoCase{
		{windows.ERROR_FILE_NOT_FOUND, KindNotFound},
		{windows.ERROR_PATH_NOT_FOUND, KindNotFound},
		{windows.ERROR_DIRECTORY, KindNotFound},
		{windows.ERROR_FILE_EXISTS, KindExist},
		{windows.ERROR_ALREADY_EXISTS, KindExist},
		{windows.ERROR_SHARING_VIOLATION, KindLocked},
		{windows.ERROR_LOCK_VIOLATION, KindLocked},
		{windows.ERROR_WRITE_PROTECT, KindReadOnly},
		{windows.ERROR_DISK_FULL, KindNoSpace},
		{windows.ERROR_HANDLE_DISK_FULL, KindNoSpace},
		{windows.ERROR_DIR_NOT_EMPTY, KindNotEmpty},
		{windows.ERROR_NOT_SAME_DEVICE, KindCrossDevice},
		// リンクの作成以外では Permission（SPEC §17）
		{windows.ERROR_PRIVILEGE_NOT_HELD, KindPermission},
		{windows.ERROR_INVALID_NAME, KindInvalidName},
		{windows.ERROR_FILENAME_EXCED_RANGE, KindInvalidName},
		// 表にない番号
		{windows.ERROR_INVALID_PARAMETER, KindUnknown},
		{windows.ERROR_NOT_SUPPORTED, KindUnknown},
		// リンクの作成以外では表にない（SPEC §17。V20）
		{windows.ERROR_INVALID_FUNCTION, KindUnknown},
	}
	testErrnoTable(t, cases)
	testReadOnlyErrno(t, windows.ERROR_ACCESS_DENIED)
	testErrnoWithOpts(t, windows.ERROR_PRIVILEGE_NOT_HELD, classifyOpts{symlinkCreate: true}, KindLinkUnsupported)
	// リンクを作れないボリューム（exFAT・FAT32。V20）
	testErrnoWithOpts(t, windows.ERROR_INVALID_FUNCTION, classifyOpts{symlinkCreate: true}, KindLinkUnsupported)
	// symlinkCreate は ERROR_PRIVILEGE_NOT_HELD・ERROR_INVALID_FUNCTION 以外の分類を変えない。
	testErrnoWithOpts(t, windows.ERROR_ACCESS_DENIED, classifyOpts{symlinkCreate: true}, KindPermission)
}

// TestClassifyLockedWindows は、共有モード 0 で開かれたファイルを開くと KindLocked になることを確かめる。
func TestClassifyLockedWindows(t *testing.T) {
	t.Parallel()
	path := filepath.Join(testfs.TempDir(t), "locked.txt")
	testfs.WriteFile(t, path, "x")
	testfs.Lock(t, path)

	f, err := os.Open(path)
	if err == nil {
		f.Close()
		t.Fatal("os.Open of a file held with share mode 0 succeeded")
	}
	if got := classify(err, classifyOpts{}); got != KindLocked {
		t.Errorf("classify(%v) = %v, want %v", err, got, KindLocked)
	}
}

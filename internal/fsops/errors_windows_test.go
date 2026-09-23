package fsops

import (
	"os"
	"path/filepath"
	"testing"

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
		{windows.ERROR_ACCESS_DENIED, KindPermission},
		{windows.ERROR_SHARING_VIOLATION, KindLocked},
		{windows.ERROR_LOCK_VIOLATION, KindLocked},
		{windows.ERROR_WRITE_PROTECT, KindReadOnly},
		{windows.ERROR_DISK_FULL, KindNoSpace},
		{windows.ERROR_HANDLE_DISK_FULL, KindNoSpace},
		{windows.ERROR_DIR_NOT_EMPTY, KindNotEmpty},
		{windows.ERROR_NOT_SAME_DEVICE, KindCrossDevice},
		{windows.ERROR_PRIVILEGE_NOT_HELD, KindLinkUnsupported},
		{windows.ERROR_INVALID_NAME, KindInvalidName},
		{windows.ERROR_FILENAME_EXCED_RANGE, KindInvalidName},
		// 表にない番号
		{windows.ERROR_INVALID_PARAMETER, KindUnknown},
		{windows.ERROR_NOT_SUPPORTED, KindUnknown},
	}
	testErrnoTable(t, cases, windows.ERROR_ACCESS_DENIED)
	testReadOnlyErrno(t, windows.ERROR_ACCESS_DENIED)
}

// TestClassifyLockedWindows は、共有モード 0 で開かれたファイルを開くと KindLocked になることを確かめる。
func TestClassifyLockedWindows(t *testing.T) {
	t.Parallel()
	path := filepath.Join(tempDir(t), "locked.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)

	f, err := os.Open(path)
	if err == nil {
		f.Close()
		t.Fatal("os.Open of a file held with share mode 0 succeeded")
	}
	if got := classify(err, classifyOpts{}); got != KindLocked {
		t.Errorf("classify(%v) = %v, want %v", err, got, KindLocked)
	}
}

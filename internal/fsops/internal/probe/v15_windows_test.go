package probe

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// TestV15 は、読み取り専用属性の付いた空のフォルダを RemoveDirectoryW で削除できるか、
// 読み取り専用属性の付いたファイルに DeleteFileW が ERROR_ACCESS_DENIED を返し、ファイルと属性がそのまま残るかを記録する（SPEC §20 V15、§13.2）。
func TestV15(t *testing.T) {
	logWindowsVersion(t, "V15")
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"rodir":    testfs.Dir().RO(),
		"ro.txt":   testfs.File("ro").RO(),
		"rw.txt":   testfs.File("rw"),
		"rodir2/a": testfs.File("a"),
		"rodir2":   testfs.Dir().RO(),
	})
	attrs := func(name string) string {
		a, err := windows.GetFileAttributes(u16(t, testfs.ExtendedPath(filepath.Join(root, name))))
		if err != nil {
			return err.Error()
		}
		return "readonly=" + strconv.FormatBool(a&windows.FILE_ATTRIBUTE_READONLY != 0)
	}

	err := windows.RemoveDirectory(u16(t, testfs.ExtendedPath(filepath.Join(root, "rodir"))))
	t.Logf("V15: RemoveDirectoryW(read-only empty dir): err=%v (errno %d) exists after=%v", err, errnoOf(err), testfs.Exists(t, filepath.Join(root, "rodir")))

	err = windows.RemoveDirectory(u16(t, testfs.ExtendedPath(filepath.Join(root, "rodir2"))))
	t.Logf("V15: RemoveDirectoryW(read-only non-empty dir): err=%v (errno %d) exists after=%v", err, errnoOf(err), testfs.Exists(t, filepath.Join(root, "rodir2")))

	err = windows.DeleteFile(u16(t, testfs.ExtendedPath(filepath.Join(root, "ro.txt"))))
	exists := testfs.Exists(t, filepath.Join(root, "ro.txt"))
	t.Logf("V15: DeleteFileW(read-only file): err=%v (errno %d) exists after=%v %s", err, errnoOf(err), exists, attrs("ro.txt"))
	if err == nil || !exists {
		t.Errorf("V15: DeleteFileW deleted a read-only file (SPEC §13.2 assumes it fails with ERROR_ACCESS_DENIED)")
	}

	err = windows.DeleteFile(u16(t, testfs.ExtendedPath(filepath.Join(root, "rw.txt"))))
	t.Logf("V15: DeleteFileW(normal file): err=%v exists after=%v", err, testfs.Exists(t, filepath.Join(root, "rw.txt")))
}

package fsops

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// TestStreamNamesWindows は、FileStreamInfo による代替データストリームの列挙（§15）が、Zone.Identifier 以外の名前付きのストリームを
// 返すことを、ファイルとフォルダ、開き方（読み取り・属性だけ）ごとに確かめる。列挙のエラーも記録する。
func TestStreamNamesWindows(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"f.txt": testfs.File("f"), "d": testfs.Dir()})
	for _, name := range []string{"f.txt", "d"} {
		p := testfs.ExtendedPath(filepath.Join(root, name))
		if err := os.WriteFile(p+":fsops-test", []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p+zoneStream, []byte("[ZoneTransfer]"), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, access := range []uint32{windows.FILE_READ_ATTRIBUTES, windows.GENERIC_READ} {
			p16, _ := windows.UTF16PtrFromString(p)
			h, err := windows.CreateFile(p16, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
				windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
			if err != nil {
				t.Fatal(err)
			}
			names, err := streamNames(h)
			windows.CloseHandle(h)
			t.Logf("%s access=%#x: names=%q err=%v", name, access, names, err)
			if !slices.Equal(names, []string{"fsops-test"}) {
				t.Errorf("%s access=%#x: names = %q (err %v), want [fsops-test]", name, access, names, err)
			}
		}
	}
}

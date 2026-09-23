package probe

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestV10 は、ごみ箱に入れた項目の $I ファイルの形式を確かめる（SPEC §20 V10）。
// 想定: 版番号 8 バイト（2）、元のサイズ 8 バイト、削除日時（FILETIME）8 バイト、パスの文字数 4 バイト、UTF-16 の元のパス。
// FSOPS_TEST_TRASH=1 のときだけ実行する。ごみ箱の中は読むだけで、変更しない。
func TestV10(t *testing.T) {
	testfs.RequireTrash(t)
	logWindowsVersion(t, "V10")
	root := testfs.TempDir(t)
	data := strings.Repeat("0123456789", 1234) // 12340 バイト
	tests := []struct {
		name string
		rel  string
		size int64
	}{
		{"file", "v10/file-日本語.txt", int64(len(data))},
		{"dir", "v10/dir", -1}, // フォルダのサイズは記録する（中身の合計と想定）
	}
	testfs.Build(t, root, testfs.Tree{
		"v10/file-日本語.txt": testfs.File(data),
		"v10/dir/a.txt":    testfs.File(data),
		"v10/dir/b.txt":    testfs.File("xyz"),
	})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := filepath.Join(root, filepath.FromSlash(tt.rel))
			before := time.Now()
			ret, aborted := shTrash(t, p)
			after := time.Now()
			if ret != 0 || aborted {
				t.Fatalf("V10: %s: SHFileOperationW ret=%#x aborted=%v", tt.name, ret, aborted)
			}
			r, ok := findInRecycleBin(t, driveRoot(p), p)
			if !ok {
				t.Fatalf("V10: %s: no $I file with the original path %s", tt.name, p)
			}
			inWindow := !r.Deleted.Before(before.Add(-2*time.Second)) && !r.Deleted.After(after.Add(2*time.Second))
			t.Logf("V10: %s: $I=%s version=%d size=%d deleted=%s (within the call: %v) original=%q (matches: %v)",
				tt.name, filepath.Base(r.IFile), r.Version, r.Size, r.Deleted.Format(time.RFC3339Nano), inWindow, r.Original, r.Original == p)
			if tt.size >= 0 {
				t.Logf("V10: %s: size matches the file size %d: %v", tt.name, tt.size, r.Size == tt.size)
			}
			rPath := r.RFile
			if tt.name == "file" {
				t.Logf("V10: %s: $R=%s exists=%v content matches=%v", tt.name, filepath.Base(rPath), testfs.Exists(t, rPath),
					testfs.Exists(t, rPath) && testfs.ReadFile(t, rPath) == data)
			} else {
				t.Logf("V10: %s: $R=%s exists=%v, $R\\a.txt exists=%v", tt.name, filepath.Base(rPath), testfs.Exists(t, rPath),
					testfs.Exists(t, filepath.Join(rPath, "a.txt")))
			}
		})
	}
}

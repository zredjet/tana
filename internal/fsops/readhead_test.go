package fsops

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestReadHead は、ファイルの先頭を max バイトまで読み、大きさを返すことを確かめる（§14.4）。読むだけで変更しない。
func TestReadHead(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"a.txt":   testfs.File("hello world"),
		"empty":   testfs.File(""),
		"日本語.txt": testfs.File("中身"),
	})
	before := testfs.Take(t, root)
	for _, tt := range []struct {
		name string
		max  int
		want string
		size int64
	}{
		{"a.txt", 5, "hello", 11},
		{"a.txt", 100, "hello world", 11},
		{"a.txt", 1 << 30, "hello world", 11}, // 大きな max でも、ファイルの大きさを超えて確保しない
		{"a.txt", 0, "", 11},
		{"empty", 10, "", 0},
		{"日本語.txt", 64, "中身", 6},
	} {
		h, err := ReadHead(filepath.Join(root, tt.name), tt.max)
		if err != nil || string(h.Data) != tt.want || h.Size != tt.size || h.NotLocal {
			t.Errorf("ReadHead(%s, %d) = %q, size %d, notLocal %v, %v; want %q, %d", tt.name, tt.max, h.Data, h.Size, h.NotLocal, err, tt.want, tt.size)
		}
	}
	if d := testfs.Diff(before, testfs.Take(t, root)); len(d) > 0 {
		t.Errorf("ReadHead changed the tree: %q", d)
	}
}

// TestReadHeadErrors は、通常のファイル以外・ないもの・正しくない指定の分類を確かめる。
func TestReadHeadErrors(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"dir": testfs.Dir(), "a.txt": testfs.File("x")})
	wantOpError := func(err error, k Kind, path string) {
		t.Helper()
		wantOpErrorReadHead(t, err, k, path)
	}
	_, err := ReadHead(filepath.Join(root, "dir"), 10)
	wantOpError(err, KindUnsupportedType, filepath.Join(root, "dir"))
	_, err = ReadHead(filepath.Join(root, "missing"), 10)
	wantOpError(err, KindNotFound, filepath.Join(root, "missing"))
	_, err = ReadHead("relative.txt", 10)
	wantOpError(err, KindInvalidRequest, "relative.txt")
	_, err = ReadHead(filepath.Join(root, "a.txt"), -1)
	wantOpError(err, KindInvalidRequest, filepath.Join(root, "a.txt"))
}

func wantOpErrorReadHead(t *testing.T, err error, k Kind, path string) {
	t.Helper()
	wantOpError(t, err, "readhead", k, path)
}

// TestReadHeadLinks は、リンクを辿って読むことを確かめる（利用者がリンクのファイルを選んだ場合）。
func TestReadHeadLinks(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"a.txt":           testfs.File("target data"),
		"dir":             testfs.Dir(),
		"link":            testfs.Symlink("a.txt"),
		"dirlink":         testfs.DirSymlink("dir"),
		"filelink-to-dir": testfs.Symlink("dir"), // Windows では、ファイル用のリンクがフォルダを指す
		"dangling":        testfs.Symlink("missing"),
	})
	if h, err := ReadHead(filepath.Join(root, "link"), 6); err != nil || string(h.Data) != "target" || h.Size != 11 {
		t.Errorf("ReadHead(link) = %q, %d, %v, want the target's head", h.Data, h.Size, err)
	}
	_, err := ReadHead(filepath.Join(root, "dirlink"), 6)
	wantOpErrorReadHead(t, err, KindUnsupportedType, filepath.Join(root, "dirlink"))
	// フォルダを指すファイル用のリンク。Windows は、この種のリンクをファイルとしてもフォルダとしても開かせず、
	// FILE_FLAG_BACKUP_SEMANTICS を付けても ERROR_ACCESS_DENIED を返す（2026-09-26 の CI）。
	_, err = ReadHead(filepath.Join(root, "filelink-to-dir"), 6)
	want := KindUnsupportedType
	if runtime.GOOS == "windows" {
		want = KindPermission
	}
	wantOpErrorReadHead(t, err, want, filepath.Join(root, "filelink-to-dir"))
	_, err = ReadHead(filepath.Join(root, "dangling"), 6)
	wantOpErrorReadHead(t, err, KindNotFound, filepath.Join(root, "dangling"))
}

// TestReadHeadFIFO は、FIFO を開いて止まらずに KindUnsupportedType にすることを確かめる（§14.4）。
// 止まったときにテストが終わらなくならないよう、別の goroutine で呼んで上限を置く（上限は失敗を知らせるためだけのもの）。
func TestReadHeadFIFO(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"fifo": testfs.FIFO()})
	done := make(chan error, 1)
	go func() {
		_, err := ReadHead(filepath.Join(root, "fifo"), 10)
		done <- err
	}()
	select {
	case err := <-done:
		wantOpErrorReadHead(t, err, KindUnsupportedType, filepath.Join(root, "fifo"))
	case <-time.After(30 * time.Second):
		t.Fatal("ReadHead blocked on a FIFO")
	}
}

// TestReadHeadPaths は、長いパスと、Win32 の正規化で変わる名前を、取り違えずに読むことを確かめる（§8.2、U4）。
func TestReadHeadPaths(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	long := testfs.LongPath(t, root)
	testfs.WriteFile(t, filepath.Join(long, "f.txt"), "long")
	if h, err := ReadHead(filepath.Join(long, "f.txt"), 10); err != nil || string(h.Data) != "long" {
		t.Errorf("ReadHead(long) = %q, %v", h.Data, err)
	}
	testfs.Build(t, root, testfs.Tree{
		testfs.NameTrailingDot: testfs.File("with dot"),
		testfs.NamePlain:       testfs.File("plain"),
	})
	if h, err := ReadHead(filepath.Join(root, testfs.NameTrailingDot), 20); err != nil || string(h.Data) != "with dot" {
		t.Errorf("ReadHead(%q) = %q, %v, want its own data (not %q's)", testfs.NameTrailingDot, h.Data, err, testfs.NamePlain)
	}
}

// TestReadHeadPermission は、読めないファイルを KindPermission にすることを確かめる（Unix）。
func TestReadHeadPermission(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission of files")
	}
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"secret": testfs.File("x")})
	p := filepath.Join(root, "secret")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	_, err := ReadHead(p, 10)
	wantOpErrorReadHead(t, err, KindPermission, p)
}

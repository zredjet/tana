package probe

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// TestV4 は、SHFileOperationW（SPEC §12.2 のフラグ）でごみ箱へ送ったときの動作を記録する（SPEC §20 V4）。
// 特に、固定ドライブ以外・長いパス・\\?\ 付きのパス・Win32 の正規化で変わる名前で、黙って完全削除されないか、
// 別のファイルを削除しないかを調べる。FSOPS_TEST_TRASH=1 のときだけ実行する。
func TestV4(t *testing.T) {
	testfs.RequireTrash(t)
	logWindowsVersion(t, "V4")
	root := testfs.TempDir(t)
	cRoot := driveRoot(root)

	t.Run("fixed drive file", func(t *testing.T) {
		p := filepath.Join(root, "fixed", "file.txt")
		testfs.Build(t, root, testfs.Tree{"fixed/file.txt": testfs.File("data")})
		t.Logf("V4: fixed drive file: %s", trashAndDescribe(t, p, p, cRoot))
	})

	t.Run("fixed drive dir", func(t *testing.T) {
		p := filepath.Join(root, "fixeddir", "dir")
		testfs.Build(t, root, testfs.Tree{"fixeddir/dir/a.txt": testfs.File("a")})
		t.Logf("V4: fixed drive dir: %s", trashAndDescribe(t, p, p, cRoot))
	})

	t.Run("long path", func(t *testing.T) {
		long := testfs.LongPath(t, filepath.Join(root, "long"))
		p := filepath.Join(long, "file.txt")
		testfs.WriteFile(t, p, "long")
		t.Logf("V4: long path (%d chars, no \\\\?\\): %s", len(p), trashAndDescribe(t, p, p, cRoot))
	})

	t.Run("long path with prefix", func(t *testing.T) {
		long := testfs.LongPath(t, filepath.Join(root, "long2"))
		p := filepath.Join(long, "file.txt")
		testfs.WriteFile(t, p, "long")
		t.Logf("V4: long path (%d chars, with \\\\?\\): %s", len(p), trashAndDescribe(t, testfs.ExtendedPath(p), p, cRoot))
	})

	t.Run("short path with prefix", func(t *testing.T) {
		p := filepath.Join(root, "prefixed", "file.txt")
		testfs.Build(t, root, testfs.Tree{"prefixed/file.txt": testfs.File("data")})
		t.Logf("V4: short path with \\\\?\\: %s", trashAndDescribe(t, testfs.ExtendedPath(p), p, cRoot))
	})

	t.Run("admin share", func(t *testing.T) {
		p := filepath.Join(root, "share", "file.txt")
		testfs.Build(t, root, testfs.Tree{"share/file.txt": testfs.File("data")})
		unc := `\\localhost\` + strings.ToUpper(p[:1]) + `$` + p[2:]
		t.Logf("V4: %s (DRIVE_REMOTE): %s", unc, trashAndDescribe(t, unc, p, cRoot))
	})

	// Win32 の正規化で変わる名前: foo. を指定したときに、foo と foo. のどちらが消えるか。
	for _, c := range []struct{ label, name string }{
		{"trailing dot", testfs.NameTrailingDot},
		{"trailing space", testfs.NameTrailingSpace},
	} {
		t.Run(c.label, func(t *testing.T) {
			dir := filepath.Join(root, sanitize(c.label))
			testfs.Build(t, dir, testfs.Tree{
				testfs.NamePlain: testfs.File("plain"),
				c.name:           testfs.File("target"),
			})
			before := readBin(t, cRoot)
			ret, aborted := shTrash(t, filepath.Join(dir, c.name))
			t.Logf("V4: trash %q next to %q: %q: %s; %q exists=%v", c.name, testfs.NamePlain,
				c.name, describeTrashResult(t, filepath.Join(dir, c.name), cRoot, before, ret, aborted),
				testfs.NamePlain, testfs.Exists(t, filepath.Join(dir, testfs.NamePlain)))
		})
	}

	// スレッドと COM の初期化: 別の goroutine から、初期化なしと、LockOSThread + CoInitializeEx ありの両方で呼ぶ。
	t.Run("goroutine without COM init", func(t *testing.T) {
		p := filepath.Join(root, "nocom", "file.txt")
		testfs.Build(t, root, testfs.Tree{"nocom/file.txt": testfs.File("data")})
		type result struct {
			ret     uintptr
			aborted bool
		}
		done := make(chan result)
		before := readBin(t, cRoot)
		go func() {
			ret, aborted := shTrash(t, p)
			done <- result{ret, aborted}
		}()
		r := <-done
		t.Logf("V4: goroutine, no LockOSThread/CoInitializeEx: %s", describeTrashResult(t, p, cRoot, before, r.ret, r.aborted))
	})
	t.Run("goroutine with COM init", func(t *testing.T) {
		p := filepath.Join(root, "com", "file.txt")
		testfs.Build(t, root, testfs.Tree{"com/file.txt": testfs.File("data")})
		type result struct {
			ret     uintptr
			aborted bool
			coInit  error
		}
		done := make(chan result)
		before := readBin(t, cRoot)
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|0x4 /* COINIT_DISABLE_OLE1DDE */)
			if err == nil {
				defer windows.CoUninitialize()
			}
			ret, aborted := shTrash(t, p)
			done <- result{ret, aborted, err}
		}()
		r := <-done
		t.Logf("V4: goroutine, LockOSThread + CoInitializeEx(STA)=%s: %s", errString(r.coInit), describeTrashResult(t, p, cRoot, before, r.ret, r.aborted))
	})

	// ボリュームをまたぐテスト用の VHD（NTFS、固定ドライブ）と、exFAT・FAT32 の VHD。
	for _, env := range []string{testfs.CrossVolEnv, envExFAT, envFAT32} {
		t.Run("volume "+env, func(t *testing.T) {
			d := testfs.EnvDir(t, env)
			p := filepath.Join(d, "file.txt")
			testfs.WriteFile(t, p, "data")
			t.Logf("V4: %s=%s: %s", env, os.Getenv(env), trashAndDescribe(t, p, p, driveRoot(p)))
			entries, err := os.ReadDir(testfs.ExtendedPath(filepath.Join(driveRoot(p), "$Recycle.Bin")))
			var names []string
			for _, e := range entries {
				names = append(names, e.Name())
			}
			t.Logf("V4: %s: %s$Recycle.Bin: %+q err=%v", env, driveRoot(p), names, err)
		})
	}
}

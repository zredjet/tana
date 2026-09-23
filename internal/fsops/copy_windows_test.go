package fsops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// symlinkPrivilegeErr は、シンボリックリンクを作る権限がない場合のエラーと、その分類を返す（テストの注入用。V8）。
func symlinkPrivilegeErr() (error, Kind) {
	return windows.ERROR_PRIVILEGE_NOT_HELD, KindLinkUnsupported
}

// noSpaceErr は、書き込み中の容量不足のエラー（§10.3）を返す（テストの注入用）。
func noSpaceErr() error { return windows.ERROR_DISK_FULL }

// attrs は、path（リンクを辿らない）のファイル属性を返す。
func attrs(t *testing.T, path string) uint32 {
	t.Helper()
	p16, err := windows.UTF16PtrFromString(testfs.ExtendedPath(path))
	if err != nil {
		t.Fatal(err)
	}
	a, err := windows.GetFileAttributes(p16)
	if err != nil {
		t.Fatalf("GetFileAttributes %s: %v", path, err)
	}
	return a
}

// TestCopyWindowsSymlinkKind は、Windows のシンボリックリンクのファイル用・フォルダ用の区別が、
// コピー元のリンクの属性のまま保たれることを確かめる（§14.2、§18.4「リンク」）。
// リンク先がコピー先にない相対リンクでも、フォルダ用のままになる。
func TestCopyWindowsSymlinkKind(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/tree/target-dir/x": testfs.File("x"),
		"src/tree/dirlink":      testfs.DirSymlink("target-dir"),
		"src/tree/missing":      testfs.DirSymlink(`..\nowhere`), // コピー先から見てもリンク先がない
		"src/tree/filelink":     testfs.Symlink("target-dir"),    // リンク先はフォルダだが、ファイル用のリンク
		"dest":                  testfs.Dir(),
	})
	dest := filepath.Join(root, "dest", "tree")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "tree")}, DestDir: filepath.Join(root, "dest")})
	if res := execPlan(t, context.Background(), plan, ExecOptions{}); res.Status != StatusCompleted {
		t.Fatalf("result = %+v", res)
	}
	for name, wantDir := range map[string]bool{"dirlink": true, "missing": true, "filelink": false} {
		a := attrs(t, filepath.Join(dest, name))
		if a&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 {
			t.Errorf("%s is not a link (attributes %#x)", name, a)
		}
		if got := a&windows.FILE_ATTRIBUTE_DIRECTORY != 0; got != wantDir {
			t.Errorf("%s: directory link = %v, want %v", name, got, wantDir)
		}
	}
}

// TestCopyWindowsAttributes は、読み取り専用属性と隠し属性がファイル・フォルダとも保持されることを確かめる（§15）。
func TestCopyWindowsAttributes(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/tree/hidden.txt": testfs.File("h"), "src/tree/hdir/x": testfs.File("x"), "src/tree/both.txt": testfs.File("b"), "dest": testfs.Dir()})
	src := filepath.Join(root, "src", "tree")
	for rel, add := range map[string]uint32{
		"hidden.txt": windows.FILE_ATTRIBUTE_HIDDEN, "hdir": windows.FILE_ATTRIBUTE_HIDDEN,
		"both.txt": windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_READONLY,
	} {
		p := filepath.Join(src, rel)
		p16, _ := windows.UTF16PtrFromString(testfs.ExtendedPath(p))
		if err := windows.SetFileAttributes(p16, attrs(t, p)|add); err != nil {
			t.Fatal(err)
		}
		if add&windows.FILE_ATTRIBUTE_READONLY != 0 {
			t.Cleanup(func() { windows.SetFileAttributes(p16, attrs(t, p)&^windows.FILE_ATTRIBUTE_READONLY) })
		}
	}
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{src}, DestDir: filepath.Join(root, "dest")})
	if res := execPlan(t, context.Background(), plan, ExecOptions{}); res.Status != StatusCompleted {
		t.Fatalf("result = %+v", res)
	}
	const mask = windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_READONLY
	for _, rel := range []string{"hidden.txt", "hdir", "hdir/x", "both.txt"} {
		s, d := attrs(t, filepath.Join(src, rel))&mask, attrs(t, filepath.Join(root, "dest", "tree", rel))&mask
		if s != d {
			t.Errorf("%s: attributes %#x, want %#x", rel, d, s)
		}
	}
}

// TestCopyZoneIdentifier は、Zone.Identifier が保持されることと、代替データストリームを扱えないコピー先（exFAT・FAT32）では
// データは無事で KindMetadata の警告になることを確かめる（§15、V6）。
func TestCopyZoneIdentifier(t *testing.T) {
	t.Parallel()
	const zone = "[ZoneTransfer]\r\nZoneId=3\r\n"
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/dl.txt": testfs.File("downloaded"), "src/plain.txt": testfs.File("plain"), "dest": testfs.Dir()})
	if err := os.WriteFile(testfs.ExtendedPath(filepath.Join(root, "src", "dl.txt"))+":Zone.Identifier", []byte(zone), 0o644); err != nil {
		t.Fatal(err)
	}
	srcs := []string{filepath.Join(root, "src", "dl.txt"), filepath.Join(root, "src", "plain.txt")}
	t.Run("NTFS", func(t *testing.T) {
		dest := filepath.Join(root, "dest")
		res := execPlan(t, context.Background(), mustPlan(t, Request{Op: OpCopy, Sources: srcs, DestDir: dest}), ExecOptions{})
		for _, it := range res.Items {
			if it.Outcome != OutcomeDone || len(it.Warnings) != 0 {
				t.Errorf("%s: %+v", it.Src, it)
			}
		}
		got, err := os.ReadFile(testfs.ExtendedPath(filepath.Join(dest, "dl.txt")) + ":Zone.Identifier")
		if err != nil || string(got) != zone {
			t.Errorf("Zone.Identifier = %q, %v; want %q", got, err, zone)
		}
		if _, err := os.Stat(testfs.ExtendedPath(filepath.Join(dest, "plain.txt")) + ":Zone.Identifier"); err == nil {
			t.Error("plain.txt got a Zone.Identifier")
		}
	})
	for _, env := range []string{testfs.ExFATEnv, testfs.FAT32Env} {
		t.Run(env, func(t *testing.T) {
			dest := testfs.EnvDir(t, env)
			res := execPlan(t, context.Background(), mustPlan(t, Request{Op: OpCopy, Sources: srcs, DestDir: dest}), ExecOptions{})
			if it := res.Items[0]; it.Outcome != OutcomeDone || len(it.Warnings) != 1 || it.Warnings[0].Kind != KindMetadata {
				t.Errorf("dl.txt = %+v, want Done with a KindMetadata warning", it)
			}
			if it := res.Items[1]; it.Outcome != OutcomeDone || len(it.Warnings) != 0 {
				t.Errorf("plain.txt = %+v", it)
			}
			wantFiles(t, dest, map[string]string{"dl.txt": "downloaded", "plain.txt": "plain"})
			noTempFiles(t, dest)
		})
	}
}

// crossDeviceErr は、ボリューム違いのリネームのエラー（§11.1）を返す（テストの注入用）。
func crossDeviceErr() error { return windows.ERROR_NOT_SAME_DEVICE }

package probe

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestV11 は、末尾が . や空白の名前と予約名のエントリを \\?\ 形式のパスで扱ったとき、
// os の各関数（Lstat、ReadDir、Mkdir、Remove、Rename、Chtimes、Readlink）が同名の別エントリ（foo）に影響しないことを確かめる（SPEC §20 V11、§8.2）。
// 別のエントリに影響した場合は、§8.2 の前提が崩れるのでテストを失敗にする。
// 比較のため、\\?\ を付けない場合の動作も記録する。
func TestV11(t *testing.T) {
	logWindowsVersion(t, "V11")
	root := testfs.TempDir(t)
	x := testfs.ExtendedPath
	mt := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)

	// 各操作の前に作り直し、操作の後に「操作対象以外が変わっていないこと」を確かめる。
	setup := func(t *testing.T, name string) (dir string, before testfs.Snapshot) {
		dir = filepath.Join(root, testfs.Sanitize(name))
		testfs.Build(t, dir, testfs.Tree{
			testfs.NamePlain:          testfs.File("plain").At(mt),
			testfs.NameTrailingDot:    testfs.File("dot").At(mt),
			testfs.NameTrailingSpace:  testfs.File("space").At(mt),
			testfs.NameReservedCON:    testfs.File("con").At(mt),
			"sub/" + testfs.NamePlain: testfs.File("plain").At(mt),
			"lnk.":                    testfs.Symlink("target-of-lnk-dot"),
			"lnk":                     testfs.File("plain lnk").At(mt),
		})
		return dir, testfs.Take(t, dir)
	}
	// checkOthers は、changed 以外のエントリが before から変わっていないことを確かめる。
	checkOthers := func(t *testing.T, op, dir string, before testfs.Snapshot, changed ...string) {
		after := testfs.Take(t, dir)
		skip := map[string]bool{".": true}
		for _, c := range changed {
			skip[c] = true
		}
		for rel, n := range before {
			if skip[rel] {
				continue
			}
			if after[rel] != n {
				t.Errorf("V11: %s affected %q: %+v -> %+v", op, rel, n, after[rel])
			}
		}
	}

	t.Run("Lstat", func(t *testing.T) {
		dir, _ := setup(t, "lstat")
		fi, err := os.Lstat(x(filepath.Join(dir, testfs.NameTrailingDot)))
		ok := err == nil && fi.Size() == int64(len("dot"))
		t.Logf("V11: Lstat(\\\\?\\...\\foo.): size=%v err=%v -> refers to foo.: %v", sizeOf(fi), err, ok)
		if !ok {
			t.Error("V11: Lstat with \\\\?\\ did not refer to foo.")
		}
		fi, err = os.Lstat(filepath.Join(dir, testfs.NameTrailingDot))
		t.Logf("V11: (without \\\\?\\) Lstat(...\\foo.): size=%v err=%v (foo is %d bytes, foo. is %d bytes)", sizeOf(fi), err, len("plain"), len("dot"))
	})

	t.Run("ReadDir", func(t *testing.T) {
		dir, _ := setup(t, "readdir")
		names := testfs.ListNames(t, dir)
		t.Logf("V11: ReadDir(\\\\?\\...): %+q", names)
		entries, err := os.ReadDir(dir)
		var plain []string
		for _, e := range entries {
			plain = append(plain, e.Name())
		}
		t.Logf("V11: (without \\\\?\\) ReadDir: %+q err=%v", plain, err)
	})

	t.Run("Mkdir", func(t *testing.T) {
		dir, before := setup(t, "mkdir")
		err := os.Mkdir(x(filepath.Join(dir, "sub.")), 0o755)
		t.Logf("V11: Mkdir(\\\\?\\...\\sub.): err=%v names=%+q", err, testfs.ListNames(t, dir))
		if err != nil {
			t.Error("V11: Mkdir with \\\\?\\ failed")
		}
		checkOthers(t, "Mkdir(sub.)", dir, before, "sub.")
	})

	t.Run("Remove", func(t *testing.T) {
		dir, before := setup(t, "remove")
		for _, name := range []string{testfs.NameTrailingDot, testfs.NameTrailingSpace, testfs.NameReservedCON} {
			err := os.Remove(x(filepath.Join(dir, name)))
			t.Logf("V11: Remove(\\\\?\\...\\%q): err=%v exists after=%v", name, err, testfs.Exists(t, filepath.Join(dir, name)))
			if err != nil {
				t.Errorf("V11: Remove(%q) with \\\\?\\ failed", name)
			}
		}
		checkOthers(t, "Remove", dir, before, testfs.NameTrailingDot, testfs.NameTrailingSpace, testfs.NameReservedCON)
	})

	t.Run("Rename", func(t *testing.T) {
		dir, before := setup(t, "rename")
		err := os.Rename(x(filepath.Join(dir, testfs.NameTrailingDot)), x(filepath.Join(dir, "renamed.")))
		t.Logf("V11: Rename(\\\\?\\...\\foo., \\\\?\\...\\renamed.): err=%v names=%+q", err, testfs.ListNames(t, dir))
		if err != nil || testfs.ReadFile(t, filepath.Join(dir, "renamed.")) != "dot" {
			t.Error("V11: Rename with \\\\?\\ did not move foo. to renamed.")
		}
		checkOthers(t, "Rename(foo. -> renamed.)", dir, before, testfs.NameTrailingDot, "renamed.")
	})

	t.Run("Chtimes", func(t *testing.T) {
		dir, before := setup(t, "chtimes")
		nt := time.Date(2011, 1, 1, 0, 0, 0, 0, time.UTC)
		for _, name := range []string{testfs.NameTrailingDot, testfs.NameReservedCON} {
			err := os.Chtimes(x(filepath.Join(dir, name)), nt, nt)
			fi, _ := os.Lstat(x(filepath.Join(dir, name)))
			t.Logf("V11: Chtimes(\\\\?\\...\\%q): err=%v mtime after=%v", name, err, modTimeOf(fi))
			if err != nil || fi == nil || !fi.ModTime().Equal(nt) {
				t.Errorf("V11: Chtimes(%q) with \\\\?\\ did not change its mtime", name)
			}
		}
		checkOthers(t, "Chtimes", dir, before, testfs.NameTrailingDot, testfs.NameReservedCON)
	})

	t.Run("Readlink", func(t *testing.T) {
		dir, before := setup(t, "readlink")
		target, err := os.Readlink(x(filepath.Join(dir, "lnk.")))
		t.Logf("V11: Readlink(\\\\?\\...\\lnk.): %q err=%v", target, err)
		if err != nil || target != "target-of-lnk-dot" {
			t.Error("V11: Readlink with \\\\?\\ did not read lnk.")
		}
		target, err = os.Readlink(filepath.Join(dir, "lnk."))
		t.Logf("V11: (without \\\\?\\) Readlink(...\\lnk.): %q err=%v", target, err)
		checkOthers(t, "Readlink", dir, before)
	})
}

func sizeOf(fi os.FileInfo) any {
	if fi == nil {
		return nil
	}
	return fi.Size()
}

func modTimeOf(fi os.FileInfo) any {
	if fi == nil {
		return nil
	}
	return fi.ModTime()
}

package fsops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

func mustID(t *testing.T, f func(string) (idStat, error), p string) idStat {
	t.Helper()
	s, err := f(p)
	if err != nil {
		t.Fatalf("fileID of %s: %v", p, err)
	}
	if s.id == (fileID{}) {
		t.Fatalf("fileID of %s is zero", p)
	}
	return s
}

// TestFileID は、fileID が同じエントリで一致し、別のエントリで異なることを確かめる（§8.3）。
func TestFileID(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"a.txt":     testfs.File("a"),
		"b.txt":     testfs.File("a"), // 内容が同じでも別のファイル
		"dir/x":     testfs.File("x"),
		"link.txt":  testfs.Symlink("a.txt"),
		"dirlink":   testfs.DirSymlink("dir"),
		"long-name": testfs.Dir(),
	})
	a := mustID(t, fileIDOf, filepath.Join(root, "a.txt"))
	b := mustID(t, fileIDOf, filepath.Join(root, "b.txt"))
	dir := mustID(t, fileIDOf, filepath.Join(root, "dir"))
	if a.id == b.id || a.id == dir.id {
		t.Errorf("different entries have the same fileID: a=%+v b=%+v dir=%+v", a, b, dir)
	}
	if a.isDir || !dir.isDir {
		t.Errorf("isDir: a=%v dir=%v", a.isDir, dir.isDir)
	}
	if a.nlink != 1 {
		t.Errorf("nlink of a.txt = %d, want 1", a.nlink)
	}

	// 同じエントリを別のパス（途中にフォルダ用のシンボリックリンクを含むパス）で指しても一致する。
	x := mustID(t, fileIDOf, filepath.Join(root, "dir", "x"))
	if got := mustID(t, fileIDOf, filepath.Join(root, "dirlink", "x")); got.id != x.id {
		t.Errorf("fileID via a symlinked ancestor differs")
	}

	// fileIDOf はリンクを辿らず、fileIDFollow は辿る。
	link := mustID(t, fileIDOf, filepath.Join(root, "link.txt"))
	if link.id == a.id {
		t.Error("fileIDOf followed a symlink")
	}
	if got := mustID(t, fileIDFollow, filepath.Join(root, "link.txt")); got.id != a.id {
		t.Error("fileIDFollow did not follow a symlink")
	}
	if got := mustID(t, fileIDFollow, filepath.Join(root, "dirlink")); got.id != dir.id {
		t.Error("fileIDFollow did not follow a directory symlink")
	}

	// 名前を変えても変わらない。
	if err := os.Rename(filepath.Join(root, "b.txt"), filepath.Join(root, "c.txt")); err != nil {
		t.Fatal(err)
	}
	if got := mustID(t, fileIDOf, filepath.Join(root, "c.txt")); got.id != b.id {
		t.Error("fileID changed after rename")
	}

	// ハードリンクは同じファイル。リンク数は 2。
	if err := os.Link(filepath.Join(root, "a.txt"), filepath.Join(root, "hard.txt")); err == nil {
		h := mustID(t, fileIDOf, filepath.Join(root, "hard.txt"))
		if h.id != a.id || h.nlink != 2 {
			t.Errorf("hard link: id equal=%v nlink=%d, want true, 2", h.id == a.id, h.nlink)
		}
	} else {
		t.Logf("hard link not available: %v", err)
	}

	// 大文字小文字を区別しないボリュームでは、大文字小文字の違うパスでも一致する。
	if testfs.FoldsCase(t, root) {
		if got := mustID(t, fileIDOf, filepath.Join(root, "A.TXT")); got.id != a.id {
			t.Error("fileID via a different case differs")
		}
	}

	_, err := fileIDOf(filepath.Join(root, "missing"))
	if KindOf(err) != KindNotFound && classify(err, classifyOpts{}) != KindNotFound {
		t.Errorf("fileIDOf(missing) err = %v, want NotFound", err)
	}
}

// TestFileIDLongPath は、260 文字を超えるパスでも fileID を得られることを確かめる。
func TestFileIDLongPath(t *testing.T) {
	t.Parallel()
	long := testfs.LongPath(t, testfs.TempDir(t))
	p := filepath.Join(long, "f")
	testfs.WriteFile(t, p, "x")
	mustID(t, fileIDOf, p)
	mustID(t, fileIDOf, long)
}

// TestFileIDOtherVolumes は、exFAT・FAT32 のボリュームでも fileID を得られ、別のファイルと区別できることを確かめる（§8.3 の代わりの方法、V14）。
func TestFileIDOtherVolumes(t *testing.T) {
	t.Parallel()
	for _, env := range []string{testfs.ExFATEnv, testfs.FAT32Env, testfs.CrossVolEnv} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			root := testfs.EnvDir(t, env)
			testfs.Build(t, root, testfs.Tree{"a.txt": testfs.File("a"), "b.txt": testfs.File("b")})
			a := mustID(t, fileIDOf, filepath.Join(root, "a.txt"))
			b := mustID(t, fileIDOf, filepath.Join(root, "b.txt"))
			if a.id == b.id {
				t.Errorf("a.txt and b.txt have the same fileID %+v", a.id)
			}
			if got := mustID(t, fileIDOf, filepath.Join(root, "a.txt")); got.id != a.id {
				t.Error("fileID is not stable")
			}
			t.Logf("%s: fileID method=%d", env, a.id.method)
		})
	}
}

// TestFileIDWin32UnsafeNames は、末尾が . や空白の名前・予約名の fileID が、同名の別ファイルと取り違えられないことを確かめる。
func TestFileIDWin32UnsafeNames(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	names := []string{testfs.NamePlain, testfs.NameTrailingDot, testfs.NameTrailingSpace, testfs.NameReservedCON}
	tree := testfs.Tree{}
	for _, n := range names {
		tree[n] = testfs.File(n)
	}
	testfs.Build(t, root, tree)
	seen := map[fileID]string{}
	for _, n := range names {
		s := mustID(t, fileIDOf, filepath.Join(root, n))
		if prev, ok := seen[s.id]; ok {
			t.Errorf("%q and %q have the same fileID", prev, n)
		}
		seen[s.id] = n
	}
}

// TestOnOtherVolume は、マウントポイントの判定（§13.1）が、Unix の Dev の違いだけを見ることを確かめる。
func TestOnOtherVolume(t *testing.T) {
	t.Parallel()
	unixID := func(vol uint64) fileID { return fileID{method: idMethodDevIno, vol: vol} }
	if !onOtherVolume(unixID(2), unixID(1)) {
		t.Error("different Dev: want true")
	}
	if onOtherVolume(unixID(1), unixID(1)) {
		t.Error("same Dev: want false")
	}
	win := fileID{method: idMethodFileID, vol: 2}
	if onOtherVolume(win, fileID{method: idMethodFileID, vol: 1}) {
		t.Error("Windows: want false (mounted folders are junctions and are never entered)")
	}
	if onOtherVolume(unixID(2), fileID{}) {
		t.Error("unknown parent: want false")
	}
}

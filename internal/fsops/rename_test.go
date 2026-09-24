package fsops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// testExclusiveRename は、排他リネームの関数 rename が §8.4 を満たすことを dir の中で確かめる。
// 既存の移動先を上書きしないこと（I1）、名前をバイト単位でそのまま使うこと（I6）を含む。
func testExclusiveRename(t *testing.T, dir string, rename func(src, dst string) error) {
	t.Helper()
	t.Run("new name", func(t *testing.T) {
		d := filepath.Join(dir, "new")
		testfs.Build(t, d, testfs.Tree{"a.txt": testfs.File("a"), "dir/x": testfs.File("x")})
		if err := rename(filepath.Join(d, "a.txt"), filepath.Join(d, "b.txt")); err != nil {
			t.Fatal(err)
		}
		if err := rename(filepath.Join(d, "dir"), filepath.Join(d, "dir2")); err != nil {
			t.Fatal(err)
		}
		if got := testfs.ListNames(t, d); !slices.Equal(got, []string{"b.txt", "dir2"}) {
			t.Errorf("names = %+q", got)
		}
		if testfs.ReadFile(t, filepath.Join(d, "dir2", "x")) != "x" {
			t.Error("the contents of the renamed dir changed")
		}
	})
	for _, c := range []struct {
		name string
		tree testfs.Tree
		src  string
		dst  string
	}{
		{"existing file", testfs.Tree{"a.txt": testfs.File("a"), "b.txt": testfs.File("b")}, "a.txt", "b.txt"},
		{"file onto existing dir", testfs.Tree{"a.txt": testfs.File("a"), "b/y": testfs.File("y")}, "a.txt", "b"},
		{"dir onto existing dir", testfs.Tree{"a/x": testfs.File("x"), "b/y": testfs.File("y")}, "a", "b"},
		{"dir onto existing empty dir", testfs.Tree{"a/x": testfs.File("x"), "b": testfs.Dir()}, "a", "b"},
		{"dir onto existing file", testfs.Tree{"a/x": testfs.File("x"), "b": testfs.File("b")}, "a", "b"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := filepath.Join(dir, testfs.Sanitize(c.name))
			testfs.Build(t, d, c.tree)
			before := testfs.Take(t, d)
			err := rename(filepath.Join(d, c.src), filepath.Join(d, c.dst))
			if KindOf(err) != KindExist && classify(err, classifyOpts{}) != KindExist {
				t.Errorf("err = %v, want KindExist", err)
			}
			if diff := testfs.Diff(before, testfs.Take(t, d)); diff != nil {
				t.Errorf("the tree changed (I1): %q", diff)
			}
		})
	}
	t.Run("missing source", func(t *testing.T) {
		d := filepath.Join(dir, "missing")
		testfs.MkdirAll(t, d)
		err := rename(filepath.Join(d, "nope"), filepath.Join(d, "b"))
		if classify(err, classifyOpts{}) != KindNotFound {
			t.Errorf("err = %v, want KindNotFound", err)
		}
	})
	t.Run("names are kept byte for byte", func(t *testing.T) {
		d := filepath.Join(dir, "i6")
		testfs.Build(t, d, testfs.Tree{"a": testfs.File("1"), "b": testfs.File("2"), "c": testfs.File("3")})
		for src, dst := range map[string]string{"a": testfs.NameJapanese, "b": testfs.NameEmoji, "c": testfs.NameNFD} {
			if err := rename(filepath.Join(d, src), filepath.Join(d, dst)); err != nil {
				t.Fatal(err)
			}
		}
		want := []string{testfs.NameJapanese, testfs.NameEmoji, testfs.NameNFD}
		got := testfs.ListNames(t, d)
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("names = %+q, want %+q among them", got, w)
			}
		}
	})
}

func TestRenameExclusive(t *testing.T) {
	t.Parallel()
	testExclusiveRename(t, testfs.TempDir(t), renameExclusive)
}

// TestRenameExclusiveOtherVolumes は、exFAT・FAT32 でも §8.4 を満たすことを確かめる。
// macOS の exFAT では RENAME_EXCL が使えず、代わりの手段（名前を確保してから置き換える）が使われる（V12）。
func TestRenameExclusiveOtherVolumes(t *testing.T) {
	t.Parallel()
	for _, env := range []string{testfs.ExFATEnv, testfs.FAT32Env, testfs.CrossVolEnv} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			testExclusiveRename(t, testfs.EnvDir(t, env), renameExclusive)
		})
	}
}

// TestRenameExclusiveSameFile は、大文字小文字・正規化の違いだけの名前変更（同じファイル）を §8.4 のとおりに扱うことを確かめる。
func TestRenameExclusiveSameFile(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	folds := testfs.FoldsCase(t, root)
	testfs.Build(t, root, testfs.Tree{
		"case/" + testfs.NameLower: testfs.File("c"),
		"casedir/abc/x":            testfs.File("x"),
		"nfc/" + testfs.NameNFC:    testfs.File("n"),
	})
	for _, c := range []struct{ dir, src, dst string }{
		{"case", testfs.NameLower, testfs.NameUpper},
		{"casedir", "abc", "ABC"},
		{"nfc", testfs.NameNFC, testfs.NameNFD},
	} {
		d := filepath.Join(root, c.dir)
		if err := renameExclusive(filepath.Join(d, c.src), filepath.Join(d, c.dst)); err != nil {
			t.Errorf("%s: renameExclusive(%q, %q): %v", c.dir, c.src, c.dst, err)
			continue
		}
		if got := testfs.ListNames(t, d); !slices.Equal(got, []string{c.dst}) {
			t.Errorf("%s: names = %+q, want [%+q]", c.dir, got, c.dst)
		}
	}
	t.Logf("case-insensitive volume: %v", folds)
}

// TestRenameExclusiveHardLink は、同じファイルへのハードリンクどうしを「同じファイルの名前変更」と取り違えないことを確かめる（§8.4）。
func TestRenameExclusiveHardLink(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"a": testfs.File("a")})
	if err := os.Link(testfs.ExtendedPath(filepath.Join(root, "a")), testfs.ExtendedPath(filepath.Join(root, "b"))); err != nil {
		t.Skipf("hard links are not available: %v", err)
	}
	err := renameExclusive(filepath.Join(root, "a"), filepath.Join(root, "b"))
	if classify(err, classifyOpts{}) != KindExist {
		t.Errorf("renameExclusive between hard links: err = %v, want KindExist", err)
	}
	if got := testfs.ListNames(t, root); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("names = %+q, want [a b]", got)
	}
}

// TestRenameReplace は、置換リネームが既存のファイルを置き換えることを確かめる（§8.4）。
func TestRenameReplace(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"a": testfs.File("new"), "b": testfs.File("old")})
	if err := renameReplace(filepath.Join(root, "a"), filepath.Join(root, "b")); err != nil {
		t.Fatal(err)
	}
	if got := testfs.ListNames(t, root); !slices.Equal(got, []string{"b"}) || testfs.ReadFile(t, filepath.Join(root, "b")) != "new" {
		t.Errorf("names = %+q, content = %q", got, testfs.ReadFile(t, filepath.Join(root, "b")))
	}
}

// TestRenameHelpersWin32UnsafeNames は、helper を通した Lstat・Remove・Rename が、
// 末尾が . や空白の名前・予約名のエントリを同名の別ファイル（foo）と取り違えないことを確かめる（§18.4「パス」、V11）。
func TestRenameHelpersWin32UnsafeNames(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		testfs.NamePlain:         testfs.File("plain"),
		testfs.NameTrailingDot:   testfs.File("dot"),
		testfs.NameTrailingSpace: testfs.File("space"),
		testfs.NameReservedCON:   testfs.File("con"),
	})
	plain := filepath.Join(root, testfs.NamePlain)
	before := testfs.Take(t, root)["foo"]

	if err := renameExclusive(filepath.Join(root, testfs.NameTrailingDot), filepath.Join(root, "renamed.")); err != nil {
		t.Fatal(err)
	}
	if err := renameReplace(filepath.Join(root, testfs.NameReservedCON), filepath.Join(root, "renamed.")); err != nil {
		t.Fatal(err)
	}
	s, err := sysPath(filepath.Join(root, testfs.NameTrailingSpace))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(s); err != nil {
		t.Fatal(err)
	}
	info, err := lstatEntry(filepath.Join(root, "renamed."))
	if err != nil || info.Size != int64(len("con")) {
		t.Errorf("lstatEntry(renamed.) = %+v, %v; want the content of CON", info, err)
	}
	if got := testfs.Take(t, root)["foo"]; got != before || testfs.ReadFile(t, plain) != "plain" {
		t.Errorf("foo changed: %+v -> %+v", before, got)
	}
	if got := testfs.ListNames(t, root); !slices.Equal(got, []string{"foo", "renamed."}) {
		t.Errorf("names = %+q, want [foo renamed.]", got)
	}
}

// TestRenameLongPath は、260 文字を超えるパスでも排他リネームできることを確かめる。
func TestRenameLongPath(t *testing.T) {
	t.Parallel()
	long := testfs.LongPath(t, testfs.TempDir(t))
	testfs.WriteFile(t, filepath.Join(long, "a"), "a")
	if err := renameExclusive(filepath.Join(long, "a"), filepath.Join(long, "b")); err != nil {
		t.Fatal(err)
	}
	if got := testfs.ListNames(t, long); !slices.Equal(got, []string{"b"}) {
		t.Errorf("names = %+q", got)
	}
}

// twoStepRename は、1 回目の変更が成功を返しても名前が変わらない（Windows の exFAT・FAT32。V1）状況をフックで再現して、
// path の名前を newName に変える（§11.3 の 2 段階の変更を通る）。failTo のパスへの変更は、使用中で失敗させ続ける（やり直しの待ちは置き換える）。
func twoStepRename(t *testing.T, path, newName string, failTo ...string) error {
	t.Helper()
	h := &testHooks{
		caseRenameNoop: true,
		lockFault:      func(op, p string) bool { return op == "rename" && slices.Contains(failTo, p) },
		lockWait:       func(string, time.Duration) {},
	}
	return renameWith(newLockRetrier(context.Background(), h), path, newName)
}

// requireFoldsCase は、大文字小文字だけの変更が §11.3 の確認（同じファイルへの変更）を通る、大文字小文字を区別しないフォルダでなければ Skip する。
func requireFoldsCase(t *testing.T, dir string) {
	t.Helper()
	if !testfs.FoldsCase(t, dir) {
		t.Skip("the folder is case-sensitive; a case-only rename is an ordinary rename there (§11.3)")
	}
}

// TestRenameTwoStep は、§11.3 の 2 段階の変更で、途中名（.fsops-rename-<16 進>）を経由して名前が変わり、途中名が残らないことを確かめる。
func TestRenameTwoStep(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	requireFoldsCase(t, root)
	testfs.Build(t, root, testfs.Tree{"a.txt": testfs.File("a")})
	if err := twoStepRename(t, filepath.Join(root, "a.txt"), "A.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := testfs.ListNames(t, root); !slices.Equal(got, []string{"A.txt"}) {
		t.Errorf("names = %q, want [A.txt]", got)
	}
}

// TestRenameTwoStepSecondFails は、2 段階の変更の 2 回目が失敗したら、元の名前に戻して、新しい名前を Dest にして失敗を返すことを確かめる（§11.3）。
func TestRenameTwoStepSecondFails(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	requireFoldsCase(t, root)
	testfs.Build(t, root, testfs.Tree{"a.txt": testfs.File("a")})
	dst := filepath.Join(root, "A.txt")
	err := twoStepRename(t, filepath.Join(root, "a.txt"), "A.txt", dst)
	var oe *OpError
	if !errors.As(err, &oe) || oe.Kind != KindLocked || oe.Dest != dst {
		t.Fatalf("err = %v, want KindLocked with Dest %s", err, dst)
	}
	if got := testfs.ListNames(t, root); !slices.Equal(got, []string{"a.txt"}) {
		t.Errorf("names = %q, want [a.txt] (restored)", got)
	}
}

// TestRenameTwoStepRollbackFails は、2 段階の変更で元の名前にも戻せなかった場合に、利用者のファイルが途中名で残り、
// そのパスを Dest で返すこと、途中名が一時ファイルの名前（.fsops-<16 進>.tmp）と形が違うことを確かめる（§11.3）。
func TestRenameTwoStepRollbackFails(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	requireFoldsCase(t, root)
	testfs.Build(t, root, testfs.Tree{"a.txt": testfs.File("user data")})
	src := filepath.Join(root, "a.txt")
	err := twoStepRename(t, src, "A.txt", filepath.Join(root, "A.txt"), src)
	var oe *OpError
	if !errors.As(err, &oe) || oe.Kind != KindLocked {
		t.Fatalf("err = %v, want KindLocked", err)
	}
	name := filepath.Base(oe.Dest)
	if filepath.Dir(oe.Dest) != root || !strings.HasPrefix(name, ".fsops-rename-") || strings.HasSuffix(name, ".tmp") {
		t.Errorf("Dest = %s, want %s/.fsops-rename-<hex> (not a temp file name)", oe.Dest, root)
	}
	if got := testfs.ReadFile(t, oe.Dest); got != "user data" {
		t.Errorf("%s = %q, want the user's file", oe.Dest, got)
	}
	if got := testfs.ListNames(t, root); !slices.Equal(got, []string{name}) {
		t.Errorf("names = %q, want only [%s]", got, name)
	}
}

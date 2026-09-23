package testfs

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func isRoot() bool { return runtime.GOOS != "windows" && os.Geteuid() == 0 }

func TestBuildAndTake(t *testing.T) {
	t.Parallel()
	root := TempDir(t)
	mt := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	Build(t, root, Tree{
		"a.txt":       File("alpha").At(mt),
		"dir":         Dir().At(mt),
		"dir/b.txt":   File("beta"),
		"deep/x/y.md": File(""),
	})

	s := Take(t, root)
	want := map[string]Node{
		"a.txt":       {Type: "file", Size: 5, SHA256: sha("alpha"), ModTime: mt.UnixNano()},
		"dir":         {Type: "dir", ModTime: mt.UnixNano()},
		"dir/b.txt":   {Type: "file", Size: 4, SHA256: sha("beta")},
		"deep/x/y.md": {Type: "file", SHA256: sha("")},
	}
	for rel, w := range want {
		got, ok := s[rel]
		if !ok {
			t.Errorf("snapshot has no %q", rel)
			continue
		}
		if got.Type != w.Type || got.Size != w.Size || got.SHA256 != w.SHA256 {
			t.Errorf("%s = %+v, want %+v", rel, got, w)
		}
		if w.ModTime != 0 && got.ModTime != w.ModTime {
			t.Errorf("%s ModTime = %v, want %v", rel, time.Unix(0, got.ModTime), mt)
		}
	}
	for _, rel := range []string{".", "deep", "deep/x"} {
		if s[rel].Type != "dir" {
			t.Errorf("%s = %+v, want a dir", rel, s[rel])
		}
	}

	if d := Diff(s, Take(t, root)); d != nil {
		t.Errorf("Diff of two snapshots of an unchanged tree = %q, want nil", d)
	}
	WriteFile(t, filepath.Join(root, "dir", "b.txt"), "changed")
	WriteFile(t, filepath.Join(root, "new.txt"), "")
	d := Diff(s, Take(t, root))
	if len(d) < 2 || !slices.ContainsFunc(d, func(l string) bool { return strings.HasPrefix(l, "~ dir/b.txt ") }) ||
		!slices.ContainsFunc(d, func(l string) bool { return strings.HasPrefix(l, "+ new.txt ") }) {
		t.Errorf("Diff after changes = %q, want entries for dir/b.txt and new.txt", d)
	}
}

func TestSymlinks(t *testing.T) {
	t.Parallel()
	root := TempDir(t)
	Build(t, root, Tree{
		"target.txt":       File("t"),
		"targetdir":        Dir(),
		"targetdir/marker": File("m"),
		"link-file":        Symlink("target.txt"),
		"link-dir":         DirSymlink("targetdir"),
		"dangling":         DirSymlink("missing"),
	})
	s := Take(t, root)
	for rel, target := range map[string]string{"link-file": "target.txt", "link-dir": "targetdir", "dangling": "missing"} {
		if n := s[rel]; n.Type != "link" || n.Target != target {
			t.Errorf("%s = %+v, want a link to %q", rel, n, target)
		}
	}
	// Take はリンクの先に入り込まない。
	if _, ok := s["link-dir/marker"]; ok {
		t.Error("Take followed a directory symlink")
	}
	if got := ReadFile(t, filepath.Join(root, "link-dir", "marker")); got != "m" {
		t.Errorf("reading through the directory symlink = %q, want %q", got, "m")
	}
}

func TestJunction(t *testing.T) {
	t.Parallel()
	root := TempDir(t)
	Build(t, root, Tree{
		"target/marker": File("m"),
		"junction":      Junction("target"),
	})
	s := Take(t, root)
	if n := s["junction"]; n.Type != "link" {
		t.Errorf("junction = %+v, want a link", n)
	}
	if _, ok := s["junction/marker"]; ok {
		t.Error("Take followed a junction")
	}
	if got := ReadFile(t, filepath.Join(root, "junction", "marker")); got != "m" {
		t.Errorf("reading through the junction = %q, want %q", got, "m")
	}
}

func TestFIFO(t *testing.T) {
	t.Parallel()
	root := TempDir(t)
	Build(t, root, Tree{"fifo": FIFO()})
	fi, err := os.Lstat(filepath.Join(root, "fifo"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Type() != fs.ModeNamedPipe {
		t.Errorf("mode = %v, want a named pipe", fi.Mode())
	}
	if n := Take(t, root)["fifo"]; n.Type != "other" {
		t.Errorf("snapshot of a FIFO = %+v, want other", n)
	}
}

func TestReadOnly(t *testing.T) {
	t.Parallel()
	if isRoot() {
		t.Skip("running as root: permission bits do not prevent writes")
	}
	root := TempDir(t)
	Build(t, root, Tree{
		"ro.txt":       File("x").RO(),
		"rodir":        Dir().RO(),
		"rodir/in.txt": File("in"),
	})
	f, err := os.OpenFile(filepath.Join(root, "ro.txt"), os.O_WRONLY, 0)
	if err == nil {
		f.Close()
		t.Error("opening a read-only file for writing succeeded")
	}
	fi, err := os.Lstat(filepath.Join(root, "ro.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o222 != 0 {
		t.Errorf("mode of a read-only file = %v, want no write bits", fi.Mode())
	}
	// Windows のフォルダの読み取り専用属性は中身の作成を妨げない（SPEC §13.2）ので、Unix だけ確かめる。
	if runtime.GOOS != "windows" {
		if err := os.WriteFile(filepath.Join(root, "rodir", "new.txt"), nil, 0o644); err == nil {
			t.Error("creating a file in a read-only directory succeeded")
		}
	}
	// 後片付け（t.Cleanup で書き込み可能に戻し、TempDir を削除できること）は、このテストの終了時に確かめられる。
}

func TestLock(t *testing.T) {
	t.Parallel()
	root := TempDir(t)
	p := filepath.Join(root, "locked.txt")
	WriteFile(t, p, "x")
	Lock(t, p)
	f, err := os.Open(p)
	if err == nil {
		f.Close()
		t.Fatal("opening a locked file succeeded")
	}
}

func TestLongPath(t *testing.T) {
	t.Parallel()
	root := TempDir(t)
	p := LongPath(t, root)
	if len(p) <= 260 {
		t.Fatalf("len(LongPath) = %d, want > 260", len(p))
	}
	f := filepath.Join(p, "file.txt")
	WriteFile(t, f, "long")
	if got := ReadFile(t, f); got != "long" {
		t.Errorf("ReadFile = %q", got)
	}
	if !Exists(t, f) {
		t.Error("Exists = false")
	}
}

// TestNames は、日本語・絵文字・NFC・NFD・大文字小文字の名前を作り、名前がバイト単位でそのまま残ることを確かめる（I6 の確認用）。
func TestNames(t *testing.T) {
	t.Parallel()
	root := TempDir(t)
	// NFC と NFD、大文字と小文字は、同じ名前とみなすボリュームがあるので別のフォルダに作る。
	tree := Tree{}
	for i, name := range []string{NameJapanese, NameEmoji, NameNFC, NameNFD, NameLower, NameUpper} {
		tree[filepath.ToSlash(filepath.Join(string(rune('a'+i)), name))] = File(name)
	}
	Build(t, root, tree)
	for rel, e := range tree {
		dir, name := filepath.Split(filepath.Join(root, filepath.FromSlash(rel)))
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || entries[0].Name() != name {
			var got []string
			for _, e := range entries {
				got = append(got, e.Name())
			}
			t.Errorf("ReadDir(%s) = %+q, want [%+q]", dir, got, name)
		}
		if got := ReadFile(t, filepath.Join(dir, name)); got != e.Data {
			t.Errorf("content of %+q = %+q", name, got)
		}
	}
	t.Logf("FoldsCase = %v, FoldsNormalization = %v (%s)", FoldsCase(t, root), FoldsNormalization(t, root), runtime.GOOS)
}

// TestWin32UnsafeNames は、末尾が . や空白の名前と予約名のエントリを作り、
// 同名の別ファイル（foo）と取り違えずに読み書きできることを確かめる。
func TestWin32UnsafeNames(t *testing.T) {
	t.Parallel()
	root := TempDir(t)
	names := []string{NamePlain, NameTrailingDot, NameTrailingSpace, NameReservedCON, NameReservedNUL}
	tree := Tree{}
	for _, n := range names {
		tree[n] = File("content of " + n)
	}
	Build(t, root, tree)

	entries, err := os.ReadDir(ExtendedPath(root))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if want := slices.Sorted(slices.Values(names)); !slices.Equal(got, want) {
		t.Fatalf("ReadDir = %+q, want %+q", got, want)
	}
	for _, n := range names {
		if got := ReadFile(t, filepath.Join(root, n)); got != "content of "+n {
			t.Errorf("content of %+q = %+q", n, got)
		}
	}
	s := Take(t, root)
	for _, n := range names {
		if s[n].SHA256 != sha("content of "+n) {
			t.Errorf("snapshot of %+q = %+v", n, s[n])
		}
	}
}

func TestCrossVolDir(t *testing.T) {
	t.Parallel()
	d := CrossVolDir(t)
	if !strings.HasPrefix(d, filepath.Clean(mustEval(t, os.Getenv(CrossVolEnv)))) {
		t.Errorf("CrossVolDir = %s, want a directory in %s", d, os.Getenv(CrossVolEnv))
	}
	Build(t, d, Tree{"a/b.txt": File("x").RO()})
	if !Exists(t, filepath.Join(d, "a", "b.txt")) {
		t.Error("file was not created")
	}
	// 読み取り専用を含むフォルダも、テストの終了時に削除できること（後片付けの確認）。
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	e, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestExists(t *testing.T) {
	t.Parallel()
	root := TempDir(t)
	Build(t, root, Tree{"f": File(""), "d": Dir(), "link": Symlink("missing")})
	for rel, want := range map[string]bool{
		"f":             true,
		"d":             true,
		"link":          true, // リンク先がなくてもリンク自体はある
		"missing":       false,
		"f/child":       false, // 途中の階層がファイル（Unix では ENOTDIR）
		"missing/child": false,
	} {
		if got := Exists(t, filepath.Join(root, filepath.FromSlash(rel))); got != want {
			t.Errorf("Exists(%s) = %v, want %v", rel, got, want)
		}
	}
}

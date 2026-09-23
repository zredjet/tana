package fsops

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// recordOf は、path 以下を記録する（テスト用。フェーズ9ではコピーしながら記録する）。
func recordOf(t *testing.T, path string) recordEntry {
	t.Helper()
	info, err := lstatEntry(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := fileIDOf(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := recordEntry{name: filepath.Base(path), info: info, id: st.id}
	if info.Type == TypeDir {
		entries, err := readDir(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			rec.children = append(rec.children, recordOf(t, filepath.Join(path, e.name)))
		}
	}
	return rec
}

func removeRec(t *testing.T, ctx context.Context, h *testHooks, path string, rec recordEntry) removeOutcome {
	t.Helper()
	return removeRecorded(ctx, path, rec, h, nil)
}

// keptWith は、Details に、path を kind で残したエントリがあるかを返す。
func keptWith(out removeOutcome, path string, kind Kind) bool {
	return slices.ContainsFunc(out.details, func(e EntryResult) bool {
		return e.Src == path && e.Outcome == OutcomeCopiedSourceKept && e.Err != nil && e.Err.Kind == kind
	})
}

// TestRemoveRecordedAll は、記録から変わっていないツリーをすべて削除することを確かめる（§13.3）。
func TestRemoveRecordedAll(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	markerTree(t, root)
	testfs.Build(t, root, testfs.Tree{
		"src/a.txt":                     testfs.File("a"),
		"src/sub/b.txt":                 testfs.File("b"),
		"src/sub/empty":                 testfs.Dir(),
		"src/link":                      testfs.DirSymlink(filepath.Join(root, "outside")),
		"src/file-link":                 testfs.Symlink(filepath.Join(root, "outside-file.txt")),
		"src/" + testfs.NameTrailingDot: testfs.File("dot"),
	})
	src := filepath.Join(root, "src")
	rec := recordOf(t, src)
	out := removeRec(t, context.Background(), nil, src, rec)
	if out.firstErr != nil || out.canceled || len(out.details) != 0 || !out.removedTop {
		t.Errorf("outcome = %+v, want everything removed", out)
	}
	if testfs.Exists(t, src) {
		t.Error("src was not removed")
	}
	checkMarkers(t, root)
}

// TestRemoveRecordedKeepsChanged は、記録の後に変更・追加・置き換えられたものを削除しないことを確かめる（I2、§18.4）。
func TestRemoveRecordedKeepsChanged(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/same.txt":     testfs.File("same"),
		"src/edited.txt":   testfs.File("before"),
		"src/replaced":     testfs.File("file"),
		"src/sub/keep.txt": testfs.File("k"),
		"src/sub2/x.txt":   testfs.File("x"),
	})
	src := filepath.Join(root, "src")
	rec := recordOf(t, src)
	// 記録の後の変更: 編集（大きさが変わる）、フォルダへの置き換え、新しいファイルの追加。
	testfs.WriteFile(t, filepath.Join(src, "edited.txt"), "after, with more bytes")
	if err := os.Remove(testfs.ExtendedPath(filepath.Join(src, "replaced"))); err != nil {
		t.Fatal(err)
	}
	testfs.MkdirAll(t, filepath.Join(src, "replaced"))
	testfs.WriteFile(t, filepath.Join(src, "sub", "added.txt"), "added during the copy")

	out := removeRec(t, context.Background(), nil, src, rec)
	if !keptWith(out, filepath.Join(src, "edited.txt"), KindSourceChanged) {
		t.Errorf("edited.txt was not reported as kept: %+v", out.details)
	}
	if !keptWith(out, filepath.Join(src, "replaced"), KindSourceChanged) {
		t.Errorf("replaced was not reported as kept: %+v", out.details)
	}
	for rel, want := range map[string]string{"edited.txt": "after, with more bytes", "sub/added.txt": "added during the copy"} {
		if got := testfs.ReadFile(t, filepath.Join(src, filepath.FromSlash(rel))); got != want {
			t.Errorf("%s = %q, want %q (I2)", rel, got, want)
		}
	}
	if !testfs.Exists(t, filepath.Join(src, "replaced")) {
		t.Error("the replacing folder was removed")
	}
	for _, rel := range []string{"same.txt", "sub/keep.txt", "sub2"} {
		if testfs.Exists(t, filepath.Join(src, filepath.FromSlash(rel))) {
			t.Errorf("%s was not removed", rel)
		}
	}
	if out.removedTop || out.firstErr == nil {
		t.Errorf("outcome = %+v, want the top folder kept and an error", out)
	}
}

// TestRemoveRecordedDirReplacedByLink は、記録したフォルダが入り込む前にリンクへ置き換えられても、リンクの先に入り込まないことを確かめる（I4）。
func TestRemoveRecordedDirReplacedByLink(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	markerTree(t, root)
	testfs.Build(t, root, testfs.Tree{"src/a.txt": testfs.File("a"), "src/sub/marker.txt": testfs.File("marker")})
	src := filepath.Join(root, "src")
	rec := recordOf(t, src)
	victim := filepath.Join(src, "sub")
	h := &testHooks{beforeEnterDir: func(p string) {
		if p == victim {
			replaceWithLink(t, root, victim)
		}
	}}
	out := removeRec(t, context.Background(), h, src, rec)
	checkMarkers(t, root)
	if !keptWith(out, victim, KindSourceChanged) {
		t.Errorf("details = %+v, want sub kept with KindSourceChanged", out.details)
	}
	if !testfs.Exists(t, victim) {
		t.Error("the injected link was removed")
	}
}

// TestRemoveRecordedCancel は、移動元の削除中にキャンセルされたら、そこで止まることを確かめる（§16）。
func TestRemoveRecordedCancel(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	tree := testfs.Tree{}
	for i := 0; i < 10; i++ {
		tree["src/f"+string(rune('a'+i))] = testfs.File("x")
	}
	testfs.Build(t, root, tree)
	src := filepath.Join(root, "src")
	rec := recordOf(t, src)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := 0
	h := &testHooks{beforeRemove: func(string) {
		if n++; n == 3 {
			cancel()
		}
	}}
	out := removeRec(t, ctx, h, src, rec)
	if !out.canceled || out.removedTop {
		t.Errorf("outcome = %+v, want canceled", out)
	}
	if left := len(testfs.ListNames(t, src)); left == 0 || left == 10 {
		t.Errorf("%d of 10 left, want a partial removal", left)
	}
}

// TestRemoveRecordedReadOnly は、読み取り専用の扱いを確かめる（§13.3）。
// Windows: 照合で一致した読み取り専用のファイルは属性を外して削除する。使用中で削除できなければ属性を元に戻す。
// macOS: ロック（UF_IMMUTABLE）は外さず、KindReadOnly で残す。
func TestRemoveRecordedReadOnly(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	switch runtime.GOOS {
	case "windows":
		testfs.Build(t, root, testfs.Tree{
			"src/ro.txt":        testfs.File("ro").RO(),
			"src/ro-locked.txt": testfs.File("rl").RO(),
			"src/rodir/x":       testfs.File("x"),
			"src/rodir":         testfs.Dir().RO(),
		})
		src := filepath.Join(root, "src")
		locked := filepath.Join(src, "ro-locked.txt")
		rec := recordOf(t, src)
		testfs.Lock(t, locked)
		out := removeRec(t, context.Background(), nil, src, rec)
		if testfs.Exists(t, filepath.Join(src, "ro.txt")) || testfs.Exists(t, filepath.Join(src, "rodir")) {
			t.Errorf("read-only entries were not removed: %+q", testfs.ListNames(t, src))
		}
		if !keptWith(out, locked, KindLocked) {
			t.Errorf("details = %+v, want ro-locked.txt kept with KindLocked", out.details)
		}
		fi, err := os.Lstat(testfs.ExtendedPath(locked))
		if err != nil || fi.Mode().Perm()&0o200 != 0 {
			t.Errorf("the read-only attribute of the locked file was not restored (%v, %v)", fi, err)
		}
	case "darwin":
		testfs.Build(t, root, testfs.Tree{"src/locked": testfs.File("l"), "src/other": testfs.File("o")})
		src := filepath.Join(root, "src")
		rec := recordOf(t, src)
		testfs.SetImmutable(t, filepath.Join(src, "locked"))
		out := removeRec(t, context.Background(), nil, src, rec)
		if !keptWith(out, filepath.Join(src, "locked"), KindReadOnly) {
			t.Errorf("details = %+v, want locked kept with KindReadOnly", out.details)
		}
		if got := testfs.ListNames(t, src); !slices.Equal(got, []string{"locked"}) {
			t.Errorf("left = %+q", got)
		}
	default:
		t.Skip("covered on Windows and macOS")
	}
}

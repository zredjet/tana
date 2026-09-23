package fsops

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// modTime は、path（リンクを辿らない）の更新日時を返す。
func modTime(t *testing.T, path string) time.Time {
	t.Helper()
	fi, err := os.Lstat(testfs.ExtendedPath(path))
	if err != nil {
		t.Fatal(err)
	}
	return fi.ModTime()
}

// readOnly は、path が読み取り専用（Windows: 読み取り専用属性、Unix: オーナーの書き込み権限がない）かを返す。
func readOnly(t *testing.T, path string) bool {
	t.Helper()
	fi, err := os.Lstat(testfs.ExtendedPath(path))
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()&0o200 == 0
}

// TestCopyModTime は、ファイルとフォルダの更新日時が保持されることを確かめる（§15、§18.4「メタデータ」）。
// フォルダの更新日時は中身をすべて処理した後に設定する（§10.2）。
func TestCopyModTime(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	t1 := time.Date(2001, 2, 3, 4, 5, 6, 700_000_000, time.UTC)
	t2 := time.Date(2002, 3, 4, 5, 6, 7, 0, time.UTC)
	t3 := time.Date(2003, 4, 5, 6, 7, 8, 0, time.UTC)
	testfs.Build(t, root, testfs.Tree{
		"src/f.txt":          testfs.File("f").At(t1),
		"src/tree/a.txt":     testfs.File("a").At(t2),
		"src/tree/sub/b.txt": testfs.File("b").At(t3),
		"src/tree/sub":       testfs.Dir().At(t2),
		"src/tree/empty":     testfs.Dir().At(t1),
		"src/tree":           testfs.Dir().At(t3),
		"dest":               testfs.Dir(),
	})
	src, dest := filepath.Join(root, "src"), filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(src, "f.txt"), filepath.Join(src, "tree")}, DestDir: dest})
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if res.Status != StatusCompleted {
		t.Fatalf("result = %+v", res)
	}
	for _, it := range res.Items {
		if len(it.Warnings) != 0 {
			t.Errorf("%s: warnings %+v", it.Src, it.Warnings)
		}
	}
	for _, rel := range []string{"f.txt", "tree", "tree/a.txt", "tree/sub", "tree/sub/b.txt", "tree/empty"} {
		s, d := modTime(t, filepath.Join(src, filepath.FromSlash(rel))), modTime(t, filepath.Join(dest, filepath.FromSlash(rel)))
		if !s.Equal(d) {
			t.Errorf("%s: mtime %v, want %v", rel, d, s)
		}
	}
}

// TestCopyReadOnly は、読み取り専用のファイルと、読み取り専用のフォルダ（Unix の 0o555 を含む）のコピーで、
// 中身も含めて複製され、読み取り専用が保持されることを確かめる（§15、§18.4「読み取り専用」「メタデータ」）。
func TestCopyReadOnly(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/ro.txt":       testfs.File("ro").RO(),
		"src/tree/rodir/x": testfs.File("x"),
		"src/tree/rodir":   testfs.Dir().RO(),
		"src/tree/rw.txt":  testfs.File("rw"),
		"dest":             testfs.Dir(),
	})
	src, dest := filepath.Join(root, "src"), filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(src, "ro.txt"), filepath.Join(src, "tree")}, DestDir: dest})
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if res.Status != StatusCompleted {
		t.Fatalf("result = %+v", res)
	}
	wantFiles(t, dest, map[string]string{"ro.txt": "ro", "tree/rodir/x": "x", "tree/rw.txt": "rw"})
	for rel, want := range map[string]bool{"ro.txt": true, "tree/rodir": true, "tree/rodir/x": false, "tree/rw.txt": false, "tree": false} {
		if got := readOnly(t, filepath.Join(dest, filepath.FromSlash(rel))); got != want {
			t.Errorf("%s: read-only = %v, want %v", rel, got, want)
		}
	}
}

// TestCopyMergeKeepsDirMetadata は、マージで既存のフォルダを使った場合、そのフォルダのメタデータ（更新日時・読み取り専用）を
// コピー元のものに変えないことを確かめる（§15）。
func TestCopyMergeKeepsDirMetadata(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	srcTime := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	testfs.Build(t, root, testfs.Tree{
		"src/m/a": testfs.File("a"), "src/m": testfs.Dir().At(srcTime).RO(),
		"dest/m/b": testfs.File("b"),
	})
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "m")}, DestDir: dest})
	decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
	if res := execPlan(t, context.Background(), plan, ExecOptions{}); res.Status != StatusCompleted {
		t.Fatalf("result = %+v", res)
	}
	m := filepath.Join(dest, "m")
	if modTime(t, m).Equal(srcTime) {
		t.Error("the merged folder got the source's mtime")
	}
	if readOnly(t, m) {
		t.Error("the merged folder became read-only")
	}
	wantFiles(t, dest, map[string]string{"m/a": "a", "m/b": "b"})
}

// TestCopyReadOnlyTempRemoved は、読み取り専用のファイルのコピーで、一時ファイルに読み取り専用を設定した後に
// 最終名にできなかった場合（計画後に現れた衝突）も、一時ファイルが残らないことを確かめる（§10.1 の手順 8、I3）。
// Windows では、読み取り専用属性を外してから削除する経路を通る。
func TestCopyReadOnlyTempRemoved(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/ro.txt": testfs.File("ro").RO(), "dest": testfs.Dir()})
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "ro.txt")}, DestDir: dest})
	h := &testHooks{beforeFinalRename: func(dst string) { testfs.WriteFile(t, dst, "appeared") }}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist {
		t.Errorf("result = %+v, want Skipped with KindExist", it)
	}
	wantFiles(t, dest, map[string]string{"ro.txt": "appeared"})
	noTempFiles(t, root)
}

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

// execDelete は srcs を完全削除する計画を作って実行する。
func execDelete(t *testing.T, ctx context.Context, h *testHooks, srcs ...string) *Result {
	t.Helper()
	plan := mustPlan(t, Request{Op: OpDelete, Sources: srcs})
	res, err := plan.Execute(ctx, ExecOptions{hooks: h})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(res.Items) != len(srcs) {
		t.Fatalf("len(Result.Items) = %d, want %d", len(res.Items), len(srcs))
	}
	return res
}

// markerTree は、削除するツリーの外に、リンクの先となる目印ファイルを置く（§18.4 の I4）。
func markerTree(t *testing.T, root string) {
	t.Helper()
	testfs.Build(t, root, testfs.Tree{
		"outside/marker.txt":   testfs.File("marker"),
		"outside/sub/deep.txt": testfs.File("deep"),
		"outside-file.txt":     testfs.File("target file"),
	})
}

func checkMarkers(t *testing.T, root string) {
	t.Helper()
	for rel, want := range map[string]string{
		"outside/marker.txt":   "marker",
		"outside/sub/deep.txt": "deep",
		"outside-file.txt":     "target file",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if !testfs.Exists(t, p) {
			t.Errorf("I4 violated: %s was deleted through a link", rel)
		} else if got := testfs.ReadFile(t, p); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
}

// TestDeleteKeepsLinkTargets は、シンボリックリンク・ジャンクションを含むツリーを完全削除しても、
// リンクの先の目印ファイルが残ることを確かめる（§18.4 の I4）。
func TestDeleteKeepsLinkTargets(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		link func(root string) testfs.Entry
	}{
		{"absolute dir symlink", func(root string) testfs.Entry { return testfs.DirSymlink(filepath.Join(root, "outside")) }},
		{"relative dir symlink", func(root string) testfs.Entry { return testfs.DirSymlink(filepath.FromSlash("../../outside")) }},
		{"file symlink", func(root string) testfs.Entry { return testfs.Symlink(filepath.Join(root, "outside-file.txt")) }},
		{"junction", func(root string) testfs.Entry { return testfs.Junction(filepath.Join(root, "outside")) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			markerTree(t, root)
			testfs.Build(t, root, testfs.Tree{
				"tree/a.txt":    testfs.File("a"),
				"tree/sub/link": c.link(root),
				"tree/sub/b":    testfs.File("b"),
			})
			res := execDelete(t, context.Background(), nil, filepath.Join(root, "tree"))
			if it := res.Items[0]; it.Outcome != OutcomeDone || it.Err != nil {
				t.Errorf("result = %+v, want Done", it)
			}
			if testfs.Exists(t, filepath.Join(root, "tree")) {
				t.Error("the tree was not deleted")
			}
			checkMarkers(t, root)
		})
	}
}

// TestDeleteTopLevelLinks は、トップレベルのリンクを完全削除すると、リンク自体だけが消えることを確かめる（§14.2）。
func TestDeleteTopLevelLinks(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		link func(root string) testfs.Entry
	}{
		{"dir symlink", func(root string) testfs.Entry { return testfs.DirSymlink(filepath.Join(root, "outside")) }},
		{"file symlink", func(root string) testfs.Entry { return testfs.Symlink(filepath.Join(root, "outside-file.txt")) }},
		{"dangling symlink", func(root string) testfs.Entry { return testfs.DirSymlink(filepath.Join(root, "missing")) }},
		{"junction", func(root string) testfs.Entry { return testfs.Junction(filepath.Join(root, "outside")) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			markerTree(t, root)
			testfs.Build(t, root, testfs.Tree{"link": c.link(root)})
			res := execDelete(t, context.Background(), nil, filepath.Join(root, "link"))
			if it := res.Items[0]; it.Outcome != OutcomeDone {
				t.Errorf("result = %+v, want Done", it)
			}
			if testfs.Exists(t, filepath.Join(root, "link")) {
				t.Error("the link was not deleted")
			}
			checkMarkers(t, root)
		})
	}
}

// replaceWithLink は、フォルダ dir を別の名前へ退避し、dir の位置に root/outside へのリンクを作る（注入用）。
// Windows ではジャンクション、それ以外ではシンボリックリンクにする。
func replaceWithLink(t *testing.T, root, dir string) {
	t.Helper()
	if err := os.Rename(testfs.ExtendedPath(dir), testfs.ExtendedPath(dir+"-moved")); err != nil {
		t.Errorf("inject: %v", err)
		return
	}
	if runtime.GOOS == "windows" {
		testfs.CreateJunction(t, filepath.Join(root, "outside"), dir)
	} else {
		testfs.CreateSymlink(t, filepath.Join(root, "outside"), dir, true)
	}
}

// TestDeleteDirReplacedByLinkBeforeEnter は、完全削除の走査で、フォルダと判定した後・入り込む前に
// そのフォルダをリンクへ置き換えても、リンクの先に入り込まないことを確かめる（§18.4 の I4、§13.1）。
func TestDeleteDirReplacedByLinkBeforeEnter(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"tree/sub", "tree"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			markerTree(t, root)
			testfs.Build(t, root, testfs.Tree{"tree/a.txt": testfs.File("a"), "tree/sub/b.txt": testfs.File("b")})
			victim := filepath.Join(root, filepath.FromSlash(target))
			injected := false
			h := &testHooks{beforeEnterDir: func(p string) {
				if p == victim && !injected {
					injected = true
					replaceWithLink(t, root, victim)
				}
			}}
			res := execDelete(t, context.Background(), h, filepath.Join(root, "tree"))
			if !injected {
				t.Fatal("the hook was not called for " + victim)
			}
			checkMarkers(t, root)
			it := res.Items[0]
			if it.Outcome == OutcomeDone {
				t.Errorf("result = %+v, want a failure (the folder was replaced)", it)
			}
			if !hasKind(it, KindSourceChanged) {
				t.Errorf("result = %+v, want KindSourceChanged in Err or Details", it)
			}
			if !testfs.Exists(t, victim) {
				t.Error("the injected link was deleted; fsops must not touch an entry it could not verify")
			}
		})
	}
}

// TestDeleteDirReplacedByFileBeforeRemove は、削除の直前にフォルダをファイルに置き換えても、そのファイルが消えないことを確かめる（§18.4、§13.2）。
func TestDeleteDirReplacedByFileBeforeRemove(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"tree/a.txt": testfs.File("a"), "tree/sub/b.txt": testfs.File("b")})
	victim := filepath.Join(root, "tree", "sub")
	h := &testHooks{beforeRemove: func(p string) {
		if p == victim {
			if err := os.Remove(testfs.ExtendedPath(victim)); err != nil { // 空になったフォルダ
				t.Errorf("inject: %v", err)
			}
			testfs.WriteFile(t, victim, "new file")
		}
	}}
	res := execDelete(t, context.Background(), h, filepath.Join(root, "tree"))
	if got := testfs.ReadFile(t, victim); got != "new file" {
		t.Errorf("the replacing file = %q", got)
	}
	if it := res.Items[0]; it.Outcome != OutcomePartial || !hasKind(it, KindSourceChanged) {
		t.Errorf("result = %+v, want Partial with KindSourceChanged", it)
	}
}

// hasKind は、項目の Err か Details の Err に kind があるかを返す。
func hasKind(it ItemResult, kind Kind) bool {
	if it.Err != nil && it.Err.Kind == kind {
		return true
	}
	return slices.ContainsFunc(it.Details, func(e EntryResult) bool { return e.Err != nil && e.Err.Kind == kind })
}

// TestDeleteTree は、ツリーの完全削除（§13.2）を確かめる。長いパス、Win32 の正規化で変わる名前、日本語の名前を含む。
func TestDeleteTree(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"tree/a.txt":                           testfs.File("a"),
		"tree/empty":                           testfs.Dir(),
		"tree/" + testfs.NameJapanese:          testfs.File("ja"),
		"tree/" + testfs.NameTrailingDot:       testfs.File("dot"),
		"tree/" + testfs.NameReservedCON:       testfs.File("con"),
		"tree/sub/" + testfs.NameTrailingSpace: testfs.Dir(),
		testfs.NamePlain:                       testfs.File("plain outside the tree"),
		"single.txt":                           testfs.File("s"),
	})
	long := testfs.LongPath(t, filepath.Join(root, "tree", "long"))
	testfs.WriteFile(t, filepath.Join(long, "f"), "f")
	res := execDelete(t, context.Background(), nil, filepath.Join(root, "tree"), filepath.Join(root, "single.txt"))
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone || it.Err != nil || len(it.Details) != 0 {
			t.Errorf("%s: %+v, want Done", it.Src, it)
		}
	}
	if res.Status != StatusCompleted {
		t.Errorf("Status = %v", res.Status)
	}
	if got := testfs.ListNames(t, root); !slices.Equal(got, []string{testfs.NamePlain}) {
		t.Errorf("names left = %+q, want [foo]", got)
	}
	if testfs.ReadFile(t, filepath.Join(root, testfs.NamePlain)) != "plain outside the tree" {
		t.Error("foo changed")
	}
}

// TestDeleteSpecial は、FIFO などの特殊なファイルをエントリ自体だけ削除することを確かめる（Unix）。
func TestDeleteSpecial(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"tree/fifo": testfs.FIFO(), "fifo": testfs.FIFO()})
	res := execDelete(t, context.Background(), nil, filepath.Join(root, "tree"), filepath.Join(root, "fifo"))
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone {
			t.Errorf("%s: %+v", it.Src, it)
		}
	}
	if len(testfs.ListNames(t, root)) != 0 {
		t.Errorf("left: %+q", testfs.ListNames(t, root))
	}
}

// TestDeleteReadOnly は、読み取り専用のエントリを含むツリーの完全削除を確かめる（§13.2、§18.4「読み取り専用」）。
// Windows: 読み取り専用のファイルは属性を外さず KindReadOnly で残り、読み取り専用のフォルダは属性を外して削除する。
// macOS: ロック（UF_IMMUTABLE）のファイルは KindReadOnly で残る。Unix: 書き込み権限のないフォルダの中身は KindPermission で残る。
func TestDeleteReadOnly(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	switch runtime.GOOS {
	case "windows":
		testfs.Build(t, root, testfs.Tree{
			"tree/ro.txt":  testfs.File("ro").RO(),
			"tree/rw.txt":  testfs.File("rw"),
			"tree/rodir/x": testfs.File("x"),
			"tree/rodir":   testfs.Dir().RO(),
			"tree/emptyro": testfs.Dir().RO(),
		})
		res := execDelete(t, context.Background(), nil, filepath.Join(root, "tree"))
		it := res.Items[0]
		if it.Outcome != OutcomePartial || it.Err == nil || it.Err.Kind != KindReadOnly {
			t.Errorf("result = %+v, want Partial with KindReadOnly", it)
		}
		if got := testfs.ListNames(t, filepath.Join(root, "tree")); !slices.Equal(got, []string{"ro.txt"}) {
			t.Errorf("left in tree = %+q, want [ro.txt]", got)
		}
		fi, err := os.Lstat(testfs.ExtendedPath(filepath.Join(root, "tree", "ro.txt")))
		if err != nil || fi.Mode().Perm()&0o200 != 0 {
			t.Errorf("the read-only attribute of ro.txt was removed (%v, %v)", fi, err)
		}
	case "darwin":
		testfs.Build(t, root, testfs.Tree{"tree/locked": testfs.File("l"), "tree/other": testfs.File("o")})
		testfs.SetImmutable(t, filepath.Join(root, "tree", "locked"))
		res := execDelete(t, context.Background(), nil, filepath.Join(root, "tree"))
		if it := res.Items[0]; it.Outcome != OutcomePartial || it.Err == nil || it.Err.Kind != KindReadOnly {
			t.Errorf("result = %+v, want Partial with KindReadOnly", it)
		}
		if got := testfs.ListNames(t, filepath.Join(root, "tree")); !slices.Equal(got, []string{"locked"}) {
			t.Errorf("left = %+q", got)
		}
	default:
		t.Skip("covered on Windows and macOS")
	}
}

// TestDeleteLocked は、使用中のファイルを含むツリーの削除で、その項目が KindLocked になり、ほかは削除されることを確かめる（Windows）。
func TestDeleteLocked(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"tree/locked.txt": testfs.File("l"), "tree/a.txt": testfs.File("a"), "tree/sub/b": testfs.File("b"), "other": testfs.File("o")})
	testfs.Lock(t, filepath.Join(root, "tree", "locked.txt"))
	res := execDelete(t, context.Background(), nil, filepath.Join(root, "tree"), filepath.Join(root, "other"))
	if it := res.Items[0]; it.Outcome != OutcomePartial || !hasKind(it, KindLocked) {
		t.Errorf("result = %+v, want Partial with KindLocked", it)
	}
	if it := res.Items[1]; it.Outcome != OutcomeDone {
		t.Errorf("the next item = %+v, want Done (processing continues)", it)
	}
	if got := testfs.ListNames(t, filepath.Join(root, "tree")); !slices.Equal(got, []string{"locked.txt"}) {
		t.Errorf("left = %+q", got)
	}
	if res.Status != StatusCompletedWithErrors {
		t.Errorf("Status = %v", res.Status)
	}
}

// TestDeleteCancel は、削除中のキャンセルで、そこで止まり、処理中の項目が Partial、残りが Skipped（KindCanceled）になることを確かめる（§16）。
func TestDeleteCancel(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	tree := testfs.Tree{"next.txt": testfs.File("n")}
	for i := 0; i < 20; i++ {
		tree["tree/f"+string(rune('a'+i))] = testfs.File("x")
	}
	testfs.Build(t, root, tree)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	removed := 0
	h := &testHooks{beforeRemove: func(p string) {
		removed++
		if removed == 5 {
			cancel()
		}
	}}
	res := execDelete(t, ctx, h, filepath.Join(root, "tree"), filepath.Join(root, "next.txt"))
	if res.Status != StatusCanceled {
		t.Errorf("Status = %v, want StatusCanceled", res.Status)
	}
	if it := res.Items[0]; it.Outcome != OutcomePartial || it.Err == nil || it.Err.Kind != KindCanceled {
		t.Errorf("item in progress = %+v, want Partial with KindCanceled", it)
	}
	if it := res.Items[1]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindCanceled {
		t.Errorf("remaining item = %+v, want Skipped with KindCanceled", it)
	}
	left := testfs.ListNames(t, filepath.Join(root, "tree"))
	if len(left) == 0 || len(left) == 20 {
		t.Errorf("left %d of 20 files; want a partial deletion", len(left))
	}
	if !testfs.Exists(t, filepath.Join(root, "next.txt")) {
		t.Error("the remaining item was deleted after cancel")
	}
}

// TestDeleteSourceGone は、計画後に消えた項目が Failed（KindNotFound）になることを確かめる（§7.3）。
func TestDeleteSourceGone(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"a": testfs.File("a")})
	plan := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "a")}})
	if err := os.Remove(testfs.ExtendedPath(filepath.Join(root, "a"))); err != nil {
		t.Fatal(err)
	}
	res, err := plan.Execute(context.Background(), ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindNotFound {
		t.Errorf("result = %+v, want Failed with KindNotFound", it)
	}
}

// TestDeleteVanishedEntries は、列挙の後に消えたエントリを失敗にしないことを確かめる（消すものがない。§6.3）。
func TestDeleteVanishedEntries(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"tree/a.txt": testfs.File("a"), "tree/b.txt": testfs.File("b"), "tree/sub/c.txt": testfs.File("c")})
	h := &testHooks{
		beforeRemove: func(p string) {
			if filepath.Base(p) == "a.txt" {
				os.Remove(testfs.ExtendedPath(p)) // ほかのプロセスが先に消した
			}
		},
		beforeEnterDir: func(p string) {
			if filepath.Base(p) == "sub" {
				os.RemoveAll(testfs.ExtendedPath(p))
			}
		},
	}
	res := execDelete(t, context.Background(), h, filepath.Join(root, "tree"))
	if it := res.Items[0]; it.Outcome != OutcomeDone || it.Err != nil || len(it.Details) != 0 {
		t.Errorf("result = %+v, want Done", it)
	}
	if testfs.Exists(t, filepath.Join(root, "tree")) {
		t.Error("the tree was not deleted")
	}
}

package fsops

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// readlink は、リンク path のリンク先の文字列を返す。
func readlink(t *testing.T, path string) string {
	t.Helper()
	s, err := os.Readlink(testfs.ExtendedPath(path))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// isLinkEntry は、path がリンク（シンボリックリンク・ジャンクション）そのものかを返す。
func isLinkEntry(t *testing.T, path string) bool {
	t.Helper()
	info, err := lstatEntry(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Type == TypeSymlink || info.Type == TypeJunction
}

// linkTree は、リンクを含むツリーを src に作る。リンクの先は markerTree の outside（root の中）。
func linkTree(t *testing.T, root string) {
	t.Helper()
	markerTree(t, root)
	tree := testfs.Tree{
		"src/tree/a.txt":      testfs.File("a"),
		"src/tree/dirlink":    testfs.DirSymlink(filepath.Join(root, "outside")),
		"src/tree/filelink":   testfs.Symlink(filepath.Join(root, "outside-file.txt")),
		"src/tree/rel":        testfs.Symlink(filepath.Join("..", "..", "outside-file.txt")),
		"src/tree/dangling":   testfs.Symlink("no-such-target"),
		"src/tree/sub/b.txt":  testfs.File("b"),
		"src/tree/sub/sublnk": testfs.DirSymlink(filepath.Join("..", "..", "..", "outside")),
		"dest":                testfs.Dir(),
	}
	if runtime.GOOS == "windows" {
		tree["src/tree/junction"] = testfs.Junction(filepath.Join(root, "outside"))
	} else {
		tree["src/tree/fifo"] = testfs.FIFO()
	}
	testfs.Build(t, root, tree)
}

// TestCopyTreeWithLinks は、リンクを含むツリーのコピーで、リンクの先の中身を複製せず（I4）、
// シンボリックリンクはリンク先の文字列をそのまま使って作り、ジャンクション・特殊なファイルは複製しないことを確かめる（§14.2、§18.4 の I4・メタデータ）。
// リンク先の更新日時・権限も変わらない。
func TestCopyTreeWithLinks(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	linkTree(t, root)
	outside := testfs.Take(t, filepath.Join(root, "outside"))
	outsideFile := testfs.Take(t, filepath.Join(root, "outside-file.txt"))
	src, dest := filepath.Join(root, "src", "tree"), filepath.Join(root, "dest", "tree")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{src}, DestDir: filepath.Join(root, "dest")})
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	it := res.Items[0]

	// ジャンクション（Windows）と FIFO（Unix）だけが Skipped になる。
	want := map[string]Kind{"junction": KindLinkUnsupported, "fifo": KindUnsupportedType}
	if it.Outcome != OutcomePartial || len(it.Details) != 1 {
		t.Fatalf("result = %+v, want Partial with one skipped entry", it)
	}
	d := it.Details[0]
	if k, ok := want[filepath.Base(d.Src)]; !ok || d.Outcome != OutcomeSkipped || d.Err == nil || d.Err.Kind != k {
		t.Errorf("detail = %+v, want Skipped with %v", d, k)
	}
	for _, name := range []string{"junction", "fifo"} {
		if testfs.Exists(t, filepath.Join(dest, name)) {
			t.Errorf("%s was copied", name)
		}
	}
	for _, rel := range []string{"dirlink", "filelink", "rel", "dangling", "sub/sublnk"} {
		s, d := filepath.Join(src, filepath.FromSlash(rel)), filepath.Join(dest, filepath.FromSlash(rel))
		if !isLinkEntry(t, d) {
			t.Errorf("%s is not a link", rel)
			continue
		}
		if got, want := readlink(t, d), readlink(t, s); got != want {
			t.Errorf("%s: link target %q, want %q", rel, got, want)
		}
	}
	wantFiles(t, dest, map[string]string{"a.txt": "a", "sub/b.txt": "b"})
	checkMarkers(t, root)
	if diff := testfs.Diff(outside, testfs.Take(t, filepath.Join(root, "outside"))); diff != nil {
		t.Errorf("the link target changed: %q", diff)
	}
	if diff := testfs.Diff(outsideFile, testfs.Take(t, filepath.Join(root, "outside-file.txt"))); diff != nil {
		t.Errorf("the file link target changed: %q", diff)
	}
	noTempFiles(t, root)
}

// TestCopyLinkSkip は、LinkSkip ではシンボリックリンクを複製せず Skipped（KindLinkUnsupported）にすることを確かめる（§14.2）。
func TestCopyLinkSkip(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	markerTree(t, root)
	testfs.Build(t, root, testfs.Tree{
		"src/tree/a.txt": testfs.File("a"), "src/tree/link": testfs.Symlink(filepath.Join(root, "outside-file.txt")),
		"src/top": testfs.Symlink(filepath.Join(root, "outside-file.txt")), "dest": testfs.Dir(),
	})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "tree"), filepath.Join(root, "src", "top")}, DestDir: filepath.Join(root, "dest")})
	res := execPlan(t, context.Background(), plan, ExecOptions{Links: LinkSkip})
	if it := res.Items[0]; it.Outcome != OutcomePartial || len(it.Details) != 1 || it.Details[0].Err == nil || it.Details[0].Err.Kind != KindLinkUnsupported {
		t.Errorf("tree = %+v, want Partial with the link skipped", it)
	}
	if it := res.Items[1]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindLinkUnsupported {
		t.Errorf("top = %+v, want Skipped with KindLinkUnsupported", it)
	}
	for _, rel := range []string{"tree/link", "top"} {
		if testfs.Exists(t, filepath.Join(root, "dest", filepath.FromSlash(rel))) {
			t.Errorf("%s was copied", rel)
		}
	}
	wantFiles(t, filepath.Join(root, "dest"), map[string]string{"tree/a.txt": "a"})
}

// TestCopyTopLevelLinks は、トップレベルの項目のリンク・特殊なファイルの扱いと、シンボリックリンクの自動リネームを確かめる（§14.2、§9.2）。
func TestCopyTopLevelLinks(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	markerTree(t, root)
	tree := testfs.Tree{
		"src/link": testfs.Symlink("target-string"), "src/ren": testfs.Symlink("other"), "dest/ren": testfs.File("existing"),
	}
	srcs := []string{filepath.Join(root, "src", "link"), filepath.Join(root, "src", "ren")}
	special := KindUnsupportedType
	if runtime.GOOS == "windows" {
		tree["src/special"] = testfs.Junction(filepath.Join(root, "outside"))
		special = KindLinkUnsupported
	} else {
		tree["src/special"] = testfs.FIFO()
	}
	srcs = append(srcs, filepath.Join(root, "src", "special"))
	testfs.Build(t, root, tree)
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: srcs, DestDir: dest})
	decide(t, plan, filepath.Join(dest, "ren"), DecisionAutoRename)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeDone || readlink(t, filepath.Join(dest, "link")) != "target-string" {
		t.Errorf("link = %+v", it)
	}
	if it := res.Items[1]; it.Outcome != OutcomeDone || it.Dst != filepath.Join(dest, "ren (2)") || readlink(t, it.Dst) != "other" {
		t.Errorf("ren = %+v, want Done at ren (2)", it)
	}
	if it := res.Items[2]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != special {
		t.Errorf("special = %+v, want Skipped with %v", it, special)
	}
	wantFiles(t, dest, map[string]string{"ren": "existing"})
	if testfs.Exists(t, filepath.Join(dest, "special")) {
		t.Error("the special entry was copied")
	}
}

// TestCopySymlinkConflictAfterPlan は、シンボリックリンクを作る直前に同名のファイルが現れても上書きしないことを確かめる（I1）。
func TestCopySymlinkConflictAfterPlan(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/link": testfs.Symlink("t"), "src/tree/inner": testfs.Symlink("u"), "dest": testfs.Dir()})
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "link"), filepath.Join(root, "src", "tree")}, DestDir: dest})
	h := &testHooks{beforeFinalRename: func(dst string) {
		if filepath.Base(dst) == "link" || filepath.Base(dst) == "inner" {
			testfs.WriteFile(t, dst, "appeared")
		}
	}}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist {
		t.Errorf("link = %+v, want Skipped with KindExist", it)
	}
	if it := res.Items[1]; it.Outcome != OutcomePartial || !hasKind(it, KindExist) {
		t.Errorf("tree = %+v, want Partial with KindExist", it)
	}
	wantFiles(t, dest, map[string]string{"link": "appeared", "tree/inner": "appeared"})
}

// TestCopySymlinkCreateFails は、シンボリックリンクの作成が失敗しても（フックで注入）、その項目だけが失敗し、ほかは続くことを確かめる（§18.4「リンク」、V8）。
// Windows では ERROR_PRIVILEGE_NOT_HELD を注入して KindLinkUnsupported になることを確かめる。
func TestCopySymlinkCreateFails(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/tree/a.txt": testfs.File("a"), "src/tree/link": testfs.Symlink("x"), "src/top": testfs.Symlink("y"), "src/next.txt": testfs.File("n"), "dest": testfs.Dir()})
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "tree"), filepath.Join(root, "src", "top"), filepath.Join(root, "src", "next.txt")}, DestDir: dest})
	injected, wantKind := symlinkPrivilegeErr()
	h := &testHooks{beforeSymlink: func(string) error { return injected }}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomePartial || len(it.Details) != 1 || it.Details[0].Outcome != OutcomeFailed || it.Details[0].Err.Kind != wantKind {
		t.Errorf("tree = %+v, want Partial with the link failed by %v", it, wantKind)
	}
	if it := res.Items[1]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != wantKind {
		t.Errorf("top = %+v, want Failed with %v", it, wantKind)
	}
	if it := res.Items[2]; it.Outcome != OutcomeDone {
		t.Errorf("next.txt = %+v, want Done", it)
	}
	wantFiles(t, dest, map[string]string{"tree/a.txt": "a", "next.txt": "n"})
}

// TestCopySymlinkToFATVolumes は、exFAT・FAT32 へのシンボリックリンクのコピーを確かめる（V20）。
// Windows では作れず（ERROR_INVALID_FUNCTION）KindLinkUnsupported で失敗し、ほかは続く。macOS では作れる。
func TestCopySymlinkToFATVolumes(t *testing.T) {
	t.Parallel()
	for _, env := range []string{testfs.ExFATEnv, testfs.FAT32Env} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			dest := testfs.EnvDir(t, env)
			src := testfs.TempDir(t)
			testfs.Build(t, src, testfs.Tree{"tree/a.txt": testfs.File("a"), "tree/link": testfs.Symlink("a.txt")})
			res := execPlan(t, context.Background(), mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(src, "tree")}, DestDir: dest}), ExecOptions{})
			it := res.Items[0]
			t.Logf("result: %+v", it)
			switch runtime.GOOS {
			case "windows":
				if it.Outcome != OutcomePartial || len(it.Details) != 1 || it.Details[0].Err == nil || it.Details[0].Err.Kind != KindLinkUnsupported {
					t.Errorf("result = %+v, want Partial with the link failed by KindLinkUnsupported", it)
				}
			case "darwin":
				if it.Outcome != OutcomeDone || readlink(t, filepath.Join(dest, "tree", "link")) != "a.txt" {
					t.Errorf("result = %+v, want Done", it)
				}
			default:
				t.Skip("recorded on Windows and macOS (V20)")
			}
			wantFiles(t, dest, map[string]string{"tree/a.txt": "a"})
		})
	}
}

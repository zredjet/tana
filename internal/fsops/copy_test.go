package fsops

import (
	"context"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// wantFiles は、root からの相対パスとファイルの内容の対応がすべて成り立つことを確かめる。
func wantFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, want := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if !testfs.Exists(t, p) {
			t.Errorf("%s does not exist", rel)
		} else if got := testfs.ReadFile(t, p); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
}

// TestCopyTree は、ファイルとフォルダのコピー（§10.1、§10.2）で、中身と名前がそのまま複製されることを確かめる（I6 を含む）。
func TestCopyTree(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/top.txt":                         testfs.File("top"),
		"src/tree/a.txt":                      testfs.File("a"),
		"src/tree/empty":                      testfs.Dir(),
		"src/tree/sub/b.txt":                  testfs.File(strings.Repeat("b", copyBufSize+7)), // バッファ 2 つ分
		"src/tree/sub/zero":                   testfs.File(""),
		"src/tree/" + testfs.NameJapanese:     testfs.File("ja"),
		"src/tree/" + testfs.NameEmoji:        testfs.File("emoji"),
		"src/tree/" + testfs.NameNFD:          testfs.File("nfd"),
		"src/tree/日本語のフォルダ/" + testfs.NameNFC: testfs.File("nfc"),
		"dest": testfs.Dir(),
	})
	src, dest := filepath.Join(root, "src"), filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(src, "top.txt"), filepath.Join(src, "tree")}, DestDir: dest})
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if res.Status != StatusCompleted {
		t.Fatalf("result = %+v", res)
	}
	for i, it := range res.Items {
		if it.Outcome != OutcomeDone || it.Err != nil || len(it.Details) != 0 || it.Dst != plan.Items()[i].Dst {
			t.Errorf("%s: %+v, want Done at the planned Dst", it.Src, it)
		}
	}
	// 名前をバイト単位で比べる（NFD の名前が NFC にならないこと。I6）。
	for _, dir := range []string{"tree", "tree/sub", "tree/日本語のフォルダ"} {
		s, d := testfs.ListNames(t, filepath.Join(src, dir)), testfs.ListNames(t, filepath.Join(dest, dir))
		if !slices.Equal(s, d) {
			t.Errorf("%s: names %+q, want %+q", dir, d, s)
		}
	}
	a, b := testfs.Take(t, filepath.Join(src, "tree")), testfs.Take(t, filepath.Join(dest, "tree"))
	for rel, n := range a {
		m, ok := b[rel]
		if !ok || m.Type != n.Type || m.SHA256 != n.SHA256 || m.Size != n.Size {
			t.Errorf("%s: %+v, want %+v", rel, m, n)
		}
	}
	if len(a) != len(b) {
		t.Errorf("dest has %d entries, want %d", len(b), len(a))
	}
	wantFiles(t, dest, map[string]string{"top.txt": "top"})
	noTempFiles(t, root)
}

// TestAutoRenameName は、自動リネームの候補の名前（§9.2）を確かめる。
func TestAutoRenameName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		isDir bool
		n     int
		want  string
	}{
		{"a.txt", false, 2, "a (2).txt"},
		{"a.txt", false, 13, "a (13).txt"},
		{"archive.tar.gz", false, 2, "archive.tar (2).gz"},
		{".gitignore", false, 2, ".gitignore (2)"},
		{"README", false, 2, "README (2)"},
		{"a (2).txt", false, 2, "a (2) (2).txt"},
		{"dir.d", true, 2, "dir.d (2)"},
		{"dir", true, 3, "dir (3)"},
		{testfs.NameNFD, false, 2, "café (2).txt"},
	} {
		if got := autoRenameName(tc.name, tc.isDir, tc.n); got != tc.want {
			t.Errorf("autoRenameName(%q, %v, %d) = %q, want %q", tc.name, tc.isDir, tc.n, got, tc.want)
		}
	}
}

// TestCopyAutoRename は、自動リネームの連番（§9.2、§18.4「衝突」）を確かめる。既存のものは変わらない。
func TestCopyAutoRename(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/a.txt": testfs.File("new a"), "dest/a.txt": testfs.File("old a"),
		"src/b.txt": testfs.File("new b"), "dest/b.txt": testfs.File("old b"), "dest/b (2).txt": testfs.File("old b2"),
		"src/.gitignore": testfs.File("new g"), "dest/.gitignore": testfs.File("old g"),
		"src/README": testfs.File("new r"), "dest/README": testfs.File("old r"),
		"src/dir.d/x": testfs.File("new x"), "dest/dir.d/x": testfs.File("old x"),
		"src/c (2).txt": testfs.File("new c"), "dest/c (2).txt": testfs.File("old c"),
		"src/kind": testfs.File("file"), "dest/kind/y": testfs.File("dir"), // 種類が違う衝突
	})
	names := []string{"a.txt", "b.txt", ".gitignore", "README", "dir.d", "c (2).txt", "kind"}
	var srcs []string
	for _, n := range names {
		srcs = append(srcs, filepath.Join(root, "src", n))
	}
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: srcs, DestDir: dest})
	for _, c := range plan.Conflicts() {
		if c.Parent == 0 {
			if err := plan.Decide(c.ID, DecisionAutoRename); err != nil {
				t.Fatal(err)
			}
		}
	}
	before := entriesIn(t, dest)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	wantDst := []string{"a (2).txt", "b (3).txt", ".gitignore (2)", "README (2)", "dir.d (2)", "c (2) (2).txt", "kind (2)"}
	for i, it := range res.Items {
		if it.Outcome != OutcomeDone || it.Err != nil || it.Dst != filepath.Join(dest, wantDst[i]) {
			t.Errorf("%s: %+v, want Done at %s", it.Src, it, wantDst[i])
		}
	}
	wantFiles(t, dest, map[string]string{
		"a (2).txt": "new a", "b (3).txt": "new b", ".gitignore (2)": "new g", "README (2)": "new r",
		"dir.d (2)/x": "new x", "c (2) (2).txt": "new c", "kind (2)": "file",
	})
	after := entriesIn(t, dest)
	for rel, n := range before {
		if after[rel] != n && !strings.HasPrefix(rel, "dir.d") && !strings.HasPrefix(rel, "kind") { // フォルダ自身は更新日時が変わりうる
			t.Errorf("I1 violated: %s changed", rel)
		}
	}
	wantFiles(t, dest, map[string]string{"a.txt": "old a", "b.txt": "old b", "b (2).txt": "old b2", "dir.d/x": "old x", "kind/y": "dir"})
	noTempFiles(t, root)
}

// TestCopySelf は、同じフォルダへのコピー（Self）を自動リネームで複製できることを確かめる（§18.4「衝突」）。
func TestCopySelf(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"f.txt": testfs.File("f"), "dir/sub/x": testfs.File("x")})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "f.txt"), filepath.Join(root, "dir")}, DestDir: root})
	cs := plan.Conflicts()
	if len(cs) != 2 || !cs[0].Self || !cs[1].Self {
		t.Fatalf("conflicts = %+v, want 2 Self conflicts", cs)
	}
	for _, c := range cs {
		if err := plan.Decide(c.ID, DecisionAutoRename); err != nil {
			t.Fatal(err)
		}
	}
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if res.Status != StatusCompleted || res.Items[0].Dst != filepath.Join(root, "f (2).txt") || res.Items[1].Dst != filepath.Join(root, "dir (2)") {
		t.Fatalf("result = %+v", res)
	}
	wantFiles(t, root, map[string]string{"f.txt": "f", "f (2).txt": "f", "dir/sub/x": "x", "dir (2)/sub/x": "x"})
	if got := testfs.ListNames(t, filepath.Join(root, "dir")); !slices.Equal(got, []string{"sub"}) {
		t.Errorf("the source folder changed: %+q", got)
	}
}

// TestCopyMerge は、マージで内側の衝突の決定がそれぞれ反映されることを確かめる（§9.4、§18.4「衝突」）。
func TestCopyMerge(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/m/new.txt":  testfs.File("new"),
		"src/m/over.txt": testfs.File("new over"), "dest/m/over.txt": testfs.File("old over"),
		"src/m/skip.txt": testfs.File("new skip"), "dest/m/skip.txt": testfs.File("old skip"),
		"src/m/unset.txt": testfs.File("new unset"), "dest/m/unset.txt": testfs.File("old unset"),
		"src/m/ren.txt": testfs.File("new ren"), "dest/m/ren.txt": testfs.File("old ren"),
		"src/m/sub/inner.txt": testfs.File("new inner"), "dest/m/sub/inner.txt": testfs.File("old inner"),
		"src/m/sub/deep/d.txt": testfs.File("d"),
		"src/m/skipdir/s.txt":  testfs.File("s"), "dest/m/skipdir/t.txt": testfs.File("t"),
		"dest/m/keep.txt": testfs.File("keep"),
	})
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "m")}, DestDir: dest})
	for rel, d := range map[string]Decision{
		"m": DecisionMerge, "m/over.txt": DecisionOverwrite, "m/skip.txt": DecisionSkip, "m/ren.txt": DecisionAutoRename,
		"m/sub": DecisionMerge, "m/sub/inner.txt": DecisionOverwrite, "m/skipdir": DecisionSkip,
	} {
		decide(t, plan, filepath.Join(dest, filepath.FromSlash(rel)), d)
	}
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	it := res.Items[0]
	if it.Outcome != OutcomeDone || it.Err != nil || res.Status != StatusCompleted {
		t.Errorf("result = %+v, want Done (skips by decision are not errors)", it)
	}
	var skipped []string
	for _, d := range it.Details {
		if d.Outcome != OutcomeSkipped || d.Err != nil {
			t.Errorf("detail %+v, want Skipped with nil Err", d)
		}
		skipped = append(skipped, filepath.Base(d.Src))
	}
	if !slices.Equal(skipped, []string{"skip.txt", "skipdir", "unset.txt"}) {
		t.Errorf("skipped = %+q", skipped)
	}
	wantFiles(t, dest, map[string]string{
		"m/new.txt": "new", "m/over.txt": "new over", "m/skip.txt": "old skip", "m/unset.txt": "old unset",
		"m/ren.txt": "old ren", "m/ren (2).txt": "new ren", "m/sub/inner.txt": "new inner", "m/sub/deep/d.txt": "d",
		"m/skipdir/t.txt": "t", "m/keep.txt": "keep",
	})
	if testfs.Exists(t, filepath.Join(dest, "m", "skipdir", "s.txt")) {
		t.Error("a skipped folder was merged")
	}
	noTempFiles(t, root)
}

// TestCopyTargetGone は、上書き先・マージ先が計画後に消えていたら、衝突なしとして書くことを確かめる（§7.3）。
func TestCopyTargetGone(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/f.txt": testfs.File("new"), "dest/f.txt": testfs.File("old"),
		"src/d/x": testfs.File("x"), "dest/d/y": testfs.File("y"),
	})
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt"), filepath.Join(root, "src", "d")}, DestDir: dest})
	decide(t, plan, filepath.Join(dest, "f.txt"), DecisionOverwrite)
	decide(t, plan, filepath.Join(dest, "d"), DecisionMerge)
	del := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(dest, "f.txt"), filepath.Join(dest, "d")}})
	if res := execPlan(t, context.Background(), del, ExecOptions{}); res.Status != StatusCompleted {
		t.Fatalf("delete: %+v", res)
	}
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if res.Status != StatusCompleted {
		t.Errorf("result = %+v", res)
	}
	wantFiles(t, dest, map[string]string{"f.txt": "new", "d/x": "x"})
}

// TestCopyOverwriteReadOnly は、読み取り専用の上書き先が、両 OS で KindReadOnly になり、元のまま残ることを確かめる（§9.3、§18.4「読み取り専用」）。
func TestCopyOverwriteReadOnly(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/ro.txt": testfs.File("new"), "dest/ro.txt": testfs.File("old"), "src/ok.txt": testfs.File("new ok"), "dest/ok.txt": testfs.File("old ok")})
	dest := filepath.Join(root, "dest")
	testfs.SetReadOnly(t, filepath.Join(dest, "ro.txt"))
	srcs := []string{filepath.Join(root, "src", "ro.txt"), filepath.Join(root, "src", "ok.txt")}
	if runtime.GOOS == "darwin" {
		testfs.Build(t, root, testfs.Tree{"src/locked.txt": testfs.File("new"), "dest/locked.txt": testfs.File("old")})
		testfs.SetImmutable(t, filepath.Join(dest, "locked.txt"))
		srcs = append(srcs, filepath.Join(root, "src", "locked.txt"))
	}
	plan := mustPlan(t, Request{Op: OpCopy, Sources: srcs, DestDir: dest})
	for _, c := range plan.Conflicts() {
		decide(t, plan, c.Dst, DecisionOverwrite)
	}
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	for i, it := range res.Items {
		if i == 1 {
			if it.Outcome != OutcomeDone {
				t.Errorf("ok.txt: %+v, want Done", it)
			}
			continue
		}
		if it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindReadOnly {
			t.Errorf("%s: %+v, want Failed with KindReadOnly", it.Src, it)
		}
	}
	files := map[string]string{"ro.txt": "old", "ok.txt": "new ok"}
	if runtime.GOOS == "darwin" {
		files["locked.txt"] = "old"
	}
	wantFiles(t, dest, files)
	noTempFiles(t, root)
}

// TestCopyLockedSource は、共有なしで開かれているコピー元が KindLocked になり、ほかの項目は続くことを確かめる（§18.4「ロック」）。
func TestCopyLockedSource(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/locked.txt": testfs.File("l"), "src/tree/in.txt": testfs.File("in"), "src/tree/free.txt": testfs.File("f"), "src/next.txt": testfs.File("n"), "dest": testfs.Dir()})
	testfs.Lock(t, filepath.Join(root, "src", "locked.txt"))
	testfs.Lock(t, filepath.Join(root, "src", "tree", "in.txt"))
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "locked.txt"), filepath.Join(root, "src", "tree"), filepath.Join(root, "src", "next.txt")}, DestDir: dest})
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindLocked {
		t.Errorf("locked.txt: %+v, want Failed with KindLocked", it)
	}
	if it := res.Items[1]; it.Outcome != OutcomePartial || it.Err == nil || it.Err.Kind != KindLocked || len(it.Details) != 1 {
		t.Errorf("tree: %+v, want Partial with KindLocked for in.txt", it)
	}
	if it := res.Items[2]; it.Outcome != OutcomeDone {
		t.Errorf("next.txt: %+v, want Done", it)
	}
	wantFiles(t, dest, map[string]string{"tree/free.txt": "f", "next.txt": "n"})
	if testfs.Exists(t, filepath.Join(dest, "locked.txt")) || testfs.Exists(t, filepath.Join(dest, "tree", "in.txt")) {
		t.Error("a locked source was copied")
	}
	noTempFiles(t, root)
}

// TestCopyOverwriteLocked は、使用中の上書き先が KindLocked になり、元のまま残り、一時ファイルも残らないことを確かめる（§9.3、§18.4「ロック」）。
func TestCopyOverwriteLocked(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/t.txt": testfs.File("new"), "dest/t.txt": testfs.File("old")})
	dst := filepath.Join(root, "dest", "t.txt")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "t.txt")}, DestDir: filepath.Join(root, "dest")})
	decide(t, plan, dst, DecisionOverwrite)
	var res *Result
	t.Run("locked", func(t *testing.T) { // サブテストの終了時にロックを外し、その後で上書き先の内容を確かめる
		testfs.Lock(t, dst)
		res = execPlan(t, context.Background(), plan, ExecOptions{})
	})
	if res == nil {
		return // Lock が Skip した（Windows 以外）
	}
	it := res.Items[0]
	t.Logf("result: %+v, err: %v", it, it.Err) // 置換リネームが返したエラー番号を CI のログに残す
	if it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindLocked {
		t.Errorf("result = %+v, want Failed with KindLocked", it)
	}
	noTempFiles(t, root)
	if got := testfs.ReadFile(t, dst); got != "old" {
		t.Errorf("the locked target changed: %q", got)
	}
}

// TestCopyAutoRenameTooLong は、自動リネームの候補の名前が長すぎれば、切り詰めずに KindInvalidName で失敗することを確かめる（§9.2、I6）。
func TestCopyAutoRenameTooLong(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	name := strings.Repeat("n", 251) + ".txt" // 255 文字（候補は 259 文字になる）
	testfs.Build(t, root, testfs.Tree{"src/" + name: testfs.File("new"), "dest/" + name: testfs.File("old")})
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", name)}, DestDir: dest})
	decide(t, plan, filepath.Join(dest, name), DecisionAutoRename)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindInvalidName {
		t.Errorf("result = %+v (%v), want Failed with KindInvalidName", it, it.Err)
	}
	if got := testfs.ListNames(t, dest); !slices.Equal(got, []string{name}) {
		t.Errorf("dest = %+q", got)
	}
	noTempFiles(t, root)
}

// TestCopySyncAlways は、SyncAlways（§10.5）でコピーでき、同期の警告が出ないことを確かめる。
func TestCopySyncAlways(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/tree/a": testfs.File("a"), "src/tree/sub/b": testfs.File("b"), "src/f": testfs.File("f"), "dest": testfs.Dir()})
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "tree"), filepath.Join(root, "src", "f")}, DestDir: dest})
	res := execPlan(t, context.Background(), plan, ExecOptions{Sync: SyncAlways})
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone || len(it.Warnings) != 0 {
			t.Errorf("%s: %+v, want Done without warnings", it.Src, it)
		}
	}
	wantFiles(t, dest, map[string]string{"tree/a": "a", "tree/sub/b": "b", "f": "f"})
}

// TestCopyCancelInFolder は、フォルダのコピーの途中でキャンセルすると Partial（KindCanceled）になり、
// 最終名のファイルはすべて完全で、一時ファイルが残らないことを確かめる（I3、§16）。
func TestCopyCancelInFolder(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	tree := testfs.Tree{"dest": testfs.Dir()}
	for i := range 6 {
		tree["src/tree/f"+string(rune('a'+i))] = testfs.File(strings.Repeat(string(rune('a'+i)), copyBufSize+1))
	}
	testfs.Build(t, root, tree)
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "tree")}, DestDir: dest})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &testHooks{onWrite: func(dst string, written int64) error {
		if filepath.Base(dst) == "fc" {
			cancel() // fc の最初のバッファの後
		}
		return nil
	}}
	res := execPlan(t, ctx, plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomePartial || it.Err == nil || it.Err.Kind != KindCanceled || len(it.Details) != 0 {
		t.Errorf("result = %+v, want Partial with KindCanceled", it)
	}
	if res.Status != StatusCanceled {
		t.Errorf("Status = %v", res.Status)
	}
	if got := testfs.ListNames(t, filepath.Join(dest, "tree")); !slices.Equal(got, []string{"fa", "fb"}) {
		t.Errorf("dest = %+q, want fa and fb only", got)
	}
	wantFiles(t, filepath.Join(dest, "tree"), map[string]string{"fa": strings.Repeat("a", copyBufSize+1), "fb": strings.Repeat("b", copyBufSize+1)})
	noTempFiles(t, root)
}

// TestCopyProgress は、コピーの進捗が StageCopy で報告され、逆戻りせず、終了時に全件が完了していることを確かめる（§16）。
// macOS の CI では -race で実行する（§18.4「並行性」）。
func TestCopyProgress(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	tree := testfs.Tree{"src/big": testfs.File(strings.Repeat("x", 3*copyBufSize+5)), "dest": testfs.Dir()}
	for i := range 20 {
		tree["src/tree/f"+string(rune('a'+i))] = testfs.File("0123456789")
	}
	testfs.Build(t, root, tree)
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "tree"), filepath.Join(root, "src", "big")}, DestDir: filepath.Join(root, "dest")})
	var got []Progress
	res := execPlan(t, context.Background(), plan, ExecOptions{Progress: func(p Progress) { got = append(got, p) }})
	if res.Status != StatusCompleted {
		t.Fatalf("result = %+v", res)
	}
	if len(got) < 3 {
		t.Fatalf("progress called %d times, want at least 3", len(got))
	}
	last := got[len(got)-1]
	if last.DoneFiles != plan.TotalFiles() || last.DoneBytes != plan.TotalBytes() || last.TotalBytes != plan.TotalBytes() {
		t.Errorf("final progress = %+v, want %d files / %d bytes", last, plan.TotalFiles(), plan.TotalBytes())
	}
	for i, p := range got {
		if p.Stage != StageCopy {
			t.Errorf("progress[%d].Stage = %v", i, p.Stage)
		}
		if i > 0 && (p.DoneFiles < got[i-1].DoneFiles || p.DoneBytes < got[i-1].DoneBytes) {
			t.Errorf("progress went backwards: %+v -> %+v", got[i-1], p)
		}
	}
}

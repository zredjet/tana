package fsops

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

func mustPlan(t *testing.T, req Request) *Plan {
	t.Helper()
	p, err := NewPlan(context.Background(), req)
	if err != nil {
		t.Fatalf("NewPlan(%+v): %v", req, err)
	}
	return p
}

// TestNewPlanInvalidRequest は、リクエスト全体の問題で NewPlan が error を返すことを確かめる（§6.1）。
func TestNewPlanInvalidRequest(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"a/b/c":     testfs.File("c"),
		"file":      testfs.File("f"),
		"dest":      testfs.Dir(),
		"real/x":    testfs.File("x"),
		"link-real": testfs.DirSymlink(filepath.Join(root, "real")),
		"dest-link": testfs.DirSymlink(filepath.Join(root, "dest")),
		"dangling":  testfs.DirSymlink(filepath.Join(root, "missing")),
	})
	p := func(n string) string { return filepath.Join(root, filepath.FromSlash(n)) }
	vroot := "/"
	if runtime.GOOS == "windows" {
		vroot = filepath.VolumeName(root) + `\`
	}
	tests := []struct {
		name string
		req  Request
		want Kind
	}{
		{"no sources", Request{Op: OpCopy, DestDir: p("dest")}, KindInvalidRequest},
		{"zero op", Request{Sources: []string{p("file")}, DestDir: p("dest")}, KindInvalidRequest},
		{"unknown op", Request{Op: 99, Sources: []string{p("file")}, DestDir: p("dest")}, KindInvalidRequest},
		{"relative source", Request{Op: OpCopy, Sources: []string{"file"}, DestDir: p("dest")}, KindInvalidRequest},
		{"volume root source", Request{Op: OpDelete, Sources: []string{vroot}}, KindInvalidRequest},
		{"duplicate", Request{Op: OpCopy, Sources: []string{p("file"), p("file")}, DestDir: p("dest")}, KindInvalidRequest},
		{"duplicate by another spelling", Request{Op: OpCopy, Sources: []string{p("a/b"), p("a/b/../b")}, DestDir: p("dest")}, KindInvalidRequest},
		{"nested", Request{Op: OpDelete, Sources: []string{p("a"), p("a/b/c")}}, KindInvalidRequest},
		{"nested, reversed", Request{Op: OpDelete, Sources: []string{p("a/b"), p("a")}}, KindInvalidRequest},
		{"nested via a symlinked ancestor", Request{Op: OpDelete, Sources: []string{p("real"), p("link-real/x")}}, KindInvalidRequest},
		{"missing dest", Request{Op: OpCopy, Sources: []string{p("file")}, DestDir: p("nope")}, KindNotFound},
		{"dangling dest link", Request{Op: OpCopy, Sources: []string{p("file")}, DestDir: p("dangling")}, KindNotFound},
		{"dest is a file", Request{Op: OpMove, Sources: []string{p("a")}, DestDir: p("file")}, KindInvalidRequest},
		{"relative dest", Request{Op: OpCopy, Sources: []string{p("file")}, DestDir: "dest"}, KindInvalidRequest},
		{"empty dest for copy", Request{Op: OpCopy, Sources: []string{p("file")}}, KindInvalidRequest},
		{"dest for trash", Request{Op: OpTrash, Sources: []string{p("file")}, DestDir: p("dest")}, KindInvalidRequest},
		{"dest for delete", Request{Op: OpDelete, Sources: []string{p("file")}, DestDir: p("dest")}, KindInvalidRequest},
	}
	if runtime.GOOS == "windows" {
		tests = append(tests,
			struct {
				name string
				req  Request
				want Kind
			}{"device path", Request{Op: OpDelete, Sources: []string{`\\.\C:\x`}}, KindInvalidRequest},
			struct {
				name string
				req  Request
				want Kind
			}{"extended path", Request{Op: OpDelete, Sources: []string{`\\?\` + p("file")}}, KindInvalidRequest},
		)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := NewPlan(context.Background(), tt.req)
			if KindOf(err) != tt.want || plan != nil {
				t.Errorf("NewPlan = %v, %v; want nil, %v", plan, err, tt.want)
			}
		})
	}

	// DestDir 自体がリンクの場合は辿ってよい。ボリュームのルートは DestDir にしてよい（ここでは計画だけ）。
	mustPlan(t, Request{Op: OpCopy, Sources: []string{p("file")}, DestDir: p("dest-link")})
	mustPlan(t, Request{Op: OpCopy, Sources: []string{p("file")}, DestDir: vroot})
	// 名前の前半が同じ別の Source は入れ子ではない。
	testfs.Build(t, root, testfs.Tree{"ab": testfs.Dir()})
	mustPlan(t, Request{Op: OpDelete, Sources: []string{p("a"), p("ab")}})
}

// TestNewPlanCanceled は、ctx がキャンセルされていれば NewPlan が KindCanceled で中断することを確かめる。
func TestNewPlanCanceled(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/a": testfs.File("a"), "dest": testfs.Dir()})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	plan, err := NewPlan(ctx, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src")}, DestDir: filepath.Join(root, "dest")})
	if KindOf(err) != KindCanceled || plan != nil {
		t.Errorf("NewPlan(canceled) = %v, %v; want nil, KindCanceled", plan, err)
	}
}

// TestNewPlanItems は、項目ごとの判定（§6.2）を確かめる。
func TestNewPlanItems(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/dir/sub/x":              testfs.File("x"),
		"src/file.txt":               testfs.File("hello"),
		"src/" + testfs.NameNFD:      testfs.File("nfd"),
		"src/" + testfs.NameJapanese: testfs.File("ja"),
		"dest":                       testfs.Dir(),
		"link-into-dir":              testfs.DirSymlink(filepath.Join(root, "src", "dir", "sub")),
	})
	p := func(n string) string { return filepath.Join(root, filepath.FromSlash(n)) }

	plan := mustPlan(t, Request{Op: OpCopy, DestDir: p("dest"), Sources: []string{
		p("src/file.txt"), p("src/missing"), p("src/" + testfs.NameNFD), p("src/" + testfs.NameJapanese), p("src/dir"),
	}})
	items := plan.Items()
	if len(items) != 5 {
		t.Fatalf("len(Items) = %d, want 5", len(items))
	}
	if it := items[0]; it.Err != nil || it.Dst != p("dest/file.txt") || it.Method != MethodCopy || it.Info.Type != TypeFile || it.Info.Size != 5 {
		t.Errorf("file item = %+v", it)
	}
	if it := items[1]; KindOf(it.Err) != KindNotFound || it.Src != p("src/missing") {
		t.Errorf("missing item = %+v, want Err KindNotFound", it)
	}
	// Dst の名前はバイト単位でコピー元と同じ（I6）。
	if it := items[2]; filepath.Base(it.Dst) != testfs.NameNFD {
		t.Errorf("NFD item Dst = %+q", filepath.Base(it.Dst))
	}
	if it := items[3]; filepath.Base(it.Dst) != testfs.NameJapanese {
		t.Errorf("Japanese item Dst = %+q", filepath.Base(it.Dst))
	}
	if it := items[4]; it.Err != nil || it.Info.Type != TypeDir {
		t.Errorf("dir item = %+v", it)
	}
	if plan.Request().Op != OpCopy || len(plan.Request().Sources) != 5 {
		t.Errorf("Request() = %+v", plan.Request())
	}

	// コピー先がコピー元の内側（リンク経由を含む）→ KindDestInsideSource（§18.4「計画」）。
	for _, dest := range []string{p("src/dir"), p("src/dir/sub"), p("link-into-dir")} {
		for _, op := range []OpKind{OpCopy, OpMove} {
			plan := mustPlan(t, Request{Op: op, Sources: []string{p("src/dir")}, DestDir: dest})
			if it := plan.Items()[0]; KindOf(it.Err) != KindDestInsideSource {
				t.Errorf("%v into %s: Item.Err = %v, want KindDestInsideSource", op, dest, it.Err)
			}
		}
	}

	// 同じフォルダへの移動は KindSameFile、同じフォルダへのコピーは Self の衝突（§6.2）。
	plan = mustPlan(t, Request{Op: OpMove, Sources: []string{p("src/file.txt")}, DestDir: p("src")})
	if it := plan.Items()[0]; KindOf(it.Err) != KindSameFile {
		t.Errorf("move into the same folder: Item.Err = %v, want KindSameFile", it.Err)
	}
	plan = mustPlan(t, Request{Op: OpCopy, Sources: []string{p("src/file.txt"), p("src/dir")}, DestDir: p("src")})
	cs := plan.Conflicts()
	if len(cs) != 2 || !cs[0].Self || !cs[1].Self || cs[0].Item != 0 || cs[1].Item != 1 {
		t.Fatalf("copy into the same folder: conflicts = %+v, want two Self conflicts", cs)
	}
	for _, c := range cs {
		if c.Parent != 0 {
			t.Errorf("Self conflict has inner conflicts: %+v", cs)
		}
	}
}

// TestNewPlanJunctionDest は、ジャンクション経由でコピー元の内側を指すコピー先を KindDestInsideSource にすることを確かめる（§18.4「計画」、Windows）。
func TestNewPlanJunctionDest(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/sub/x": testfs.File("x"), "j": testfs.Junction("src/sub")})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src")}, DestDir: filepath.Join(root, "j")})
	if it := plan.Items()[0]; KindOf(it.Err) != KindDestInsideSource {
		t.Errorf("Item.Err = %v, want KindDestInsideSource", it.Err)
	}
}

// TestNewPlanMoveMethod は、移動の方式が同じボリュームなら MethodRename、違えば MethodCopyThenRemove になることを確かめる（§6.2）。
func TestNewPlanMoveMethod(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/a": testfs.File("a"), "dest": testfs.Dir()})
	plan := mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(root, "src")}, DestDir: filepath.Join(root, "dest")})
	if it := plan.Items()[0]; it.Method != MethodRename || it.Err != nil {
		t.Errorf("same volume: %+v, want MethodRename", it)
	}
	if plan.TotalFiles() != 1 || plan.TotalBytes() != 0 {
		t.Errorf("MethodRename without conflicts: TotalFiles=%d TotalBytes=%d, want 1, 0", plan.TotalFiles(), plan.TotalBytes())
	}
	cross := testfs.CrossVolDir(t)
	plan = mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(root, "src")}, DestDir: cross})
	if it := plan.Items()[0]; it.Method != MethodCopyThenRemove || it.Err != nil {
		t.Errorf("cross volume: %+v, want MethodCopyThenRemove", it)
	}
	if plan.TotalFiles() != 1 || plan.TotalBytes() != 1 {
		t.Errorf("MethodCopyThenRemove: TotalFiles=%d TotalBytes=%d, want 1, 1", plan.TotalFiles(), plan.TotalBytes())
	}
}

// TestNewPlanTotals は、TotalFiles と TotalBytes（§6.3）を確かめる。
func TestNewPlanTotals(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/a":       testfs.File("aaaa"),
		"src/sub/b":   testfs.File("bb"),
		"src/sub/c":   testfs.File(""),
		"src/empty":   testfs.Dir(),
		"src/link":    testfs.Symlink("a"),
		"target/big":  testfs.File(strings.Repeat("z", 1000)),
		"src/dirlink": testfs.DirSymlink(filepath.Join(root, "target")),
		"single":      testfs.File("123"),
		"dest":        testfs.Dir(),
	})
	p := func(n string) string { return filepath.Join(root, filepath.FromSlash(n)) }
	// フォルダ以外のエントリを 1 ファイルとして数え、バイト数は通常のファイルの大きさの合計。リンクの先は数えない（I4）。
	for _, op := range []OpKind{OpCopy, OpDelete} {
		req := Request{Op: op, Sources: []string{p("src"), p("single")}}
		if op == OpCopy {
			req.DestDir = p("dest")
		}
		plan := mustPlan(t, req)
		if plan.TotalFiles() != 6 || plan.TotalBytes() != 4+2+3 {
			t.Errorf("%v: TotalFiles=%d TotalBytes=%d, want 6, 9", op, plan.TotalFiles(), plan.TotalBytes())
		}
	}
	plan := mustPlan(t, Request{Op: OpTrash, Sources: []string{p("src"), p("single")}})
	if plan.TotalFiles() != 2 || plan.TotalBytes() != 0 {
		t.Errorf("OpTrash: TotalFiles=%d TotalBytes=%d, want 2, 0", plan.TotalFiles(), plan.TotalBytes())
	}
}

// TestNewPlanConflicts は、衝突の検出（§6.3）を確かめる。
func TestNewPlanConflicts(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/file.txt":      testfs.File("new"),
		"src/dir/same.txt":  testfs.File("s"),
		"src/dir/only.txt":  testfs.File("o"),
		"src/dir/sub/deep":  testfs.File("d"),
		"src/dir/sub/new":   testfs.File("n"),
		"src/dir/kind":      testfs.Dir(),
		"src/other":         testfs.File("x"),
		"dest/file.txt":     testfs.File("old!"),
		"dest/dir/same.txt": testfs.File("s2"),
		"dest/dir/sub/deep": testfs.File("d2"),
		"dest/dir/kind":     testfs.File("file here"),
		"dest/dir/extra":    testfs.File("e"),
		"dest/other":        testfs.Dir(),
		"elsewhere":         testfs.Dir(),
		"dest2/dir":         testfs.DirSymlink(filepath.Join(root, "elsewhere")),
	})
	p := func(n string) string { return filepath.Join(root, filepath.FromSlash(n)) }
	plan := mustPlan(t, Request{Op: OpCopy, DestDir: p("dest"), Sources: []string{p("src/file.txt"), p("src/dir"), p("src/other")}})
	cs := plan.Conflicts()
	byDst := map[string]Conflict{}
	for i, c := range cs {
		if c.ID != ConflictID(i+1) {
			t.Errorf("conflict %d has ID %d, want %d", i, c.ID, i+1)
		}
		byDst[c.Dst] = c
	}
	want := []struct {
		dst     string
		src     string
		parent  string // 親の衝突の Dst（トップレベルなら ""）
		item    int
		srcType EntryType
		dstType EntryType
		dstSize int64
	}{
		{"dest/file.txt", "src/file.txt", "", 0, TypeFile, TypeFile, 4},
		{"dest/dir", "src/dir", "", 1, TypeDir, TypeDir, 0},
		{"dest/dir/same.txt", "src/dir/same.txt", "dest/dir", 1, TypeFile, TypeFile, 2},
		{"dest/dir/sub", "src/dir/sub", "dest/dir", 1, TypeDir, TypeDir, 0},
		{"dest/dir/sub/deep", "src/dir/sub/deep", "dest/dir/sub", 1, TypeFile, TypeFile, 2},
		{"dest/dir/kind", "src/dir/kind", "dest/dir", 1, TypeDir, TypeFile, 9},
		{"dest/other", "src/other", "", 2, TypeFile, TypeDir, 0},
	}
	if len(cs) != len(want) {
		t.Fatalf("conflicts = %+v, want %d", cs, len(want))
	}
	for _, w := range want {
		c, ok := byDst[p(w.dst)]
		if !ok {
			t.Errorf("no conflict for %s", w.dst)
			continue
		}
		var parent ConflictID
		if w.parent != "" {
			parent = byDst[p(w.parent)].ID
		}
		if c.Src != p(w.src) || c.Parent != parent || c.Item != w.item || c.Self ||
			c.SrcInfo.Type != w.srcType || c.DstInfo.Type != w.dstType || c.DstInfo.Size != w.dstSize || c.Decision != DecisionUnset {
			t.Errorf("conflict %s = %+v, want src=%s parent=%d item=%d types=%v/%v dstSize=%d", w.dst, c, w.src, parent, w.item, w.srcType, w.dstType, w.dstSize)
		}
	}
	// 上書き先の fileID を記録する（§6.3、§7.3）。
	for i, c := range cs {
		id, err := fileIDOf(c.Dst)
		if err != nil || plan.conflictDst[i] != id.id {
			t.Errorf("recorded fileID of %s = %+v, want %+v (%v)", c.Dst, plan.conflictDst[i], id.id, err)
		}
	}

	// コピー先のフォルダがリンクなら、種類が違う衝突として扱い、中に入らない（I4）。
	plan = mustPlan(t, Request{Op: OpCopy, DestDir: p("dest2"), Sources: []string{p("src/dir")}})
	if cs := plan.Conflicts(); len(cs) != 1 || cs[0].DstInfo.Type != TypeSymlink {
		t.Errorf("conflicts with a symlinked dest dir = %+v, want one conflict with TypeSymlink", cs)
	}

	// 同一ボリュームの移動でも、フォルダ同士の衝突なら中を走査して内側の衝突を加える（§6.3）。
	plan = mustPlan(t, Request{Op: OpMove, DestDir: p("dest"), Sources: []string{p("src/dir")}})
	if it := plan.Items()[0]; it.Method != MethodRename {
		t.Fatalf("method = %v", it.Method)
	}
	if n := len(plan.Conflicts()); n != 5 {
		t.Errorf("MethodRename with a dir conflict: %d conflicts, want 5: %+v", n, plan.Conflicts())
	}
	if plan.TotalBytes() != 0 {
		t.Errorf("MethodRename counted bytes: %d", plan.TotalBytes())
	}
}

// TestDecide は、Decide と §9.1 の表を確かめる。
func TestDecide(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/f":    testfs.File("f"),
		"src/d/x":  testfs.File("x"),
		"src/k":    testfs.File("k"),
		"src/l":    testfs.Symlink("f"),
		"dest/f":   testfs.File("f2"),
		"dest/d/y": testfs.File("y"),
		"dest/k":   testfs.Dir(),
		"dest/l":   testfs.Symlink("zzz"),
	})
	p := func(n string) string { return filepath.Join(root, filepath.FromSlash(n)) }
	plan := mustPlan(t, Request{Op: OpCopy, DestDir: p("dest"), Sources: []string{p("src/f"), p("src/d"), p("src/k"), p("src/l")}})
	self := mustPlan(t, Request{Op: OpCopy, DestDir: p("src"), Sources: []string{p("src/f")}})
	id := func(pl *Plan, dst string) ConflictID {
		for _, c := range pl.Conflicts() {
			if c.Dst == dst {
				return c.ID
			}
		}
		t.Fatalf("no conflict for %s", dst)
		return 0
	}
	allowed := map[string][5]bool{ // Unset, Skip, Overwrite, AutoRename, Merge
		"file-file": {true, true, true, true, false},
		"dir-dir":   {true, true, false, true, true},
		"file-dir":  {true, true, false, true, false},
		"link-link": {true, true, false, true, false},
		"self":      {true, true, false, true, false},
	}
	cases := map[string]struct {
		pl *Plan
		id ConflictID
	}{
		"file-file": {plan, id(plan, p("dest/f"))},
		"dir-dir":   {plan, id(plan, p("dest/d"))},
		"file-dir":  {plan, id(plan, p("dest/k"))},
		"link-link": {plan, id(plan, p("dest/l"))},
		"self":      {self, id(self, p("src/f"))},
	}
	for name, c := range cases {
		for d := DecisionUnset; d <= DecisionMerge; d++ {
			err := c.pl.Decide(c.id, d)
			if ok := err == nil; ok != allowed[name][d] {
				t.Errorf("%s: Decide(%v) = %v, want allowed=%v", name, d, err, allowed[name][d])
			}
			if err != nil && KindOf(err) != KindInvalidRequest {
				t.Errorf("%s: Decide(%v) error kind = %v, want KindInvalidRequest", name, d, KindOf(err))
			}
		}
	}
	// 決定は Conflicts() に反映される。許されない決定は、それまでの決定を変えない。
	fileID := id(plan, p("dest/f"))
	if err := plan.Decide(fileID, DecisionOverwrite); err != nil {
		t.Fatal(err)
	}
	plan.Decide(fileID, DecisionMerge)
	for _, c := range plan.Conflicts() {
		if c.ID == fileID && c.Decision != DecisionOverwrite {
			t.Errorf("Decision = %v, want DecisionOverwrite", c.Decision)
		}
	}
	// 存在しない ID、範囲外の決定、Execute の開始後は error。
	for _, bad := range []struct {
		id ConflictID
		d  Decision
	}{{0, DecisionSkip}, {99, DecisionSkip}, {-1, DecisionSkip}, {fileID, Decision(42)}, {fileID, Decision(-1)}} {
		if err := plan.Decide(bad.id, bad.d); err == nil {
			t.Errorf("Decide(%d, %v) = nil, want an error", bad.id, bad.d)
		}
	}
	plan.mu.Lock()
	plan.started = true
	plan.mu.Unlock()
	if err := plan.Decide(fileID, DecisionSkip); err == nil {
		t.Error("Decide after Execute started = nil, want an error")
	}
	var zero Plan
	if err := zero.Decide(1, DecisionSkip); err == nil {
		t.Error("Decide on a zero Plan = nil, want an error")
	}
}

// TestNewPlanWarnings は、フォルダ内の走査エラーを Warnings に入れて計画を続けることを確かめる（§6.3）。
func TestNewPlanWarnings(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix permission bits to make a directory unreadable")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits do not prevent reading")
	}
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/ok": testfs.File("ok"), "src/locked/x": testfs.File("x"), "dest": testfs.Dir()})
	locked := filepath.Join(root, "src", "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src")}, DestDir: filepath.Join(root, "dest")})
	ws := plan.Warnings()
	if len(ws) != 1 || ws[0].Kind != KindPermission || ws[0].Path != locked {
		t.Errorf("Warnings = %+v, want one KindPermission for %s", ws, locked)
	}
	if plan.Items()[0].Err != nil || plan.TotalFiles() != 1 {
		t.Errorf("item = %+v, TotalFiles = %d", plan.Items()[0], plan.TotalFiles())
	}
}

// TestNewPlanNoSpace は、空き容量不足の見込みを Warnings に入れることを確かめる（§6.4、§18.4「容量」）。
func TestNewPlanNoSpace(t *testing.T) {
	t.Parallel()
	cross := testfs.CrossVolDir(t) // 64 MB のボリューム（CI）
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"small": testfs.File("x")})
	big := filepath.Join(root, "big")
	f, err := os.Create(testfs.ExtendedPath(big))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 30); err != nil { // 1 GiB（疎なファイル）
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	for _, op := range []OpKind{OpCopy, OpMove} {
		plan := mustPlan(t, Request{Op: op, Sources: []string{big}, DestDir: cross})
		ws := plan.Warnings()
		if len(ws) != 1 || ws[0].Kind != KindNoSpace {
			t.Errorf("%v: Warnings = %+v, want one KindNoSpace", op, ws)
		}
	}
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "small")}, DestDir: cross})
	if ws := plan.Warnings(); len(ws) != 0 {
		t.Errorf("small copy: Warnings = %+v, want none", ws)
	}
}

// TestNewPlanDoesNotChangeFS は、計画の作成前後でファイルシステムが変化しないことを確かめる（§6、§18.4「計画」）。
func TestNewPlanDoesNotChangeFS(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	tree := testfs.Tree{
		"src/a.txt":             testfs.File("a"),
		"src/dir/b.txt":         testfs.File("b"),
		"src/dir/sub/c":         testfs.File("c"),
		"src/ro.txt":            testfs.File("ro").RO(),
		"src/link":              testfs.Symlink("a.txt"),
		"src/dirlink":           testfs.DirSymlink("dir"),
		"src/" + testfs.NameNFD: testfs.File("n"),
		"src/foo":               testfs.File("plain"),
		"src/foo.":              testfs.File("dot"),
		"dest/a.txt":            testfs.File("old"),
		"dest/dir/b.txt":        testfs.File("old"),
		"dest/dir/extra":        testfs.File("e"),
	}
	if runtime.GOOS == "windows" {
		tree["src/junction"] = testfs.Junction("src/dir")
	}
	testfs.Build(t, root, tree)
	p := func(n string) string { return filepath.Join(root, filepath.FromSlash(n)) }
	srcs := []string{p("src/a.txt"), p("src/dir"), p("src/ro.txt"), p("src/link"), p("src/dirlink"), p("src/" + testfs.NameNFD), p("src/foo."), p("src/missing")}
	before := testfs.Take(t, root)
	for _, req := range []Request{
		{Op: OpCopy, Sources: srcs, DestDir: p("dest")},
		{Op: OpCopy, Sources: srcs, DestDir: p("src")},
		{Op: OpMove, Sources: srcs, DestDir: p("dest")},
		{Op: OpTrash, Sources: srcs},
		{Op: OpDelete, Sources: srcs},
		{Op: OpCopy, Sources: []string{p("src")}, DestDir: p("src/dir")},
	} {
		if _, err := NewPlan(context.Background(), req); err != nil {
			t.Fatalf("NewPlan(%v): %v", req.Op, err)
		}
		if d := testfs.Diff(before, testfs.Take(t, root)); d != nil {
			t.Fatalf("NewPlan(%v) changed the file system: %q", req.Op, d)
		}
	}
}

package fsops

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// Win32 の正規化で変わる名前（末尾の . や空白、予約名）と、260 文字を超えるパスのコピー・移動（§18.4「パス」、§8.2、V11）。
// Windows では \\?\ 形式のパスでだけ扱える名前で、正規化されると同名の別ファイル（foo）と取り違えられる。
// Unix では普通の名前なので、同じテストで取り違えないことを確かめる。

// unsafeNames は、Win32 の正規化で変わる名前と、それと取り違えられる名前（foo）のファイルの中身。
var unsafeNames = map[string]string{
	testfs.NamePlain:         "plain",
	testfs.NameTrailingDot:   "dot",
	testfs.NameTrailingSpace: "space",
	testfs.NameReservedCON:   "con",
}

// unsafeTree は、dir の中に unsafeNames のファイルと、末尾が . のフォルダ（bar.）と普通のフォルダ（bar）を作る。
func unsafeTree(t *testing.T, dir string) {
	t.Helper()
	tree := testfs.Tree{"bar./x": testfs.File("in bar."), "bar/y": testfs.File("in bar")}
	for name, data := range unsafeNames {
		tree[name] = testfs.File(data)
	}
	testfs.Build(t, dir, tree)
}

// checkUnsafeTree は、dir が unsafeTree のとおりであることを確かめる（名前がバイト単位で一致し、取り違えていない）。
func checkUnsafeTree(t *testing.T, dir string) {
	t.Helper()
	want := []string{"CON", "bar", "bar.", "foo", "foo ", "foo."}
	if got := testfs.ListNames(t, dir); !slices.Equal(got, want) {
		t.Errorf("%s: names %+q, want %+q", dir, got, want)
	}
	files := map[string]string{"bar./x": "in bar.", "bar/y": "in bar"}
	for name, data := range unsafeNames {
		files[name] = data
	}
	wantFiles(t, dir, files)
}

// TestCopyWin32UnsafeNames は、末尾が . や空白の名前・予約名を含むツリーと、そうした名前のトップレベルの項目をコピーしても、
// 名前がそのまま複製され、同名の別ファイル（foo）に影響しないことを確かめる。上書きの衝突も foo. だけに対して検出される。
func TestCopyWin32UnsafeNames(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	unsafeTree(t, filepath.Join(root, "src", "tree"))
	testfs.Build(t, root, testfs.Tree{
		"src/" + testfs.NameTrailingDot: testfs.File("top dot"), "src/" + testfs.NameReservedCON: testfs.File("top con"),
		"dest/" + testfs.NamePlain: testfs.File("dest plain"), "dest/" + testfs.NameTrailingDot: testfs.File("old dot"),
	})
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, DestDir: dest, Sources: []string{
		filepath.Join(root, "src", "tree"), filepath.Join(root, "src", testfs.NameTrailingDot), filepath.Join(root, "src", testfs.NameReservedCON),
	}})
	cs := plan.Conflicts()
	if len(cs) != 1 || filepath.Base(cs[0].Dst) != testfs.NameTrailingDot {
		t.Fatalf("conflicts = %+v, want only foo.", cs)
	}
	decide(t, plan, filepath.Join(dest, testfs.NameTrailingDot), DecisionOverwrite)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone {
			t.Errorf("%s: %+v (%v), want Done", it.Src, it, it.Err)
		}
	}
	checkUnsafeTree(t, filepath.Join(dest, "tree"))
	checkUnsafeTree(t, filepath.Join(root, "src", "tree"))
	wantFiles(t, dest, map[string]string{testfs.NamePlain: "dest plain", testfs.NameTrailingDot: "top dot", testfs.NameReservedCON: "top con"})
	noTempFiles(t, root)
}

// TestMoveWin32UnsafeNames は、同一ボリュームの移動（マージを含む）で、末尾が . や空白の名前・予約名を取り違えないことを確かめる。
func TestMoveWin32UnsafeNames(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	unsafeTree(t, filepath.Join(root, "src", "tree"))
	unsafeTree(t, filepath.Join(root, "src", "m"))
	testfs.Build(t, root, testfs.Tree{
		"src/" + testfs.NameTrailingDot: testfs.File("top dot"),
		"dest/" + testfs.NamePlain:      testfs.File("dest plain"),
		"dest/m/" + testfs.NamePlain:    testfs.File("dest m plain"),
	})
	dest := filepath.Join(root, "dest")
	plan := sameMove(t, root, "tree", testfs.NameTrailingDot, "m")
	decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
	decide(t, plan, filepath.Join(dest, "m", testfs.NamePlain), DecisionSkip)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone {
			t.Errorf("%s: %+v (%v), want Done", it.Src, it, it.Err)
		}
	}
	checkUnsafeTree(t, filepath.Join(dest, "tree"))
	wantFiles(t, dest, map[string]string{
		testfs.NamePlain: "dest plain", testfs.NameTrailingDot: "top dot",
		"m/" + testfs.NamePlain: "dest m plain", "m/" + testfs.NameTrailingDot: "dot", "m/" + testfs.NameTrailingSpace: "space",
		"m/" + testfs.NameReservedCON: "con", "m/bar./x": "in bar.", "m/bar/y": "in bar",
	})
	// スキップした foo だけが移動元に残る。
	if got := testfs.ListNames(t, filepath.Join(root, "src")); !slices.Equal(got, []string{"m"}) {
		t.Errorf("left in src: %+q, want [m]", got)
	}
	if got := testfs.ListNames(t, filepath.Join(root, "src", "m")); !slices.Equal(got, []string{testfs.NamePlain}) {
		t.Errorf("left in src/m: %+q, want [foo]", got)
	}
}

// TestMoveCrossVolumeWin32UnsafeNames は、ボリュームをまたぐ移動で、末尾が . や空白の名前・予約名を取り違えず、
// 移動元から記録したものだけを消すことを確かめる（§13.3 の照合も \\?\ のパスで行う）。
func TestMoveCrossVolumeWin32UnsafeNames(t *testing.T) {
	t.Parallel()
	dest := testfs.CrossVolDir(t)
	root := testfs.TempDir(t)
	unsafeTree(t, filepath.Join(root, "src", "tree"))
	res := execPlan(t, context.Background(), crossMove(t, root, dest, "tree"), ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeDone {
		t.Errorf("result = %+v (%v), want Done", it, it.Err)
	}
	checkUnsafeTree(t, filepath.Join(dest, "tree"))
	if testfs.Exists(t, filepath.Join(root, "src", "tree")) {
		t.Errorf("left in the source: %+q", testfs.ListNames(t, filepath.Join(root, "src", "tree")))
	}
}

// longTree は、260 文字を超えるパスのフォルダ（dir の中）にファイルとフォルダを作り、そのフォルダのパスを返す。
func longTree(t *testing.T, dir string) string {
	t.Helper()
	long := testfs.LongPath(t, dir)
	testfs.Build(t, long, testfs.Tree{"a.txt": testfs.File("a"), "sub/b.txt": testfs.File("b")})
	return long
}

// TestCopyLongPath は、260 文字を超えるパスからのコピー・への コピー（上書き・自動リネームを含む）を確かめる（§18.4「パス」、§8.2）。
func TestCopyLongPath(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	src := longTree(t, filepath.Join(root, "src"))
	dest := testfs.LongPath(t, filepath.Join(root, "dest"))
	testfs.Build(t, dest, testfs.Tree{"a.txt": testfs.File("old a"), "sub/b.txt": testfs.File("old b")})
	plan := mustPlan(t, Request{Op: OpCopy, DestDir: dest, Sources: []string{filepath.Join(src, "a.txt"), filepath.Join(src, "sub")}})
	decide(t, plan, filepath.Join(dest, "a.txt"), DecisionOverwrite)
	decide(t, plan, filepath.Join(dest, "sub"), DecisionAutoRename)
	res := execPlan(t, context.Background(), plan, ExecOptions{Verify: VerifyHash})
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone {
			t.Errorf("%s: %+v (%v), want Done", it.Src, it, it.Err)
		}
	}
	wantFiles(t, dest, map[string]string{"a.txt": "a", "sub/b.txt": "old b", "sub (2)/b.txt": "b"})
	// 短いパスのコピー先へ、長いパスのフォルダをそのままコピーする。
	short := filepath.Join(root, "short")
	testfs.MkdirAll(t, short)
	res = execPlan(t, context.Background(), mustPlan(t, Request{Op: OpCopy, DestDir: short, Sources: []string{src}}), ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeDone {
		t.Errorf("long folder: %+v (%v), want Done", it, it.Err)
	}
	wantFiles(t, filepath.Join(short, filepath.Base(src)), map[string]string{"a.txt": "a", "sub/b.txt": "b"})
	noTempFiles(t, root)
}

// TestMoveLongPath は、260 文字を超えるパスの同一ボリュームの移動（マージを含む）を確かめる（§18.4「パス」）。
func TestMoveLongPath(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	src := longTree(t, filepath.Join(root, "src"))
	dest := testfs.LongPath(t, filepath.Join(root, "dest"))
	testfs.Build(t, dest, testfs.Tree{"sub/old.txt": testfs.File("old")})
	plan := mustPlan(t, Request{Op: OpMove, DestDir: dest, Sources: []string{filepath.Join(src, "a.txt"), filepath.Join(src, "sub")}})
	decide(t, plan, filepath.Join(dest, "sub"), DecisionMerge)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone {
			t.Errorf("%s: %+v (%v), want Done", it.Src, it, it.Err)
		}
	}
	wantFiles(t, dest, map[string]string{"a.txt": "a", "sub/b.txt": "b", "sub/old.txt": "old"})
	if got := testfs.ListNames(t, src); len(got) != 0 {
		t.Errorf("left in the source: %+q", got)
	}
}

// TestMoveCrossVolumeLongPath は、260 文字を超えるパスのボリュームをまたぐ移動を確かめる（§18.4「パス」。移動元の削除を含む）。
func TestMoveCrossVolumeLongPath(t *testing.T) {
	t.Parallel()
	dest := testfs.LongPath(t, testfs.CrossVolDir(t))
	root := testfs.TempDir(t)
	src := longTree(t, filepath.Join(root, "src"))
	plan := mustPlan(t, Request{Op: OpMove, DestDir: dest, Sources: []string{src}})
	if it := plan.Items()[0]; it.Method != MethodCopyThenRemove {
		t.Fatalf("method = %v, want MethodCopyThenRemove", it.Method)
	}
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeDone {
		t.Errorf("result = %+v (%v), want Done", it, it.Err)
	}
	wantFiles(t, filepath.Join(dest, filepath.Base(src)), map[string]string{"a.txt": "a", "sub/b.txt": "b"})
	if testfs.Exists(t, src) {
		t.Error("the source is left")
	}
}

// TestWithUserPathsOpError は、OS ごとの関数が \\?\ 形式のパスで作った *OpError のパスも、呼び出し側に返す形にすることを確かめる（§8.2）。
func TestWithUserPathsOpError(t *testing.T) {
	t.Parallel()
	inner := &OpError{Op: "open", Path: `\\?\C:\dir\f.txt`, Kind: KindSourceChanged}
	err := withUserPaths(inner, `C:\dir\f.txt`, "")
	if inner.Path != `C:\dir\f.txt` || err != error(inner) {
		t.Errorf("Path = %q, want the user path", inner.Path)
	}
}

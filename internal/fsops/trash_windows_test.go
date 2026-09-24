package fsops

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestTrashPrecheckWindows は、Windows のごみ箱の事前確認（§12.2）を確かめる。ファイルシステムは変更しない。
func TestTrashPrecheckWindows(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"ok.txt":                 testfs.File("x"),
		testfs.NamePlain:         testfs.File("plain"),
		testfs.NameTrailingDot:   testfs.File("dot"),
		testfs.NameTrailingSpace: testfs.File("space"),
		testfs.NameReservedCON:   testfs.File("con"),
	})
	long := testfs.LongPath(t, root)
	testfs.WriteFile(t, filepath.Join(long, "f"), "x")
	share := `\\localhost\` + strings.ToUpper(root[:1]) + `$` + root[2:]
	tests := []struct {
		name string
		path string
		want Kind // KindUnknown は「使える」
	}{
		{"fixed drive", filepath.Join(root, "ok.txt"), KindUnknown},
		{"plain name", filepath.Join(root, testfs.NamePlain), KindUnknown},
		{"trailing dot", filepath.Join(root, testfs.NameTrailingDot), KindTrashUnavailable},
		{"trailing space", filepath.Join(root, testfs.NameTrailingSpace), KindTrashUnavailable},
		{"reserved name", filepath.Join(root, testfs.NameReservedCON), KindTrashUnavailable},
		{"long path", filepath.Join(long, "f"), KindTrashUnavailable},
		{"network (admin share)", filepath.Join(share, "ok.txt"), KindTrashUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := mustPlan(t, Request{Op: OpTrash, Sources: []string{tt.path}})
			it := plan.Items()[0]
			if tt.want == KindUnknown && it.Err != nil || tt.want != KindUnknown && KindOf(it.Err) != tt.want {
				t.Errorf("%s: Item.Err = %v, want %v", tt.path, it.Err, tt.want)
			}
		})
	}
}

// TestTrashPrecheckCapacity は、ごみ箱が「すぐに削除する」設定のボリュームと、最大サイズを超える項目を、
// 計画時に KindTrashUnavailable にすることを確かめる（§12.2、V13、V19）。CI の W:・X: を使う。
func TestTrashPrecheckCapacity(t *testing.T) {
	t.Parallel()
	nuke := testfs.EnvDir(t, testfs.TrashNukeEnv)
	small := testfs.EnvDir(t, testfs.TrashSmallEnv) // 最大サイズ 1 MB（1048576 バイト）
	testfs.Build(t, nuke, testfs.Tree{"a.txt": testfs.File("a")})
	testfs.Build(t, small, testfs.Tree{
		"exact.bin":      testfs.File(strings.Repeat("x", 1<<20)),
		"over.bin":       testfs.File(strings.Repeat("x", 1<<20+1)),
		"small.bin":      testfs.File("x"),
		"dir-over/a.bin": testfs.File(strings.Repeat("x", 600000)),
		"dir-over/b.bin": testfs.File(strings.Repeat("x", 600000)),
		"dir-ok/a.bin":   testfs.File(strings.Repeat("x", 400000)),
		"dir-ok/b.bin":   testfs.File(strings.Repeat("x", 400000)),
	})
	tests := []struct {
		path string
		ok   bool
	}{
		{filepath.Join(nuke, "a.txt"), false},
		{filepath.Join(small, "exact.bin"), true}, // 最大サイズちょうどは入る（V19）
		{filepath.Join(small, "over.bin"), false},
		{filepath.Join(small, "small.bin"), true},
		{filepath.Join(small, "dir-over"), false}, // フォルダは中身の合計（§12.1）
		{filepath.Join(small, "dir-ok"), true},
	}
	for _, tt := range tests {
		plan := mustPlan(t, Request{Op: OpTrash, Sources: []string{tt.path}})
		it := plan.Items()[0]
		if tt.ok && it.Err != nil || !tt.ok && KindOf(it.Err) != KindTrashUnavailable {
			t.Errorf("%s: Item.Err = %v, want ok=%v", tt.path, it.Err, tt.ok)
		}
	}
}

// checkTrashed は、src がごみ箱の trashed（$Recycle.Bin の $R ファイル）に入ったことを確かめる（§18.4「ごみ箱」、V10）。
// 対になる $I ファイル（版 2）に、元のパスが記録されていることも確かめる。
func checkTrashed(t *testing.T, src, trashed string, info EntryInfo) {
	t.Helper()
	if trashed == "" {
		t.Errorf("%s: TrashedPath is empty", src)
		return
	}
	now, err := lstatEntry(trashed)
	if err != nil {
		t.Errorf("%s: TrashedPath %s: %v", src, trashed, err)
		return
	}
	if now.Type != info.Type || info.Type == TypeFile && now.Size != info.Size {
		t.Errorf("%s: in the trash %+v, want %+v", src, now, info)
	}
	base := filepath.Base(trashed)
	if !strings.HasPrefix(base, "$R") {
		t.Errorf("%s: TrashedPath %s is not a $R entry", src, trashed)
		return
	}
	b, err := os.ReadFile(testfs.ExtendedPath(filepath.Join(filepath.Dir(trashed), "$I"+base[2:])))
	if err != nil {
		t.Errorf("%s: $I file: %v", src, err)
		return
	}
	if len(b) < 28 || binary.LittleEndian.Uint64(b) != 2 {
		t.Errorf("%s: unexpected $I file (%d bytes)", src, len(b))
		return
	}
	n := int(binary.LittleEndian.Uint32(b[24:]))
	if len(b) < 28+2*n {
		t.Errorf("%s: short $I file", src)
		return
	}
	u := make([]uint16, n)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[28+2*i:])
	}
	if got := strings.TrimRight(string(utf16.Decode(u)), "\x00"); got != src {
		t.Errorf("$I original path = %q, want %q", got, src)
	}
}

// TestTrashWindowsUnavailable は、ごみ箱が使えない場所の項目を実行しても、KindTrashUnavailable で失敗し、項目が完全に残ることを確かめる
// （§18.4 の I5。固定ドライブ以外のパス、ごみ箱の最大サイズを超える項目、「すぐに削除する」設定のボリューム、260 文字以上のパス、
// Win32 の正規化で変わる名前）。foo. をごみ箱に入れようとしても、隣の foo は残る。
func TestTrashWindowsUnavailable(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		testfs.NamePlain: testfs.File("plain"), testfs.NameTrailingDot: testfs.File("dot"), testfs.NameReservedCON: testfs.File("con"),
		"share.txt": testfs.File("share"),
	})
	long := testfs.LongPath(t, filepath.Join(root, "long"))
	testfs.WriteFile(t, filepath.Join(long, "f"), "long")
	srcs := []string{
		filepath.Join(root, testfs.NameTrailingDot), filepath.Join(root, testfs.NameReservedCON), filepath.Join(long, "f"),
		`\\localhost\` + strings.ToUpper(root[:1]) + `$` + root[2:] + `\share.txt`,
	}
	for _, env := range []string{testfs.TrashNukeEnv, testfs.TrashSmallEnv} {
		if os.Getenv(env) == "" {
			continue
		}
		d := testfs.EnvDir(t, env)
		p := filepath.Join(d, "item.bin")
		size := 16
		if env == testfs.TrashSmallEnv {
			size = 1<<20 + 1 // 最大サイズ（1 MB）を 1 バイト超える
		}
		testfs.WriteFile(t, p, strings.Repeat("x", size))
		srcs = append(srcs, p)
	}
	before := map[string]string{}
	for _, p := range srcs {
		before[p] = testfs.ReadFile(t, p)
	}
	plan := mustPlan(t, Request{Op: OpTrash, Sources: srcs})
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	for i, it := range res.Items {
		if KindOf(plan.Items()[i].Err) != KindTrashUnavailable || it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindTrashUnavailable {
			t.Errorf("%s: Item.Err %v, result %+v; want KindTrashUnavailable", it.Src, plan.Items()[i].Err, it)
		}
		if got := testfs.ReadFile(t, it.Src); got != before[it.Src] {
			t.Errorf("I5 violated: %s = %q", it.Src, got)
		}
	}
	wantFiles(t, root, map[string]string{testfs.NamePlain: "plain"})
}

// TestTrashExecuteRecheck は、計画の後にごみ箱の最大サイズを超えるまで大きくなった項目を、実行時の事前確認で止めることを確かめる（§12.1、§12.2）。
func TestTrashExecuteRecheck(t *testing.T) {
	t.Parallel()
	d := testfs.EnvDir(t, testfs.TrashSmallEnv)
	p := filepath.Join(d, "grows.bin")
	testfs.WriteFile(t, p, "small")
	plan := mustPlan(t, Request{Op: OpTrash, Sources: []string{p}})
	if err := plan.Items()[0].Err; err != nil {
		t.Fatalf("Item.Err = %v, want nil", err)
	}
	big := strings.Repeat("x", 2<<20)
	testfs.WriteFile(t, p, big)
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindTrashUnavailable {
		t.Errorf("result = %+v, want Failed with KindTrashUnavailable", it)
	}
	if testfs.ReadFile(t, p) != big {
		t.Error("I5 violated: the item was deleted")
	}
}

// TestTrashPreDeleteAbort は、事前確認を飛ばしても（フック）、「すぐに削除する」設定のボリュームの項目は、
// PreDeleteItem での中止（§12.2 の手順 4。二つ目の防御）により KindTrashUnavailable になり、完全に残ることを確かめる（I5、V18）。
// 事前確認なしでは Windows の確認ダイアログ（FOF_WANTNUKEWARNING）で止まる可能性があるので、別プロセスで実行し、時間切れなら強制終了する。
func TestTrashPreDeleteAbort(t *testing.T) {
	testfs.RequireTrash(t)
	t.Parallel()
	d := testfs.EnvDir(t, testfs.TrashNukeEnv)
	p := filepath.Join(d, "nuke.bin")
	testfs.WriteFile(t, p, "keep me")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTrashPreDeleteAbortHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "FSOPS_TRASH_HELPER_PATH="+p)
	out, err := cmd.CombinedOutput()
	t.Logf("helper: err=%v timedOut=%v\n%s", err, ctx.Err() != nil, out)
	if ctx.Err() != nil {
		t.Error("the helper timed out (a confirmation dialog may have been shown)")
	}
	if !strings.Contains(string(out), "HELPER-RESULT: "+KindTrashUnavailable.String()) {
		t.Error("the item was not reported as KindTrashUnavailable")
	}
	if got := testfs.ReadFile(t, p); got != "keep me" {
		t.Errorf("I5 violated: %q", got)
	}
}

// TestTrashPreDeleteAbortHelper は TestTrashPreDeleteAbort が別プロセスとして実行する。環境変数がなければ何もしない。
func TestTrashPreDeleteAbortHelper(t *testing.T) {
	p := os.Getenv("FSOPS_TRASH_HELPER_PATH")
	if p == "" {
		t.Skip("helper process for TestTrashPreDeleteAbort")
	}
	info, err := lstatEntry(p)
	if err != nil {
		t.Fatal(err)
	}
	ex := &executor{ctx: context.Background(), opt: ExecOptions{hooks: &testHooks{bypassTrashPrecheck: true}}, progress: &progressReporter{}}
	res := ex.trashItem(Item{Src: p, Info: info, Method: MethodTrash})
	kind := KindUnknown
	if res.Err != nil {
		kind = res.Err.Kind
	}
	t.Logf("HELPER-RESULT: %v outcome=%v err=%v", kind, res.Outcome, res.Err)
}

// TestHresultFailed は、HRESULT の成否を SUCCEEDED/FAILED（最上位ビット）で判定することを確かめる。
// ごみ箱へ入れるのに成功しても、PostDeleteItem は S_OK ではなく COPYENGINE_S_DONT_PROCESS_CHILDREN（0x00270008）を渡す
// （2026-09-24 の CI の V18 のログで確認）。S_OK 以外を失敗とすると、ごみ箱に入った項目を失敗と報告してしまう。
func TestHresultFailed(t *testing.T) {
	t.Parallel()
	for hr, want := range map[uint32]bool{
		sOK: false, sFalse: false, 0x00270008: false,
		eAbort: true, 0x80070020: true, 0x80270000: true,
	} {
		if got := hresultFailed(hr); got != want {
			t.Errorf("hresultFailed(%#x) = %v, want %v", hr, got, want)
		}
	}
}

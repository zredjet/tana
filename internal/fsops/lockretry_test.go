package fsops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// lockInjector は、§17.1 のやり直しを確かめるためのフック（lockFault・lockWait）の組み立てと記録。
type lockInjector struct {
	mu       sync.Mutex
	left     map[string]int // 操作ごとの、残りの注入回数（-1 は無制限）
	attempts map[string]int // 操作ごとの、注入を問い合わせた回数（やり直しを含む試行の数）
	waits    []time.Duration
	onWait   func(path string, n int) // n 回目（1 から）の待ちで呼ばれる
}

func newLockInjector(faults map[string]int) *lockInjector {
	return &lockInjector{left: faults, attempts: map[string]int{}}
}

func (li *lockInjector) hooks() *testHooks {
	return &testHooks{
		lockFault: func(op, path string) bool {
			li.mu.Lock()
			defer li.mu.Unlock()
			li.attempts[op]++
			switch n := li.left[op]; {
			case n < 0:
				return true
			case n > 0:
				li.left[op] = n - 1
				return true
			}
			return false
		},
		lockWait: func(path string, d time.Duration) {
			li.mu.Lock()
			li.waits = append(li.waits, d)
			n := len(li.waits)
			li.mu.Unlock()
			if li.onWait != nil {
				li.onWait(path, n)
			}
		},
	}
}

func (li *lockInjector) totalWait() time.Duration {
	var sum time.Duration
	for _, d := range li.waits {
		sum += d
	}
	return sum
}

func ms(ns ...int) []time.Duration {
	var ds []time.Duration
	for _, n := range ns {
		ds = append(ds, time.Duration(n)*time.Millisecond)
	}
	return ds
}

// TestLockRetrier は、やり直しの間隔と上限（§17.1）を確かめる。
func TestLockRetrier(t *testing.T) {
	t.Parallel()
	locked := func(h *testHooks) func() error {
		return func() error { return h.lockFaultErr("rename", "/x") }
	}

	t.Run("intervals and per-operation limit", func(t *testing.T) {
		li := newLockInjector(map[string]int{"rename": -1})
		h := li.hooks()
		lr := newLockRetrier(context.Background(), h)
		err := lr.retry("/x", false, locked(h), lockedErr)
		if KindOf(err) != KindLocked {
			t.Errorf("err = %v, want KindLocked", err)
		}
		if want := ms(10, 20, 40, 80, 160, 200, 200, 200, 90); !slices.Equal(li.waits, want) {
			t.Errorf("waits = %v, want %v", li.waits, want)
		}
		if li.attempts["rename"] != 10 {
			t.Errorf("attempts = %d, want 10", li.attempts["rename"])
		}
	})

	t.Run("succeeds after transient failures", func(t *testing.T) {
		li := newLockInjector(map[string]int{"rename": 3})
		h := li.hooks()
		lr := newLockRetrier(context.Background(), h)
		if err := lr.retry("/x", false, locked(h), lockedErr); err != nil {
			t.Errorf("err = %v, want nil", err)
		}
		if want := ms(10, 20, 40); !slices.Equal(li.waits, want) {
			t.Errorf("waits = %v, want %v", li.waits, want)
		}
	})

	t.Run("other errors are not retried", func(t *testing.T) {
		li := newLockInjector(nil)
		lr := newLockRetrier(context.Background(), li.hooks())
		n := 0
		perm := &OpError{Op: "rename", Kind: KindPermission}
		err := lr.retry("/x", false, func() error { n++; return perm }, lockedErr)
		if err != perm || n != 1 || len(li.waits) != 0 {
			t.Errorf("err = %v after %d attempts and waits %v, want the permission error after 1 attempt and no waits", err, n, li.waits)
		}
	})

	t.Run("per-Execute limit", func(t *testing.T) {
		li := newLockInjector(map[string]int{"rename": -1})
		h := li.hooks()
		lr := newLockRetrier(context.Background(), h)
		for range 11 {
			if err := lr.retry("/x", false, locked(h), lockedErr); KindOf(err) != KindLocked {
				t.Fatalf("err = %v, want KindLocked", err)
			}
		}
		if got := li.totalWait(); got != 10*time.Second {
			t.Errorf("total wait = %v, want 10s", got)
		}
		if li.attempts["rename"] != 10*10+1 {
			t.Errorf("attempts = %d, want 101 (the 11th operation is tried once)", li.attempts["rename"])
		}
	})

	t.Run("cancel during wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		li := newLockInjector(map[string]int{"rename": -1})
		li.onWait = func(string, int) { cancel() }
		h := li.hooks()
		lr := newLockRetrier(ctx, h)
		err := lr.retry("/x", false, locked(h), lockedErr)
		if KindOf(err) != KindCanceled || !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want KindCanceled wrapping context.Canceled", err)
		}
		if li.attempts["rename"] != 1 || len(li.waits) != 1 {
			t.Errorf("attempts = %d, waits = %v; want 1 attempt and 1 wait", li.attempts["rename"], li.waits)
		}
	})

	t.Run("temp cleanup ignores cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		li := newLockInjector(map[string]int{"rename": 2})
		h := li.hooks()
		lr := newLockRetrier(ctx, h)
		if err := lr.retry("/x", true, locked(h), lockedErr); err != nil {
			t.Errorf("err = %v, want nil (retried despite cancel)", err)
		}
		if li.attempts["rename"] != 3 {
			t.Errorf("attempts = %d, want 3", li.attempts["rename"])
		}
	})
}

// TestLockRetryCopy は、コピー元を開く・最終名へのリネームが一時的に使用中でも、やり直して完了することを確かめる（§17.1）。
func TestLockRetryCopy(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("data"), "dest": testfs.Dir()})
	li := newLockInjector(map[string]int{"open": 2, "rename": 3})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: filepath.Join(root, "dest")})
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: li.hooks()})
	if it := res.Items[0]; it.Outcome != OutcomeDone {
		t.Fatalf("result = %+v, want Done", it)
	}
	if got := testfs.ReadFile(t, filepath.Join(root, "dest", "f.txt")); got != "data" {
		t.Errorf("dest = %q, want %q", got, "data")
	}
	if len(li.waits) != 5 {
		t.Errorf("waits = %v, want 5", li.waits)
	}
	noTempFiles(t, filepath.Join(root, "dest"))
}

// TestLockRetryCopyExhausted は、使用中が続けば 1 秒待った後に KindLocked で失敗し、一時ファイルを残さないことを確かめる（§17.1、I3）。
// 一時ファイルの削除も一時的に使用中にし、やり直して消すことを確かめる。
func TestLockRetryCopyExhausted(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("data"), "dest": testfs.Dir()})
	li := newLockInjector(map[string]int{"rename": -1, "unlink-temp": 2})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: filepath.Join(root, "dest")})
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: li.hooks()})
	if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindLocked {
		t.Fatalf("result = %+v, want Failed with KindLocked", it)
	}
	if got := li.totalWait(); got != time.Second+30*time.Millisecond {
		t.Errorf("total wait = %v, want 1s for the rename and 30ms for the temp file cleanup", got)
	}
	if testfs.Exists(t, filepath.Join(root, "dest", "f.txt")) {
		t.Error("dest/f.txt exists")
	}
	noTempFiles(t, filepath.Join(root, "dest"))
}

// TestLockRetryExecuteLimit は、1 回の Execute の待ちの合計が 10 秒に達したら、それ以降はやり直さないことを確かめる（§17.1）。
func TestLockRetryExecuteLimit(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	tree := testfs.Tree{"dest": testfs.Dir()}
	var srcs []string
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"} {
		tree["src/"+n] = testfs.File(n)
		srcs = append(srcs, filepath.Join(root, "src", n))
	}
	testfs.Build(t, root, tree)
	li := newLockInjector(map[string]int{"rename": -1})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: srcs, DestDir: filepath.Join(root, "dest")})
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: li.hooks()})
	for _, it := range res.Items {
		if it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindLocked {
			t.Errorf("%s: %+v, want Failed with KindLocked", it.Src, it)
		}
	}
	if got := li.totalWait(); got != 10*time.Second {
		t.Errorf("total wait = %v, want 10s", got)
	}
	if li.attempts["rename"] != 10*10+2 {
		t.Errorf("rename attempts = %d, want 102 (10 per item for the first 10 items, then 1 each)", li.attempts["rename"])
	}
	noTempFiles(t, filepath.Join(root, "dest"))
}

// TestLockRetryCopyCancel は、待っている間にキャンセルされたら、すぐに KindCanceled で打ち切り、
// キャンセルの後も一時ファイルの削除はやり直して、一時ファイルを残さないことを確かめる（§17.1、I3、I7）。
func TestLockRetryCopyCancel(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("data"), "src/g.txt": testfs.File("g"), "dest": testfs.Dir()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	li := newLockInjector(map[string]int{"rename": -1, "unlink-temp": 2})
	li.onWait = func(_ string, n int) {
		if n == 1 {
			cancel()
		}
	}
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt"), filepath.Join(root, "src", "g.txt")}, DestDir: filepath.Join(root, "dest")})
	res := execPlan(t, ctx, plan, ExecOptions{hooks: li.hooks()})
	if res.Status != StatusCanceled {
		t.Errorf("status = %v, want Canceled", res.Status)
	}
	for _, it := range res.Items {
		if it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindCanceled {
			t.Errorf("%s: %+v, want Skipped with KindCanceled", it.Src, it)
		}
	}
	if li.attempts["unlink-temp"] != 3 {
		t.Errorf("temp file removal attempts = %d, want 3 (retried after cancel)", li.attempts["unlink-temp"])
	}
	if names := testfs.ListNames(t, filepath.Join(root, "dest")); len(names) != 0 {
		t.Errorf("dest has %v, want nothing", names)
	}
}

// TestLockRetryConflictDuringWait は、待っている間に最終名に別のものが現れたら、やり直しで上書きせず Skipped（KindExist）にすることを確かめる（I1）。
func TestLockRetryConflictDuringWait(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("new"), "dest": testfs.Dir()})
	dst := filepath.Join(root, "dest", "f.txt")
	li := newLockInjector(map[string]int{"rename": 1})
	li.onWait = func(string, int) { testfs.WriteFile(t, dst, "other") }
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: filepath.Join(root, "dest")})
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: li.hooks()})
	if it := res.Items[0]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist {
		t.Errorf("result = %+v, want Skipped with KindExist", it)
	}
	if got := testfs.ReadFile(t, dst); got != "other" {
		t.Errorf("I1 violated: dest = %q, want %q", got, "other")
	}
	noTempFiles(t, filepath.Join(root, "dest"))
}

// TestLockRetryOverwriteChangedDuringWait は、上書きの決定があっても、待っている間に上書き先が変わったら、
// やり直しの前の照合で気づいて上書きしないことを確かめる（I1、§7.3）。
func TestLockRetryOverwriteChangedDuringWait(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("new"), "dest/f.txt": testfs.File("old")})
	dst := filepath.Join(root, "dest", "f.txt")
	li := newLockInjector(map[string]int{"rename": 1})
	li.onWait = func(string, int) { testfs.WriteFile(t, dst, "changed by someone") }
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: filepath.Join(root, "dest")})
	decide(t, plan, dst, DecisionOverwrite)
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: li.hooks()})
	if it := res.Items[0]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindExist {
		t.Errorf("result = %+v, want Skipped with KindExist", it)
	}
	if got := testfs.ReadFile(t, dst); got != "changed by someone" {
		t.Errorf("I1 violated: dest = %q", got)
	}
	noTempFiles(t, filepath.Join(root, "dest"))
}

// TestLockRetryOverwrite は、上書きのリネームが一時的に使用中でも、やり直して上書きすることを確かめる（§17.1）。
func TestLockRetryOverwrite(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("new"), "dest/f.txt": testfs.File("old")})
	dst := filepath.Join(root, "dest", "f.txt")
	li := newLockInjector(map[string]int{"rename": 2})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: filepath.Join(root, "dest")})
	decide(t, plan, dst, DecisionOverwrite)
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: li.hooks()})
	if it := res.Items[0]; it.Outcome != OutcomeDone {
		t.Errorf("result = %+v, want Done", it)
	}
	if got := testfs.ReadFile(t, dst); got != "new" {
		t.Errorf("dest = %q, want %q", got, "new")
	}
	noTempFiles(t, filepath.Join(root, "dest"))
}

// TestLockRetryMove は、同一ボリュームの移動のリネーム（マージ移動の中身と、その後の移動元のフォルダの削除を含む）が
// 一時的に使用中でも、やり直して完了することを確かめる（§17.1）。
func TestLockRetryMove(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("f"), "src/d/x.txt": testfs.File("x"), "dest/d/y.txt": testfs.File("y")})
	li := newLockInjector(map[string]int{"rename": 3, "remove": 2})
	plan := mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(root, "src", "f.txt"), filepath.Join(root, "src", "d")}, DestDir: filepath.Join(root, "dest")})
	decide(t, plan, filepath.Join(root, "dest", "d"), DecisionMerge)
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: li.hooks()})
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone {
			t.Errorf("%s: %+v, want Done", it.Src, it)
		}
	}
	if names := testfs.ListNames(t, filepath.Join(root, "src")); len(names) != 0 {
		t.Errorf("src has %v, want nothing", names)
	}
	if got := testfs.ReadFile(t, filepath.Join(root, "dest", "d", "x.txt")); got != "x" {
		t.Errorf("dest/d/x.txt = %q", got)
	}
	if li.attempts["remove"] != 3 {
		t.Errorf("remove attempts = %d, want 3 (the merged source folder)", li.attempts["remove"])
	}
}

// TestLockRetryDelete は、完全削除が一時的に使用中でも、やり直して完了することを確かめる（§17.1）。
func TestLockRetryDelete(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"target/a.txt": testfs.File("a"), "target/sub/b.txt": testfs.File("b")})
	li := newLockInjector(map[string]int{"remove": 4})
	plan := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "target")}})
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: li.hooks()})
	if it := res.Items[0]; it.Outcome != OutcomeDone {
		t.Errorf("result = %+v, want Done", it)
	}
	if testfs.Exists(t, filepath.Join(root, "target")) {
		t.Error("target still exists")
	}
	if len(li.waits) != 4 {
		t.Errorf("waits = %v, want 4", li.waits)
	}
}

// TestLockRetryDeleteLinkSwapDuringWait は、削除を待っている間にエントリがリンクに置き換えられても、
// やり直しでリンクの先を消さないことを確かめる（I4。やり直しでも §13.2 の確認をやり直す）。
func TestLockRetryDeleteLinkSwapDuringWait(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"target/f.txt": testfs.File("f"), "outside/keep.txt": testfs.File("keep")})
	f := filepath.Join(root, "target", "f.txt")
	li := newLockInjector(map[string]int{"remove": 1})
	li.onWait = func(path string, n int) {
		if path != f {
			t.Errorf("waited for %s, want %s", path, f)
		}
		if err := os.Remove(testfs.ExtendedPath(f)); err != nil {
			t.Fatal(err)
		}
		testfs.Build(t, root, testfs.Tree{"target/f.txt": testfs.Symlink(filepath.Join(root, "outside", "keep.txt"))})
	}
	plan := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "target")}})
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: li.hooks()})
	t.Logf("result: %+v", res.Items[0])
	if got := testfs.ReadFile(t, filepath.Join(root, "outside", "keep.txt")); got != "keep" {
		t.Errorf("I4 violated: outside/keep.txt = %q", got)
	}
}

// TestLockRetryDeleteCancel は、削除を待っている間にキャンセルされたら、すぐに打ち切り、ファイルが残ることを確かめる（§17.1、I7）。
func TestLockRetryDeleteCancel(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"f.txt": testfs.File("f")})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	li := newLockInjector(map[string]int{"remove": -1})
	li.onWait = func(string, int) { cancel() }
	plan := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "f.txt")}})
	res := execPlan(t, ctx, plan, ExecOptions{hooks: li.hooks()})
	if it := res.Items[0]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindCanceled {
		t.Errorf("result = %+v, want Skipped with KindCanceled", it)
	}
	if res.Status != StatusCanceled {
		t.Errorf("status = %v, want Canceled", res.Status)
	}
	if !testfs.Exists(t, filepath.Join(root, "f.txt")) {
		t.Error("f.txt was removed")
	}
	if len(li.waits) != 1 {
		t.Errorf("waits = %v, want 1", li.waits)
	}
}

// TestLockRetryMoveCrossVolume は、ボリュームをまたぐ移動で、移動元の削除（§13.3）が一時的に使用中でも、
// やり直して完了することを確かめる（§17.1）。
func TestLockRetryMoveCrossVolume(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	other := testfs.CrossVolDir(t)
	testfs.Build(t, root, testfs.Tree{"src/d/a.txt": testfs.File("a"), "src/f.txt": testfs.File("f")})
	li := newLockInjector(map[string]int{"remove": 3})
	plan := mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(root, "src", "d"), filepath.Join(root, "src", "f.txt")}, DestDir: other})
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: li.hooks()})
	for _, it := range res.Items {
		if it.Outcome != OutcomeDone {
			t.Errorf("%s: %+v, want Done", it.Src, it)
		}
	}
	if names := testfs.ListNames(t, filepath.Join(root, "src")); len(names) != 0 {
		t.Errorf("src has %v, want nothing", names)
	}
	if li.attempts["remove"] < 4 {
		t.Errorf("remove attempts = %d, want at least 4", li.attempts["remove"])
	}
}

// TestLockRetryRename は、Rename が一時的に使用中でも、やり直して名前を変えることを確かめる（§17.1）。
func TestLockRetryRename(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"a.txt": testfs.File("a")})
	li := newLockInjector(map[string]int{"rename": 2})
	if err := renameWith(newLockRetrier(context.Background(), li.hooks()), filepath.Join(root, "a.txt"), "b.txt"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if got := testfs.ListNames(t, root); !slices.Equal(got, []string{"b.txt"}) {
		t.Errorf("names = %v, want [b.txt]", got)
	}
	if want := ms(10, 20); !slices.Equal(li.waits, want) {
		t.Errorf("waits = %v, want %v", li.waits, want)
	}
}

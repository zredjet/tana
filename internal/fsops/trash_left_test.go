package fsops

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// ごみ箱へ移す操作が成功を返したのに、元の場所に項目が残っている場合（§12.1。総点検の穴 11）。
// 本物のごみ箱では起こせないので、ごみ箱へ移す呼び出しをフック（trashCall）で差し替える。本物のごみ箱には触れない。

// TestTrashReportedButLeft は、ごみ箱へ移す操作が成功を返しても元の場所に残っている項目を、OutcomeFailed（KindUnknown）にし、
// その項目に手を付けず、ほかの項目は続けることを確かめる。元の場所から消えた項目（ごみ箱に入ったことにする）は Done になる。
func TestTrashReportedButLeft(t *testing.T) {
	t.Parallel()
	if !trashAvailableHere() {
		// 計画時の事前確認で KindTrashUnavailable になり、ごみ箱へ移す呼び出しまで進まない（TestTrashUnavailable が確かめる）。
		t.Skipf("the trash is not available on %s (cgo=%v)", runtime.GOOS, cgoEnabled)
	}
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/left.txt": testfs.File("left"), "src/leftdir/x": testfs.File("x"), "src/gone.txt": testfs.File("gone"),
		"fake-trash": testfs.Dir(),
	})
	src := filepath.Join(root, "src")
	keep := func(p string) bool { return filepath.Base(p) != "gone.txt" }
	var called []string
	hooks := &testHooks{trashCall: func(p string, _ EntryInfo) (string, error) {
		called = append(called, p)
		if keep(p) {
			return filepath.Join(root, "fake-trash", filepath.Base(p)), nil // 成功を返すが、何もしない
		}
		to := filepath.Join(root, "fake-trash", filepath.Base(p))
		if err := os.Rename(testfs.ExtendedPath(p), testfs.ExtendedPath(to)); err != nil {
			t.Errorf("fake trash: %v", err)
		}
		return to, nil
	}}
	before := testfs.Take(t, src)
	names := []string{"left.txt", "leftdir", "gone.txt"}
	var srcs []string
	for _, n := range names {
		srcs = append(srcs, filepath.Join(src, n))
	}
	plan := mustPlan(t, Request{Op: OpTrash, Sources: srcs})
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: hooks})
	if len(called) != len(names) {
		t.Fatalf("trash calls = %q, want one per item", called)
	}
	for _, it := range res.Items {
		if keep(it.Src) {
			if it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindUnknown || it.TrashedPath != "" {
				t.Errorf("%s: %+v, want Failed with KindUnknown and no TrashedPath (reported as trashed but still there)", it.Src, it)
			}
			continue
		}
		if want := filepath.Join(root, "fake-trash", "gone.txt"); it.Outcome != OutcomeDone || it.Err != nil || it.TrashedPath != want {
			t.Errorf("%s: %+v, want Done with TrashedPath %s", it.Src, it, want)
		}
	}
	// 残った項目は元のまま（消えたのは gone.txt だけ。src 自身は gone.txt が消えて更新日時が変わるので比べない）。
	after := testfs.Take(t, src)
	delete(before, "gone.txt")
	delete(before, ".")
	delete(after, ".")
	if d := testfs.Diff(before, after); d != nil {
		t.Errorf("the items left in place changed: %q", d)
	}
}

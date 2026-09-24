package fsops

import (
	"context"
	"errors"
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

// TestTrashOutcomeFromState は、ごみ箱へ移す呼び出しの結果を、呼び出しが返した成否ではなく呼び出しの後の状態で決めることを確かめる（§12.1）。
// 呼び出しはフック（trashCall）で差し替え、本物のごみ箱には触れない。
func TestTrashOutcomeFromState(t *testing.T) {
	t.Parallel()
	if !trashAvailableHere() {
		t.Skipf("the trash is not available on %s (cgo=%v)", runtime.GOOS, cgoEnabled)
	}
	failure := errors.New("injected trash failure")
	for _, tc := range []struct {
		name      string
		act       string // "delete"（消す）、"move"（偽のごみ箱へ移す）、"none"（何もしない）
		path      string // 返すパス: "moved"（移した先）、"missing"（存在しないパス）、""（なし）
		err       error  // 返すエラー
		outcome   Outcome
		kind      Kind // Err の Kind（Done では Err が nil）
		trashPath bool // TrashedPath が移した先になる
	}{
		{"deleted, no path, success", "delete", "", nil, OutcomeTrashUnconfirmed, KindUnknown, false},
		{"deleted, no path, not recycled", "delete", "", &OpError{Op: "trash", Kind: KindTrashUnavailable}, OutcomeTrashUnconfirmed, KindTrashUnavailable, false},
		{"deleted, missing path", "delete", "missing", nil, OutcomeTrashUnconfirmed, KindUnknown, false},
		{"moved, path, failure reported", "move", "moved", failure, OutcomeDone, 0, true},
		{"moved, path, success", "move", "moved", nil, OutcomeDone, 0, true},
		{"left, failure", "none", "", &OpError{Op: "trash", Kind: KindPermission}, OutcomeFailed, KindPermission, false},
		{"left, success", "none", "moved", nil, OutcomeFailed, KindUnknown, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			testfs.Build(t, root, testfs.Tree{"item.txt": testfs.File("item"), "fake-trash": testfs.Dir()})
			item := filepath.Join(root, "item.txt")
			moved := filepath.Join(root, "fake-trash", "item.txt")
			hooks := &testHooks{trashCall: func(p string, _ EntryInfo) (string, error) {
				switch tc.act {
				case "delete":
					if err := os.Remove(testfs.ExtendedPath(p)); err != nil {
						t.Errorf("fake trash: %v", err)
					}
				case "move":
					if err := os.Rename(testfs.ExtendedPath(p), testfs.ExtendedPath(moved)); err != nil {
						t.Errorf("fake trash: %v", err)
					}
				}
				switch tc.path {
				case "moved":
					return moved, tc.err
				case "missing":
					return filepath.Join(root, "fake-trash", "missing.txt"), tc.err
				}
				return "", tc.err
			}}
			res := execPlan(t, context.Background(), mustPlan(t, Request{Op: OpTrash, Sources: []string{item}}), ExecOptions{hooks: hooks})
			it := res.Items[0]
			if it.Outcome != tc.outcome {
				t.Errorf("outcome = %v, want %v (%+v)", it.Outcome, tc.outcome, it)
			}
			switch {
			case tc.outcome == OutcomeDone && it.Err != nil:
				t.Errorf("err = %v, want nil", it.Err)
			case tc.outcome != OutcomeDone && (it.Err == nil || it.Err.Kind != tc.kind):
				t.Errorf("err = %v, want %v", it.Err, tc.kind)
			}
			if want := ""; tc.trashPath && it.TrashedPath != moved || !tc.trashPath && it.TrashedPath != want {
				t.Errorf("TrashedPath = %q (want the moved path: %v)", it.TrashedPath, tc.trashPath)
			}
			wantStatus := StatusCompletedWithErrors
			if tc.outcome == OutcomeDone {
				wantStatus = StatusCompleted
			}
			if res.Status != wantStatus {
				t.Errorf("status = %v, want %v", res.Status, wantStatus)
			}
			if tc.act == "none" && testfs.ReadFile(t, item) != "item" {
				t.Error("the item left in place changed")
			}
		})
	}
}

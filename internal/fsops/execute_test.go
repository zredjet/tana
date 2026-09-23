package fsops

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestExecutePreconditions は、実行前の検査（§7.1）で何も実行せず error を返すことを確かめる。
func TestExecutePreconditions(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"a": testfs.File("a"), "b": testfs.File("b"), "src/f": testfs.File("f"), "dest/f": testfs.Dir()})
	before := testfs.Take(t, root)

	var nilPlan *Plan
	if res, err := nilPlan.Execute(context.Background(), ExecOptions{}); err == nil || res != nil {
		t.Errorf("nil Plan: %v, %v; want an error", res, err)
	}
	var zero Plan
	if res, err := zero.Execute(context.Background(), ExecOptions{}); err == nil || res != nil {
		t.Errorf("zero Plan: %v, %v; want an error", res, err)
	}

	// 許されない決定（§9.1）。Decide を通さずに設定して、実行前の検査を確かめる。
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f")}, DestDir: filepath.Join(root, "dest")})
	plan.conflicts[0].Decision = DecisionOverwrite // ファイル → フォルダには許されない
	if res, err := plan.Execute(context.Background(), ExecOptions{}); KindOf(err) != KindInvalidRequest || res != nil {
		t.Errorf("invalid decision: %v, %v; want KindInvalidRequest", res, err)
	}

	// 2 回目（並行を含む）は何もせず error。
	plan = mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "a")}})
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = plan.Execute(context.Background(), ExecOptions{})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if KindOf(err) != KindInvalidRequest {
			t.Errorf("second Execute: %v, want KindInvalidRequest", err)
		}
	}
	if ok != 1 {
		t.Errorf("%d of 4 concurrent Execute calls ran, want exactly 1", ok)
	}
	if err := plan.Decide(1, DecisionSkip); err == nil {
		t.Error("Decide after Execute = nil, want an error")
	}
	after := testfs.Take(t, root)
	delete(before, "a")
	for rel, n := range before {
		if after[rel] != n && rel != "." {
			t.Errorf("%s changed: %+v -> %+v", rel, n, after[rel])
		}
	}
}

// TestExecuteItemErrAndStatus は、計画時の Item.Err が Failed になり、Status が §7.4 に従うことを確かめる。
func TestExecuteItemErrAndStatus(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"a": testfs.File("a")})
	plan := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "missing"), filepath.Join(root, "a")}})
	itemErr := plan.Items()[0].Err
	res, err := plan.Execute(context.Background(), ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != itemErr.Kind || it.Src != filepath.Join(root, "missing") {
		t.Errorf("item with Item.Err = %+v, want Failed with %v", it, itemErr)
	}
	if it := res.Items[1]; it.Outcome != OutcomeDone || it.Err != nil {
		t.Errorf("second item = %+v, want Done", it)
	}
	if res.Status != StatusCompletedWithErrors {
		t.Errorf("Status = %v, want StatusCompletedWithErrors", res.Status)
	}

	testfs.Build(t, root, testfs.Tree{"ok": testfs.File("x")})
	plan2 := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "ok")}})
	if res, _ := plan2.Execute(context.Background(), ExecOptions{}); res.Status != StatusCompleted {
		t.Errorf("all done: Status = %v, want StatusCompleted", res.Status)
	}
}

// TestExecuteCanceledBeforeStart は、開始前にキャンセルされた ctx では、全項目が Skipped（KindCanceled）で StatusCanceled になることを確かめる。
func TestExecuteCanceledBeforeStart(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"a": testfs.File("a"), "b": testfs.File("b")})
	plan := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "a"), filepath.Join(root, "b")}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := plan.Execute(ctx, ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range res.Items {
		if it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindCanceled {
			t.Errorf("%s: %+v, want Skipped with KindCanceled", it.Src, it)
		}
	}
	if res.Status != StatusCanceled {
		t.Errorf("Status = %v", res.Status)
	}
	if !testfs.Exists(t, filepath.Join(root, "a")) || !testfs.Exists(t, filepath.Join(root, "b")) {
		t.Error("a file was deleted although the context was canceled")
	}
}

// TestExecuteProgress は、進捗が項目の区切りと終了時に報告され、終了時に全件が完了していることを確かめる（§16）。
// macOS の CI では -race で実行し、データ競合がないことも確かめる（§18.4「並行性」）。
func TestExecuteProgress(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	tree := testfs.Tree{}
	for i := 0; i < 30; i++ {
		tree["tree/sub/f"+string(rune('a'+i%26))+string(rune('a'+i/26))] = testfs.File("0123456789")
	}
	tree["single"] = testfs.File("abc")
	testfs.Build(t, root, tree)
	plan := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "tree"), filepath.Join(root, "single")}})
	var got []Progress
	res, err := plan.Execute(context.Background(), ExecOptions{Progress: func(p Progress) { got = append(got, p) }})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusCompleted {
		t.Fatalf("Status = %v", res.Status)
	}
	if len(got) < 3 { // 項目の区切り 2 回と終了時
		t.Fatalf("progress called %d times, want at least 3", len(got))
	}
	last := got[len(got)-1]
	if last.DoneFiles != plan.TotalFiles() || last.TotalFiles != plan.TotalFiles() || last.DoneBytes != plan.TotalBytes() || last.TotalBytes != plan.TotalBytes() {
		t.Errorf("final progress = %+v, want all %d files / %d bytes done", last, plan.TotalFiles(), plan.TotalBytes())
	}
	for i, p := range got {
		if p.Stage != StageDelete {
			t.Errorf("progress[%d].Stage = %v, want StageDelete", i, p.Stage)
		}
		if i > 0 && (p.DoneFiles < got[i-1].DoneFiles || p.DoneBytes < got[i-1].DoneBytes) {
			t.Errorf("progress went backwards: %+v -> %+v", got[i-1], p)
		}
	}
}

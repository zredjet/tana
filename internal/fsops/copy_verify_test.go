package fsops

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// appendTo は、ファイル path の末尾に data を書き足す。
func appendTo(t *testing.T, path, data string) {
	t.Helper()
	f, err := os.OpenFile(testfs.ExtendedPath(path), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Error(err)
		return
	}
	defer f.Close()
	if _, err := f.WriteString(data); err != nil {
		t.Error(err)
	}
}

// TestCopySourceChangedDuringCopy は、コピー中にコピー元が変更されたら KindSourceChanged で失敗し、
// 最終名のファイルも一時ファイルも残らないことを確かめる（§10.4、§18.4「検証」、I3）。
func TestCopySourceChangedDuringCopy(t *testing.T) {
	t.Parallel()
	for _, mode := range []VerifyMode{VerifySize, VerifyHash} {
		t.Run(mode.String(), func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			testfs.Build(t, root, testfs.Tree{
				"src/f.bin": testfs.File(strings.Repeat("x", 2*copyBufSize+3)), "src/tree/g.bin": testfs.File(strings.Repeat("y", copyBufSize+3)),
				"src/next.txt": testfs.File("n"), "dest": testfs.Dir(),
			})
			dest := filepath.Join(root, "dest")
			plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.bin"), filepath.Join(root, "src", "tree"), filepath.Join(root, "src", "next.txt")}, DestDir: dest})
			h := &testHooks{onWrite: func(dst string, written int64) error {
				if written == copyBufSize { // 最初のバッファの後にコピー元を書き足す
					rel, _ := filepath.Rel(dest, dst)
					appendTo(t, filepath.Join(root, "src", rel), "changed")
				}
				return nil
			}}
			res := execPlan(t, context.Background(), plan, ExecOptions{Verify: mode, hooks: h})
			if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindSourceChanged {
				t.Errorf("f.bin = %+v, want Failed with KindSourceChanged", it)
			}
			if it := res.Items[1]; it.Outcome != OutcomePartial || !hasKind(it, KindSourceChanged) {
				t.Errorf("tree = %+v, want Partial with KindSourceChanged", it)
			}
			if it := res.Items[2]; it.Outcome != OutcomeDone {
				t.Errorf("next.txt = %+v, want Done", it)
			}
			if testfs.Exists(t, filepath.Join(dest, "f.bin")) || testfs.Exists(t, filepath.Join(dest, "tree", "g.bin")) {
				t.Error("a file copied while its source changed has its final name")
			}
			noTempFiles(t, root)
		})
	}
}

// TestCopyVerifyHash は、VerifyHash が、書き込んだ内容と一時ファイルの内容の違いを見つけることを確かめる（§10.4）。
// 一時ファイルを同じ大きさのまま書き換える（フックで注入）と、VerifyHash では失敗し、VerifySize では見つからない。
func TestCopyVerifyHash(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		mode VerifyMode
		want Outcome
	}{{VerifySize, OutcomeDone}, {VerifyHash, OutcomeFailed}} {
		t.Run(tc.mode.String(), func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			testfs.Build(t, root, testfs.Tree{"src/f": testfs.File(strings.Repeat("a", copyBufSize+10)), "dest": testfs.Dir()})
			plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f")}, DestDir: filepath.Join(root, "dest")})
			h := &testHooks{beforeVerify: func(tmp string) {
				f, err := os.OpenFile(testfs.ExtendedPath(tmp), os.O_WRONLY, 0)
				if err != nil {
					t.Error(err)
					return
				}
				defer f.Close()
				if _, err := f.WriteAt([]byte("B"), 5); err != nil {
					t.Error(err)
				}
			}}
			var stages []Stage
			res := execPlan(t, context.Background(), plan, ExecOptions{Verify: tc.mode, hooks: h, Progress: func(p Progress) { stages = append(stages, p.Stage) }})
			it := res.Items[0]
			if it.Outcome != tc.want {
				t.Errorf("result = %+v (%v), want %v", it, it.Err, tc.want)
			}
			if tc.want == OutcomeFailed && testfs.Exists(t, filepath.Join(root, "dest", "f")) {
				t.Error("a file that failed verification has its final name")
			}
			noTempFiles(t, root)
			sawVerify := false
			for _, s := range stages {
				sawVerify = sawVerify || s == StageVerify
			}
			if sawVerify != (tc.mode == VerifyHash) {
				t.Errorf("StageVerify reported = %v, want %v", sawVerify, tc.mode == VerifyHash)
			}
		})
	}
}

// TestCopyNoSpaceInjected は、書き込み中の容量不足（フックで注入）で、ファイルは Failed、フォルダは Partial（KindNoSpace）になり、
// 残りの書き込みを伴う項目が Skipped（KindNoSpace）になり、一時ファイルが残らないことを確かめる（§10.3、§7.2）。
func TestCopyNoSpaceInjected(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	big := strings.Repeat("z", copyBufSize+1)
	testfs.Build(t, root, testfs.Tree{
		"src/a/1.txt": testfs.File("one"), "src/a/2.big": testfs.File(big), "src/a/3.txt": testfs.File("three"),
		"src/b.big": testfs.File(big), "src/c.txt": testfs.File("c"), "dest": testfs.Dir(),
	})
	dest := filepath.Join(root, "dest")
	for _, tc := range []struct {
		name  string
		srcs  []string
		first Outcome
	}{
		{"folder", []string{"a", "c.txt"}, OutcomePartial},
		{"file", []string{"b.big", "c.txt"}, OutcomeFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := filepath.Join(dest, tc.name)
			testfs.MkdirAll(t, d)
			var srcs []string
			for _, s := range tc.srcs {
				srcs = append(srcs, filepath.Join(root, "src", s))
			}
			plan := mustPlan(t, Request{Op: OpCopy, Sources: srcs, DestDir: d})
			h := &testHooks{onWrite: func(dst string, written int64) error {
				if strings.HasSuffix(dst, ".big") {
					return &os.PathError{Op: "write", Path: dst, Err: noSpaceErr()}
				}
				return nil
			}}
			res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
			if it := res.Items[0]; it.Outcome != tc.first || it.Err == nil || it.Err.Kind != KindNoSpace {
				t.Errorf("first = %+v, want %v with KindNoSpace", it, tc.first)
			}
			if it := res.Items[1]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindNoSpace {
				t.Errorf("second = %+v, want Skipped with KindNoSpace", it)
			}
			if tc.name == "folder" {
				wantFiles(t, d, map[string]string{"a/1.txt": "one"})
				if testfs.Exists(t, filepath.Join(d, "a", "3.txt")) {
					t.Error("the folder copy continued after running out of space")
				}
			}
			if testfs.Exists(t, filepath.Join(d, "c.txt")) {
				t.Error("an item after running out of space was copied")
			}
			noTempFiles(t, root)
		})
	}
}

// TestCopyNoSpaceCrossVolume は、コピー先のボリュームが実際に一杯になったとき、KindNoSpace で失敗し、残りが Skipped になり、
// 一時ファイルが残らないことを確かめる（§18.4「容量」。CROSSVOL）。
// ボリュームを一杯にするので t.Parallel しない（CI では go test -p 1 で、ほかのパッケージとも並行させない）。
func TestCopyNoSpaceCrossVolume(t *testing.T) {
	cross := testfs.CrossVolDir(t)
	free, err := freeSpace(cross)
	if err != nil {
		t.Fatal(err)
	}
	if free > 1<<30 {
		t.Skipf("the volume of %s has %d bytes free; too large to fill in a test", testfs.CrossVolEnv, free)
	}
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"small.txt": testfs.File("s")})
	big := filepath.Join(root, "big.bin")
	f, err := os.Create(testfs.ExtendedPath(big))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(int64(free) + 16<<20); err != nil { // 空き容量より 16 MiB 大きい疎なファイル
		f.Close()
		t.Fatal(err)
	}
	f.Close()
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{big, filepath.Join(root, "small.txt")}, DestDir: cross})
	if ws := plan.Warnings(); len(ws) != 1 || ws[0].Kind != KindNoSpace {
		t.Errorf("Warnings = %+v, want one KindNoSpace", ws)
	}
	res := execPlan(t, context.Background(), plan, ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindNoSpace {
		t.Errorf("big = %+v (%v), want Failed with KindNoSpace", it, it.Err)
	}
	if it := res.Items[1]; it.Outcome != OutcomeSkipped || it.Err == nil || it.Err.Kind != KindNoSpace {
		t.Errorf("small = %+v, want Skipped with KindNoSpace", it)
	}
	if names := testfs.ListNames(t, cross); len(names) != 0 {
		t.Errorf("left on the volume: %+q", names)
	}
	after, err := freeSpace(cross)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("free space: before %d, after %d", free, after)
}

// TestCopyVerifyReadsWholeFile は、VerifyHash で、一時ファイルの読み直しが最後まで読むことを確かめる（大きいファイル）。
func TestCopyVerifyReadsWholeFile(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	data := strings.Repeat("0123456789", 3*copyBufSize/10+7)
	testfs.Build(t, root, testfs.Tree{"src/f": testfs.File(data), "dest": testfs.Dir()})
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f")}, DestDir: filepath.Join(root, "dest")})
	res := execPlan(t, context.Background(), plan, ExecOptions{Verify: VerifyHash, Sync: SyncAlways})
	if it := res.Items[0]; it.Outcome != OutcomeDone || len(it.Warnings) != 0 {
		t.Fatalf("result = %+v", it)
	}
	f, err := os.Open(testfs.ExtendedPath(filepath.Join(root, "dest", "f")))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := io.ReadAll(f)
	if err != nil || string(got) != data {
		t.Errorf("content differs (%v)", errors.Join(err))
	}
}

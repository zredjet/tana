package fsops

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// fatMaxFile は、FAT 系のファイルシステムのファイルの大きさの上限（§10.6）。
const fatMaxFile = 1<<32 - 1

// TestFileSizeLimit は、ファイルシステムの種類からファイルの大きさの上限を決めることを確かめる（§10.6）。
// パス（計画用）と、確かめて開いたフォルダ（実行用）の両方で調べる。
func TestFileSizeLimit(t *testing.T) {
	t.Parallel()
	check := func(t *testing.T, dir string, want int64) {
		t.Helper()
		if got := fileSizeLimitPath(dir); got != want {
			t.Errorf("fileSizeLimitPath(%s) = %d, want %d", dir, got, want)
		}
		st, err := fileIDFollow(dir)
		if err != nil {
			t.Fatal(err)
		}
		d, err := openDestRoot(dir, st.id)
		if err != nil {
			t.Fatal(err)
		}
		defer d.close()
		if got := d.fileSizeLimit(); got != want {
			t.Errorf("secDir(%s).fileSizeLimit() = %d, want %d", dir, got, want)
		}
	}
	t.Run("temp dir", func(t *testing.T) { check(t, testfs.TempDir(t), 0) })
	t.Run("FAT32", func(t *testing.T) { check(t, testfs.EnvDir(t, testfs.FAT32Env), fatMaxFile) })
	t.Run("exFAT", func(t *testing.T) { check(t, testfs.EnvDir(t, testfs.ExFATEnv), 0) })
}

// tooLargeTree は、上限を超えるファイル（big.bin、dir/big2.bin）と超えないファイルを、トップレベルとフォルダの中に作り、コピー元のパスを返す。
// big が真なら、超えるファイルは FAT32 の上限を超える穴だけのファイルにし、偽ならフックの上限（10 バイト）を超える小さなファイルにする。
func tooLargeTree(t *testing.T, root string, big bool) []string {
	t.Helper()
	testfs.Build(t, root, testfs.Tree{"src/small.txt": testfs.File("0123456789"), "src/dir/ok.txt": testfs.File("ok")})
	if big {
		testfs.SparseFile(t, filepath.Join(root, "src", "big.bin"), fatMaxFile+1)
		testfs.SparseFile(t, filepath.Join(root, "src", "dir", "big2.bin"), fatMaxFile+1)
	} else {
		testfs.Build(t, root, testfs.Tree{"src/big.bin": testfs.File("01234567890"), "src/dir/big2.bin": testfs.File("01234567890123")})
	}
	return []string{filepath.Join(root, "src", "big.bin"), filepath.Join(root, "src", "small.txt"), filepath.Join(root, "src", "dir")}
}

// checkTooLargeResult は、tooLargeTree のコピー・移動の結果を確かめる（§10.6）。上限を超えるファイルだけが KindFileTooLarge で失敗し、
// 残りは続き（§7.2 の打ち切りをしない）、一時ファイルを残さず、移動では失敗したファイルが移動元に残る（I2）。
func checkTooLargeResult(t *testing.T, root, dest string, res *Result) {
	t.Helper()
	big, small, dir := res.Items[0], res.Items[1], res.Items[2]
	if big.Outcome != OutcomeFailed || big.Err == nil || big.Err.Kind != KindFileTooLarge {
		t.Errorf("big.bin: %+v, want Failed with KindFileTooLarge", big)
	}
	if small.Outcome != OutcomeDone {
		t.Errorf("small.txt: %+v, want Done", small)
	}
	if dir.Outcome != OutcomePartial { // 移動でも、コピーが Partial なら移動元に手を付けずにそのまま返す（§11.2）
		t.Errorf("dir: %+v, want Partial", dir)
	}
	big2 := filepath.Join(root, "src", "dir", "big2.bin")
	if !slices.ContainsFunc(dir.Details, func(e EntryResult) bool {
		return e.Src == big2 && e.Outcome == OutcomeFailed && e.Err != nil && e.Err.Kind == KindFileTooLarge
	}) {
		t.Errorf("dir details = %+v, want big2.bin Failed with KindFileTooLarge", dir.Details)
	}
	if testfs.Exists(t, filepath.Join(dest, "big.bin")) || testfs.Exists(t, filepath.Join(dest, "dir", "big2.bin")) {
		t.Error("a too large file was written")
	}
	if got := testfs.ReadFile(t, filepath.Join(dest, "dir", "ok.txt")); got != "ok" {
		t.Errorf("dir/ok.txt = %q", got)
	}
	if !testfs.Exists(t, filepath.Join(root, "src", "big.bin")) || !testfs.Exists(t, big2) {
		t.Error("I2 violated: a too large source file was removed")
	}
	noTempFiles(t, dest)
}

// TestCopyFileTooLarge は、実行時の上限（フックで 10 バイトにしたもの）を超えるファイルを、書かずに KindFileTooLarge で失敗にすることを確かめる（§10.6）。
func TestCopyFileTooLarge(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	srcs := tooLargeTree(t, root, false)
	dest := filepath.Join(root, "dest")
	testfs.MkdirAll(t, dest)
	plan := mustPlan(t, Request{Op: OpCopy, Sources: srcs, DestDir: dest})
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: &testHooks{fileSizeLimit: 10}})
	checkTooLargeResult(t, root, dest, res)
}

// TestMoveFileTooLarge は、ボリュームをまたぐ移動で、上限を超えるファイルを移動元に残すことを確かめる（§10.6、I2）。
func TestMoveFileTooLarge(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	dest := testfs.CrossVolDir(t)
	srcs := tooLargeTree(t, root, false)
	plan := mustPlan(t, Request{Op: OpMove, Sources: srcs, DestDir: dest})
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: &testHooks{fileSizeLimit: 10}})
	checkTooLargeResult(t, root, dest, res)
	if testfs.Exists(t, filepath.Join(root, "src", "small.txt")) {
		t.Error("small.txt was not moved")
	}
}

// TestCopyFileTooLargeGrows は、コピー中にコピー元が大きくなって上限を超えたときの容量不足を、KindFileTooLarge にすることを確かめる（§10.6）。
// 上限を超えていなければ、今までどおり KindNoSpace で残りを Skipped にする（§7.2）。
func TestCopyFileTooLargeGrows(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		limit int64
		kind  Kind
		next  Outcome
	}{
		{"past the limit", 10, KindFileTooLarge, OutcomeDone},
		{"within the limit", 100, KindNoSpace, OutcomeSkipped},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			testfs.Build(t, root, testfs.Tree{"src/grow.txt": testfs.File("01234567"), "src/next.txt": testfs.File("n"), "dest": testfs.Dir()})
			grow := filepath.Join(root, "src", "grow.txt")
			dest := filepath.Join(root, "dest")
			h := &testHooks{fileSizeLimit: tc.limit, onWrite: func(dst string, written int64) error {
				if filepath.Base(dst) != "grow.txt" {
					return nil
				}
				if written == 8 { // コピー元を大きくする。次の読み込みで続きが読める
					f, err := os.OpenFile(testfs.ExtendedPath(grow), os.O_WRONLY|os.O_APPEND, 0)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if _, err := f.WriteString("89abcdef"); err != nil {
						t.Fatal(err)
					}
					return nil
				}
				return &OpError{Op: "write", Path: dst, Kind: KindNoSpace} // 容量不足の注入
			}}
			plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{grow, filepath.Join(root, "src", "next.txt")}, DestDir: dest})
			res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
			if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != tc.kind {
				t.Errorf("grow.txt: %+v, want Failed with %v", it, tc.kind)
			}
			if it := res.Items[1]; it.Outcome != tc.next {
				t.Errorf("next.txt: %+v, want %v", it, tc.next)
			}
			noTempFiles(t, dest)
		})
	}
}

// TestFileTooLargeFAT32 は、FAT32 のボリュームへ、上限を超える大きさのファイル（穴だけのファイル）をコピー・移動すると、
// 計画が警告し、実行は書かずにそのファイルだけを KindFileTooLarge で失敗にすることを確かめる（§6.4、§10.6、V24）。
func TestFileTooLargeFAT32(t *testing.T) {
	t.Parallel()
	for _, op := range []OpKind{OpCopy, OpMove} {
		t.Run(op.String(), func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			dest := testfs.EnvDir(t, testfs.FAT32Env)
			srcs := tooLargeTree(t, root, true)
			plan := mustPlan(t, Request{Op: op, Sources: srcs, DestDir: dest})
			var warned []string
			for _, w := range plan.Warnings() {
				if w.Kind == KindFileTooLarge {
					warned = append(warned, w.Path)
				}
			}
			slices.Sort(warned)
			if want := []string{filepath.Join(root, "src", "big.bin"), filepath.Join(root, "src", "dir", "big2.bin")}; !slices.Equal(warned, want) {
				t.Errorf("KindFileTooLarge warnings = %q, want %q", warned, want)
			}
			res := execPlan(t, context.Background(), plan, ExecOptions{})
			checkTooLargeResult(t, root, dest, res)
		})
	}
}

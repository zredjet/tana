package fsops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// replaceTemp は、フォルダ dir にある fsops の一時ファイル（.fsops-*.tmp）を、中身の違う別のファイルに置き換え、そのパスを返す。
func replaceTemp(t *testing.T, dir string) string {
	t.Helper()
	for _, name := range testfs.ListNames(t, dir) {
		if strings.HasPrefix(name, ".fsops-") {
			p := filepath.Join(dir, name)
			if err := os.Remove(testfs.ExtendedPath(p)); err != nil {
				t.Errorf("inject: %v", err)
				return ""
			}
			testfs.WriteFile(t, p, "intruder")
			return p
		}
	}
	t.Error("inject: no temporary file")
	return ""
}

// TestCopyTempReplacedBeforeFinalRename は、書き終えた一時ファイルが、最終名にする直前に別のファイルへ置き換えられても（フックで注入）、
// その置き換えたファイルを最終名にせず（新規・上書き・自動リネームのどれでも）、消しもしないことを確かめる（I1・I3。総点検の穴 3）。
func TestCopyTempReplacedBeforeFinalRename(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		decision Decision
		final    string // 最終名（自動リネームでは候補の名前）
		old      string // コピー先に元からある内容（なければ空）
	}{
		{"new", DecisionUnset, "f.txt", ""},
		{"overwrite", DecisionOverwrite, "f.txt", "old"},
		{"autorename", DecisionAutoRename, "f (2).txt", "old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("copied"), "dest": testfs.Dir()})
			dest := filepath.Join(root, "dest")
			if tc.old != "" {
				testfs.WriteFile(t, filepath.Join(dest, "f.txt"), tc.old)
			}
			plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: dest})
			if tc.decision != DecisionUnset {
				decide(t, plan, filepath.Join(dest, "f.txt"), tc.decision)
			}
			var intruder string
			h := &testHooks{beforeFinalRename: func(string) {
				if intruder == "" {
					intruder = replaceTemp(t, dest)
				}
			}}
			res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
			if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindDestChanged || !it.Err.OnDest {
				t.Errorf("result = %+v, want Failed with KindDestChanged on the destination side", it)
			}
			finalPath := filepath.Join(dest, tc.final)
			if testfs.Exists(t, finalPath) && testfs.ReadFile(t, finalPath) == "intruder" {
				t.Errorf("the replaced temporary file was given the final name %s", tc.final)
			}
			if tc.old != "" {
				wantFiles(t, dest, map[string]string{"f.txt": tc.old})
			}
			if intruder != "" && (!testfs.Exists(t, intruder) || testfs.ReadFile(t, intruder) != "intruder") {
				t.Error("the file that replaced the temporary file was removed")
			}
		})
	}
}

// TestTempReplacedBeforeVerify は、書き終えて fileID を記録した一時ファイルが、検証（§10.4）の前に別のファイルへ置き換えられ、
// 検証などで失敗した場合に、置き換えたファイルを消さないことを確かめる（§10.1 の手順 8。fileID が一致する場合だけ削除する）。
// VerifyHash では一時ファイルの読み直しで、VerifySize ではコピー元の変化で失敗させる。
func TestTempReplacedBeforeVerify(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		verify      VerifyMode
		touchSource bool
		kind        Kind // 一時ファイルの置き換えはコピー先側、コピー元の変化はコピー元側
	}{
		{"hash", VerifyHash, false, KindDestChanged},
		{"size with source changed", VerifySize, true, KindSourceChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("copied"), "dest": testfs.Dir()})
			src := filepath.Join(root, "src", "f.txt")
			dest := filepath.Join(root, "dest")
			plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{src}, DestDir: dest})
			var intruder string
			h := &testHooks{beforeVerify: func(string) {
				intruder = replaceTempNewID(t, dest)
				if tc.touchSource {
					testfs.WriteFile(t, src, "changed source")
				}
			}}
			res := execPlan(t, context.Background(), plan, ExecOptions{Verify: tc.verify, hooks: h})
			if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != tc.kind {
				t.Errorf("result = %+v, want Failed with %v", it, tc.kind)
			}
			if intruder == "" {
				t.Fatal("no injection")
			}
			if !testfs.Exists(t, intruder) {
				t.Fatal("the file that replaced the temporary file was removed (§10.1 step 8)")
			}
			if got := testfs.ReadFile(t, intruder); got != "intruder" {
				t.Errorf("the file that replaced the temporary file = %q", got)
			}
		})
	}
}

// replaceTempNewID は replaceTemp と同じく一時ファイルを別のファイル（"intruder"）に置き換えるが、新しいファイルを別名で作ってから
// 名前を置き換えるので、fileID は必ず変わる（先に消すと、ext4 は番号を再利用しうる。V16）。
func replaceTempNewID(t *testing.T, dir string) string {
	t.Helper()
	for _, name := range testfs.ListNames(t, dir) {
		if strings.HasPrefix(name, ".fsops-") {
			p := filepath.Join(dir, name)
			other := filepath.Join(dir, "intruder.new")
			testfs.WriteFile(t, other, "intruder")
			if err := os.Rename(testfs.ExtendedPath(other), testfs.ExtendedPath(p)); err != nil {
				t.Errorf("inject: %v", err)
				return ""
			}
			return p
		}
	}
	t.Error("inject: no temporary file")
	return ""
}

// TestTempReplacedDuringFinalRename は、一時ファイルを確かめてから最終名にするまでの間に、一時ファイルが別のファイルに置き換えられた場合
// （フックで注入）、最終名には置き換えたものが置かれているので、Failed ではなく Partial（KindSourceChanged）と報告することを確かめる
// （§10.1 の手順 7、§7.4）。新規と上書きの両方。Windows の exFAT・FAT32 では最終名にした後に確かめないので、一時フォルダだけで行う。
func TestTempReplacedDuringFinalRename(t *testing.T) {
	t.Parallel()
	for _, overwrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "overwrite"}[overwrite], func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("copied"), "dest": testfs.Dir()})
			dest := filepath.Join(root, "dest")
			dst := filepath.Join(dest, "f.txt")
			if overwrite {
				testfs.WriteFile(t, dst, "old")
			}
			plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: dest})
			if overwrite {
				decide(t, plan, dst, DecisionOverwrite)
			}
			injected := false
			h := &testHooks{lockFault: func(op, p string) bool {
				// 一時ファイルを確かめた直後、リネームの直前に呼ばれる。使用中の失敗は注入しない。
				if op == "rename" && !injected {
					injected = true
					replaceTempNewID(t, dest)
				}
				return false
			}}
			res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
			if !injected {
				t.Fatal("no injection")
			}
			if it := res.Items[0]; it.Outcome != OutcomePartial || it.Err == nil || it.Err.Kind != KindDestChanged || !it.Err.OnDest {
				t.Errorf("result = %+v, want Partial with KindDestChanged on the destination side (something was placed at the final name)", it)
			}
			if got := testfs.ReadFile(t, dst); got != "intruder" {
				t.Errorf("final name = %q, want the replacing file", got)
			}
		})
	}
}

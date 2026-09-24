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
			if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindSourceChanged {
				t.Errorf("result = %+v, want Failed with KindSourceChanged", it)
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

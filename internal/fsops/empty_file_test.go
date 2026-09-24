package fsops

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestEmptyFilesOtherVolumes は、exFAT・FAT32 のボリュームへの・からの、空のファイルのコピー・移動・上書きが Done になり、
// 一時ファイルが残らないことを確かめる（§8.3。macOS では空のファイルの fileID が操作のたびに変わる。V25）。
func TestEmptyFilesOtherVolumes(t *testing.T) {
	t.Parallel()
	for _, env := range []string{testfs.ExFATEnv, testfs.FAT32Env} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			vol := testfs.EnvDir(t, env)
			local := testfs.TempDir(t)
			testfs.Build(t, local, testfs.Tree{"out/empty.txt": testfs.File(""), "out/tree/zero": testfs.File(""), "back": testfs.Dir()})
			testfs.Build(t, vol, testfs.Tree{"in/e1.txt": testfs.File(""), "in/e2.txt": testfs.File(""), "in/over.txt": testfs.File(""),
				"dest": testfs.Dir(), "dest/over.txt": testfs.File("")})
			run := func(name string, req Request, overwrite string) {
				t.Helper()
				plan := mustPlan(t, req)
				if overwrite != "" {
					decide(t, plan, overwrite, DecisionOverwrite)
				}
				res := execPlan(t, context.Background(), plan, ExecOptions{})
				for _, it := range res.Items {
					if it.Outcome != OutcomeDone {
						t.Errorf("%s: %s: %+v, want Done", name, it.Src, it)
					}
				}
			}
			dest := filepath.Join(vol, "dest")
			run("copy to the volume", Request{Op: OpCopy, Sources: []string{filepath.Join(local, "out", "empty.txt"), filepath.Join(local, "out", "tree")}, DestDir: dest}, "")
			run("copy from the volume", Request{Op: OpCopy, Sources: []string{filepath.Join(vol, "in", "e1.txt")}, DestDir: filepath.Join(local, "back")}, "")
			run("overwrite on the volume", Request{Op: OpCopy, Sources: []string{filepath.Join(vol, "in", "over.txt")}, DestDir: dest}, filepath.Join(dest, "over.txt"))
			run("move within the volume", Request{Op: OpMove, Sources: []string{filepath.Join(vol, "in", "e2.txt")}, DestDir: dest}, "")
			run("move from the volume", Request{Op: OpMove, Sources: []string{filepath.Join(vol, "in", "e1.txt")}, DestDir: filepath.Join(local, "back", "..")}, "")
			wantFiles(t, dest, map[string]string{"empty.txt": "", "tree/zero": "", "over.txt": "", "e2.txt": ""})
			wantFiles(t, filepath.Join(vol, "in"), map[string]string{"over.txt": ""})
			noTempFiles(t, vol)
			noTempFiles(t, local)
		})
	}
}

package fsops

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/unix"
)

// macOS の exFAT・FAT32 の AppleDouble ファイル（`._名前`）と、§15 で保持する com.apple.quarantine（総点検の穴 12、V22）。
// FAT 系のボリュームでは、拡張属性は `._名前` に保存され、列挙にはそれも通常のファイルとして現れる。
// コピー元・移動元が FAT 系のボリュームにあっても、各操作の後に com.apple.quarantine が残ることを確かめる。

// TestAppleDoubleQuarantineOtherVolumes は、exFAT・FAT32 の中のファイル（拡張属性つき）のコピー・移動の後に、
// 移動先・コピー先のファイルに com.apple.quarantine が残ることを確かめる（§15）。
func TestAppleDoubleQuarantineOtherVolumes(t *testing.T) {
	t.Parallel()
	const q = "0081;6500a000;Safari;"
	// setup は、root/src/m/dl.txt（com.apple.quarantine つき）と root/src/m/plain.txt を作り、移動先 root/dest/m（old.txt 入り）を作る。
	setup := func(t *testing.T, root string) {
		testfs.Build(t, root, testfs.Tree{
			"src/m/dl.txt": testfs.File("downloaded"), "src/m/plain.txt": testfs.File("plain"), "dest/m/old.txt": testfs.File("old"),
		})
		if err := unix.Lsetxattr(filepath.Join(root, "src", "m", "dl.txt"), quarantineName, []byte(q), 0); err != nil {
			t.Fatal(err)
		}
		t.Logf("src/m = %+q", testfs.ListNames(t, filepath.Join(root, "src", "m")))
	}
	// check は、dir/dl.txt と dir/plain.txt の内容と、dl.txt の com.apple.quarantine を確かめる。
	check := func(t *testing.T, dir string) {
		t.Helper()
		t.Logf("%s = %+q", dir, testfs.ListNames(t, dir))
		wantFiles(t, dir, map[string]string{"dl.txt": "downloaded", "plain.txt": "plain"})
		if got := quarantine(t, filepath.Join(dir, "dl.txt")); string(got) != q {
			t.Errorf("%s/dl.txt: quarantine = %q, want %q (§15)", dir, got, q)
		}
	}
	done := func(t *testing.T, res *Result) {
		t.Helper()
		for _, it := range res.Items {
			if it.Outcome != OutcomeDone {
				t.Errorf("%s: %+v, want Done", it.Src, it)
			}
		}
	}
	for _, env := range []string{testfs.ExFATEnv, testfs.FAT32Env} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			t.Run("copy", func(t *testing.T) {
				t.Parallel()
				root := testfs.EnvDir(t, env)
				setup(t, root)
				dest := filepath.Join(root, "dest")
				done(t, execPlan(t, context.Background(), mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "m")}, DestDir: filepath.Join(dest, "m")}), ExecOptions{}))
				check(t, filepath.Join(dest, "m", "m"))
			})
			t.Run("copy merge", func(t *testing.T) {
				t.Parallel()
				root := testfs.EnvDir(t, env)
				setup(t, root)
				dest := filepath.Join(root, "dest")
				plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "m")}, DestDir: dest})
				decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
				done(t, execPlan(t, context.Background(), plan, ExecOptions{}))
				check(t, filepath.Join(dest, "m"))
			})
			t.Run("move", func(t *testing.T) {
				t.Parallel()
				root := testfs.EnvDir(t, env)
				setup(t, root)
				dest := filepath.Join(root, "dest")
				done(t, execPlan(t, context.Background(), mustPlan(t, Request{Op: OpMove, Sources: []string{filepath.Join(root, "src", "m")}, DestDir: filepath.Join(dest, "m")}), ExecOptions{}))
				check(t, filepath.Join(dest, "m", "m"))
			})
			t.Run("move merge", func(t *testing.T) {
				t.Parallel()
				root := testfs.EnvDir(t, env)
				setup(t, root)
				dest := filepath.Join(root, "dest")
				plan := sameMove(t, root, "m")
				decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
				done(t, execPlan(t, context.Background(), plan, ExecOptions{}))
				check(t, filepath.Join(dest, "m"))
			})
			t.Run("move across volumes", func(t *testing.T) {
				t.Parallel()
				root := testfs.EnvDir(t, env)
				setup(t, root)
				dest := testfs.TempDir(t)
				done(t, execPlan(t, context.Background(), crossMove(t, root, dest, "m"), ExecOptions{}))
				check(t, filepath.Join(dest, "m"))
				if testfs.Exists(t, filepath.Join(root, "src", "m")) {
					t.Errorf("left in the source: %+q", testfs.ListNames(t, filepath.Join(root, "src", "m")))
				}
			})
		})
	}
}

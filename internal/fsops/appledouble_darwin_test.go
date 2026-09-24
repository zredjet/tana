package fsops

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/unix"
)

// macOS の exFAT・FAT32 の AppleDouble ファイル（`._名前`）と、§15 で保持する com.apple.quarantine（総点検の穴 12、V22、§8.5）。
// FAT 系のボリュームでは、拡張属性は `._名前` に保存され、列挙にはそれも通常のファイルとして現れる。OS は名前の変更・削除で
// `._名前` を `名前` と一緒に移す・消す。fsops は `名前` があるときの `._名前` を付属として扱い、独立した項目にしない。
// コピー元・移動元が FAT 系のボリュームにあっても、各操作の後に com.apple.quarantine が残り、付属がファイルとして増えないことを確かめる。
// `名前` のない `._名前`（孤立したもの）は通常のファイルとして扱う。

// TestAppleDoubleQuarantineOtherVolumes は、exFAT・FAT32 の中のファイル（拡張属性つき）のコピー・移動の後に、
// 移動先・コピー先のファイルに com.apple.quarantine が残ることを確かめる（§15）。
func TestAppleDoubleQuarantineOtherVolumes(t *testing.T) {
	t.Parallel()
	const q = "0081;6500a000;Safari;"
	// setup は、root/src/m/dl.txt（com.apple.quarantine つき）と root/src/m/plain.txt、孤立した root/src/m/._orphan.txt を作り、
	// 移動先 root/dest/m（old.txt 入り）を作る。
	setup := func(t *testing.T, root string) {
		testfs.Build(t, root, testfs.Tree{
			"src/m/dl.txt": testfs.File("downloaded"), "src/m/plain.txt": testfs.File("plain"), "src/m/._orphan.txt": testfs.File("orphan"),
			"dest/m/old.txt": testfs.File("old"),
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
		wantFiles(t, dir, map[string]string{"dl.txt": "downloaded", "plain.txt": "plain", "._orphan.txt": "orphan"})
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
				// APFS のコピー先には、付属がファイルとして増えない（孤立したものだけがファイルとしてコピーされる）。
				if got := testfs.ListRawNames(t, filepath.Join(dest, "m")); !slices.Equal(got, []string{"._orphan.txt", "dl.txt", "plain.txt"}) {
					t.Errorf("names on APFS = %+q, want [._orphan.txt dl.txt plain.txt] (no AppleDouble files copied as files)", got)
				}
				if testfs.Exists(t, filepath.Join(root, "src", "m")) {
					t.Errorf("left in the source: %+q", testfs.ListRawNames(t, filepath.Join(root, "src", "m")))
				}
			})
			t.Run("move merge skip", func(t *testing.T) {
				// スキップした dl.txt は、付属（com.apple.quarantine）ごと移動元に残る。衝突は dl.txt だけに出る。
				t.Parallel()
				root := testfs.EnvDir(t, env)
				setup(t, root)
				testfs.WriteFile(t, filepath.Join(root, "dest", "m", "dl.txt"), "other")
				dest := filepath.Join(root, "dest")
				plan := sameMove(t, root, "m")
				var names []string
				for _, c := range plan.Conflicts() {
					names = append(names, filepath.Base(c.Dst))
				}
				if !slices.Equal(names, []string{"m", "dl.txt"}) {
					t.Errorf("conflicts = %+q, want [m dl.txt]", names)
				}
				decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
				decide(t, plan, filepath.Join(dest, "m", "dl.txt"), DecisionSkip)
				res := execPlan(t, context.Background(), plan, ExecOptions{})
				if it := res.Items[0]; it.Outcome != OutcomeDone || len(it.Details) != 1 || filepath.Base(it.Details[0].Src) != "dl.txt" {
					t.Errorf("result = %+v, want Done with only dl.txt kept by the skip", it)
				}
				src := filepath.Join(root, "src", "m")
				wantFiles(t, src, map[string]string{"dl.txt": "downloaded"})
				if got := quarantine(t, filepath.Join(src, "dl.txt")); string(got) != q {
					t.Errorf("src/m/dl.txt: quarantine = %q, want %q", got, q)
				}
				wantFiles(t, filepath.Join(dest, "m"), map[string]string{"dl.txt": "other", "plain.txt": "plain", "._orphan.txt": "orphan", "old.txt": "old"})
			})
			t.Run("delete", func(t *testing.T) {
				t.Parallel()
				root := testfs.EnvDir(t, env)
				setup(t, root)
				done(t, execDelete(t, context.Background(), nil, filepath.Join(root, "src", "m")))
				if got := testfs.ListRawNames(t, filepath.Join(root, "src")); len(got) != 0 {
					t.Errorf("left in src: %+q", got)
				}
			})
		})
	}
}

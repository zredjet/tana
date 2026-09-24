package fsops

import (
	"context"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// mountImage は、小さな APFS のイメージを作って mountpoint（作っておくフォルダ）にマウントする。テストの終了時に外す。
// hdiutil が使えなければ Skip する。イメージもマウントポイントも t.TempDir() の中に置く。
func mountImage(t *testing.T, mountpoint string) {
	t.Helper()
	img := filepath.Join(testfs.TempDir(t), "mnt.dmg")
	if out, err := exec.Command("hdiutil", "create", "-quiet", "-size", "4m", "-fs", "APFS", "-volname", "fsopsmnt", img).CombinedOutput(); err != nil {
		t.Skipf("hdiutil create failed (%v): %s", err, out)
	}
	if out, err := exec.Command("hdiutil", "attach", "-quiet", "-nobrowse", "-mountpoint", mountpoint, img).CombinedOutput(); err != nil {
		t.Skipf("hdiutil attach failed (%v): %s", err, out)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("hdiutil", "detach", "-force", mountpoint).CombinedOutput(); err != nil {
			t.Errorf("hdiutil detach: %v: %s", err, out)
		}
	})
}

// TestDeleteMountPoint は、完全削除で、フォルダの中のマウントポイントに入らず、マウントされたボリュームの中身を消さないこと、
// そのエントリを KindMountPoint で報告し、計画でも警告すること、トップレベルのマウントポイントは計画で KindMountPoint になることを確かめる（§13.1）。
func TestDeleteMountPoint(t *testing.T) {
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"tree/a.txt": testfs.File("a"), "tree/mnt": testfs.Dir()})
	mnt := filepath.Join(root, "tree", "mnt")
	mountImage(t, mnt)
	testfs.WriteFile(t, filepath.Join(mnt, "onvol.txt"), "on the mounted volume")

	t.Run("top level", func(t *testing.T) {
		plan := mustPlan(t, Request{Op: OpDelete, Sources: []string{mnt}})
		if err := plan.Items()[0].Err; KindOf(err) != KindMountPoint {
			t.Errorf("Item.Err = %v, want KindMountPoint", err)
		}
		res := execPlan(t, context.Background(), plan, ExecOptions{})
		if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindMountPoint {
			t.Errorf("result = %+v, want Failed with KindMountPoint", it)
		}
		if got := testfs.ReadFile(t, filepath.Join(mnt, "onvol.txt")); got != "on the mounted volume" {
			t.Errorf("onvol.txt = %q", got)
		}
	})

	t.Run("inside a folder", func(t *testing.T) {
		plan := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "tree")}})
		if !slices.ContainsFunc(plan.Warnings(), func(w *OpError) bool { return w.Kind == KindMountPoint && w.Path == mnt }) {
			t.Errorf("plan warnings = %v, want KindMountPoint for %s", plan.Warnings(), mnt)
		}
		res := execPlan(t, context.Background(), plan, ExecOptions{})
		it := res.Items[0]
		if it.Outcome != OutcomePartial {
			t.Errorf("result = %+v, want Partial", it)
		}
		if !slices.ContainsFunc(it.Details, func(e EntryResult) bool {
			return e.Src == mnt && e.Outcome == OutcomeFailed && e.Err != nil && e.Err.Kind == KindMountPoint
		}) {
			t.Errorf("details = %+v, want %s Failed with KindMountPoint", it.Details, mnt)
		}
		if got := testfs.ReadFile(t, filepath.Join(mnt, "onvol.txt")); got != "on the mounted volume" {
			t.Errorf("the mounted volume was modified: onvol.txt = %q", got)
		}
		if testfs.Exists(t, filepath.Join(root, "tree", "a.txt")) {
			t.Error("tree/a.txt was not deleted")
		}
	})
}

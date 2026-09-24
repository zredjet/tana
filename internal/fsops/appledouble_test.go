package fsops

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestAppleDoubleNamesOrdinary は、OS が拡張属性の保存に使わないボリューム（Windows・Linux のすべて、macOS の APFS）では、
// `名前` と並ぶ `._名前` も通常のファイルとしてコピー・移動されることを確かめる（§8.5）。
func TestAppleDoubleNamesOrdinary(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/m/x": testfs.File("x"), "src/m/._x": testfs.File("apple double"), "src/m/._y": testfs.File("orphan"), "copy": testfs.Dir()})
	want := map[string]string{"x": "x", "._x": "apple double", "._y": "orphan"}
	names := []string{"._x", "._y", "x"}
	res := execPlan(t, context.Background(), mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "m")}, DestDir: filepath.Join(root, "copy")}), ExecOptions{})
	if it := res.Items[0]; it.Outcome != OutcomeDone {
		t.Errorf("copy: %+v, want Done", it)
	}
	wantFiles(t, filepath.Join(root, "copy", "m"), want)
	if got := testfs.ListRawNames(t, filepath.Join(root, "copy", "m")); !slices.Equal(got, names) {
		t.Errorf("copied names = %+q, want %+q", got, names)
	}
	testfs.Build(t, root, testfs.Tree{"dest/m/old": testfs.File("old")})
	plan := sameMove(t, root, "m")
	decide(t, plan, filepath.Join(root, "dest", "m"), DecisionMerge)
	if res := execPlan(t, context.Background(), plan, ExecOptions{}); res.Items[0].Outcome != OutcomeDone {
		t.Errorf("move merge: %+v, want Done", res.Items[0])
	}
	want["old"] = "old"
	wantFiles(t, filepath.Join(root, "dest", "m"), want)
}

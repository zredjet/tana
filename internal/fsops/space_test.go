package fsops

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// spacePlan は、衝突のないファイル a、上書き先と衝突するファイル b、マージの衝突があるフォルダ d（中に衝突のない x と、衝突する y）を
// コピーする計画を作る。
func spacePlan(t *testing.T) (*Plan, string) {
	t.Helper()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"src/a": testfs.File(strings.Repeat("a", 100)), "src/b": testfs.File(strings.Repeat("b", 200)),
		"src/d/x": testfs.File(strings.Repeat("x", 300)), "src/d/y": testfs.File(strings.Repeat("y", 400)),
		"dest/b": testfs.File(strings.Repeat("B", 50)), "dest/d/y": testfs.File(strings.Repeat("Y", 10)),
	})
	src, dest := filepath.Join(root, "src"), filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(src, "a"), filepath.Join(src, "b"), filepath.Join(src, "d")}, DestDir: dest})
	return plan, dest
}

// TestSpaceNeedFollowsDecisions は、空き容量と比べる書き込むバイト数を、衝突の決定に応じて計算し直すことを確かめる（§6.4）。
func TestSpaceNeedFollowsDecisions(t *testing.T) {
	t.Parallel()
	plan, dest := spacePlan(t)
	steps := []struct {
		name   string
		decide map[string]Decision // dest からの相対パス → 決定
		want   int64
	}{
		{"all undecided (skip)", nil, 100},
		{"b overwritten (200 - 50)", map[string]Decision{"b": DecisionOverwrite}, 250},
		{"b auto-renamed", map[string]Decision{"b": DecisionAutoRename}, 300},
		{"d merged, y undecided", map[string]Decision{"d": DecisionMerge}, 600},
		{"d merged, y overwritten (400 - 10)", map[string]Decision{"d/y": DecisionOverwrite}, 990},
		{"d auto-renamed (all of d, whatever y's decision)", map[string]Decision{"d": DecisionAutoRename}, 1000},
		{"d skipped", map[string]Decision{"d": DecisionSkip}, 300},
	}
	for _, s := range steps {
		for rel, d := range s.decide {
			decide(t, plan, filepath.Join(dest, filepath.FromSlash(rel)), d)
		}
		if got := plan.spaceNeed(); got != s.want {
			t.Errorf("%s: need = %d, want %d", s.name, got, s.want)
		}
	}
}

// TestSpaceWarningFollowsDecisions は、Warnings の KindNoSpace が、呼んだ時点の決定で書き込むバイト数と空き容量の比較に従うことを確かめる（§6.4）。
func TestSpaceWarningFollowsDecisions(t *testing.T) {
	t.Parallel()
	plan, dest := spacePlan(t)
	plan.space.free = 500 // 計画時に測った空き容量を、この試験用に小さくする
	hasNoSpace := func() bool {
		return slices.ContainsFunc(plan.Warnings(), func(w *OpError) bool { return w.Kind == KindNoSpace })
	}
	if hasNoSpace() {
		t.Error("undecided conflicts (100 bytes to write): unexpected KindNoSpace")
	}
	decide(t, plan, filepath.Join(dest, "d"), DecisionAutoRename)
	if !hasNoSpace() {
		t.Error("d auto-renamed (800 bytes to write): want KindNoSpace")
	}
	decide(t, plan, filepath.Join(dest, "d"), DecisionSkip)
	if hasNoSpace() {
		t.Error("d skipped again: unexpected KindNoSpace")
	}
}

// TestTooLargeWarningFollowsDecisions は、コピー先の上限を超えるファイルの警告（§6.4、§10.6）を、そのファイルを書く決定のときだけ出すことを確かめる。
func TestTooLargeWarningFollowsDecisions(t *testing.T) {
	t.Parallel()
	plan, dest := spacePlan(t)
	var bID ConflictID
	for _, c := range plan.Conflicts() {
		if c.Dst == filepath.Join(dest, "b") {
			bID = c.ID
		}
	}
	// b がコピー先の上限を超えるものとして記録する（実際の FAT32 のボリュームでの確認は TestFileTooLargeFAT32）。
	plan.space.tooLarge = append(plan.space.tooLarge, ownedPath{path: "b", owner: bID}, ownedPath{path: "free", owner: 0})
	tooLarge := func() []string {
		var ps []string
		for _, w := range plan.Warnings() {
			if w.Kind == KindFileTooLarge {
				ps = append(ps, w.Path)
			}
		}
		return ps
	}
	if got := tooLarge(); !slices.Equal(got, []string{"free"}) {
		t.Errorf("b undecided: warnings for %q, want only the file without a conflict", got)
	}
	decide(t, plan, filepath.Join(dest, "b"), DecisionOverwrite)
	if got := tooLarge(); !slices.Equal(got, []string{"b", "free"}) {
		t.Errorf("b overwritten: warnings for %q, want b and free", got)
	}
}

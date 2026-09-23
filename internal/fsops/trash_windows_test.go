package fsops

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestTrashPrecheckWindows は、Windows のごみ箱の事前確認（§12.2）を確かめる。ファイルシステムは変更しない。
func TestTrashPrecheckWindows(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{
		"ok.txt":                 testfs.File("x"),
		testfs.NamePlain:         testfs.File("plain"),
		testfs.NameTrailingDot:   testfs.File("dot"),
		testfs.NameTrailingSpace: testfs.File("space"),
		testfs.NameReservedCON:   testfs.File("con"),
	})
	long := testfs.LongPath(t, root)
	testfs.WriteFile(t, filepath.Join(long, "f"), "x")
	share := `\\localhost\` + strings.ToUpper(root[:1]) + `$` + root[2:]
	tests := []struct {
		name string
		path string
		want Kind // KindUnknown は「使える」
	}{
		{"fixed drive", filepath.Join(root, "ok.txt"), KindUnknown},
		{"plain name", filepath.Join(root, testfs.NamePlain), KindUnknown},
		{"trailing dot", filepath.Join(root, testfs.NameTrailingDot), KindTrashUnavailable},
		{"trailing space", filepath.Join(root, testfs.NameTrailingSpace), KindTrashUnavailable},
		{"reserved name", filepath.Join(root, testfs.NameReservedCON), KindTrashUnavailable},
		{"long path", filepath.Join(long, "f"), KindTrashUnavailable},
		{"network (admin share)", filepath.Join(share, "ok.txt"), KindTrashUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := mustPlan(t, Request{Op: OpTrash, Sources: []string{tt.path}})
			it := plan.Items()[0]
			if tt.want == KindUnknown && it.Err != nil || tt.want != KindUnknown && KindOf(it.Err) != tt.want {
				t.Errorf("%s: Item.Err = %v, want %v", tt.path, it.Err, tt.want)
			}
		})
	}
}

// TestTrashPrecheckCapacity は、ごみ箱が「すぐに削除する」設定のボリュームと、最大サイズを超える項目を、
// 計画時に KindTrashUnavailable にすることを確かめる（§12.2、V13、V19）。CI の W:・X: を使う。
func TestTrashPrecheckCapacity(t *testing.T) {
	t.Parallel()
	nuke := testfs.EnvDir(t, testfs.TrashNukeEnv)
	small := testfs.EnvDir(t, testfs.TrashSmallEnv) // 最大サイズ 1 MB（1048576 バイト）
	testfs.Build(t, nuke, testfs.Tree{"a.txt": testfs.File("a")})
	testfs.Build(t, small, testfs.Tree{
		"exact.bin":      testfs.File(strings.Repeat("x", 1<<20)),
		"over.bin":       testfs.File(strings.Repeat("x", 1<<20+1)),
		"small.bin":      testfs.File("x"),
		"dir-over/a.bin": testfs.File(strings.Repeat("x", 600000)),
		"dir-over/b.bin": testfs.File(strings.Repeat("x", 600000)),
		"dir-ok/a.bin":   testfs.File(strings.Repeat("x", 400000)),
		"dir-ok/b.bin":   testfs.File(strings.Repeat("x", 400000)),
	})
	tests := []struct {
		path string
		ok   bool
	}{
		{filepath.Join(nuke, "a.txt"), false},
		{filepath.Join(small, "exact.bin"), true}, // 最大サイズちょうどは入る（V19）
		{filepath.Join(small, "over.bin"), false},
		{filepath.Join(small, "small.bin"), true},
		{filepath.Join(small, "dir-over"), false}, // フォルダは中身の合計（§12.1）
		{filepath.Join(small, "dir-ok"), true},
	}
	for _, tt := range tests {
		plan := mustPlan(t, Request{Op: OpTrash, Sources: []string{tt.path}})
		it := plan.Items()[0]
		if tt.ok && it.Err != nil || !tt.ok && KindOf(it.Err) != KindTrashUnavailable {
			t.Errorf("%s: Item.Err = %v, want ok=%v", tt.path, it.Err, tt.ok)
		}
	}
}

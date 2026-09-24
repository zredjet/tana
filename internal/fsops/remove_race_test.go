package fsops

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// フォルダの中身を処理してハンドルを閉じた後・フォルダ自体を削除する直前に、そのフォルダをリンク（Windows ではジャンクション）へ
// 置き換える注入（beforeRemove フック）。削除は、確かめたフォルダだけに対して行い、置き換えたリンクを消さない（I4。総点検の穴 2）。
// リンクの先の目印ファイルも残る。

// linkRemains は、victim が置き換えたリンクのまま残り、リンクの先の目印ファイルも残ることを確かめる。
func linkRemains(t *testing.T, root, victim string) {
	t.Helper()
	if !testfs.Exists(t, victim) || !isLinkEntry(t, victim) {
		t.Errorf("the link that replaced %s was removed", victim)
	}
	checkMarkers(t, root)
}

// replaceOnRemove は、削除の直前に victims のフォルダをリンクへ置き換えるフックを返す。
func replaceOnRemove(t *testing.T, root string, victims ...string) *testHooks {
	pending := map[string]bool{}
	for _, v := range victims {
		pending[v] = true
	}
	return &testHooks{beforeRemove: func(p string) {
		if pending[p] {
			delete(pending, p)
			replaceWithLink(t, root, p)
		}
	}}
}

// TestDeleteDirReplacedByLinkBeforeRemove は、完全削除（§13.2）で、中身を消した後・フォルダを削除する直前にフォルダをリンクへ
// 置き換えても、そのリンクを消さず KindSourceChanged で報告することを確かめる（入れ子のフォルダとトップレベルのフォルダ）。
func TestDeleteDirReplacedByLinkBeforeRemove(t *testing.T) {
	t.Parallel()
	for _, top := range []bool{false, true} {
		t.Run(map[bool]string{false: "nested", true: "top"}[top], func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			markerTree(t, root)
			testfs.Build(t, root, testfs.Tree{"tree/sub/x": testfs.File("x"), "tree/y": testfs.File("y")})
			victim := filepath.Join(root, "tree", "sub")
			if top {
				victim = filepath.Join(root, "tree")
			}
			res := execDelete(t, context.Background(), replaceOnRemove(t, root, victim), filepath.Join(root, "tree"))
			if it := res.Items[0]; !hasKind(it, KindSourceChanged) {
				t.Errorf("result = %+v, want KindSourceChanged for %s", it, victim)
			}
			linkRemains(t, root, victim)
		})
	}
}

// TestRemoveRecordedDirReplacedByLinkBeforeRemove は、記録した項目だけの削除（§13.3）で、同じ置き換えをしても、
// そのリンクを消さず KindSourceChanged で報告することを確かめる。
func TestRemoveRecordedDirReplacedByLinkBeforeRemove(t *testing.T) {
	t.Parallel()
	for _, top := range []bool{false, true} {
		t.Run(map[bool]string{false: "nested", true: "top"}[top], func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			markerTree(t, root)
			testfs.Build(t, root, testfs.Tree{"src/sub/x": testfs.File("x"), "src/y": testfs.File("y")})
			src := filepath.Join(root, "src")
			victim := filepath.Join(src, "sub")
			if top {
				victim = src
			}
			out := removeRec(t, context.Background(), replaceOnRemove(t, root, victim), src, recordOf(t, src))
			if !keptWith(out, victim, KindSourceChanged) {
				t.Errorf("details = %+v, want %s kept with KindSourceChanged", out.details, victim)
			}
			linkRemains(t, root, victim)
		})
	}
}

// TestMoveMergeDirReplacedByLinkBeforeRemove は、同一ボリュームのマージ移動（§11.1）で、中身を移動した後・空になった移動元の
// フォルダを削除する直前に、そのフォルダをリンクへ置き換えても、そのリンクを消さず KindSourceChanged で報告することを確かめる。
func TestMoveMergeDirReplacedByLinkBeforeRemove(t *testing.T) {
	t.Parallel()
	for _, top := range []bool{false, true} {
		t.Run(map[bool]string{false: "nested", true: "top"}[top], func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			markerTree(t, root)
			testfs.Build(t, root, testfs.Tree{
				"src/m/a.txt": testfs.File("a"), "src/m/sub/b.txt": testfs.File("b"),
				"dest/m/old.txt": testfs.File("o"), "dest/m/sub/c.txt": testfs.File("c"),
			})
			dest := filepath.Join(root, "dest")
			plan := sameMove(t, root, "m")
			decide(t, plan, filepath.Join(dest, "m"), DecisionMerge)
			decide(t, plan, filepath.Join(dest, "m", "sub"), DecisionMerge)
			victim := filepath.Join(root, "src", "m", "sub")
			if top {
				victim = filepath.Join(root, "src", "m")
			}
			res := execPlan(t, context.Background(), plan, ExecOptions{hooks: replaceOnRemove(t, root, victim)})
			if it := res.Items[0]; !hasKind(it, KindSourceChanged) {
				t.Errorf("result = %+v, want KindSourceChanged for %s", it, victim)
			}
			linkRemains(t, root, victim)
			wantFiles(t, dest, map[string]string{"m/a.txt": "a", "m/sub/b.txt": "b", "m/old.txt": "o", "m/sub/c.txt": "c"})
		})
	}
}

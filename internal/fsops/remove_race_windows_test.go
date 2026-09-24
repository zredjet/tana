package fsops

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// Windows のファイル（ファイル用のリンクを含む）の削除（§13.2、§13.3）で、確かめた後・削除する直前に、そのファイルを別のもので
// 置き換える・書き換える注入（beforeRemove フック）。削除は、確かめたファイルだけに対して行い、置き換えたもの・書き換えた移動元を消さない
// （I2、I7。穴 2 で残った DeleteFileW の経路）。Unix の unlink・unlinkat は名前で消すので、この注入は Windows だけで行う（SPEC §13.2）。

// replaceFile は、p を p-moved に移し、p に別のファイル（内容 intruder）を置く。readOnly なら置いたファイルを読み取り専用にする。
func replaceFile(t *testing.T, p string, readOnly bool) {
	t.Helper()
	if err := os.Rename(testfs.ExtendedPath(p), testfs.ExtendedPath(p+"-moved")); err != nil {
		t.Errorf("inject: %v", err)
		return
	}
	testfs.WriteFile(t, p, "intruder")
	if readOnly {
		testfs.SetReadOnly(t, p)
	}
}

// replaceFileOnRemove は、削除の直前に victim を別のファイルに置き換えるフックを返す。
func replaceFileOnRemove(t *testing.T, victim string, readOnly bool) *testHooks {
	done := false
	return &testHooks{beforeRemove: func(p string) {
		if p == victim && !done {
			done = true
			replaceFile(t, p, readOnly)
		}
	}}
}

// intruderRemains は、victim に置いたファイルが（読み取り専用なら属性も）そのまま残り、元のファイルが victim-moved に残ることを確かめる。
func intruderRemains(t *testing.T, victim, original string, readOnly bool) {
	t.Helper()
	if !testfs.Exists(t, victim) {
		t.Fatalf("the file that replaced %s was removed", victim)
	}
	if got := testfs.ReadFile(t, victim); got != "intruder" {
		t.Errorf("%s = %q, want the intruder", victim, got)
	}
	if readOnly {
		p, _ := windows.UTF16PtrFromString(testfs.ExtendedPath(victim))
		if a, err := windows.GetFileAttributes(p); err != nil || a&windows.FILE_ATTRIBUTE_READONLY == 0 {
			t.Errorf("%s: attributes %#x (%v), want the read-only attribute kept", victim, a, err)
		}
	}
	if got := testfs.ReadFile(t, victim+"-moved"); got != original {
		t.Errorf("%s-moved = %q, want %q", victim, got, original)
	}
}

// TestDeleteFileReplacedBeforeRemove は、完全削除（§13.2）で、確かめた後・削除の直前にファイルを別のファイルに置き換えても、
// 置き換えたファイルを消さず KindSourceChanged で報告することを確かめる（入れ子のファイルとトップレベルのファイル）。
func TestDeleteFileReplacedBeforeRemove(t *testing.T) {
	t.Parallel()
	for _, top := range []bool{false, true} {
		t.Run(map[bool]string{false: "nested", true: "top"}[top], func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			testfs.Build(t, root, testfs.Tree{"tree/x": testfs.File("x"), "tree/y": testfs.File("y")})
			src, victim := filepath.Join(root, "tree"), filepath.Join(root, "tree", "x")
			if top {
				src = victim
			}
			res := execDelete(t, context.Background(), replaceFileOnRemove(t, victim, false), src)
			if it := res.Items[0]; !hasKind(it, KindSourceChanged) {
				t.Errorf("result = %+v, want KindSourceChanged for %s", it, victim)
			}
			intruderRemains(t, victim, "x", false)
		})
	}
}

// TestRemoveRecordedFileReplacedBeforeRemove は、記録した項目だけの削除（§13.3）で、照合の後・削除の直前にファイルを別のファイルに
// 置き換えても、置き換えたファイルを消さず、その属性も変えず（読み取り専用を外さない）、KindSourceChanged で報告することを確かめる。
func TestRemoveRecordedFileReplacedBeforeRemove(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name          string
		top, readOnly bool
	}{{"nested", false, false}, {"top", true, false}, {"nested read-only", false, true}, {"top read-only", true, true}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			root := testfs.TempDir(t)
			tree := testfs.Tree{"src/x": testfs.File("x"), "src/y": testfs.File("y")}
			if c.readOnly {
				tree["src/x"] = testfs.File("x").RO()
			}
			testfs.Build(t, root, tree)
			src, victim := filepath.Join(root, "src"), filepath.Join(root, "src", "x")
			if c.top {
				src = victim
			}
			out := removeRec(t, context.Background(), replaceFileOnRemove(t, victim, c.readOnly), src, recordOf(t, src))
			if !keptWith(out, victim, KindSourceChanged) {
				t.Errorf("details = %+v, want %s kept with KindSourceChanged", out.details, victim)
			}
			intruderRemains(t, victim, "x", c.readOnly)
		})
	}
}

// TestRemoveRecordedFileEditedBeforeRemove は、記録した項目だけの削除（§13.3）で、照合の後・削除の直前に移動元のファイルが
// 書き換えられても（同じファイルのまま、内容と大きさ・更新日時が変わる）、それを消さず KindSourceChanged で報告することを確かめる（I2）。
func TestRemoveRecordedFileEditedBeforeRemove(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/x": testfs.File("x"), "src/y": testfs.File("y")})
	src, victim := filepath.Join(root, "src"), filepath.Join(root, "src", "x")
	rec := recordOf(t, src)
	h := &testHooks{beforeRemove: func(p string) {
		if p == victim {
			f, err := os.OpenFile(testfs.ExtendedPath(p), os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Errorf("inject: %v", err)
				return
			}
			f.WriteString(" edited")
			f.Close()
		}
	}}
	out := removeRec(t, context.Background(), h, src, rec)
	if !keptWith(out, victim, KindSourceChanged) {
		t.Errorf("details = %+v, want %s kept with KindSourceChanged", out.details, victim)
	}
	if got := testfs.ReadFile(t, victim); got != "x edited" {
		t.Errorf("%s = %q, want the edited contents kept", victim, got)
	}
}

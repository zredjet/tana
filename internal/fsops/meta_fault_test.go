package fsops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// メタデータ（§15）の設定・読み込みに失敗する経路（総点検の穴 6）。データは無事なので、不変条件は破らない。
// メタデータを、fsops が作ったもの（一時ファイル・作ったフォルダ）以外に設定しないことを確かめる。

var metaOld = time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)

// TestMetaTempReplaced は、書き終えた一時ファイルが置き換えられていれば、メタデータ（更新日時）を置き換えたものに設定せず、
// 最終名にもせず、消しもしないことを確かめる（setMetaIn の fileID の照合、§10.1 の手順 6・7）。
func TestMetaTempReplaced(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/f.txt": testfs.File("copied").At(metaOld), "dest": testfs.Dir()})
	dest := filepath.Join(root, "dest")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "f.txt")}, DestDir: dest})
	var intruder string
	h := &testHooks{beforeVerify: func(string) { intruder = replaceTemp(t, dest) }}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	if it := res.Items[0]; it.Outcome != OutcomeFailed || it.Err == nil || it.Err.Kind != KindDestChanged || !it.Err.OnDest {
		t.Errorf("result = %+v, want Failed with KindDestChanged on the destination side", it)
	}
	if intruder == "" {
		t.Fatal("no injection")
	}
	if got := testfs.ReadFile(t, intruder); got != "intruder" {
		t.Errorf("the file that replaced the temporary file = %q", got)
	}
	if modTime(t, intruder).Equal(metaOld) {
		t.Error("the metadata of the source was set on the file that replaced the temporary file")
	}
	if testfs.Exists(t, filepath.Join(dest, "f.txt")) {
		t.Error("the replaced temporary file got the final name")
	}
}

// TestMetaCreatedDirReplaced は、コピーで作ったフォルダが、メタデータを設定する前に別のフォルダへ置き換えられていれば、
// そのフォルダにメタデータを設定せず、KindMetadata の警告にすることを確かめる（setMetaIn の fileID の照合、§10.2、§15）。
func TestMetaCreatedDirReplaced(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/tree/a.txt": testfs.File("a"), "src/tree": testfs.Dir().At(metaOld), "dest": testfs.Dir()})
	dest := filepath.Join(root, "dest", "tree")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{filepath.Join(root, "src", "tree")}, DestDir: filepath.Join(root, "dest")})
	h := &testHooks{beforeDirMeta: func(p string) {
		if p == dest {
			if err := os.Rename(testfs.ExtendedPath(dest), testfs.ExtendedPath(dest+"-moved")); err != nil {
				t.Errorf("inject: %v", err)
				return
			}
			testfs.MkdirAll(t, dest) // 別のフォルダ（作ったものではない）
		}
	}}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	it := res.Items[0]
	if it.Outcome != OutcomeDone || len(it.Warnings) != 1 || it.Warnings[0].Kind != KindMetadata {
		t.Errorf("result = %+v, want Done with one KindMetadata warning", it)
	}
	if modTime(t, dest).Equal(metaOld) {
		t.Error("the metadata was set on the folder that replaced the created one")
	}
	wantFiles(t, root, map[string]string{"dest/tree-moved/a.txt": "a"})
}

// TestMetaSourceDirUnreadable は、コピー元のフォルダのメタデータを読めなければ（注入）、中身はコピーし、KindMetadata の警告にすることを確かめる（§15）。
func TestMetaSourceDirUnreadable(t *testing.T) {
	t.Parallel()
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"src/tree/a.txt": testfs.File("a"), "src/tree": testfs.Dir().At(metaOld), "dest": testfs.Dir()})
	src := filepath.Join(root, "src", "tree")
	plan := mustPlan(t, Request{Op: OpCopy, Sources: []string{src}, DestDir: filepath.Join(root, "dest")})
	injected := errors.New("injected")
	h := &testHooks{dirMetaFault: func(p string) error {
		if p == src {
			return injected
		}
		return nil
	}}
	res := execPlan(t, context.Background(), plan, ExecOptions{hooks: h})
	it := res.Items[0]
	if it.Outcome != OutcomeDone || len(it.Warnings) != 1 || it.Warnings[0].Kind != KindMetadata || !errors.Is(it.Warnings[0], injected) {
		t.Errorf("result = %+v, want Done with the injected KindMetadata warning", it)
	}
	wantFiles(t, root, map[string]string{"dest/tree/a.txt": "a"})
	if modTime(t, filepath.Join(root, "dest", "tree")).Equal(metaOld) {
		t.Error("metadata was set although it could not be read")
	}
}

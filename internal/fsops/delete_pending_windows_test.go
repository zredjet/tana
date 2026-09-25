package fsops

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"unsafe"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// holdPending は、別のプロセス（ウイルス対策ソフトなど）の代わりに、p を開いたまま、印を付けるだけのハンドルで従来の方式（FileDispositionInfo）の
// 削除の印を付ける（p は削除待ちになり、ほかのハンドルが閉じるまで名前が残る。V26）。markDelete が偽なら印を付けず、開いたままにするだけ。
// 返す関数で開いていたハンドルを閉じる（何度呼んでもよい。テストの終了時にも閉じる）。
func holdPending(t *testing.T, p string, markDelete bool) (release func()) {
	t.Helper()
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	p16, _ := windows.UTF16PtrFromString(testfs.ExtendedPath(p))
	holder, err := windows.CreateFile(p16, windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatalf("hold %s: %v", p, err)
	}
	if markDelete {
		del, err := windows.CreateFile(p16, windows.DELETE, share, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			t.Fatalf("open %s for delete: %v", p, err)
		}
		info := fileDispositionInfo{DeleteFile: true}
		err = windows.SetFileInformationByHandle(del, windows.FileDispositionInfo, (*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
		windows.CloseHandle(del)
		if err != nil {
			t.Fatalf("mark %s for delete: %v", p, err)
		}
	}
	var once sync.Once
	release = func() { once.Do(func() { windows.CloseHandle(holder) }) }
	t.Cleanup(release)
	return release
}

// TestDeletePendingByOther は、ほかのプロセスが開いたまま削除の印を付けた（削除待ちの）ファイルを、「権限がありません」ではなく
// 使用中（KindLocked）として扱い、§17.1 のとおりやり直すことを確かめる（§17、§17.1、V26）。
func TestDeletePendingByOther(t *testing.T) {
	t.Parallel()
	// フォルダの中の削除待ちのファイル: 待つ間にそのプロセスが閉じれば、やり直しで消えていて、フォルダも削除できる（Done）。
	t.Run("inside a folder, released while waiting", func(t *testing.T) {
		t.Parallel()
		root := testfs.TempDir(t)
		testfs.Build(t, root, testfs.Tree{"tree/pending.txt": testfs.File("p"), "tree/other.txt": testfs.File("o")})
		plan := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "tree")}})
		release := holdPending(t, filepath.Join(root, "tree", "pending.txt"), true)
		li := newLockInjector(nil)
		li.onWait = func(string, int) { release() }
		res := execPlan(t, context.Background(), plan, ExecOptions{hooks: li.hooks()})
		if it := res.Items[0]; it.Outcome != OutcomeDone {
			t.Errorf("result = %+v, want Done (the other process closed while waiting)", it)
		}
		if len(li.waits) == 0 {
			t.Error("no retry happened")
		}
		if names := testfs.ListNames(t, root); len(names) != 0 {
			t.Errorf("left: %q", names)
		}
	})
	// トップレベルの削除待ちのファイル: 閉じられなければ、上限の後に KindLocked（「権限がありません」ではない）。
	t.Run("top level, still held", func(t *testing.T) {
		t.Parallel()
		root := testfs.TempDir(t)
		testfs.Build(t, root, testfs.Tree{"f.txt": testfs.File("f")})
		plan := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "f.txt")}})
		holdPending(t, filepath.Join(root, "f.txt"), true)
		it := execPlan(t, context.Background(), plan, ExecOptions{hooks: newLockInjector(nil).hooks()}).Items[0]
		if it.Outcome != OutcomeFailed || !hasKind(it, KindLocked) {
			t.Errorf("result = %+v, want Failed with KindLocked (not a permission error)", it)
		}
	})
	// 計画の時点で削除待ちなら、Item.Err を KindLocked にする。
	t.Run("pending when planning", func(t *testing.T) {
		t.Parallel()
		root := testfs.TempDir(t)
		testfs.Build(t, root, testfs.Tree{"f.txt": testfs.File("f")})
		holdPending(t, filepath.Join(root, "f.txt"), true)
		plan := mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "f.txt")}})
		if err := plan.Items()[0].Err; KindOf(err) != KindLocked {
			t.Errorf("Item.Err = %v, want KindLocked", err)
		}
	})
}

// TestDeleteFolderWithPendingChildren は、exFAT・FAT32（削除の印が従来の方式になり、ほかのハンドルが開いていると名前が残る。V23）で、
// ほかのプロセスが開いているファイルを含むフォルダを完全削除すると、フォルダの削除の「空ではありません」を使用中（KindLocked）として
// やり直し、そのプロセスが閉じれば Done になることを確かめる（§13.2、§17.1、V26）。
func TestDeleteFolderWithPendingChildren(t *testing.T) {
	t.Parallel()
	for _, env := range []string{testfs.FAT32Env, testfs.ExFATEnv} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			root := testfs.EnvDir(t, env)
			testfs.Build(t, root, testfs.Tree{"tree/held.txt": testfs.File("h"), "tree/other.txt": testfs.File("o")})
			release := holdPending(t, filepath.Join(root, "tree", "held.txt"), false) // 開いているだけ（印は fsops が付ける）
			li := newLockInjector(nil)
			var waitedFor []string
			li.onWait = func(p string, _ int) { waitedFor = append(waitedFor, p); release() }
			res := execPlan(t, context.Background(), mustPlan(t, Request{Op: OpDelete, Sources: []string{filepath.Join(root, "tree")}}), ExecOptions{hooks: li.hooks()})
			if it := res.Items[0]; it.Outcome != OutcomeDone {
				t.Errorf("result = %+v, want Done (the other process closed while waiting)", it)
			}
			if !slices.Contains(waitedFor, filepath.Join(root, "tree")) {
				t.Errorf("waited for %q, want a retry of the folder removal", waitedFor)
			}
			if testfs.Exists(t, filepath.Join(root, "tree")) {
				t.Error("tree is left")
			}
		})
	}
}

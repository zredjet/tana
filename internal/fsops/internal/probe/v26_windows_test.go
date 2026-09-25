package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

var procRtlGetLastNtStatus = windows.NewLazySystemDLL("ntdll.dll").NewProc("RtlGetLastNtStatus")

// lastNtStatus は、直前に失敗した Win32 の呼び出しの NT ステータスを返す（同じ OS スレッドで呼ぶこと）。
func lastNtStatus() uint32 {
	r, _, _ := procRtlGetLastNtStatus.Call()
	return uint32(r)
}

// TestV26 は、ほかのハンドルが開いたまま削除の印を付けられた（削除待ちの）ファイルに対する操作が、どのエラーと NT ステータスを返すかを記録する
// （V26。削除待ちを「権限がありません」「空ではありません」と報告しないために、STATUS_DELETE_PENDING（0xC0000056）で見分けられるか）。
// 削除の印は、exFAT・FAT32 で使われる従来の方式（FileDispositionInfo）で付ける。
func TestV26(t *testing.T) {
	logWindowsVersion(t, "V26")
	// 最初の呼び出しで DLL を読み込むと、その処理で NT ステータスが上書きされうるので、先に読み込んでおく。
	if err := procRtlGetLastNtStatus.Find(); err != nil {
		t.Skipf("RtlGetLastNtStatus: %v", err)
	}
	runtime.LockOSThread() // NT ステータスは OS スレッドごとなので、呼び出しと同じスレッドで読む
	defer runtime.UnlockOSThread()
	check := func(t *testing.T, label, root string) {
		dir := filepath.Join(root, "v26")
		f := filepath.Join(dir, "pending.txt")
		testfs.Build(t, root, testfs.Tree{"v26/pending.txt": testfs.File("p")})
		share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
		holder, err := windows.CreateFile(u16(t, testfs.ExtendedPath(f)), windows.GENERIC_READ, share, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			t.Fatalf("V26: open holder: %v", err)
		}
		del, err := windows.CreateFile(u16(t, testfs.ExtendedPath(f)), windows.DELETE, share, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			t.Fatalf("V26: open for delete: %v", err)
		}
		legacy := struct{ DeleteFile bool }{true}
		err = windows.SetFileInformationByHandle(del, windows.FileDispositionInfo, (*byte)(unsafe.Pointer(&legacy)), uint32(unsafe.Sizeof(legacy)))
		windows.CloseHandle(del)
		t.Logf("V26: %s: mark delete (FileDispositionInfo) with another handle open: err=%v", label, err)

		report := func(op string, err error) {
			st := lastNtStatus()
			t.Logf("V26: %s: %s on the delete-pending file: err=%v (errno %d) ntstatus=%#x", label, op, err, errnoOf(err), st)
		}
		_, err = windows.GetFileAttributes(u16(t, testfs.ExtendedPath(f)))
		report("GetFileAttributes", err)
		h, err := windows.CreateFile(u16(t, testfs.ExtendedPath(f)), windows.FILE_READ_ATTRIBUTES, share, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		report("CreateFile(FILE_READ_ATTRIBUTES)", err)
		if err == nil {
			windows.CloseHandle(h)
		}
		h, err = windows.CreateFile(u16(t, testfs.ExtendedPath(f)), windows.DELETE|windows.FILE_READ_ATTRIBUTES, share, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		report("CreateFile(DELETE)", err)
		if err == nil {
			windows.CloseHandle(h)
		}
		err = windows.DeleteFile(u16(t, testfs.ExtendedPath(f)))
		report("DeleteFileW", err)
		err = windows.RemoveDirectory(u16(t, testfs.ExtendedPath(dir)))
		report("RemoveDirectoryW (parent)", err)
		entries, err := os.ReadDir(testfs.ExtendedPath(dir))
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Logf("V26: %s: parent listing while pending: names=%q err=%v", label, names, err)

		windows.CloseHandle(holder)
		_, err = os.Lstat(testfs.ExtendedPath(f))
		t.Logf("V26: %s: after closing the other handle: Lstat err=%v; %s", label, err, fmt.Sprintf("RemoveDirectoryW=%v", windows.RemoveDirectory(u16(t, testfs.ExtendedPath(dir)))))
	}
	check(t, "NTFS (temp dir)", testfs.TempDir(t))
	for _, env := range []string{envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) { check(t, env, testfs.EnvDir(t, env)) })
	}
}

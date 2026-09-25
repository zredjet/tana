package fsops

import (
	"errors"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// procRtlGetLastNtStatus は、直前に失敗した呼び出しの NT ステータスを返す ntdll の関数（x/sys にない）。
var procRtlGetLastNtStatus = windows.NewLazySystemDLL("ntdll.dll").NewProc("RtlGetLastNtStatus")

// statusDeletePending は、削除の印が付いていて、ほかのハンドルが閉じるまで名前が残っていることを表す NT ステータス（V26）。
const statusDeletePending = 0xC0000056

// callDeletePending は fn を呼び、ERROR_ACCESS_DENIED で失敗したら、その原因が削除待ち（STATUS_DELETE_PENDING）かも返す（§17、V26）。
// NT ステータスは OS スレッドごとなので、fn とその読み取りを同じ OS スレッドで行う。RtlGetLastNtStatus を読み込めなければ見分けない。
func callDeletePending(fn func() error) (err error, pending bool) {
	if procRtlGetLastNtStatus.Find() != nil { // 最初の読み込みで NT ステータスが上書きされないよう、先に読み込む
		return fn(), false
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	err = fn()
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		r, _, _ := procRtlGetLastNtStatus.Call()
		pending = uint32(r) == statusDeletePending
	}
	return err, pending
}

// deletePendingErr は、削除待ちで失敗した err を KindLocked の *OpError にする（§17。ほかのプロセスが閉じれば消えるので、使用中として扱う）。
func deletePendingErr(op, path string, err error) *OpError {
	return &OpError{Op: op, Path: path, Kind: KindLocked, Err: err}
}

// onlyDeletePending は、フォルダ dir（\\?\ 形式）に残っている名前がすべて削除待ちかを返す（§13.2。空でないのは、ほかのプロセスが
// 閉じれば消えるものだけ）。何もなければ、または調べられなければ偽。
func onlyDeletePending(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		p16, err := windows.UTF16PtrFromString(dir + `\` + e.Name())
		if err != nil {
			return false
		}
		if _, pending := callDeletePending(func() error { _, err := windows.GetFileAttributes(p16); return err }); !pending {
			return false
		}
	}
	return true
}

package fsops

import (
	"errors"
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

// deletePending は、p（\\?\ 形式）が削除待ちかを、1 回の呼び出し（GetFileAttributes）で確かめる（§17、V26）。
// os の関数は失敗した後にも中で別の呼び出しをするので、その後の NT ステータスでは見分けられないため。
func deletePending(p string) bool {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return false
	}
	_, pending := callDeletePending(func() error { _, err := windows.GetFileAttributes(p16); return err })
	return pending
}

// onlyDeletePending は、フォルダ dir（\\?\ 形式）に残っている名前がすべて削除待ちかを返す（§13.2。空でないのは、ほかのプロセスが
// 閉じれば消えるものだけ）。何もなければ、または調べられなければ偽。
// 呼び出し側がそのフォルダを削除のアクセス権で開いているので、共有モードを問わない FindFirstFile で列挙する。
func onlyDeletePending(dir string) bool {
	pattern, err := windows.UTF16PtrFromString(dir + `\*`)
	if err != nil {
		return false
	}
	var fd windows.Win32finddata
	h, err := windows.FindFirstFile(pattern, &fd)
	if err != nil {
		return false
	}
	defer windows.FindClose(h)
	found := false
	for {
		name := windows.UTF16ToString(fd.FileName[:])
		if name != "." && name != ".." {
			if !deletePending(dir + `\` + name) {
				return false
			}
			found = true
		}
		if err := windows.FindNextFile(h, &fd); err != nil {
			return found && errors.Is(err, windows.ERROR_NO_MORE_FILES)
		}
	}
}

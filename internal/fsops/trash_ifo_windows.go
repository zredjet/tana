package fsops

import (
	"errors"
	"runtime"
	"strconv"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// IFileOperation（COM）でごみ箱へ移す（§12.2）。cgo を使わず、vtable を syscall.SyscallN で呼び、
// 進捗通知（IFileOperationProgressSink）は syscall.NewCallback で作る。

// COM の GUID（shobjidl.h）。
var (
	clsidFileOperation            = windows.GUID{Data1: 0x3ad05575, Data2: 0x8857, Data3: 0x4850, Data4: [8]byte{0x92, 0x77, 0x11, 0xb8, 0x5b, 0xdb, 0x8e, 0x09}}
	iidIFileOperation             = windows.GUID{Data1: 0x947aab5f, Data2: 0x0a5c, Data3: 0x4c13, Data4: [8]byte{0xb4, 0xd6, 0x4b, 0xf7, 0x83, 0x6f, 0xc9, 0xf8}}
	iidIShellItem                 = windows.GUID{Data1: 0x43826d1e, Data2: 0xe718, Data3: 0x42ee, Data4: [8]byte{0xbc, 0x55, 0xa1, 0xe2, 0x61, 0xc3, 0x7b, 0xfe}}
	iidIUnknown                   = windows.GUID{Data1: 0x00000000, Data2: 0x0000, Data3: 0x0000, Data4: [8]byte{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	iidIFileOperationProgressSink = windows.GUID{Data1: 0x04b0f1a7, Data2: 0x9490, Data3: 0x44bc, Data4: [8]byte{0x96, 0xe1, 0x42, 0x96, 0xa3, 0x12, 0x52, 0xe2}}

	procCoCreateInstance            = windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance")
	procSHCreateItemFromParsingName = windows.NewLazySystemDLL("shell32.dll").NewProc("SHCreateItemFromParsingName")

	// progressSinkVtbl は、進捗通知の vtable。syscall.NewCallback で作ったものは解放できず数に上限があるので、
	// パッケージの初期化時に 1 回だけ作り、以後は変更しない（状態は各操作の progressSink が持つ）。
	progressSinkVtbl = newProgressSinkVtbl()
)

const (
	clsctxInprocServer         = 0x1
	coinitDisableOLE1DDE       = 0x4
	rpcEChangedMode            = 0x80010106
	sOK                        = 0
	sFalse                     = 1
	eAbort                     = 0x80004004
	eNoInterface               = 0x80004002
	sigdnFileSysPath           = 0x80058000
	tsfDeleteRecycleIfPossible = 0x80 // TSF_DELETE_RECYCLE_IF_POSSIBLE（V18）

	fofSilent           = 0x0004
	fofNoConfirmation   = 0x0010
	fofAllowUndo        = 0x0040
	fofNoErrorUI        = 0x0400
	fofWantNukeWarning  = 0x4000
	fofxRecycleOnDelete = 0x00080000

	// vtable の添字（IUnknown の 3 つを含む）。
	iunknownQueryInterface   = 0
	iunknownAddRef           = 1
	iunknownRelease          = 2
	ifileOperationSetFlags   = 5
	ifileOperationDeleteItem = 18
	ifileOperationPerform    = 21
	ifileOperationAnyAborted = 22
	ishellItemGetDisplayName = 5

	progressSinkMethods          = 19
	progressSinkPreDeleteItem    = 11
	progressSinkPostDeleteItem   = 12
	trashOperationFlags          = fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI | fofxRecycleOnDelete | fofWantNukeWarning
	hresultFacilityWin32Prefix   = 0x8007
	hresultFacilityWin32CodeMask = 0xffff
)

// comObj は COM のオブジェクト（先頭が vtable へのポインタ）。
type comObj struct{ vtbl *[32]uintptr }

func (o *comObj) call(method int, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(o.vtbl[method], append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)...)
	return r
}

func (o *comObj) release() {
	if o != nil {
		o.call(iunknownRelease)
	}
}

// fileSysPath は IShellItem のファイルシステム上のパスを返す。取れなければ空。
func (o *comObj) fileSysPath() string {
	if o == nil {
		return ""
	}
	var p *uint16
	if o.call(ishellItemGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&p))) != sOK || p == nil {
		return ""
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(p))
	return windows.UTF16PtrToString(p)
}

// progressSink は IFileOperationProgressSink の実装。COM に渡すのはこの構造体へのポインタで、先頭が vtable。
// 進捗通知は PerformOperations を呼んだスレッドで同期的に呼ばれるので、フィールドへの書き込みは競合しない。
type progressSink struct {
	vtbl        *[progressSinkMethods]uintptr
	notRecycled bool   // PreDeleteItem で、ごみ箱に入らない（完全削除になる）と通知されたので中止した（§12.2 の手順 4）
	posted      bool   // PostDeleteItem が呼ばれた
	postHR      uint32 // PostDeleteItem の結果
	trashed     string // ごみ箱の中の項目のパス（取得できた場合）
}

func newProgressSinkVtbl() *[progressSinkMethods]uintptr {
	var v [progressSinkMethods]uintptr
	// 使わない通知には何もしない関数を割り当てる（x64 の呼び出し規約では呼び出し側が引数を片付けるので、引数の数は問わない）。
	ok := syscall.NewCallback(func(this *progressSink) uintptr { return sOK })
	for i := range v {
		v[i] = ok
	}
	v[iunknownQueryInterface] = syscall.NewCallback(func(this *progressSink, riid *windows.GUID, ppv **progressSink) uintptr {
		if *riid == iidIUnknown || *riid == iidIFileOperationProgressSink {
			*ppv = this
			return sOK
		}
		*ppv = nil
		return eNoInterface
	})
	v[iunknownAddRef] = syscall.NewCallback(func(this *progressSink) uintptr { return 1 })
	v[iunknownRelease] = syscall.NewCallback(func(this *progressSink) uintptr { return 1 })
	v[progressSinkPreDeleteItem] = syscall.NewCallback(func(this *progressSink, flags uintptr, item *comObj) uintptr {
		if flags&tsfDeleteRecycleIfPossible == 0 {
			// ごみ箱に入らず完全削除になる。中止して項目を残す（I5）。
			this.notRecycled = true
			return eAbort
		}
		return sOK
	})
	v[progressSinkPostDeleteItem] = syscall.NewCallback(func(this *progressSink, flags uintptr, item *comObj, hr uintptr, newly *comObj) uintptr {
		this.posted, this.postHR = true, uint32(hr)
		this.trashed = newly.fileSysPath()
		return sOK
	})
	return &v
}

// hresultError は、分類できない HRESULT（§12.2）。
type hresultError uint32

func (e hresultError) Error() string { return "HRESULT 0x" + strconv.FormatUint(uint64(e), 16) }

// hresultErr は HRESULT を error にする。FACILITY_WIN32 のものは Win32 のエラー番号にして §17 の分類に使えるようにする。
func hresultErr(hr uint32) error {
	if hr>>16 == hresultFacilityWin32Prefix {
		return windows.Errno(hr & hresultFacilityWin32CodeMask)
	}
	return hresultError(hr)
}

// trashSys は、src を IFileOperation でごみ箱へ移し、ごみ箱の中のパスを返す（§12.2 の手順 1〜5）。
// COM は、このためだけに LockOSThread した goroutine で初期化する。goroutine は Unlock せずに終わるので、そのスレッドは破棄される。
func trashSys(src string, info EntryInfo) (string, error) {
	type result struct {
		trashed string
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		trashed, err := trashLocked(src)
		ch <- result{trashed, err}
	}()
	r := <-ch
	return r.trashed, r.err
}

func trashLocked(src string) (string, error) {
	fail := func(kind Kind, err error) (string, error) {
		return "", &OpError{Op: "trash", Path: src, Kind: kind, Err: err}
	}
	switch err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|coinitDisableOLE1DDE); {
	case err == nil, errors.Is(err, syscall.Errno(sFalse)):
		defer windows.CoUninitialize()
	case errors.Is(err, syscall.Errno(rpcEChangedMode)):
		// 既に別の方式で初期化されている。IFileOperation はどの方式でも動いた（V18）。
	default:
		return fail(KindUnknown, err)
	}
	var op *comObj
	if hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidFileOperation)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIFileOperation)), uintptr(unsafe.Pointer(&op))); hr != sOK {
		return fail(KindUnknown, hresultErr(uint32(hr)))
	}
	defer op.release()
	if hr := op.call(ifileOperationSetFlags, trashOperationFlags); hr != sOK {
		return fail(KindUnknown, hresultErr(uint32(hr)))
	}
	// SHCreateItemFromParsingName は \\?\ 付きのパスを受け付けない（V18）。事前確認で、260 文字未満で Win32 の正規化で
	// 変わらないことを確かめたパスだけを渡す（§12.2）。
	p16, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return fail(KindInvalidRequest, err)
	}
	var item *comObj
	if hr, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(p16)), 0,
		uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&item))); hr != sOK {
		err := hresultErr(uint32(hr))
		return fail(classify(err, classifyOpts{}), err)
	}
	defer item.release()
	sink := &progressSink{vtbl: progressSinkVtbl}
	if hr := op.call(ifileOperationDeleteItem, uintptr(unsafe.Pointer(item)), uintptr(unsafe.Pointer(sink))); hr != sOK {
		return fail(KindUnknown, hresultErr(uint32(hr)))
	}
	perform := uint32(op.call(ifileOperationPerform))
	var aborted int32
	op.call(ifileOperationAnyAborted, uintptr(unsafe.Pointer(&aborted)))
	runtime.KeepAlive(sink)
	switch {
	case sink.notRecycled:
		return fail(KindTrashUnavailable, nil) // PreDeleteItem で中止した。項目は残る（V18）
	case perform != sOK:
		err := hresultErr(perform)
		return fail(classify(err, classifyOpts{}), err)
	case aborted != 0 || !sink.posted:
		return fail(KindUnknown, hresultErr(eAbort))
	case sink.postHR != sOK:
		err := hresultErr(sink.postHR)
		return fail(classify(err, classifyOpts{}), err)
	}
	return sink.trashed, nil
}

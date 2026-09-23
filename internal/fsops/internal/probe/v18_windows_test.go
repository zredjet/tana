package probe

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"unsafe"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// ---- COM の最小限の定義（cgo を使わない。SPEC §12.2） ----

var (
	clsidFileOperation            = mustGUID("{3ad05575-8857-4850-9277-11b85bdb8e09}")
	iidIFileOperation             = mustGUID("{947aab5f-0a5c-4c13-b4d6-4bf7836fc9f8}")
	iidIShellItem                 = mustGUID("{43826d1e-e718-42ee-bc55-a1e261c37bfe}")
	iidIUnknown                   = mustGUID("{00000000-0000-0000-c000-000000000046}")
	iidIFileOperationProgressSink = mustGUID("{04b0f1a7-9490-44bc-96e1-4296a31252e2}")
	procCoCreateInstance          = windows.NewLazySystemDLL("ole32.dll").NewProc("CoCreateInstance")
	procSHCreateItemFromParsing   = windows.NewLazySystemDLL("shell32.dll").NewProc("SHCreateItemFromParsingName")
)

func mustGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return g
}

const (
	clsctxInprocServer            = 0x1
	fofxRecycleOnDelete           = 0x00080000
	tsfDeleteRecycleIfPossible    = 0x100
	sigdnFileSysPath              = 0x80058000
	eAbort                        = 0x80004004
	eNoInterface                  = 0x80004002
	sOK                           = 0
	ifileOperationDeleteItem      = 18
	ifileOperationSetFlags        = 5
	ifileOperationPerform         = 21
	ifileOperationAnyAborted      = 22
	ishellItemGetDisplayName      = 5
	iunknownRelease               = 2
	progressSinkMethods           = 19
	progressSinkPreDeleteItem     = 11
	progressSinkPostDeleteItem    = 12
	progressSinkFinishOperations  = 4
	progressSinkQueryInterface    = 0
	progressSinkAddRef            = 1
	progressSinkRelease           = 2
	defaultIFileOperationFlagsSet = fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI
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

// path は IShellItem のファイルシステム上のパスを返す。
func (o *comObj) path() string {
	if o == nil {
		return "<nil>"
	}
	var p *uint16
	if hr := o.call(ishellItemGetDisplayName, sigdnFileSysPath, uintptr(unsafe.Pointer(&p))); hr != sOK {
		return fmt.Sprintf("<GetDisplayName hr=%#x>", uint32(hr))
	}
	defer windows.CoTaskMemFree(unsafe.Pointer(p))
	return windows.UTF16PtrToString(p)
}

// sinkObj は IFileOperationProgressSink の実装。COM に渡すのは、この構造体へのポインタ。
type sinkObj struct {
	vtbl *[progressSinkMethods]uintptr
}

// sinkRecord は、1 回の操作で進捗通知が受け取った内容。
type sinkRecord struct {
	abortIfNotRecycle bool
	preFlags          []string
	post              []string
	finish            string
}

var (
	sinkMu      sync.Mutex
	sinkCurrent *sinkRecord // 進捗通知は PerformOperations を呼んだスレッドで同期的に呼ばれる。テストは 1 つずつ実行する
	sinkVtbl    *[progressSinkMethods]uintptr
	sinkOnce    sync.Once
)

func progressSinkVtbl() *[progressSinkMethods]uintptr {
	sinkOnce.Do(func() {
		var v [progressSinkMethods]uintptr
		ok := syscall.NewCallback(func(this *sinkObj) uintptr { return sOK })
		// 引数の数が違うメソッドにも、何もしない関数を割り当てる（stdcall/x64 では呼び出し側が片付けるので引数の数は問わない）。
		for i := range v {
			v[i] = ok
		}
		v[progressSinkQueryInterface] = syscall.NewCallback(func(this *sinkObj, riid *windows.GUID, ppv **sinkObj) uintptr {
			if *riid == iidIUnknown || *riid == iidIFileOperationProgressSink {
				*ppv = this
				return sOK
			}
			*ppv = nil
			return eNoInterface
		})
		v[progressSinkAddRef] = syscall.NewCallback(func(this *sinkObj) uintptr { return 1 })
		v[progressSinkRelease] = syscall.NewCallback(func(this *sinkObj) uintptr { return 1 })
		v[progressSinkFinishOperations] = syscall.NewCallback(func(this *sinkObj, hr uintptr) uintptr {
			sinkCurrent.finish = fmt.Sprintf("%#x", uint32(hr))
			return sOK
		})
		v[progressSinkPreDeleteItem] = syscall.NewCallback(func(this *sinkObj, flags uintptr, item *comObj) uintptr {
			r := sinkCurrent
			recycle := flags&tsfDeleteRecycleIfPossible != 0
			abort := r.abortIfNotRecycle && !recycle
			r.preFlags = append(r.preFlags, fmt.Sprintf("flags=%#x recycle=%v item=%q abort=%v", uint32(flags), recycle, item.path(), abort))
			if abort {
				return eAbort
			}
			return sOK
		})
		v[progressSinkPostDeleteItem] = syscall.NewCallback(func(this *sinkObj, flags uintptr, item *comObj, hr uintptr, newly *comObj) uintptr {
			sinkCurrent.post = append(sinkCurrent.post, fmt.Sprintf("flags=%#x hr=%#x newlyCreated=%q", uint32(flags), uint32(hr), newly.path()))
			return sOK
		})
		sinkVtbl = &v
	})
	return sinkVtbl
}

// coInit は COM の初期化の方法。
type coInit int

const (
	coInitSTA coInit = iota
	coInitMTA
	coInitNone
)

// ifoTrash は、LockOSThread した goroutine で COM を初期化し、IFileOperation で path をごみ箱へ送る（SPEC §12.2 の手順）。
// abortIfNotRecycle が真なら、PreDeleteItem でごみ箱に入らない項目を中止する。結果を 1 行で返す。
func ifoTrash(path string, init coInit, abortIfNotRecycle bool) string {
	sinkMu.Lock()
	defer sinkMu.Unlock()
	rec := &sinkRecord{abortIfNotRecycle: abortIfNotRecycle}
	sinkCurrent = rec
	done := make(chan string)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		done <- ifoTrashLocked(path, init, rec)
	}()
	s := <-done
	return fmt.Sprintf("%s | PreDeleteItem=%v PostDeleteItem=%v FinishOperations=%s", s, rec.preFlags, rec.post, rec.finish)
}

func ifoTrashLocked(path string, init coInit, rec *sinkRecord) string {
	switch init {
	case coInitSTA, coInitMTA:
		mode := uint32(windows.COINIT_APARTMENTTHREADED | 0x4)
		if init == coInitMTA {
			mode = windows.COINIT_MULTITHREADED
		}
		if err := windows.CoInitializeEx(0, mode); err != nil {
			return "CoInitializeEx: " + err.Error()
		}
		defer windows.CoUninitialize()
	}
	var op *comObj
	hr, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(&clsidFileOperation)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidIFileOperation)), uintptr(unsafe.Pointer(&op)))
	if hr != sOK {
		return fmt.Sprintf("CoCreateInstance hr=%#x", uint32(hr))
	}
	defer op.release()
	if hr := op.call(ifileOperationSetFlags, defaultIFileOperationFlagsSet|fofxRecycleOnDelete); hr != sOK {
		return fmt.Sprintf("SetOperationFlags hr=%#x", uint32(hr))
	}
	p16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err.Error()
	}
	var item *comObj
	hr, _, _ = procSHCreateItemFromParsing.Call(uintptr(unsafe.Pointer(p16)), 0,
		uintptr(unsafe.Pointer(&iidIShellItem)), uintptr(unsafe.Pointer(&item)))
	if hr != sOK {
		return fmt.Sprintf("SHCreateItemFromParsingName hr=%#x", uint32(hr))
	}
	defer item.release()
	sink := &sinkObj{vtbl: progressSinkVtbl()}
	if hr := op.call(ifileOperationDeleteItem, uintptr(unsafe.Pointer(item)), uintptr(unsafe.Pointer(sink))); hr != sOK {
		return fmt.Sprintf("DeleteItem hr=%#x", uint32(hr))
	}
	perform := op.call(ifileOperationPerform)
	var aborted int32
	op.call(ifileOperationAnyAborted, uintptr(unsafe.Pointer(&aborted)))
	runtime.KeepAlive(sink)
	return fmt.Sprintf("item=%q PerformOperations hr=%#x anyAborted=%v", item.path(), uint32(perform), aborted != 0)
}

// TestV18 は、IFileOperation と PreDeleteItem による方式（SPEC §12.2）の動作を記録する（SPEC §20 V18）。
// ごみ箱に入らない場合（すぐに削除する設定・最大サイズ超過）に PreDeleteItem で中止した項目が残ることは、
// I5 に関わるのでテストの失敗として検出する。FSOPS_TEST_TRASH=1 のときだけ実行する。
func TestV18(t *testing.T) {
	testfs.RequireTrash(t)
	logWindowsVersion(t, "V18")
	root := testfs.TempDir(t)
	cRoot := driveRoot(root)

	type tc struct {
		name       string
		dir        func(t *testing.T) string // 項目を置くフォルダ
		size       int
		callPath   func(p string) string
		mustRemain bool // 中止されて残らなければならない（I5）
	}
	same := func(p string) string { return p }
	tmp := func(t *testing.T) string { return root }
	env := func(name string) func(t *testing.T) string {
		return func(t *testing.T) string { return testfs.EnvDir(t, name) }
	}
	cases := []tc{
		{"fixed drive file", tmp, 16, same, false},
		{"nuke volume", env(envTrashNuke), 1024, same, true},
		{"small volume, over the max size", env(envTrashSmall), 4 << 20, same, true},
		{"small volume, within the max size", env(envTrashSmall), 1024, same, false},
		{"exFAT", env(envExFAT), 16, same, false},
		{"FAT32", env(envFAT32), 16, same, false},
		{"crossvol NTFS", env(testfs.CrossVolEnv), 16, same, false},
		{"with \\\\?\\ prefix", tmp, 16, testfs.ExtendedPath, false},
		{"admin share", tmp, 16, func(p string) string { return `\\localhost\` + strings.ToUpper(p[:1]) + `$` + p[2:] }, false},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := filepath.Join(c.dir(t), fmt.Sprintf("v18-%d", i))
			testfs.MkdirAll(t, d)
			p := filepath.Join(d, "file.bin")
			testfs.WriteFile(t, p, strings.Repeat("x", c.size))
			bin := driveRoot(p)
			before := readBin(t, bin)
			s := ifoTrash(c.callPath(p), coInitSTA, true)
			verdict := trashVerdict(t, p, bin, before)
			t.Logf("V18: %s (%d bytes): %s || %s", c.name, c.size, verdict, s)
			if c.mustRemain && !testfs.Exists(t, p) {
				t.Errorf("V18: %s: the item was not kept although PreDeleteItem aborted it (I5)", c.name)
			}
		})
	}

	t.Run("long path", func(t *testing.T) {
		long := testfs.LongPath(t, filepath.Join(root, "long"))
		p := filepath.Join(long, "file.bin")
		testfs.WriteFile(t, p, "x")
		for _, cp := range []string{p, testfs.ExtendedPath(p)} {
			if !testfs.Exists(t, p) {
				testfs.WriteFile(t, p, "x")
			}
			before := readBin(t, cRoot)
			s := ifoTrash(cp, coInitSTA, true)
			t.Logf("V18: long path (%d chars, prefix=%v): %s || %s", len(p), strings.HasPrefix(cp, `\\?\`),
				trashVerdict(t, p, cRoot, before), s)
		}
	})

	t.Run("trailing dot", func(t *testing.T) {
		d := filepath.Join(root, "dot")
		testfs.Build(t, d, testfs.Tree{testfs.NamePlain: testfs.File("plain"), testfs.NameTrailingDot: testfs.File("dot")})
		for _, cp := range []string{filepath.Join(d, testfs.NameTrailingDot), testfs.ExtendedPath(filepath.Join(d, testfs.NameTrailingDot))} {
			before := readBin(t, cRoot)
			s := ifoTrash(cp, coInitSTA, true)
			t.Logf("V18: trash %q (prefix=%v): %s; foo exists=%v foo. exists=%v || %s", testfs.NameTrailingDot, strings.HasPrefix(cp, `\\?\`),
				trashVerdict(t, filepath.Join(d, testfs.NameTrailingDot), cRoot, before),
				testfs.Exists(t, filepath.Join(d, testfs.NamePlain)), testfs.Exists(t, filepath.Join(d, testfs.NameTrailingDot)), s)
		}
	})

	t.Run("COM initialization", func(t *testing.T) {
		for _, c := range []struct {
			name string
			init coInit
		}{{"MTA", coInitMTA}, {"none", coInitNone}} {
			p := filepath.Join(root, "init-"+c.name, "file.bin")
			testfs.MkdirAll(t, filepath.Dir(p))
			testfs.WriteFile(t, p, "x")
			before := readBin(t, cRoot)
			s := ifoTrash(p, c.init, true)
			t.Logf("V18: COM init %s: %s || %s", c.name, trashVerdict(t, p, cRoot, before), s)
		}
	})
}

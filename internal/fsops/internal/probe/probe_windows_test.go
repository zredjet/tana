package probe

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

func u16(t *testing.T, s string) *uint16 {
	t.Helper()
	p, err := windows.UTF16PtrFromString(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// logWindowsVersion は Windows のバージョンを記録する（POSIX 形式の削除など、バージョンで変わる動作のため）。
func logWindowsVersion(t *testing.T, v string) {
	t.Helper()
	info := windows.RtlGetVersion()
	t.Logf("%s: Windows %d.%d build %d", v, info.MajorVersion, info.MinorVersion, info.BuildNumber)
}

// ---- SHFileOperationW（V4、V10、V13） ----

// shFileOpStructW は SHFILEOPSTRUCTW（64 ビット版。x/sys/windows に定義がない）。
type shFileOpStructW struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

const (
	foDelete          = 0x3
	fofSilent         = 0x4
	fofNoConfirmation = 0x10
	fofAllowUndo      = 0x40
	fofNoErrorUI      = 0x400
	// trashFlags は SPEC §12.2 のフラグ。
	trashFlags = fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI
)

// shTrash は SPEC §12.2 の方法で path を 1 項目だけごみ箱へ送り、戻り値と fAnyOperationsAborted を返す。
// path はそのまま渡す（\\?\ 形式への変換はしない）。
func shTrash(t *testing.T, path string) (ret uintptr, aborted bool) {
	t.Helper()
	from := append(utf16.Encode([]rune(path)), 0, 0) // NUL 文字 2 つで終わる形式
	op := shFileOpStructW{wFunc: foDelete, pFrom: &from[0], fFlags: trashFlags}
	proc := windows.NewLazySystemDLL("shell32.dll").NewProc("SHFileOperationW")
	ret, _, _ = proc.Call(uintptr(unsafe.Pointer(&op)))
	return ret, op.fAnyOperationsAborted != 0
}

// ---- ごみ箱の $I ファイル（V10） ----

// recycleInfo は $Recycle.Bin\<SID>\$I... の内容。
type recycleInfo struct {
	IFile    string
	RFile    string
	Version  int64
	Size     int64
	Deleted  time.Time
	Original string
}

func currentSID(t *testing.T) string {
	t.Helper()
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return u.User.Sid.String()
}

// parseIFile は $I ファイルを読む。形式: 版 8 バイト、元のサイズ 8 バイト、削除日時（FILETIME）8 バイト、
// 版 2 ではパスの文字数 4 バイトと UTF-16 のパス、版 1 では 260 文字固定の UTF-16 のパス。
func parseIFile(b []byte) (recycleInfo, error) {
	var r recycleInfo
	if len(b) < 24 {
		return r, fmt.Errorf("$I file too short: %d bytes", len(b))
	}
	r.Version = int64(binary.LittleEndian.Uint64(b[0:]))
	r.Size = int64(binary.LittleEndian.Uint64(b[8:]))
	ft := windows.Filetime{
		LowDateTime:  binary.LittleEndian.Uint32(b[16:]),
		HighDateTime: binary.LittleEndian.Uint32(b[20:]),
	}
	r.Deleted = time.Unix(0, ft.Nanoseconds())
	var name []byte
	switch r.Version {
	case 2:
		if len(b) < 28 {
			return r, fmt.Errorf("version 2 $I file too short: %d bytes", len(b))
		}
		n := int(binary.LittleEndian.Uint32(b[24:]))
		if len(b) < 28+2*n {
			return r, fmt.Errorf("version 2 $I file: %d chars declared, %d bytes present", n, len(b)-28)
		}
		name = b[28 : 28+2*n]
	case 1:
		name = b[24:min(len(b), 24+2*260)]
	default:
		return r, fmt.Errorf("unknown $I version %d", r.Version)
	}
	u := make([]uint16, len(name)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(name[2*i:])
	}
	r.Original = windows.UTF16ToString(u)
	return r, nil
}

// findInRecycleBin は、root（ボリュームのルート、例: C:\）のごみ箱から、元のパスが original の $I を探す。
// ごみ箱の中は読むだけで、変更しない。
func findInRecycleBin(t *testing.T, root, original string) (recycleInfo, bool) {
	t.Helper()
	dir := filepath.Join(root, "$Recycle.Bin", currentSID(t))
	entries, err := os.ReadDir(testfs.ExtendedPath(dir))
	if err != nil {
		t.Logf("reading %s: %v", dir, err)
		return recycleInfo{}, false
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "$I") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(testfs.ExtendedPath(p))
		if err != nil {
			continue
		}
		r, err := parseIFile(b)
		if err != nil {
			t.Logf("%s: %v", p, err)
			continue
		}
		if strings.EqualFold(r.Original, original) {
			r.IFile = p
			r.RFile = filepath.Join(dir, "$R"+strings.TrimPrefix(e.Name(), "$I"))
			return r, true
		}
	}
	return recycleInfo{}, false
}

// driveRoot は "C:\..." の形のパスのドライブのルート（"C:\"）を返す。
func driveRoot(p string) string {
	if len(p) >= 2 && p[1] == ':' {
		return p[:2] + `\`
	}
	return ""
}

// describeTrash は、shTrash の結果と、元の場所・ごみ箱の状態を 1 行にまとめる。
func describeTrash(t *testing.T, path, binRoot string, ret uintptr, aborted bool) string {
	t.Helper()
	s := fmt.Sprintf("ret=%#x aborted=%v stillExists=%v", ret, aborted, testfs.Exists(t, path))
	if binRoot == "" {
		return s
	}
	if r, ok := findInRecycleBin(t, binRoot, path); ok {
		s += fmt.Sprintf(" inRecycleBin=true ($I version=%d size=%d deleted=%s original=%q; $R exists=%v)",
			r.Version, r.Size, r.Deleted.Format(time.RFC3339), r.Original, testfs.Exists(t, r.RFile))
	} else {
		s += " inRecycleBin=false"
	}
	return s
}

// errnoOf は err に含まれる Windows のエラー番号を返す（なければ 0）。
func errnoOf(err error) windows.Errno {
	var e windows.Errno
	errors.As(err, &e)
	return e
}

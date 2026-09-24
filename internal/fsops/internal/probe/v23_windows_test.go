package probe

import (
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// TestV23 は、ファイル・フォルダを開いたハンドルで削除する方法（SetFileInformationByHandle の FileDispositionInfo と、
// POSIX 形式の FileDispositionInfoEx）が、NTFS・exFAT・FAT32 で使えるか、ほかのハンドルが開いているときに名前がすぐ消えるか
// （DeleteFileW と比べる）、読み取り専用のファイルでどう失敗するかを記録する（V23。§13.2 のファイルの削除をハンドルで行うため）。
func TestV23(t *testing.T) {
	logWindowsVersion(t, "V23")
	type method struct {
		name string
		del  func(t *testing.T, p string, dir bool) error
	}
	open := func(t *testing.T, p string) (windows.Handle, error) {
		return windows.CreateFile(u16(t, testfs.ExtendedPath(p)), windows.DELETE|windows.FILE_READ_ATTRIBUTES,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	}
	byHandle := func(class uint32, info unsafe.Pointer, size uint32) func(t *testing.T, p string, dir bool) error {
		return func(t *testing.T, p string, dir bool) error {
			h, err := open(t, p)
			if err != nil {
				return err
			}
			defer windows.CloseHandle(h)
			return windows.SetFileInformationByHandle(h, class, (*byte)(info), size)
		}
	}
	legacy := struct{ DeleteFile bool }{true}
	posix := struct{ Flags uint32 }{windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS}
	methods := []method{
		{"DeleteFileW/RemoveDirectoryW", func(t *testing.T, p string, dir bool) error {
			if dir {
				return windows.RemoveDirectory(u16(t, testfs.ExtendedPath(p)))
			}
			return windows.DeleteFile(u16(t, testfs.ExtendedPath(p)))
		}},
		{"FileDispositionInfo", byHandle(windows.FileDispositionInfo, unsafe.Pointer(&legacy), uint32(unsafe.Sizeof(legacy)))},
		{"FileDispositionInfoEx(DELETE|POSIX_SEMANTICS)", byHandle(windows.FileDispositionInfoEx, unsafe.Pointer(&posix), uint32(unsafe.Sizeof(posix)))},
	}
	check := func(t *testing.T, label, dir string) {
		for i, m := range methods {
			for _, isDir := range []bool{false, true} {
				kind := map[bool]string{false: "file", true: "empty dir"}[isDir]
				// ほかのハンドルが開いていない場合。
				p := filepath.Join(dir, "plain", string(rune('a'+i))+kind)
				v23Build(t, p, isDir, false)
				err := m.del(t, p, isDir)
				t.Logf("V23: %s: %s: %s: err=%v (errno %d) exists after=%v", label, m.name, kind, err, errnoOf(err), testfs.Exists(t, p))
				// ほかのハンドル（FILE_SHARE_DELETE つき）が開いている場合。閉じる前に名前が残っているか。
				p = filepath.Join(dir, "held", string(rune('a'+i))+kind)
				v23Build(t, p, isDir, false)
				other, oerr := windows.CreateFile(u16(t, testfs.ExtendedPath(p)), windows.FILE_READ_ATTRIBUTES,
					windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
				err = m.del(t, p, isDir)
				before := testfs.Exists(t, p)
				if oerr == nil {
					windows.CloseHandle(other)
				}
				t.Logf("V23: %s: %s: %s with another handle open: err=%v (errno %d) exists while held=%v exists after close=%v",
					label, m.name, kind, err, errnoOf(err), before, testfs.Exists(t, p))
			}
			// 読み取り専用のファイル。
			p := filepath.Join(dir, "ro", string(rune('a'+i))+"file")
			v23Build(t, p, false, true)
			err := m.del(t, p, false)
			t.Logf("V23: %s: %s: read-only file: err=%v (errno %d) exists after=%v", label, m.name, err, errnoOf(err), testfs.Exists(t, p))
		}
	}
	check(t, "NTFS (temp dir)", testfs.TempDir(t))
	for _, env := range []string{envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) { check(t, env, testfs.EnvDir(t, env)) })
	}
}

// v23Build は、p にファイル（isDir なら空のフォルダ）を作る。readOnly なら読み取り専用属性を付ける。
func v23Build(t *testing.T, p string, isDir, readOnly bool) {
	t.Helper()
	e := testfs.File("x")
	if isDir {
		e = testfs.Dir()
	}
	if readOnly {
		e = e.RO()
	}
	testfs.Build(t, filepath.Dir(p), testfs.Tree{filepath.Base(p): e})
}

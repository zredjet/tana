package probe

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// dirEntryInfo は、フォルダのハンドルによる列挙で得た 1 エントリ。
type dirEntryInfo struct {
	Name    string
	Attrs   uint32
	Tag     uint32 // FileIdExtdDirectoryInfo の ReparsePointTag（FileIdBothDirectoryInfo では EaSize の位置の値）
	FileID  string // 16 進
	Size    int64
	ModTime int64
}

// alignedBuf は、LARGE_INTEGER を含む構造体のために 8 バイト境界にそろえたバッファを返す。
func alignedBuf(n int) []byte {
	b := make([]uint64, n/8)
	return unsafe.Slice((*byte)(unsafe.Pointer(&b[0])), n)
}

func openDir(t *testing.T, dir string) windows.Handle {
	t.Helper()
	h, err := windows.CreateFile(u16(t, testfs.ExtendedPath(dir)), windows.FILE_LIST_DIRECTORY|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	return h
}

// enumerate は、フォルダのハンドルに GetFileInformationByHandleEx（restartClass、続けて class）を使って列挙する。
// extd が真なら FILE_ID_EXTD_DIR_INFO、偽なら FILE_ID_BOTH_DIR_INFO として読む。
func enumerate(t *testing.T, dir string, restartClass, class uint32, extd bool) ([]dirEntryInfo, error) {
	t.Helper()
	h := openDir(t, dir)
	defer windows.CloseHandle(h)
	buf := alignedBuf(64 * 1024)
	var out []dirEntryInfo
	c := restartClass
	for {
		err := windows.GetFileInformationByHandleEx(h, c, &buf[0], uint32(len(buf)))
		if err == windows.ERROR_NO_MORE_FILES {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		c = class
		for off := 0; ; {
			e := buf[off:]
			next := binary.LittleEndian.Uint32(e[0:])
			var d dirEntryInfo
			d.ModTime = int64(binary.LittleEndian.Uint64(e[24:]))
			d.Size = int64(binary.LittleEndian.Uint64(e[40:]))
			d.Attrs = binary.LittleEndian.Uint32(e[56:])
			nameLen := int(binary.LittleEndian.Uint32(e[60:]))
			d.Tag = binary.LittleEndian.Uint32(e[64:]) // BOTH: EaSize（リパースポイントではタグが入る）
			var nameOff int
			if extd {
				d.Tag = binary.LittleEndian.Uint32(e[68:])
				d.FileID = hex.EncodeToString(e[72:88])
				nameOff = 88
			} else {
				d.FileID = hex.EncodeToString(e[96:104])
				nameOff = 104
			}
			u := unsafe.Slice((*uint16)(unsafe.Pointer(&e[nameOff])), nameLen/2)
			d.Name = windows.UTF16ToString(append([]uint16(nil), u...))
			if d.Name != "." && d.Name != ".." {
				out = append(out, d)
			}
			if next == 0 {
				break
			}
			off += int(next)
		}
	}
}

// fileIDInfo は FILE_ID_INFO（x/sys/windows に定義がない）。
type fileIDInfo struct {
	VolumeSerialNumber uint64
	FileID             [16]byte
}

// idAndTag は、path をリパースポイントを辿らずに開き、FileIdInfo と FileAttributeTagInfo を返す。
func idAndTag(t *testing.T, path string) (fileIDInfo, uint32, uint32, error) {
	t.Helper()
	h, err := windows.CreateFile(u16(t, testfs.ExtendedPath(path)), windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fileIDInfo{}, 0, 0, err
	}
	defer windows.CloseHandle(h)
	var id fileIDInfo
	if err := windows.GetFileInformationByHandleEx(h, windows.FileIdInfo, (*byte)(unsafe.Pointer(&id)), uint32(unsafe.Sizeof(id))); err != nil {
		return id, 0, 0, fmt.Errorf("FileIdInfo: %w", err)
	}
	var tag struct{ Attrs, Tag uint32 }
	if err := windows.GetFileInformationByHandleEx(h, windows.FileAttributeTagInfo, (*byte)(unsafe.Pointer(&tag)), uint32(unsafe.Sizeof(tag))); err != nil {
		return id, 0, 0, fmt.Errorf("FileAttributeTagInfo: %w", err)
	}
	return id, tag.Attrs, tag.Tag, nil
}

// fsNameOf は dir のあるボリュームのファイルシステム名を返す。
func fsNameOf(t *testing.T, dir string) string {
	t.Helper()
	root, err := volumeRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	fsName := make([]uint16, windows.MAX_PATH)
	if err := windows.GetVolumeInformation(u16(t, root), nil, 0, nil, nil, nil, &fsName[0], uint32(len(fsName))); err != nil {
		t.Fatal(err)
	}
	return windows.UTF16ToString(fsName)
}

// TestV14 は、FileIdExtdDirectoryInfo による列挙が NTFS・exFAT・FAT32 で使えるか、
// 得られるファイル ID・リパースタグが FileIdInfo・FileAttributeTagInfo と一致するかを記録する（SPEC §20 V14、§13.1）。
// 使えない場合に備えて、FileIdBothDirectoryInfo の結果も記録する。
func TestV14(t *testing.T) {
	logWindowsVersion(t, "V14")
	check := func(t *testing.T, label, dir string) {
		fs := fsNameOf(t, dir)
		tree := testfs.Tree{"file.txt": testfs.File("12345"), "dir/x": testfs.File("x"), "名前.txt": testfs.File("")}
		if fs == "NTFS" {
			tree["symlink"] = testfs.Symlink("file.txt")
			tree["dirlink"] = testfs.DirSymlink("dir")
			tree["junction"] = testfs.Junction("dir")
		}
		testfs.Build(t, dir, tree)
		dirID, _, _, err := idAndTag(t, dir)
		t.Logf("V14: %s (%s): FileIdInfo of the dir: volume serial=%#x err=%v", label, fs, dirID.VolumeSerialNumber, err)

		for _, v := range []struct {
			name           string
			restart, class uint32
			extd           bool
		}{
			{"FileIdExtdDirectoryInfo", windows.FileIdExtdDirectoryRestartInfo, windows.FileIdExtdDirectoryInfo, true},
			{"FileIdBothDirectoryInfo", windows.FileIdBothDirectoryRestartInfo, windows.FileIdBothDirectoryInfo, false},
		} {
			entries, err := enumerate(t, dir, v.restart, v.class, v.extd)
			t.Logf("V14: %s (%s): %s: %d entries, err=%v (errno %d)", label, fs, v.name, len(entries), err, errnoOf(err))
			for _, e := range entries {
				id, attrs, tag, err := idAndTag(t, filepath.Join(dir, e.Name))
				want := hex.EncodeToString(id.FileID[:])
				if !v.extd {
					want = hex.EncodeToString(id.FileID[:8])
				}
				tagMatch := attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0 || e.Tag == tag
				t.Logf("V14: %s (%s): %s: %q attrs=%#x tag=%#x id=%s size=%d | FileIdInfo id=%s serial=%#x attrs=%#x tag=%#x err=%v | id match=%v tag match=%v",
					label, fs, v.name, e.Name, e.Attrs, e.Tag, e.FileID, e.Size, want, id.VolumeSerialNumber, attrs, tag, err,
					e.FileID == want, tagMatch)
			}
		}
	}
	check(t, "temp dir", testfs.TempDir(t))
	for _, env := range []string{testfs.CrossVolEnv, envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			check(t, env+"="+os.Getenv(env), testfs.EnvDir(t, env))
		})
	}
}

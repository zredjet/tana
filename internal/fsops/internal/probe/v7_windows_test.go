package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

var driveTypeNames = map[uint32]string{
	windows.DRIVE_UNKNOWN:     "DRIVE_UNKNOWN",
	windows.DRIVE_NO_ROOT_DIR: "DRIVE_NO_ROOT_DIR",
	windows.DRIVE_REMOVABLE:   "DRIVE_REMOVABLE",
	windows.DRIVE_FIXED:       "DRIVE_FIXED",
	windows.DRIVE_REMOTE:      "DRIVE_REMOTE",
	windows.DRIVE_CDROM:       "DRIVE_CDROM",
	windows.DRIVE_RAMDISK:     "DRIVE_RAMDISK",
}

// volumeRoot は GetVolumePathName で path のボリュームのルートを求める。
func volumeRoot(path string) (string, error) {
	p16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	if err := windows.GetVolumePathName(p16, &buf[0], uint32(len(buf))); err != nil {
		return "", fmt.Errorf("GetVolumePathName(%s): %w", path, err)
	}
	return windows.UTF16ToString(buf), nil
}

// describeVolume は、ボリュームのルート root のシリアル番号・ファイルシステム名・ドライブの種類を返す。
func describeVolume(root string) (serial uint32, desc string, err error) {
	r16, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, "", err
	}
	fsName := make([]uint16, windows.MAX_PATH)
	var flags, maxLen uint32
	if err := windows.GetVolumeInformation(r16, nil, 0, &serial, &maxLen, &flags, &fsName[0], uint32(len(fsName))); err != nil {
		return 0, "", fmt.Errorf("GetVolumeInformation(%s): %w", root, err)
	}
	dt := windows.GetDriveType(r16)
	return serial, fmt.Sprintf("root=%s serial=%#08x fs=%s driveType=%s", root, serial,
		windows.UTF16ToString(fsName), driveTypeNames[dt]), nil
}

// volumeInfo は path のあるボリュームの識別子（シリアル番号）と説明を返す。
func volumeInfo(t *testing.T, path string) (id, desc string) {
	t.Helper()
	root, err := volumeRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	serial, desc, err := describeVolume(root)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprint(serial), desc
}

func isCrossDevice(errno syscall.Errno) bool { return errno == windows.ERROR_NOT_SAME_DEVICE }

// TestV7AdminShare は、一時フォルダを \\localhost\X$\... の形で開けるか、
// そのときの GetDriveType が DRIVE_REMOTE になるかを記録する（SPEC §20 V7。§18.4 の I5 のテストの前提）。
func TestV7AdminShare(t *testing.T) {
	local := testfs.TempDir(t)
	testfs.Build(t, local, testfs.Tree{"marker.txt": testfs.File("via local path")})
	if len(local) < 3 || local[1] != ':' {
		t.Skipf("V7: temp dir %s is not on a drive letter", local)
	}
	unc := `\\localhost\` + strings.ToUpper(local[:1]) + `$` + local[2:]
	t.Logf("V7: UNC path: %s", unc)

	b, err := os.ReadFile(testfs.ExtendedPath(filepath.Join(unc, "marker.txt")))
	t.Logf("V7: reading via the admin share: %q, %v", b, err)
	if err == nil && string(b) != "via local path" {
		t.Errorf("V7: the admin share returned different content: %q", b)
	}

	for _, p := range []string{unc, testfs.ExtendedPath(unc)} {
		root, err := volumeRoot(p)
		if err != nil {
			t.Logf("V7: %s: %v", p, err)
			continue
		}
		_, desc, err := describeVolume(root)
		t.Logf("V7: %s: %s %v", p, desc, err)
	}
	// ルートを直接指定した場合も記録する。
	shareRoot := `\\localhost\` + strings.ToUpper(local[:1]) + `$\`
	r16, _ := windows.UTF16PtrFromString(shareRoot)
	t.Logf("V7: GetDriveType(%s) = %s", shareRoot, driveTypeNames[windows.GetDriveType(r16)])
}

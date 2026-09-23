package probe

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// bitBucketSettings は、root のボリュームのごみ箱の設定（HKCU の BitBucket\Volume\{GUID}）を読んで返す。
// CI が設定した値の確認用で、書き込みはしない。
func bitBucketSettings(t *testing.T, root string) string {
	t.Helper()
	buf := make([]uint16, 64)
	if err := windows.GetVolumeNameForVolumeMountPoint(u16(t, root), &buf[0], uint32(len(buf))); err != nil {
		return "GetVolumeNameForVolumeMountPoint: " + err.Error()
	}
	guid := regexp.MustCompile(`\{[^}]+\}`).FindString(windows.UTF16ToString(buf))
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\`+guid, registry.QUERY_VALUE)
	if err != nil {
		return guid + ": " + err.Error()
	}
	defer k.Close()
	var s []string
	for _, name := range []string{"NukeOnDelete", "MaxCapacity"} {
		if v, _, err := k.GetIntegerValue(name); err != nil {
			s = append(s, name+"="+err.Error())
		} else {
			s = append(s, fmt.Sprintf("%s=%d", name, v))
		}
	}
	return guid + " " + strings.Join(s, " ")
}

// TestV13 は、ごみ箱が「すぐに削除する」設定のボリュームと、ごみ箱の最大サイズを超える項目で、
// SHFileOperationW（FOF_ALLOWUNDO | FOF_NOCONFIRMATION）が確認なしに完全削除するかを記録する（SPEC §20 V13、§12.2）。
// ボリュームと設定は CI が用意する（FSOPS_PROBE_TRASH_NUKE_DIR、FSOPS_PROBE_TRASH_SMALL_DIR）。FSOPS_TEST_TRASH=1 のときだけ実行する。
func TestV13(t *testing.T) {
	testfs.RequireTrash(t)
	logWindowsVersion(t, "V13")
	for _, c := range []struct {
		env  string
		size int
	}{
		{envTrashNuke, 1024},
		{envTrashSmall, 4 << 20}, // 4 MiB（最大サイズ 1 MB を超える）
		{envTrashSmall, 1024},    // 最大サイズ以内
	} {
		t.Run(fmt.Sprintf("%s/%d", c.env, c.size), func(t *testing.T) {
			d := testfs.EnvDir(t, c.env)
			root := driveRoot(d)
			t.Logf("V13: %s=%s: settings: %s", c.env, os.Getenv(c.env), bitBucketSettings(t, root))
			p := filepath.Join(d, "file.bin")
			testfs.WriteFile(t, p, strings.Repeat("x", c.size))
			t.Logf("V13: %s, %d bytes: %s", c.env, c.size, trashAndDescribe(t, p, p, root))
		})
	}
}

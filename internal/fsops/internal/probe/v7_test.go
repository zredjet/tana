package probe

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestV7CrossVolume は、FSOPS_CROSSVOL_DIR が一時フォルダと別のボリュームにあり、
// そこへの os.Rename がボリューム違いのエラーになることを確かめる（SPEC §20 V7、§18.2）。
// CI では、ボリュームの作成・マウント（Windows: diskpart の VHD、macOS: hdiutil）の後に実行する。
func TestV7CrossVolume(t *testing.T) {
	cross := testfs.CrossVolDir(t)
	local := testfs.TempDir(t)
	crossID, crossDesc := volumeInfo(t, cross)
	localID, localDesc := volumeInfo(t, local)
	t.Logf("V7: %s=%s: %s", testfs.CrossVolEnv, os.Getenv(testfs.CrossVolEnv), crossDesc)
	t.Logf("V7: temp dir %s: %s", local, localDesc)
	if crossID == localID {
		t.Fatalf("V7: %s is on the same volume as the temp dir (%s)", testfs.CrossVolEnv, crossID)
	}

	// 別ボリュームの中での作成・リネームができること。
	testfs.Build(t, cross, testfs.Tree{"a.txt": testfs.File("a"), "dir/b.txt": testfs.File("b")})
	if err := os.Rename(filepath.Join(cross, "a.txt"), filepath.Join(cross, "dir", "a.txt")); err != nil {
		t.Fatalf("V7: rename within the cross volume: %v", err)
	}

	// ボリュームをまたぐリネームは失敗すること（§11.1 の方式の切り替えの前提）。
	testfs.Build(t, local, testfs.Tree{"c.txt": testfs.File("c")})
	err := os.Rename(filepath.Join(local, "c.txt"), filepath.Join(cross, "c.txt"))
	var errno syscall.Errno
	errors.As(err, &errno)
	t.Logf("V7: os.Rename across volumes: %v (errno %d)", err, uint(errno))
	if err == nil {
		t.Fatal("V7: os.Rename across volumes succeeded; want a cross-device error")
	}
	if !isCrossDevice(errno) {
		t.Errorf("V7: os.Rename across volumes: errno %d, want a cross-device error", uint(errno))
	}
	if !testfs.Exists(t, filepath.Join(local, "c.txt")) || testfs.Exists(t, filepath.Join(cross, "c.txt")) {
		t.Error("V7: a failed cross-volume rename changed the files")
	}
}

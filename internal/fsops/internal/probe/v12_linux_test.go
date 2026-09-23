package probe

import (
	"os"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/unix"
)

// renameNoReplace は renameat2(..., RENAME_NOREPLACE)（SPEC §8.4 の排他リネーム）。
func renameNoReplace(from, to string) error {
	return unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
}

// TestV12 は、Linux の renameat2(RENAME_NOREPLACE) が、一時フォルダ（ubuntu ランナーでは ext4）と
// loop マウントした vfat（FSOPS_PROBE_FAT32_DIR）で使えるか、EINVAL を返す場合があるかを記録する（SPEC §20 V12）。
func TestV12(t *testing.T) {
	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		t.Logf("V12: kernel %s", unix.ByteSliceToString(u.Release[:]))
	}
	root := testfs.TempDir(t)
	var fs unix.Statfs_t
	if err := unix.Statfs(root, &fs); err == nil {
		t.Logf("V12: temp dir %s: f_type=%#x", root, fs.Type)
	}
	renameCases(t, "V12", "temp dir, renameat2(RENAME_NOREPLACE)", root, renameNoReplace)
	t.Run(envFAT32, func(t *testing.T) {
		d := testfs.EnvDir(t, envFAT32)
		renameCases(t, "V12", envFAT32+"="+os.Getenv(envFAT32)+", renameat2(RENAME_NOREPLACE)", d, renameNoReplace)
	})
}

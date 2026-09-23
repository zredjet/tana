package probe

import (
	"os"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestV12 は、APFS 以外のボリューム（hdiutil で作った exFAT・FAT32 のイメージ）で renamex_np(RENAME_EXCL) が使えるかを記録する（SPEC §20 V12）。
// ボリュームは CI が用意する（FSOPS_PROBE_EXFAT_DIR、FSOPS_PROBE_FAT32_DIR）。SMB は CI で用意できないため対象外。
func TestV12(t *testing.T) {
	for _, env := range []string{envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			d := testfs.EnvDir(t, env)
			renameCases(t, "V12", env+"="+os.Getenv(env)+", renamex_np(RENAME_EXCL)", d, renamexExcl)
		})
	}
}

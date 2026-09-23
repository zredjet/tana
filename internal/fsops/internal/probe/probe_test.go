package probe

import "github.com/zredjet/tana/internal/fsops/internal/testfs"

// CI が用意する特別なボリューム・設定のフォルダを指す環境変数（SPEC §18.2）。
const (
	envExFAT      = testfs.ExFATEnv
	envFAT32      = testfs.FAT32Env
	envTrashNuke  = testfs.TrashNukeEnv
	envTrashSmall = testfs.TrashSmallEnv
)

// errString は、ログ用にエラーを文字列にする（nil は "ok"）。
func errString(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

package probe

import (
	"os"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// moveFileExclusive は MoveFileExW(src, dst, 0)（SPEC §8.4 の排他リネーム）。
func moveFileExclusive(t *testing.T) renameFunc {
	return func(from, to string) error {
		return windows.MoveFileEx(u16(t, testfs.ExtendedPath(from)), u16(t, testfs.ExtendedPath(to)), 0)
	}
}

// TestV1 は、MoveFileExW(src, dst, 0) で、大文字小文字だけ違う名前への変更ができるかなどを記録する（SPEC §20 V1）。
// NTFS（一時フォルダとボリュームをまたぐテスト用の VHD）と、exFAT・FAT32 の VHD で調べる。
func TestV1(t *testing.T) {
	logWindowsVersion(t, "V1")
	renameCases(t, "V1", "NTFS (temp dir)", testfs.TempDir(t), moveFileExclusive(t))
	for _, env := range []string{testfs.CrossVolEnv, envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			d := testfs.EnvDir(t, env)
			renameCases(t, "V1", env+"="+os.Getenv(env), d, moveFileExclusive(t))
		})
	}
}

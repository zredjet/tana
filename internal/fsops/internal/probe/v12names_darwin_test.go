package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestV12Names は、APFS 以外のボリューム（exFAT・FAT32 のイメージ）で、NFC・NFD の名前がどう保存・列挙されるか、
// ReadDir が返した名前で Lstat・削除ができるかを記録する（V12 の確認中に見つかった問題。SPEC §8.5、§13.1 の前提）。
func TestV12Names(t *testing.T) {
	check := func(t *testing.T, label, dir string) {
		for _, c := range []struct{ name, created, other string }{
			{"created as NFC", testfs.NameNFC, testfs.NameNFD},
			{"created as NFD", testfs.NameNFD, testfs.NameNFC},
		} {
			d := filepath.Join(dir, sanitize(c.name))
			testfs.Build(t, d, testfs.Tree{c.created: testfs.File("x")})
			listed := listNames(t, d)
			t.Logf("V12: %s: %s %+q: ReadDir=%+q", label, c.name, c.created, listed)
			for _, n := range []string{c.created, c.other} {
				_, err := os.Lstat(filepath.Join(d, n))
				t.Logf("V12: %s: %s: Lstat(%+q): err=%v", label, c.name, n, err)
			}
			if len(listed) == 1 {
				err := os.Remove(filepath.Join(d, listed[0]))
				t.Logf("V12: %s: %s: Remove(listed name %+q): err=%v; names after=%+q", label, c.name, listed[0], err, listNames(t, d))
			}
			if names := listNames(t, d); len(names) > 0 {
				err := os.Remove(filepath.Join(d, c.created))
				t.Logf("V12: %s: %s: Remove(created name %+q): err=%v; names after=%+q", label, c.name, c.created, err, listNames(t, d))
			}
		}
	}
	check(t, "APFS (temp dir)", testfs.TempDir(t))
	for _, env := range []string{envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			check(t, env+"="+os.Getenv(env), testfs.EnvDir(t, env))
		})
	}
}

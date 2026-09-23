package probe

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestV20 は、シンボリックリンクを作れないボリュームでシンボリックリンクを作ったときに返るエラー番号を記録する
// （V20。§14.2 のコピーで、§17 のどの Kind にするかを決めるため）。一時フォルダ（作れるはず）と比べる。
func TestV20(t *testing.T) {
	check := func(t *testing.T, label, dir string) {
		testfs.Build(t, dir, testfs.Tree{"target.txt": testfs.File("t"), "targetdir": testfs.Dir()})
		for _, c := range []struct{ name, target string }{{"filelink", "target.txt"}, {"dirlink", "targetdir"}, {"dangling", "missing"}} {
			err := os.Symlink(c.target, testfs.ExtendedPath(filepath.Join(dir, c.name)))
			var errno syscall.Errno
			errors.As(err, &errno)
			t.Logf("V20: %s: symlink %s -> %s: err=%v errno=%d", label, c.name, c.target, err, uint64(errno))
		}
	}
	check(t, "temp dir", testfs.TempDir(t))
	for _, env := range []string{envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			check(t, env+"="+os.Getenv(env), testfs.EnvDir(t, env))
		})
	}
}

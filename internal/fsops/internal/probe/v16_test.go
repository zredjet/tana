package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestV16 は、ボリュームの種類ごとに、fileID（SPEC §8.3）が取得できるか、名前の変更・別フォルダへの移動・書き換え・
// 同じ名前での作り直しの前後で変わるかを記録する（V16。V14 の確認中に見つかった問題。§13.3 の照合の前提）。
// fileID の取得方法は OS ごとに fileIDs（v16_windows_test.go、v16_unix_test.go）で定める。
func TestV16(t *testing.T) {
	check := func(t *testing.T, label, dir string) {
		testfs.Build(t, dir, testfs.Tree{"d1/a.txt": testfs.File("a"), "d2": testfs.Dir()})
		steps := []struct {
			name string
			do   func() string // 操作の後のファイルのパス
		}{
			{"created", func() string { return filepath.Join(dir, "d1", "a.txt") }},
			{"renamed in the same dir", func() string {
				mustRename(t, filepath.Join(dir, "d1", "a.txt"), filepath.Join(dir, "d1", "b.txt"))
				return filepath.Join(dir, "d1", "b.txt")
			}},
			{"moved to another dir", func() string {
				mustRename(t, filepath.Join(dir, "d1", "b.txt"), filepath.Join(dir, "d2", "b.txt"))
				return filepath.Join(dir, "d2", "b.txt")
			}},
			{"rewritten in place", func() string {
				p := filepath.Join(dir, "d2", "b.txt")
				f, err := os.OpenFile(testfs.ExtendedPath(p), os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				f.WriteString("more data")
				f.Close()
				return p
			}},
			{"deleted and recreated with the same name", func() string {
				p := filepath.Join(dir, "d2", "b.txt")
				if err := os.Remove(testfs.ExtendedPath(p)); err != nil {
					t.Fatal(err)
				}
				testfs.WriteFile(t, p, "new")
				return p
			}},
		}
		for _, s := range steps {
			p := s.do()
			t.Logf("V16: %s: %s: %s", label, s.name, fileIDs(t, p))
		}
	}
	check(t, "temp dir", testfs.TempDir(t))
	for _, env := range []string{testfs.CrossVolEnv, envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			check(t, env+"="+os.Getenv(env), testfs.EnvDir(t, env))
		})
	}
}

func mustRename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(testfs.ExtendedPath(from), testfs.ExtendedPath(to)); err != nil {
		t.Fatal(err)
	}
}

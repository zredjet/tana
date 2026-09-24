package probe

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestV25 は、空のファイルの fileID（SPEC §8.3）が、メタデータの設定（パーミッション・更新日時）・同じフォルダの中での名前の変更・
// 書き込みで変わるかを、中身のあるファイルと比べて記録する（V25。macOS の exFAT・FAT32 へ空のファイルをコピーすると、
// メタデータを設定した後の照合（§10.1）が合わずに失敗し、一時ファイルが残ることが見つかったため。V16 は中身のあるファイルだけを確かめていた）。
func TestV25(t *testing.T) {
	check := func(t *testing.T, label, dir string) {
		for _, c := range []struct{ name, data string }{{"empty", ""}, {"one byte", "x"}} {
			p := filepath.Join(dir, "v25-"+c.name+".txt")
			testfs.WriteFile(t, p, c.data)
			steps := []struct {
				name string
				do   func() string
			}{
				{"created", func() string { return p }},
				{"chmod", func() string {
					if err := os.Chmod(testfs.ExtendedPath(p), 0o644); err != nil {
						t.Logf("V25: chmod: %v", err)
					}
					return p
				}},
				{"chtimes", func() string {
					m := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
					if err := os.Chtimes(testfs.ExtendedPath(p), m, m); err != nil {
						t.Logf("V25: chtimes: %v", err)
					}
					return p
				}},
				{"renamed in the same dir (same name length)", func() string {
					q := p + "2"
					mustRename(t, p, q)
					mustRename(t, q, p)
					return p
				}},
				{"written", func() string {
					f, err := os.OpenFile(testfs.ExtendedPath(p), os.O_WRONLY|os.O_APPEND, 0)
					if err != nil {
						t.Fatal(err)
					}
					f.WriteString("more data")
					f.Close()
					return p
				}},
			}
			for _, s := range steps {
				t.Logf("V25: %s: %s: %s: %s", label, c.name, s.name, fileIDs(t, s.do()))
			}
		}
	}
	check(t, "temp dir", testfs.TempDir(t))
	for _, env := range []string{envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			check(t, env+"="+os.Getenv(env), testfs.EnvDir(t, env))
		})
	}
}

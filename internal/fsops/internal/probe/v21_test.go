package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestV21 は、排他リネーム（§8.4）の代わりになる不可分な操作が、ボリュームの種類ごとに使えるかを記録する（SPEC §20 V21。総点検の穴 5）。
// §8.4 の代わりの手段（名前を確保してから置き換える）の残る危険を、別の操作で閉じられるかを判断するため。
// ハードリンク（link。移動先があれば失敗する）と、OS ごとの操作（v21Ops。macOS の renamex_np の RENAME_EXCL・RENAME_SWAP と clonefile、
// Linux の renameat2 の RENAME_NOREPLACE・RENAME_EXCHANGE、Windows の MoveFileExW(0)）を試す。
func TestV21(t *testing.T) {
	check := func(t *testing.T, label, dir string) {
		d := filepath.Join(dir, "v21")
		testfs.MkdirAll(t, d)
		a, b, existing := filepath.Join(d, "a"), filepath.Join(d, "b"), filepath.Join(d, "existing")
		testfs.WriteFile(t, a, "a")
		testfs.WriteFile(t, existing, "existing")
		t.Logf("V21: %s: link(new name): %v", label, errText(os.Link(testfs.ExtendedPath(a), testfs.ExtendedPath(b))))
		t.Logf("V21: %s: link(existing name): %v", label, errText(os.Link(testfs.ExtendedPath(a), testfs.ExtendedPath(existing))))
		for _, op := range v21Ops(d) {
			t.Logf("V21: %s: %s: %v", label, op.name, errText(op.run()))
		}
		if got := testfs.ReadFile(t, existing); got != "existing" {
			t.Errorf("V21: %s: an existing file was overwritten: %q", label, got)
		}
	}
	check(t, "temp dir", testfs.TempDir(t))
	for _, env := range []string{envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			check(t, env+"="+os.Getenv(env), testfs.EnvDir(t, env))
		})
	}
}

// v21Op は、TestV21 で試す操作。
type v21Op struct {
	name string
	run  func() error
}

func errText(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

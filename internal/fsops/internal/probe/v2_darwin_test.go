package probe

import (
	"os"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/unix"
)

// renamexExcl は renamex_np(from, to, RENAME_EXCL)（SPEC §8.4 の排他リネーム）。
func renamexExcl(from, to string) error { return unix.RenamexNp(from, to, unix.RENAME_EXCL) }

// TestV2 は、APFS で renamex_np(RENAME_EXCL) を使って、大文字小文字だけ・NFC/NFD だけ違う名前へ変更したときの動作を記録する（SPEC §20 V2）。
// 比較のため、os.Rename（rename(2)）の動作も記録する。
func TestV2(t *testing.T) {
	root := testfs.TempDir(t)
	t.Logf("V2: temp dir: FoldsCase=%v FoldsNormalization=%v", testfs.FoldsCase(t, root), testfs.FoldsNormalization(t, root))
	renameCases(t, "V2", "APFS (temp dir), renamex_np(RENAME_EXCL)", root, renamexExcl)
	t.Run("os.Rename", func(t *testing.T) {
		logPlainRename(t, "V2", "APFS (temp dir), rename(2)", testfs.TempDir(t))
	})
	t.Run(testfs.CrossVolEnv, func(t *testing.T) {
		renameCases(t, "V2", testfs.CrossVolEnv+"="+os.Getenv(testfs.CrossVolEnv)+", renamex_np(RENAME_EXCL)", testfs.CrossVolDir(t), renamexExcl)
	})
}

// logPlainRename は、比較のために os.Rename で大文字小文字だけ・NFC/NFD だけの変更を行った結果を記録する。
// os.Rename は上書きするので、既存の移動先がある場合は試さない。
func logPlainRename(t *testing.T, v, label, dir string) {
	t.Helper()
	for _, c := range []struct{ name, from, to string }{
		{"case only (file)", testfs.NameLower, testfs.NameUpper},
		{"NFC to NFD (file)", testfs.NameNFC, testfs.NameNFD},
	} {
		d := dir + "/" + sanitize(c.name)
		testfs.Build(t, d, testfs.Tree{c.from: testfs.File("x")})
		err := os.Rename(d+"/"+c.from, d+"/"+c.to)
		t.Logf("%s: %s: %s: %+q -> %+q: err=%v; names after=%+q", v, label, c.name, c.from, c.to, err, listNames(t, d))
	}
}

package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

const zoneIdentifier = "[ZoneTransfer]\r\nZoneId=3\r\nHostUrl=https://example.com/file.txt\r\n"

// TestV6 は、Windows の Zone.Identifier（代替データストリーム）を os で読み書きできるかを記録する（SPEC §20 V6、§15）。
// NTFS（一時フォルダ）と、exFAT・FAT32 の VHD（代替データストリームがないファイルシステム）で調べる。
func TestV6(t *testing.T) {
	logWindowsVersion(t, "V6")
	check := func(t *testing.T, label, dir string) {
		p := filepath.Join(dir, "downloaded.txt")
		testfs.WriteFile(t, p, "main content")
		ads := testfs.ExtendedPath(p) + ":Zone.Identifier"

		_, err := os.ReadFile(ads)
		t.Logf("V6: %s: read a missing stream: err=%v (errno %d)", label, err, errnoNum(err))

		err = os.WriteFile(ads, []byte(zoneIdentifier), 0o644)
		t.Logf("V6: %s: write %s via \\\\?\\ path: err=%v", label, ":Zone.Identifier", err)
		if err != nil {
			return
		}
		b, err := os.ReadFile(ads)
		t.Logf("V6: %s: read back: equal=%v err=%v", label, string(b) == zoneIdentifier, err)
		b, err = os.ReadFile(p + ":Zone.Identifier")
		t.Logf("V6: %s: read back without \\\\?\\: equal=%v err=%v", label, string(b) == zoneIdentifier, err)
		fi, err := os.Stat(ads)
		t.Logf("V6: %s: Stat(stream): size=%v err=%v", label, sizeOf(fi), err)
		t.Logf("V6: %s: main content unchanged: %v", label, testfs.ReadFile(t, p) == "main content")

		// 同じボリューム内のリネームで、ストリームが一緒に移るか。
		moved := filepath.Join(dir, "moved.txt")
		if err := os.Rename(testfs.ExtendedPath(p), testfs.ExtendedPath(moved)); err == nil {
			b, err := os.ReadFile(testfs.ExtendedPath(moved) + ":Zone.Identifier")
			t.Logf("V6: %s: after rename: stream equal=%v err=%v", label, string(b) == zoneIdentifier, err)
		}
	}
	check(t, "NTFS (temp dir)", testfs.TempDir(t))
	for _, env := range []string{envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			check(t, env+"="+os.Getenv(env), testfs.EnvDir(t, env))
		})
	}
}

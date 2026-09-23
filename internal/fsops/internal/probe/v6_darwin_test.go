package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/unix"
)

const quarantine = "com.apple.quarantine"

// TestV6 は、macOS の com.apple.quarantine を golang.org/x/sys/unix の Getxattr / Setxattr で読み書きできるかを記録する（SPEC §20 V6、§15）。
// APFS（一時フォルダ）と、exFAT・FAT32 のイメージ（拡張属性は AppleDouble の ._ ファイルに保存される）で調べる。
func TestV6(t *testing.T) {
	value := []byte("0083;66f00000;Safari;F0E0D0C0-0000-0000-0000-000000000000")
	check := func(t *testing.T, label, dir string) {
		p := filepath.Join(dir, "downloaded.txt")
		testfs.WriteFile(t, p, "main content")

		buf := make([]byte, 256)
		_, err := unix.Getxattr(p, quarantine, buf)
		t.Logf("V6: %s: Getxattr on a file without it: err=%v (errno %d)", label, err, errnoNum(err))

		err = unix.Setxattr(p, quarantine, value, 0)
		t.Logf("V6: %s: Setxattr: err=%v", label, err)
		if err != nil {
			return
		}
		n, err := unix.Getxattr(p, quarantine, buf)
		got := ""
		if err == nil {
			got = string(buf[:n])
		}
		t.Logf("V6: %s: Getxattr: %q equal=%v err=%v", label, got, got == string(value), err)

		// 大きさを問い合わせる呼び方（dest に nil）
		n, err = unix.Getxattr(p, quarantine, nil)
		t.Logf("V6: %s: Getxattr(size query): %d err=%v", label, n, err)

		lbuf := make([]byte, 1024)
		n, err = unix.Listxattr(p, lbuf)
		t.Logf("V6: %s: Listxattr: %q err=%v", label, lbuf[:max(n, 0)], err)
		t.Logf("V6: %s: names in the dir: %+q", label, testfs.ListNames(t, dir))

		// シンボリックリンク自体には付けない（XATTR_NOFOLLOW で リンク先に付かないこと）の確認。
		link := filepath.Join(dir, "link")
		if err := os.Symlink("downloaded.txt", link); err == nil {
			err := unix.Setxattr(link, "com.example.fsops-probe", []byte("x"), unix.XATTR_NOFOLLOW)
			_, gerr := unix.Getxattr(p, "com.example.fsops-probe", buf)
			t.Logf("V6: %s: Setxattr(link, XATTR_NOFOLLOW): err=%v; target got it: %v", label, err, gerr == nil)
		}
	}
	check(t, "APFS (temp dir)", testfs.TempDir(t))
	for _, env := range []string{envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			check(t, env+"="+os.Getenv(env), testfs.EnvDir(t, env))
		})
	}
}

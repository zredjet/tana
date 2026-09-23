package probe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/unix"
)

// reserveThenRename は SPEC §8.4 の「排他リネームが使えないボリュームでの代わりの手段」を、そのとおりに実装したもの。
// 1. 移動先の名前を確保する（ファイルは O_EXCL で空のファイル、フォルダは mkdir）。
// 2. 確保したものが自分の作ったもの（fileID が一致し、ファイルならサイズ 0、フォルダなら空）であることを確かめる。
// 3. 通常の rename で置き換える。4. 失敗したら、確保したものを（fileID が一致する場合だけ）消す。
func reserveThenRename(from, to string) error {
	var src unix.Stat_t
	if err := unix.Lstat(from, &src); err != nil {
		return err
	}
	isDir := src.Mode&unix.S_IFMT == unix.S_IFDIR
	if isDir {
		if err := unix.Mkdir(to, 0o700); err != nil {
			return fmt.Errorf("reserve: %w", err)
		}
	} else {
		fd, err := unix.Open(to, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW, 0o600)
		if err != nil {
			return fmt.Errorf("reserve: %w", err)
		}
		unix.Close(fd)
	}
	var reserved unix.Stat_t
	if err := unix.Lstat(to, &reserved); err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	cleanup := func() {
		var now unix.Stat_t
		if unix.Lstat(to, &now) == nil && now.Dev == reserved.Dev && now.Ino == reserved.Ino {
			if isDir {
				unix.Rmdir(to)
			} else {
				unix.Unlink(to)
			}
		}
	}
	if !isDir && reserved.Size != 0 {
		return errors.New("verify: the reserved file is not empty")
	}
	if isDir {
		if entries, err := os.ReadDir(to); err != nil || len(entries) != 0 {
			return fmt.Errorf("verify: the reserved dir is not empty (%v)", err)
		}
	}
	if err := unix.Rename(from, to); err != nil {
		cleanup()
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// TestV12Fallback は、§8.4 の代わりの手段が、RENAME_EXCL の使えない exFAT（と比較のため APFS・FAT32）で期待どおりに動くかを確かめる（V12 の追加確認）。
// 既存の移動先を上書きしないこと（I1）は、テストの失敗として検出する。
func TestV12Fallback(t *testing.T) {
	check := func(t *testing.T, label, dir string) {
		t.Logf("V12: %s: RENAME_EXCL for a new name: %v", label, func() error {
			d := filepath.Join(dir, "probe-excl")
			testfs.Build(t, d, testfs.Tree{"a": testfs.File("a")})
			return renamexExcl(filepath.Join(d, "a"), filepath.Join(d, "b"))
		}())
		renameCases(t, "V12", label+", reserve-then-rename (§8.4 fallback)", dir, reserveThenRename)

		// フォルダを、中身のあるまま新しい名前へ。
		d := filepath.Join(dir, "dir-with-contents")
		testfs.Build(t, d, testfs.Tree{"src/a.txt": testfs.File("a"), "src/sub/b.txt": testfs.File("b")})
		err := reserveThenRename(filepath.Join(d, "src"), filepath.Join(d, "dst"))
		t.Logf("V12: %s: reserve-then-rename a non-empty dir: err=%v names=%+q dst/a.txt=%v", label, err, listNames(t, d),
			testfs.Exists(t, filepath.Join(d, "dst", "a.txt")))
		if err != nil {
			t.Errorf("V12: %s: reserve-then-rename of a directory failed: %v", label, err)
		}

		// 大文字小文字だけの変更は §8.4 の「同じファイルの名前変更」として通常の rename で行う。その動作を記録する。
		d = filepath.Join(dir, "case-plain-rename")
		testfs.Build(t, d, testfs.Tree{testfs.NameLower: testfs.File("c")})
		err = unix.Rename(filepath.Join(d, testfs.NameLower), filepath.Join(d, testfs.NameUpper))
		t.Logf("V12: %s: plain rename(2) case only: err=%v names=%+q", label, err, listNames(t, d))
	}
	check(t, "APFS (temp dir)", testfs.TempDir(t))
	for _, env := range []string{envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			check(t, env+"="+os.Getenv(env), testfs.EnvDir(t, env))
		})
	}
}

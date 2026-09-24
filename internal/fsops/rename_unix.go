//go:build unix

package fsops

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// renamePlainSys は、同じファイルの名前変更（§8.4）に使う OS の通常のリネーム（rename(2)）。
func renamePlainSys(s, d string) error {
	if err := ignoringEINTR(func() error { return unix.Rename(s, d) }); err != nil {
		return &os.LinkError{Op: "rename", Old: s, New: d, Err: err}
	}
	return nil
}

// reserveThenRename は §8.4 の「排他リネームが使えないボリュームでの代わりの手段」。src・dst は checkPath を通ったパス。
func reserveThenRename(src, dst string) error {
	return withUserPaths(reserveThenRenameSys(src, dst), src, dst)
}

// reserveThenRenameSys は、名前を確保してから置き換える（§8.4 の手順 1〜4）。
func reserveThenRenameSys(s, d string) error {
	return reserveThenRenameAt(unix.AT_FDCWD, s, unix.AT_FDCWD, d)
}

// reserveThenRenameAt は reserveThenRenameSys の、移動元をフォルダ sfd からの相対の名前 s、移動先をフォルダ dfd からの相対の名前 d で
// 指定する形（§13.1。書き込み先・マージ移動の移動元を、確かめて開いたフォルダからの相対で扱う）。AT_FDCWD ならパス。
func reserveThenRenameAt(sfd int, s string, dfd int, d string) error {
	linkErr := func(err error) error { return &os.LinkError{Op: "rename", Old: s, New: d, Err: err} }
	var src unix.Stat_t
	if err := ignoringEINTR(func() error { return unix.Fstatat(sfd, s, &src, unix.AT_SYMLINK_NOFOLLOW) }); err != nil {
		return linkErr(err)
	}
	isDir := src.Mode&unix.S_IFMT == unix.S_IFDIR

	// 1. 移動先の名前を確保し、作ったものの fileID を記録する。
	var reserved unix.Stat_t
	if isDir {
		if err := ignoringEINTR(func() error { return unix.Mkdirat(dfd, d, 0o700) }); err != nil {
			return linkErr(err)
		}
		if err := ignoringEINTR(func() error { return unix.Fstatat(dfd, d, &reserved, unix.AT_SYMLINK_NOFOLLOW) }); err != nil {
			return linkErr(unix.EEXIST) // 確かめられない
		}
	} else {
		fd, err := ignoringEINTR2(func() (int, error) {
			return unix.Openat(dfd, d, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		})
		if err != nil {
			return linkErr(err)
		}
		err = ignoringEINTR(func() error { return unix.Fstat(fd, &reserved) })
		unix.Close(fd)
		if err != nil {
			return linkErr(unix.EEXIST)
		}
	}

	// 2. 確保したものが自分の作ったもののままであることを確かめる。
	var now unix.Stat_t
	ok := ignoringEINTR(func() error { return unix.Fstatat(dfd, d, &now, unix.AT_SYMLINK_NOFOLLOW) }) == nil && now.Dev == reserved.Dev && now.Ino == reserved.Ino
	if ok && isDir {
		ok = now.Mode&unix.S_IFMT == unix.S_IFDIR && dirIsEmptyAt(dfd, d)
	} else if ok {
		ok = now.Mode&unix.S_IFMT == unix.S_IFREG && now.Size == 0
	}
	if !ok {
		removeIfSame(dfd, d, &reserved, isDir)
		return linkErr(unix.EEXIST)
	}

	// 3. 置き換える。4. 失敗したら確保したものを消す。
	if err := ignoringEINTR(func() error { return unix.Renameat(sfd, s, dfd, d) }); err != nil {
		removeIfSame(dfd, d, &reserved, isDir)
		return linkErr(err)
	}
	return nil
}

// removeIfSame は、フォルダ dfd の中の d が want と同じ fileID の場合だけ消す。
func removeIfSame(dfd int, d string, want *unix.Stat_t, isDir bool) {
	var now unix.Stat_t
	if ignoringEINTR(func() error { return unix.Fstatat(dfd, d, &now, unix.AT_SYMLINK_NOFOLLOW) }) != nil || now.Dev != want.Dev || now.Ino != want.Ino {
		return
	}
	flags := 0
	if isDir {
		flags = unix.AT_REMOVEDIR
	}
	ignoringEINTR(func() error { return unix.Unlinkat(dfd, d, flags) })
}

// dirIsEmptyAt は、フォルダ dfd の中のフォルダ d が空かを返す。読めなければ空でないとみなす。
func dirIsEmptyAt(dfd int, d string) bool {
	fd, err := ignoringEINTR2(func() (int, error) {
		return unix.Openat(dfd, d, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	})
	if err != nil {
		return false
	}
	f := os.NewFile(uintptr(fd), d)
	defer f.Close()
	_, err = f.Readdirnames(1)
	return errors.Is(err, io.EOF)
}

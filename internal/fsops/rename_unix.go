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
	if err := unix.Rename(s, d); err != nil {
		return &os.LinkError{Op: "rename", Old: s, New: d, Err: err}
	}
	return nil
}

// reserveThenRename は §8.4 の「排他リネームが使えないボリュームでの代わりの手段」。src・dst は checkPath を通ったパス。
func reserveThenRename(src, dst string) error {
	return withUserPaths(reserveThenRenameSys(src, dst), src, dst)
}

// reserveThenRenameSys は、名前を確保してから置き換える（§8.4 の手順 1〜4）。
func reserveThenRenameSys(s, d string) error { return reserveThenRenameAt(unix.AT_FDCWD, s, d) }

// reserveThenRenameAt は reserveThenRenameSys の、移動元をフォルダ fd からの相対の名前 s で指定する形（§13.1 のマージ移動用）。
// fd が AT_FDCWD なら s はパス。d はパス。
func reserveThenRenameAt(fd int, s, d string) error {
	linkErr := func(err error) error { return &os.LinkError{Op: "rename", Old: s, New: d, Err: err} }
	var src unix.Stat_t
	if err := unix.Fstatat(fd, s, &src, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return linkErr(err)
	}
	isDir := src.Mode&unix.S_IFMT == unix.S_IFDIR

	// 1. 移動先の名前を確保し、作ったものの fileID を記録する。
	var reserved unix.Stat_t
	if isDir {
		if err := unix.Mkdir(d, 0o700); err != nil {
			return linkErr(err)
		}
		if err := unix.Lstat(d, &reserved); err != nil {
			return linkErr(unix.EEXIST) // 確かめられない
		}
	} else {
		fd, err := unix.Open(d, unix.O_CREAT|unix.O_EXCL|unix.O_WRONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
		if err != nil {
			return linkErr(err)
		}
		err = unix.Fstat(fd, &reserved)
		unix.Close(fd)
		if err != nil {
			removeIfSame(d, nil, isDir)
			return linkErr(unix.EEXIST)
		}
	}

	// 2. 確保したものが自分の作ったもののままであることを確かめる。
	var now unix.Stat_t
	ok := unix.Lstat(d, &now) == nil && now.Dev == reserved.Dev && now.Ino == reserved.Ino
	if ok && isDir {
		ok = now.Mode&unix.S_IFMT == unix.S_IFDIR && dirIsEmpty(d)
	} else if ok {
		ok = now.Mode&unix.S_IFMT == unix.S_IFREG && now.Size == 0
	}
	if !ok {
		removeIfSame(d, &reserved, isDir)
		return linkErr(unix.EEXIST)
	}

	// 3. 置き換える。4. 失敗したら確保したものを消す。
	if err := unix.Renameat(fd, s, unix.AT_FDCWD, d); err != nil {
		removeIfSame(d, &reserved, isDir)
		return linkErr(err)
	}
	return nil
}

// removeIfSame は、d が want と同じ fileID の場合だけ消す（want が nil なら何もしない）。
func removeIfSame(d string, want *unix.Stat_t, isDir bool) {
	if want == nil {
		return
	}
	var now unix.Stat_t
	if unix.Lstat(d, &now) != nil || now.Dev != want.Dev || now.Ino != want.Ino {
		return
	}
	if isDir {
		unix.Rmdir(d)
	} else {
		unix.Unlink(d)
	}
}

// dirIsEmpty は、フォルダ d が空かを返す。読めなければ空でないとみなす。
func dirIsEmpty(d string) bool {
	f, err := os.Open(d)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	return errors.Is(err, io.EOF)
}

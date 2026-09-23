//go:build unix

package fsops

import (
	"errors"
	"io/fs"
	"os"
	"slices"
	"time"

	"golang.org/x/sys/unix"
)

// readDirSys は、フォルダを O_NOFOLLOW で開き、名前を列挙して、各エントリを fstatat(AT_SYMLINK_NOFOLLOW) で調べる。
func readDirSys(s string) ([]dirEntry, error) {
	fd, err := unix.Open(s, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: s, Err: err}
	}
	defer unix.Close(fd)
	return listFD(fd, s)
}

// listFD は、開いたフォルダ fd の中身を名前のバイト順で列挙する。fd は閉じない。s はエラーに使うパス。
func listFD(fd int, s string) ([]dirEntry, error) {
	dup, err := unix.Dup(fd) // 列挙で読み進める位置を fd と共有しないように複製する
	if err != nil {
		return nil, &os.PathError{Op: "dup", Path: s, Err: err}
	}
	f := os.NewFile(uintptr(dup), s)
	defer f.Close()
	if _, err := unix.Seek(dup, 0, 0); err != nil {
		return nil, &os.PathError{Op: "seek", Path: s, Err: err}
	}
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	entries := make([]dirEntry, 0, len(names))
	for _, name := range names {
		e, err := statAt(fd, name)
		if errors.Is(err, unix.ENOENT) {
			continue // 列挙の後に消えた
		}
		if err != nil {
			return nil, &os.PathError{Op: "fstatat", Path: s + "/" + name, Err: err}
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// statAt は、フォルダ fd の中の name を fstatat(AT_SYMLINK_NOFOLLOW) で調べる。
func statAt(fd int, name string) (dirEntry, error) {
	var st unix.Stat_t
	if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return dirEntry{}, err
	}
	t := entryTypeFromMode(fileModeFromStat(uint32(st.Mode)))
	info := EntryInfo{Type: t, ModTime: time.Unix(st.Mtim.Unix())}
	if t == TypeFile {
		info.Size = st.Size
	}
	return dirEntry{name: name, info: info, id: idStatFromStat(&st).id, dirAttr: t == TypeDir}, nil
}

// fileModeFromStat は Stat_t の Mode の種類の部分を fs.FileMode にする（entryTypeFromMode で使う分だけ）。
func fileModeFromStat(m uint32) fs.FileMode {
	switch m & unix.S_IFMT {
	case unix.S_IFREG:
		return 0
	case unix.S_IFDIR:
		return fs.ModeDir
	case unix.S_IFLNK:
		return fs.ModeSymlink
	}
	return fs.ModeIrregular
}

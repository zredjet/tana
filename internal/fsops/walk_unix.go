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
	f := os.NewFile(uintptr(fd), s)
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	entries := make([]dirEntry, 0, len(names))
	for _, name := range names {
		var st unix.Stat_t
		if err := unix.Fstatat(fd, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if errors.Is(err, unix.ENOENT) {
				continue // 列挙の後に消えた
			}
			return nil, &os.PathError{Op: "fstatat", Path: s + "/" + name, Err: err}
		}
		t := entryTypeFromMode(fileModeFromStat(uint32(st.Mode)))
		info := EntryInfo{Type: t, ModTime: time.Unix(st.Mtim.Unix())}
		if t == TypeFile {
			info.Size = st.Size
		}
		entries = append(entries, dirEntry{name: name, info: info, id: idStatFromStat(&st).id})
	}
	return entries, nil
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

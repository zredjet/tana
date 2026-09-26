//go:build unix

package fsops

import (
	"os"

	"golang.org/x/sys/unix"
)

// readHeadSys は ReadHead の Unix の実装。stat（リンクを辿る）で通常のファイルかを調べてから O_NONBLOCK で開き
// （調べた後に FIFO に置き換えられていても open で止まらない）、開いたものをもう一度 fstat で調べてから読む。
func readHeadSys(s string, max int) (Head, error) {
	var st unix.Stat_t
	if err := ignoringEINTR(func() error { return unix.Stat(s, &st) }); err != nil {
		return Head{}, &os.PathError{Op: "stat", Path: s, Err: err}
	}
	if h, done, err := checkHeadStat(s, &st); done {
		return h, err
	}
	fd, err := ignoringEINTR2(func() (int, error) {
		return unix.Open(s, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	})
	if err != nil {
		return Head{}, &os.PathError{Op: "open", Path: s, Err: err}
	}
	defer unix.Close(fd)
	if err := ignoringEINTR(func() error { return unix.Fstat(fd, &st) }); err != nil {
		return Head{}, &os.PathError{Op: "fstat", Path: s, Err: err}
	}
	if h, done, err := checkHeadStat(s, &st); done {
		return h, err
	}
	buf := make([]byte, headBufSize(max, st.Size))
	n := 0
	for n < len(buf) {
		m, err := ignoringEINTR2(func() (int, error) { return unix.Read(fd, buf[n:]) })
		if err != nil {
			return Head{}, &os.PathError{Op: "read", Path: s, Err: err}
		}
		if m == 0 {
			break
		}
		n += m
	}
	return Head{Data: buf[:n], Size: st.Size}, nil
}

// checkHeadStat は、st が通常のファイルでなければエラーを、中身が手元になければ NotLocal を返す（done が真）。
func checkHeadStat(s string, st *unix.Stat_t) (h Head, done bool, err error) {
	switch {
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		return Head{}, true, &os.PathError{Op: "stat", Path: s, Err: errNotRegular}
	case notLocalStat(st):
		return Head{Size: st.Size, NotLocal: true}, true, nil
	}
	return Head{}, false, nil
}

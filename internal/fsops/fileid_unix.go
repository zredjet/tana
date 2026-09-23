//go:build unix

package fsops

import (
	"encoding/binary"
	"os"

	"golang.org/x/sys/unix"
)

// statIDSys は、sysPath で変換済みのパス p の fileID を返す。follow が偽ならリンクを辿らない。
func statIDSys(p string, follow bool) (idStat, error) {
	var st unix.Stat_t
	var err error
	op := "lstat"
	if follow {
		op = "stat"
		err = unix.Stat(p, &st)
	} else {
		err = unix.Lstat(p, &st)
	}
	if err != nil {
		return idStat{}, &os.PathError{Op: op, Path: p, Err: err}
	}
	return idStatFromStat(&st), nil
}

func idStatFromStat(st *unix.Stat_t) idStat {
	var id fileID
	id.method = idMethodDevIno
	id.vol = uint64(st.Dev)
	binary.LittleEndian.PutUint64(id.id[:], uint64(st.Ino))
	return idStat{id: id, isDir: st.Mode&unix.S_IFMT == unix.S_IFDIR, nlink: uint64(st.Nlink)}
}

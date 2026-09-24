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
		err = ignoringEINTR(func() error { return unix.Stat(p, &st) })
	} else {
		err = ignoringEINTR(func() error { return unix.Lstat(p, &st) })
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
	ino := uint64(st.Ino)
	if ino >= syntheticIno && st.Size == 0 && st.Mode&unix.S_IFMT == unix.S_IFREG {
		// fileID では同一性を確かめられないので、「そのボリュームの空のファイル」を表す一定の値にする（§8.3）。
		// 照合は、一緒に比べる種類・大きさ・更新日時で行うことになる。空のファイルはデータを持たないので、取り違えても失うデータはない。
		ino = syntheticIno
	}
	binary.LittleEndian.PutUint64(id.id[:], ino)
	return idStat{id: id, isDir: st.Mode&unix.S_IFMT == unix.S_IFDIR, nlink: uint64(st.Nlink)}
}

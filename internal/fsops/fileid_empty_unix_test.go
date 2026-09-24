//go:build unix

package fsops

import (
	"testing"

	"golang.org/x/sys/unix"
)

// TestIDStatEmptySynthetic は、macOS の exFAT・FAT32 の空のファイルの仮の ino（2^63 以上。操作のたびに変わる。V25）を、
// 一定の値に置き換えることを確かめる（§8.3）。中身のあるファイル・フォルダ・小さな ino は置き換えない。
func TestIDStatEmptySynthetic(t *testing.T) {
	t.Parallel()
	// Stat_t.Mode の型は OS ごとに違う（macOS は uint16、Linux は uint32）ので、型の付かない定数で代入する。
	st := func(dir bool, size int64, ino uint64) *unix.Stat_t {
		var s unix.Stat_t
		s.Dev = 7
		s.Mode = unix.S_IFREG
		if dir {
			s.Mode = unix.S_IFDIR
		}
		s.Size = size
		s.Ino = ino
		return &s
	}
	const synthetic1, synthetic2 = 1<<64 - 5, 1<<64 - 6
	a := idStatFromStat(st(false, 0, synthetic1)).id
	b := idStatFromStat(st(false, 0, synthetic2)).id
	if a != b {
		t.Errorf("empty files with synthetic inodes: %v != %v, want the same fileID", a, b)
	}
	for _, c := range []struct {
		name string
		s    *unix.Stat_t
	}{
		{"file with data", st(false, 1, synthetic2)},
		{"folder", st(true, 0, synthetic2)},
		{"empty file with a real inode", st(false, 0, 12)},
	} {
		if idStatFromStat(c.s).id == a {
			t.Errorf("%s: fileID was replaced", c.name)
		}
	}
	if idStatFromStat(st(false, 0, 12)).id == idStatFromStat(st(false, 0, 13)).id {
		t.Error("empty files with different real inodes got the same fileID")
	}
}

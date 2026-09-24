package probe

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestV24 は、FAT32 のファイルの大きさの上限（4 GiB − 1 バイト）を超える書き込みで返るエラー番号を記録する
// （V24。§17 で容量不足（KindNoSpace）と区別できるか、区別できれば専用の Kind を設けるため）。
// CI のボリュームは 64 MB なので、実際に 4 GiB を書く代わりに、上限の直前・直後の位置へ 1 バイト書く・大きさを変える。
// 上限ちょうどまで（大きさ 4 GiB − 1）は容量不足になるはずで、上限を超える場合と比べる。
// exFAT（上限がない）でも同じことをして、容量不足のエラー番号を比べる。
func TestV24(t *testing.T) {
	const limit = 1<<32 - 1 // FAT32 のファイルの大きさの上限
	cases := []struct {
		name  string
		write bool  // 真なら WriteAt、偽なら Truncate
		size  int64 // 操作後のファイルの大きさ
	}{
		{"write to size limit", true, limit},
		{"write to size limit+1", true, limit + 1},
		{"write to size limit+2", true, limit + 2},
		{"truncate to size limit", false, limit},
		{"truncate to size limit+1", false, limit + 1},
	}
	for _, env := range []string{envFAT32, envExFAT} {
		t.Run(env, func(t *testing.T) {
			dir := testfs.EnvDir(t, env)
			for i, c := range cases {
				p := testfs.ExtendedPath(filepath.Join(dir, "v24-"+string(rune('a'+i))+".bin"))
				f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
				if err != nil {
					t.Fatalf("V24: create %s: %v", p, err)
				}
				if c.write {
					_, err = f.WriteAt([]byte{1}, c.size-1)
				} else {
					err = f.Truncate(c.size)
				}
				var errno syscall.Errno
				errors.As(err, &errno)
				size := int64(-1)
				if fi, serr := f.Stat(); serr == nil {
					size = fi.Size()
				}
				f.Close()
				t.Logf("V24: %s=%s: %s: err=%s errno=%d size after=%d", env, os.Getenv(env), c.name, errString(err), uint64(errno), size)
				if err := os.Remove(p); err != nil {
					t.Errorf("V24: remove %s: %v", p, err)
				}
			}
		})
	}
}

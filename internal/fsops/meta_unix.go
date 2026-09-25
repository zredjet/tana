//go:build unix

package fsops

import (
	"bytes"
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// metaFromStat は、Stat_t から srcMeta を作る（extra は含めない）。
func metaFromStat(st *unix.Stat_t) srcMeta {
	return srcMeta{size: st.Size, mtime: time.Unix(st.Mtim.Unix()), perm: uint32(st.Mode) & 0o777}
}

// dirMetaSys は、フォルダ s のメタデータ（更新日時・パーミッション）をリンクを辿らずに読む（§15）。
func dirMetaSys(s string) (srcMeta, error) {
	var st unix.Stat_t
	if err := ignoringEINTR(func() error { return unix.Lstat(s, &st) }); err != nil {
		return srcMeta{}, &os.PathError{Op: "lstat", Path: s, Err: err}
	}
	return metaFromStat(&st), nil
}

// setMetaIn は、フォルダ d の中の、fsops が作ったファイル（一時ファイル）・フォルダ name に、メタデータ m を設定する（§15）。
// リンクを辿らずに d からの相対で開き、fileID が作ったときの want と一致することを確かめてから設定する。
// 設定できなかったものがあっても残りは続け、エラーをまとめて返す（呼び出し側は KindMetadata の警告にする）。
// 順序: 安全上のメタデータ（書き込み権限が要る）→ 更新日時 → パーミッション（読み取り専用にするのは最後）。
func setMetaIn(d *secDir, name string, want fileID, m srcMeta, isDir bool, _ *testHooks) error {
	s := d.join(name)
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	if isDir {
		flags |= unix.O_DIRECTORY
	}
	fd, err := ignoringEINTR2(func() (int, error) { return unix.Openat(d.fd, name, flags, 0) })
	if err != nil {
		return &os.PathError{Op: "open", Path: s, Err: err}
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := ignoringEINTR(func() error { return unix.Fstat(fd, &st) }); err != nil {
		return &os.PathError{Op: "fstat", Path: s, Err: err}
	}
	// ファイルは大きさも照合する（fileID だけでは、削除と作り直しで番号が再利用される ext4 で取り違えるため。§7.3、V16）。
	if idStatFromStat(&st).id != want || !isDir && st.Size != m.size {
		return &OpError{Op: "metadata", Path: s, Kind: KindSourceChanged}
	}
	var errs []error
	if len(m.extra) > 0 && !isDir {
		if err := setExtraFd(fd, m.extra); err != nil {
			errs = append(errs, &os.PathError{Op: "setxattr", Path: s, Err: err})
		}
	}
	mt, err := unix.TimeToTimespec(m.mtime)
	if err == nil {
		err = ignoringEINTR(func() error { return unix.UtimesNanoAt(d.fd, name, []unix.Timespec{mt, mt}, unix.AT_SYMLINK_NOFOLLOW) })
	}
	if err != nil {
		errs = append(errs, &os.PathError{Op: "utimensat", Path: s, Err: err})
	}
	if err := ignoringEINTR(func() error { return unix.Fchmod(fd, m.perm) }); err != nil {
		errs = append(errs, &os.PathError{Op: "fchmod", Path: s, Err: err})
	}
	return errors.Join(errs...)
}

// xattrNames は、list（listxattr の呼び出し）で拡張属性の名前を列挙する。列挙できなければ nil。
func xattrNames(list func(dest []byte) (int, error)) []string {
	for range 4 { // 列挙の間に増えた場合は読み直す
		n, err := list(nil)
		if err != nil || n <= 0 {
			return nil
		}
		buf := make([]byte, n)
		n, err = list(buf)
		if errors.Is(err, unix.ERANGE) {
			continue
		}
		if err != nil {
			return nil
		}
		var names []string
		for _, b := range bytes.Split(buf[:n], []byte{0}) {
			if len(b) > 0 {
				names = append(names, string(b))
			}
		}
		return names
	}
	return nil
}

// unkeptMetadataFd は、開いたコピー元 f にある、fsops が保持しないメタデータの名前を返す（§15）。
func unkeptMetadataFd(f *os.File) []string {
	fd := int(f.Fd())
	return filterUnkept(xattrNames(func(b []byte) (int, error) {
		return ignoringEINTR2(func() (int, error) { return unix.Flistxattr(fd, b) })
	}))
}

// unkeptMetadataPath は、パス p（リンクを辿らない）にある、fsops が保持しないメタデータの名前を返す（§15。フォルダに使う）。
func unkeptMetadataPath(p string) []string {
	return filterUnkept(xattrNames(func(b []byte) (int, error) {
		return ignoringEINTR2(func() (int, error) { return unix.Llistxattr(p, b) })
	}))
}

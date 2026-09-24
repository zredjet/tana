//go:build unix

package fsops

import (
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
	if err := unix.Lstat(s, &st); err != nil {
		return srcMeta{}, &os.PathError{Op: "lstat", Path: s, Err: err}
	}
	return metaFromStat(&st), nil
}

// setMetaIn は、フォルダ d の中の、fsops が作ったファイル（一時ファイル）・フォルダ name に、メタデータ m を設定する（§15）。
// リンクを辿らずに d からの相対で開き、fileID が作ったときの want と一致することを確かめてから設定する。
// 設定できなかったものがあっても残りは続け、エラーをまとめて返す（呼び出し側は KindMetadata の警告にする）。
// 順序: 安全上のメタデータ（書き込み権限が要る）→ 更新日時 → パーミッション（読み取り専用にするのは最後）。
func setMetaIn(d *secDir, name string, want fileID, m srcMeta, isDir bool) error {
	s := d.join(name)
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	if isDir {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(d.fd, name, flags, 0)
	if err != nil {
		return &os.PathError{Op: "open", Path: s, Err: err}
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return &os.PathError{Op: "fstat", Path: s, Err: err}
	}
	if idStatFromStat(&st).id != want {
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
		err = unix.UtimesNanoAt(d.fd, name, []unix.Timespec{mt, mt}, unix.AT_SYMLINK_NOFOLLOW)
	}
	if err != nil {
		errs = append(errs, &os.PathError{Op: "utimensat", Path: s, Err: err})
	}
	if err := unix.Fchmod(fd, m.perm); err != nil {
		errs = append(errs, &os.PathError{Op: "fchmod", Path: s, Err: err})
	}
	return errors.Join(errs...)
}

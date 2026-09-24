//go:build unix

package fsops

import "golang.org/x/sys/unix"

// ignoringEINTR は fn を呼び、EINTR が返る間は同じ呼び出しをやり直す（SPEC §4 のシステムコールのルール）。
// Go のランタイムはシグナルハンドラを SA_RESTART で入れるが、SMB・NFS・FUSE などでは遅いシステムコールが EINTR で返ることがある。
// os パッケージは内部でやり直すが、x/sys/unix はやり直さない。unix.Close には使わない（Linux では EINTR でも fd は閉じられている）。
func ignoringEINTR(fn func() error) error {
	for {
		if err := fn(); err != unix.EINTR {
			return err
		}
	}
}

// ignoringEINTR2 は、値も返す fn 用の ignoringEINTR。
func ignoringEINTR2[T any](fn func() (T, error)) (T, error) {
	for {
		v, err := fn()
		if err != unix.EINTR {
			return v, err
		}
	}
}

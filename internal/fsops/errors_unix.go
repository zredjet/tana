//go:build unix

package fsops

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// lockRetrySys は、使用中（KindLocked）の失敗を §17.1 のとおりやり直すか。Unix の EBUSY（マウントポイントなど）は一時的なものではないので、やり直さない。
const lockRetrySys = false

// classifyErrno は err に含まれる errno を分類する（SPEC §17）。
// 対応表にない番号、または errno を含まない場合は ok が false。
func classifyErrno(err error, o classifyOpts) (k Kind, ok bool) {
	errno, ok := errors.AsType[syscall.Errno](err)
	if !ok {
		return KindUnknown, false
	}
	switch errno {
	case unix.ENOENT, unix.ENOTDIR:
		return KindNotFound, true
	case unix.EEXIST:
		return KindExist, true
	case unix.EACCES:
		return KindPermission, true
	case unix.EPERM:
		// macOS のロック（UF_IMMUTABLE）でも EPERM になるため、対象の属性で決める。
		if o.readOnly != nil && o.readOnly() {
			return KindReadOnly, true
		}
		// シンボリックリンクを作れないファイルシステム（Linux の vfat。V20）は、リンクの作成に EPERM を返す。
		if o.symlinkCreate {
			return KindLinkUnsupported, true
		}
		return KindPermission, true
	case unix.EBUSY:
		return KindLocked, true
	case unix.EROFS:
		return KindReadOnly, true
	case unix.ENOSPC, unix.EDQUOT:
		return KindNoSpace, true
	case unix.ENOTEMPTY:
		return KindNotEmpty, true
	case unix.EXDEV:
		return KindCrossDevice, true
	case unix.ENAMETOOLONG, unix.EILSEQ:
		return KindInvalidName, true
	case unix.ELOOP:
		// SPEC §17 では、O_NOFOLLOW でリンクに当たった場合だけ SourceChanged とする。
		// それ以外（リンクの循環など）は対応表にないので ok を false にする。
		if o.noFollow {
			return KindSourceChanged, true
		}
	}
	return KindUnknown, false
}

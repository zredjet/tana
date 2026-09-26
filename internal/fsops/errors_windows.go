package fsops

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// lockRetrySys は、使用中（KindLocked）の失敗を §17.1 のとおりやり直すか。
const lockRetrySys = true

// classifyErrno は err に含まれる Windows のエラー番号を分類する（SPEC §17）。
// 対応表にない番号、またはエラー番号を含まない場合は ok が false。
func classifyErrno(err error, o classifyOpts) (k Kind, ok bool) {
	errno, ok := errors.AsType[syscall.Errno](err)
	if !ok {
		return KindUnknown, false
	}
	switch errno {
	case windows.ERROR_FILE_NOT_FOUND, windows.ERROR_PATH_NOT_FOUND, windows.ERROR_DIRECTORY:
		return KindNotFound, true
	case windows.ERROR_FILE_EXISTS, windows.ERROR_ALREADY_EXISTS:
		return KindExist, true
	case windows.ERROR_ACCESS_DENIED:
		// 原因が複数あるため、対象の属性で ReadOnly か Permission かを決める。
		if o.readOnly != nil && o.readOnly() {
			return KindReadOnly, true
		}
		return KindPermission, true
	case windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION:
		return KindLocked, true
	case windows.ERROR_WRITE_PROTECT:
		return KindReadOnly, true
	case windows.ERROR_DISK_FULL, windows.ERROR_HANDLE_DISK_FULL:
		return KindNoSpace, true
	case windows.ERROR_FILE_TOO_LARGE:
		// FAT32 の上限では ERROR_DISK_FULL が返る（V24）ので、それは §10.6 で書く前に判断する。
		return KindFileTooLarge, true
	case windows.ERROR_DIR_NOT_EMPTY:
		return KindNotEmpty, true
	case windows.ERROR_NOT_SAME_DEVICE:
		return KindCrossDevice, true
	case windows.ERROR_PRIVILEGE_NOT_HELD:
		// SPEC §17 では、リンクの作成時だけ LinkUnsupported とする。
		if o.symlinkCreate {
			return KindLinkUnsupported, true
		}
		return KindPermission, true
	case windows.ERROR_INVALID_FUNCTION:
		// シンボリックリンクを作れないファイルシステム（exFAT・FAT32。V20）は、リンクの作成にこの番号を返す。
		if o.symlinkCreate {
			return KindLinkUnsupported, true
		}
	case windows.ERROR_INVALID_NAME, windows.ERROR_FILENAME_EXCED_RANGE:
		return KindInvalidName, true
	case windows.ERROR_BAD_NETPATH, windows.ERROR_NETNAME_DELETED, windows.ERROR_BAD_NET_NAME, windows.ERROR_REM_NOT_LIST,
		windows.ERROR_UNEXP_NET_ERR, windows.ERROR_BAD_NET_RESP, windows.ERROR_NETWORK_BUSY, windows.ERROR_DEV_NOT_EXIST,
		windows.ERROR_SEM_TIMEOUT, windows.ERROR_NETWORK_UNREACHABLE, windows.ERROR_HOST_UNREACHABLE, windows.ERROR_CONNECTION_REFUSED,
		windows.ERROR_NO_NET_OR_BAD_PATH, windows.ERROR_NOT_CONNECTED, windows.ERROR_NOT_READY:
		return KindUnreachable, true
	}
	return KindUnknown, false
}

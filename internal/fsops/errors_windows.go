package fsops

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

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
	}
	return KindUnknown, false
}

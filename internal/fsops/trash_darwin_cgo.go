//go:build darwin && cgo

package fsops

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

// fsops_trash は、path（ファイルシステムの表現のまま。名前を変換しない）を NSFileManager でごみ箱へ移す（SPEC §12.3）。
// 成功なら 0 を返し、*out にごみ箱の中のパス（取得できなければ NULL。malloc）を入れる。
// 失敗なら 1 を返し、*domain（malloc）、*code と、下位の POSIX のエラー番号 *posix（なければ 0）を入れる。
static int fsops_trash(const char *path, int isDir, char **out, char **domain, long *code, int *posix) {
	@autoreleasepool {
		NSURL *url = [NSURL fileURLWithFileSystemRepresentation:path isDirectory:(isDir ? YES : NO) relativeToURL:nil];
		NSURL *res = nil;
		NSError *err = nil;
		if ([[NSFileManager defaultManager] trashItemAtURL:url resultingItemURL:&res error:&err]) {
			*out = res != nil ? strdup(res.fileSystemRepresentation) : NULL;
			return 0;
		}
		*domain = strdup(err != nil ? err.domain.UTF8String : "");
		*code = err != nil ? (long)err.code : 0;
		NSError *u = err.userInfo[NSUnderlyingErrorKey];
		if (u != nil && [u.domain isEqualToString:NSPOSIXErrorDomain]) {
			*posix = (int)u.code;
		}
		return 1;
	}
}
*/
import "C"

import (
	"context"
	"strconv"
	"syscall"
	"unsafe"
)

// trashAvailable は macOS（cgo あり）の事前確認。ごみ箱に入れられるかは実行時に NSFileManager が判断する（§12.3）。
func trashAvailable(ctx context.Context, src string, info EntryInfo) (bool, error) { return true, nil }

// NSCocoaErrorDomain のエラーコード（Foundation の FoundationErrors.h）。
const (
	nsFileNoSuchFileError          = 4
	nsFileWriteNoPermissionError   = 513
	nsFileWriteOutOfSpaceError     = 640
	nsFileWriteVolumeReadOnlyError = 642
	nsFeatureUnsupportedError      = 3328
)

// nsError は NSFileManager が返したエラー。下位の POSIX のエラー番号があれば Unwrap で返す（§17 の分類に使う）。
type nsError struct {
	domain string
	code   int64
	posix  syscall.Errno
}

func (e *nsError) Error() string {
	s := "NSError " + e.domain + " " + strconv.FormatInt(e.code, 10)
	if e.posix != 0 {
		s += ": " + e.posix.Error()
	}
	return s
}

func (e *nsError) Unwrap() error {
	if e.posix == 0 {
		return nil
	}
	return e.posix
}

// kind は、エラーの分類（§12.3、§17）。ごみ箱を使えない場所（ネットワークボリュームなど）は KindTrashUnavailable。
func (e *nsError) kind() Kind {
	if e.domain == "NSCocoaErrorDomain" {
		switch e.code {
		case nsFeatureUnsupportedError:
			return KindTrashUnavailable
		case nsFileNoSuchFileError:
			return KindNotFound
		case nsFileWriteNoPermissionError:
			return KindPermission
		case nsFileWriteOutOfSpaceError:
			return KindNoSpace
		case nsFileWriteVolumeReadOnlyError:
			return KindReadOnly
		}
	}
	if e.posix != 0 {
		return classify(e.posix, classifyOpts{})
	}
	return KindUnknown
}

// trashSys は、src を NSFileManager の trashItemAtURL:resultingItemURL:error: でごみ箱へ移し、ごみ箱の中のパスを返す（§12.3）。
// シンボリックリンクはリンク自体だけが入る（V5）。
func trashSys(src string, info EntryInfo) (string, error) {
	cs := C.CString(src)
	defer C.free(unsafe.Pointer(cs))
	isDir := C.int(0)
	if info.Type == TypeDir {
		isDir = 1
	}
	var out, domain *C.char
	var code C.long
	var posix C.int
	if C.fsops_trash(cs, isDir, &out, &domain, &code, &posix) == 0 {
		if out == nil {
			return "", nil
		}
		defer C.free(unsafe.Pointer(out))
		return C.GoString(out), nil
	}
	defer C.free(unsafe.Pointer(domain))
	e := &nsError{domain: C.GoString(domain), code: int64(code), posix: syscall.Errno(posix)}
	return "", &OpError{Op: "trash", Path: src, Kind: e.kind(), Err: e}
}

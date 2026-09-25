package fsops

import (
	"errors"
	"os"
	"slices"

	"golang.org/x/sys/unix"
)

// quarantineAttr は、インターネットから取得したことを示す拡張属性（§15。V6）。
const quarantineAttr = "com.apple.quarantine"

// readExtra は、開いたコピー元 f の com.apple.quarantine を読む（§15）。なければ nil。s はエラーに使うパス。
func readExtra(f *os.File, s string) ([]byte, error) {
	fd := int(f.Fd())
	for range 4 { // 読む間に大きくなった場合は読み直す
		n, err := ignoringEINTR2(func() (int, error) { return unix.Fgetxattr(fd, quarantineAttr, nil) })
		if errors.Is(err, unix.ENOATTR) {
			return nil, nil
		}
		if err != nil {
			return nil, &os.PathError{Op: "getxattr", Path: s, Err: err}
		}
		buf := make([]byte, n)
		n, err = ignoringEINTR2(func() (int, error) { return unix.Fgetxattr(fd, quarantineAttr, buf) })
		if errors.Is(err, unix.ERANGE) {
			continue
		}
		if errors.Is(err, unix.ENOATTR) {
			return nil, nil
		}
		if err != nil {
			return nil, &os.PathError{Op: "getxattr", Path: s, Err: err}
		}
		return buf[:n], nil
	}
	return nil, &os.PathError{Op: "getxattr", Path: s, Err: unix.ERANGE}
}

// setExtraFd は、開いたファイル fd に com.apple.quarantine を設定する（§15）。
func setExtraFd(fd int, data []byte) error {
	return ignoringEINTR(func() error { return unix.Fsetxattr(fd, quarantineAttr, data, 0) })
}

// unkeptXattrs は、macOS で利用者が付ける、fsops が保持しない拡張属性（§15）。
var unkeptXattrs = []string{"com.apple.metadata:_kMDItemUserTags", "com.apple.metadata:kMDItemFinderComment", "com.apple.ResourceFork"}

// filterUnkept は、拡張属性の名前のうち、利用者が付けた、fsops が保持しないもの（タグ・コメント・リソースフォーク）を返す（§15）。
func filterUnkept(names []string) []string {
	return slices.DeleteFunc(names, func(n string) bool { return !slices.Contains(unkeptXattrs, n) })
}

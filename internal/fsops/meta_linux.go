package fsops

import (
	"os"
	"slices"
	"strings"
)

// readExtra は、Linux では何もしない（安全上保持するメタデータは Windows と macOS だけ。§15）。
func readExtra(*os.File, string) ([]byte, error) { return nil, nil }

// setExtraFd は、Linux では何もしない。
func setExtraFd(int, []byte) error { return nil }

// filterUnkept は、拡張属性の名前のうち、利用者が付けた、fsops が保持しないもの（user.*）を返す（§15）。
func filterUnkept(names []string) []string {
	return slices.DeleteFunc(names, func(n string) bool { return !strings.HasPrefix(n, "user.") })
}

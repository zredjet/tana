package fsops

import (
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

// dropAppleDouble は、開いたフォルダ fd が拡張属性を AppleDouble ファイルに保存するボリューム（statfs のファイルシステム名が
// msdos・exfat）にあれば、names から、同じフォルダにある name の付属の ._name を除く（§8.5、V22）。
// OS は、名前の変更・削除で ._name を name と一緒に移す・消し、name の拡張属性をそこから読む。
// 付属を独立した項目として先に移すと、name を移したときに OS がそれを消し、§15 の com.apple.quarantine が失われるため。
// name のない ._name（孤立したもの）は残す。調べられなければ names をそのまま返す（通常のファイルとして扱う）。
func dropAppleDouble(fd int, names []string) []string {
	var st unix.Statfs_t
	if err := ignoringEINTR(func() error { return unix.Fstatfs(fd, &st) }); err != nil {
		return names
	}
	if fs := unix.ByteSliceToString(st.Fstypename[:]); fs != "msdos" && fs != "exfat" {
		return names
	}
	// names は名前のバイト順に並んでいる。
	return slices.DeleteFunc(slices.Clone(names), func(n string) bool {
		main, ok := strings.CutPrefix(n, "._")
		if !ok || main == "" {
			return false
		}
		_, found := slices.BinarySearch(names, main)
		return found
	})
}

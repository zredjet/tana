package testfs

import (
	"slices"
	"strings"

	"golang.org/x/sys/unix"
)

// dropAppleDouble は、dir が拡張属性を AppleDouble ファイルに保存するボリューム（exFAT・FAT32）にあれば、
// names から、同じフォルダにある name の付属の ._name を除く（fsops と同じ見方。SPEC §8.5、V22）。
// 手元の Mac では、テストが作るファイルに OS が拡張属性（com.apple.provenance）を付け、._name ができるため。
func dropAppleDouble(dir string, names []string) []string {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return names
	}
	if fs := unix.ByteSliceToString(st.Fstypename[:]); fs != "msdos" && fs != "exfat" {
		return names
	}
	return slices.DeleteFunc(slices.Clone(names), func(n string) bool {
		main, ok := strings.CutPrefix(n, "._")
		return ok && main != "" && slices.Contains(names, main)
	})
}

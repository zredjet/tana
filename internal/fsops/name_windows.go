package fsops

import "strings"

// validNameOS は Windows 固有の検査（§11.3）。
// 使えない文字（< > : " / \ | ? * と制御文字）、末尾の . と空白、予約名（拡張子付きを含む）を拒否する。
func validNameOS(name string) bool {
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return false
		}
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	base, _, _ := strings.Cut(name, ".")
	base = strings.ToUpper(strings.TrimRight(base, " "))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && '1' <= base[3] && base[3] <= '9' {
		return false
	}
	return true
}

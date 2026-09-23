package fsops

import "strings"

// validateName は Rename の新しい名前を検査する（§11.3）。使えない名前は KindInvalidName。
// 名前の長さの上限などボリュームによる制限は、OS が返したエラーで判定する。
func validateName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") || !validNameOS(name) {
		return &OpError{Op: "name", Path: name, Kind: KindInvalidName}
	}
	return nil
}

//go:build unix && !darwin

package fsops

// dropAppleDouble は、macOS 以外では names をそのまま返す（AppleDouble ファイルも通常のファイル。§8.5）。
func dropAppleDouble(_ int, names []string) []string { return names }

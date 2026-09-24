//go:build !darwin

package testfs

// dropAppleDouble は、macOS 以外では names をそのまま返す（SPEC §8.5）。
func dropAppleDouble(_ string, names []string) []string { return names }

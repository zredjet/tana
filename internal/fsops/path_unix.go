//go:build unix

package fsops

// sysPath は、OS に渡すパスを返す。Unix では変換しない（Windows の \\?\ 形式への変換に合わせた関数）。
func sysPath(p string) (string, error) {
	return p, nil
}

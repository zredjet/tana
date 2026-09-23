//go:build unix

package fsops

import "path/filepath"

// checkPathOS は Unix の §8.1 の検査。
func checkPathOS(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", invalidPath(p)
	}
	return filepath.Clean(p), nil
}

// isVolumeRoot は、checkPath を通ったパス p がボリュームのルート（/）かを返す。
func isVolumeRoot(p string) bool { return p == "/" }

// sysPath は、OS に渡すパスを返す。Unix では変換しない（Windows の \\?\ 形式への変換に合わせた関数）。
func sysPath(p string) (string, error) { return p, nil }

// userPath は sysPath の逆。Unix では変換しない。
func userPath(sys string) string { return sys }

// realPathSys は、p のリンクをすべて解決した実パスを返す（§8.3）。
func realPathSys(p string) (string, error) { return filepath.EvalSymlinks(p) }

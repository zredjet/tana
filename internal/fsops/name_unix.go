//go:build unix

package fsops

// validNameOS は Unix 固有の検査。Unix では / と NUL 文字（validateName で検査済み）以外はすべて使える。
func validNameOS(name string) bool { return true }

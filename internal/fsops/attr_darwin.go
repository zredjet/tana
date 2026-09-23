package fsops

import "golang.org/x/sys/unix"

// readOnlySys は、p が macOS のロック（UF_IMMUTABLE）付きかを返す関数を返す。
// §17 の分類（EPERM を ReadOnly と Permission に分ける）に使う。
func readOnlySys(p string) func() bool {
	return func() bool {
		var st unix.Stat_t
		return unix.Lstat(p, &st) == nil && st.Flags&unix.UF_IMMUTABLE != 0
	}
}

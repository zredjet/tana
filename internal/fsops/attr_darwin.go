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

// statReadOnly は、上書き先が読み取り専用（§9.3。オーナーの書き込み権限がない、またはロック（UF_IMMUTABLE））かを返す。
func statReadOnly(st *unix.Stat_t) bool { return st.Mode&0o200 == 0 || st.Flags&unix.UF_IMMUTABLE != 0 }

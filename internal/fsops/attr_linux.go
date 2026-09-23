package fsops

import "golang.org/x/sys/unix"

// readOnlySys は §17 の分類に使う関数を返す。Linux では EPERM を読み取り専用とみなさない（chattr +i は扱わない）。
func readOnlySys(p string) func() bool { return nil }

// targetReadOnlySys は、上書き先 p が読み取り専用（§9.3。オーナーの書き込み権限がない）かを返す。
func targetReadOnlySys(p string) bool {
	var st unix.Stat_t
	return unix.Lstat(p, &st) == nil && st.Mode&0o200 == 0
}

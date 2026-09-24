package fsops

import "golang.org/x/sys/unix"

// readOnlySys は §17 の分類に使う関数を返す。Linux では EPERM を読み取り専用とみなさない（chattr +i は扱わない）。
func readOnlySys(p string) func() bool { return nil }

// statReadOnly は、上書き先が読み取り専用（§9.3。オーナーの書き込み権限がない）かを返す。
func statReadOnly(st *unix.Stat_t) bool { return st.Mode&0o200 == 0 }

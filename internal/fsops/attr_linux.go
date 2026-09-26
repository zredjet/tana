package fsops

import "golang.org/x/sys/unix"

// readOnlySys は §17 の分類に使う関数を返す。Linux では EPERM を読み取り専用とみなさない（chattr +i は扱わない）。
func readOnlySys(p string) func() bool { return nil }

// dirLockedSys は、Linux では nil（EPERM を読み取り専用とみなさない。readOnlySys と同じ）。
func dirLockedSys(string) func() bool { return nil }

// statHidden は、Linux では常に false（隠しの属性がない。名前の . による判断は UI が行う）。
func statHidden(*unix.Stat_t) bool { return false }

// statReadOnly は、上書き先が読み取り専用（§9.3。オーナーの書き込み権限がない）かを返す。
func statReadOnly(st *unix.Stat_t) bool { return st.Mode&0o200 == 0 }

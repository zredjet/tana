package fsops

// readOnlySys は §17 の分類に使う関数を返す。Linux では EPERM を読み取り専用とみなさない（chattr +i は扱わない）。
func readOnlySys(p string) func() bool { return nil }

package fsops

import "golang.org/x/sys/windows"

// readOnlySys は、sysPath で変換済みのパス p が読み取り専用（§9.3。Windows ではファイルの読み取り専用属性）かを返す関数を返す。
// §17 の分類（ERROR_ACCESS_DENIED を ReadOnly と Permission に分ける）に使う。
// フォルダの読み取り専用属性は見ない。Windows はそれで書き込みを妨げず、Documents などのシェルフォルダには保護の意味なしに付いている（§13.2）。
func readOnlySys(p string) func() bool {
	return func() bool {
		p16, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return false
		}
		a, err := windows.GetFileAttributes(p16)
		if err != nil {
			return false
		}
		_, readOnly := attrFlags(a) // 一覧の読み取り専用（§14.3）と同じ判定。フォルダの属性は見ない
		return readOnly
	}
}

// dirLockedSys は、Windows では nil。フォルダの読み取り専用属性は保護を意味しないので、Mkdir の分類に使わない（§11.4）。
func dirLockedSys(string) func() bool { return nil }

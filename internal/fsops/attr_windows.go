package fsops

import "golang.org/x/sys/windows"

// readOnlySys は、sysPath で変換済みのパス p が読み取り専用（§9.3。Windows では読み取り専用属性）かを返す関数を返す。
// §17 の分類（ERROR_ACCESS_DENIED を ReadOnly と Permission に分ける）に使う。
func readOnlySys(p string) func() bool {
	return func() bool {
		p16, err := windows.UTF16PtrFromString(p)
		if err != nil {
			return false
		}
		a, err := windows.GetFileAttributes(p16)
		return err == nil && a&windows.FILE_ATTRIBUTE_READONLY != 0
	}
}

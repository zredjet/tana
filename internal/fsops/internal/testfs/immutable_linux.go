package testfs

import "testing"

// SetImmutable は macOS 専用なので t.Skip する（Linux の chattr +i は root 権限が必要）。
func SetImmutable(t testing.TB, path string) {
	t.Helper()
	t.Skip("UF_IMMUTABLE is only available on macOS")
}

//go:build !windows && !darwin

package platform

import "testing"

// TestOther は、Windows・macOS 以外（Linux など）の既定を確かめる。コンパイルが通ることだけを保証する（filer §3）。
func TestOther(t *testing.T) {
	t.Parallel()
	if !DotFilesHidden || !CanOpen("/tmp/foo.") {
		t.Error("hide names starting with a dot, and open any name")
	}
}

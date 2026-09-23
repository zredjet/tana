//go:build unix

package fsops

import (
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// TestReserveThenRename は、§8.4 の代わりの手段そのものを、どのボリュームでも直接確かめる。
func TestReserveThenRename(t *testing.T) {
	t.Parallel()
	testExclusiveRename(t, testfs.TempDir(t), reserveThenRename)
}

//go:build unix

package fsops

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestIgnoringEINTR(t *testing.T) {
	t.Parallel()
	// EINTR の間はやり直し、それ以外のエラー・成功はそのまま返す。
	for _, tc := range []struct {
		name string
		errs []error
		want error
	}{
		{"success", []error{nil}, nil},
		{"retry then success", []error{unix.EINTR, unix.EINTR, nil}, nil},
		{"retry then error", []error{unix.EINTR, unix.ENOENT}, unix.ENOENT},
		{"error", []error{unix.EEXIST}, unix.EEXIST},
	} {
		n := 0
		err := ignoringEINTR(func() error { n++; return tc.errs[n-1] })
		if err != tc.want || n != len(tc.errs) {
			t.Errorf("%s: ignoringEINTR = %v after %d calls, want %v after %d", tc.name, err, n, tc.want, len(tc.errs))
		}
	}

	n := 0
	v, err := ignoringEINTR2(func() (int, error) {
		n++
		if n < 3 {
			return -1, unix.EINTR
		}
		return 7, nil
	})
	if v != 7 || err != nil || n != 3 {
		t.Errorf("ignoringEINTR2 = %d, %v after %d calls, want 7, nil after 3", v, err, n)
	}
}

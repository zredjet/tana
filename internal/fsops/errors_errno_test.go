package fsops

import (
	"fmt"
	"os"
	"syscall"
	"testing"
)

// errnoCase は OS のエラー番号と、期待する分類の組。表は errors_windows_test.go と errors_unix_test.go にある。
type errnoCase struct {
	errno syscall.Errno
	want  Kind
}

// wrapForms は、os パッケージなどが返す形で errno を包んだエラーを返す。
func wrapForms(errno syscall.Errno) []error {
	return []error{
		errno,
		&os.PathError{Op: "open", Path: "p", Err: errno},
		&os.LinkError{Op: "rename", Old: "a", New: "b", Err: errno},
		os.NewSyscallError("syscall", errno),
		fmt.Errorf("wrapped: %w", &os.PathError{Op: "open", Path: "p", Err: errno}),
	}
}

// testErrnoTable は、表の各エラー番号が、どの包み方でも期待どおりに分類されることを確かめる。
// readOnlyErrno 以外では readOnly が呼ばれないことも確かめる。
func testErrnoTable(t *testing.T, cases []errnoCase, readOnlyErrno syscall.Errno) {
	t.Helper()
	for _, c := range cases {
		if c.errno == readOnlyErrno {
			continue // testReadOnlyErrno で確かめる
		}
		for _, err := range wrapForms(c.errno) {
			called := false
			o := classifyOpts{readOnly: func() bool { called = true; return true }}
			if got := classify(err, o); got != c.want {
				t.Errorf("classify(%#v) = %v, want %v", err, got, c.want)
			}
			if called {
				t.Errorf("classify(%#v) called readOnly; it must only be called for %v", err, readOnlyErrno)
			}
		}
	}
}

// testReadOnlyErrno は、errno が readOnly の結果に応じて KindReadOnly か KindPermission になることを確かめる。
func testReadOnlyErrno(t *testing.T, errno syscall.Errno) {
	t.Helper()
	tests := []struct {
		name     string
		readOnly func() bool
		want     Kind
	}{
		{"nil", nil, KindPermission},
		{"false", func() bool { return false }, KindPermission},
		{"true", func() bool { return true }, KindReadOnly},
	}
	for _, tt := range tests {
		for _, err := range wrapForms(errno) {
			if got := classify(err, classifyOpts{readOnly: tt.readOnly}); got != tt.want {
				t.Errorf("readOnly %s: classify(%#v) = %v, want %v", tt.name, err, got, tt.want)
			}
		}
	}
}

//go:build unix

package fsops

import "testing"

// TestCheckPathUnix は §8.1 の検査（Unix）を確かめる。
func TestCheckPathUnix(t *testing.T) {
	t.Parallel()
	ok := []struct{ in, want string }{
		{"/", "/"},
		{"/a", "/a"},
		{"/a/b/../c/./d/", "/a/c/d"},
		{"//a//b", "/a/b"},
		{"/a/foo.", "/a/foo."},
		{"/a/foo ", "/a/foo "},
		{"/a/b:c", "/a/b:c"},
	}
	for _, tt := range ok {
		got, err := checkPath(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("checkPath(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{"", "a", "./a", "../a", "a/b", "/a\x00b"} {
		if got, err := checkPath(in); KindOf(err) != KindInvalidRequest {
			t.Errorf("checkPath(%q) = %q, %v; want KindInvalidRequest", in, got, err)
		}
	}
	roots := map[string]bool{"/": true, "/a": false, "/a/b": false}
	for p, want := range roots {
		if got := isVolumeRoot(p); got != want {
			t.Errorf("isVolumeRoot(%q) = %v, want %v", p, got, want)
		}
	}
}

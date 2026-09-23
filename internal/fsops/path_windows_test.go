package fsops

import "testing"

func TestSysPathWindows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{`C:\`, `\\?\C:\`},
		{`C:\foo\bar`, `\\?\C:\foo\bar`},
		{`c:\foo`, `\\?\c:\foo`},
		{`C:/foo/bar`, `\\?\C:\foo\bar`},
		{`C:\foo\..\bar`, `\\?\C:\bar`},
		{`C:\foo\bar.`, `\\?\C:\foo\bar.`}, // 末尾の . を残す
		{`C:\foo\bar `, `\\?\C:\foo\bar `}, // 末尾の空白を残す
		{`C:\dir\CON`, `\\?\C:\dir\CON`},   // 予約名もそのまま
		{`\\server\share`, `\\?\UNC\server\share`},
		{`\\server\share\a\b`, `\\?\UNC\server\share\a\b`},
	}
	for _, tt := range tests {
		got, err := sysPath(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("sysPath(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	for _, in := range []string{`foo`, `C:foo`, `\foo`, `\\?\C:\foo`, `\\.\C:\foo`, ``} {
		if got, err := sysPath(in); KindOf(err) != KindInvalidRequest {
			t.Errorf("sysPath(%q) = %q, %v; want KindInvalidRequest", in, got, err)
		}
	}
}

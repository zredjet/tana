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
		{`C:\foo\bar.`, `\\?\C:\foo\bar.`},          // 末尾の . を残す
		{`C:\foo\bar `, `\\?\C:\foo\bar `},          // 末尾の空白を残す
		{`C:\dir\CON`, `\\?\C:\dir\CON`},            // 予約名もそのまま
		{`\\server\share`, `\\?\UNC\server\share\`}, // 共有のルートには末尾の \ を付ける
		{`\\server\share\`, `\\?\UNC\server\share\`},
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

// TestCheckPathWindows は §8.1 の検査（Windows）を確かめる。
func TestCheckPathWindows(t *testing.T) {
	t.Parallel()
	ok := []struct{ in, want string }{
		{`C:\`, `C:\`},
		{`C:\a`, `C:\a`},
		{`c:\a\b\..\c`, `c:\a\c`},
		{`C:/a/b`, `C:\a\b`},
		{`C:\a\foo.`, `C:\a\foo.`},
		{`C:\a\foo `, `C:\a\foo `},
		{`C:\a\CON`, `C:\a\CON`},
		{`\\server\share`, `\\server\share`},
		{`\\server\share\`, `\\server\share\`},
		{`\\server\share\a\b`, `\\server\share\a\b`},
		{`//server/share/a`, `\\server\share\a`},
	}
	for _, tt := range ok {
		got, err := checkPath(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("checkPath(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	bad := []string{
		"", `a`, `a\b`, `.\a`, `..\a`,
		`C:`, `C:a`, `C:a\b`, // ドライブ相対
		`\a`, `\`, `/a`, // ルート相対
		`\\?\C:\a`, `//?/C:/a`, `\\?\UNC\server\share`, // 呼び出し側が付けた \\?\
		`\\.\C:\a`, `\\.\PhysicalDrive0`, `//./C:/a`, // デバイスパス
		`\??\C:\a`,                                            // NT 形式
		`C:\a:stream`, `C:\a\b:x:$DATA`, `\\server\share\a:b`, // 代替データストリーム
		`\\server`, `\\server\`, `\\\share`, // 共有名のない UNC
		`\\server\..\x`, `\\server\.\x`, `\\..\share\x`, // . と .. はサーバー名・共有名にできない
		"C:\\a\x00b",
	}
	for _, in := range bad {
		if got, err := checkPath(in); KindOf(err) != KindInvalidRequest {
			t.Errorf("checkPath(%q) = %q, %v; want KindInvalidRequest", in, got, err)
		}
	}
	roots := map[string]bool{
		`C:\`: true, `c:\`: true, `C:\a`: false,
		`\\server\share`: true, `\\server\share\`: true, `\\server\share\a`: false,
	}
	for p, want := range roots {
		if got := isVolumeRoot(p); got != want {
			t.Errorf("isVolumeRoot(%q) = %v, want %v", p, got, want)
		}
	}
}

// TestUserPathWindows は、\\?\ 形式のパスを呼び出し側に返す形に戻すことを確かめる（§8.2）。
func TestUserPathWindows(t *testing.T) {
	t.Parallel()
	tests := []struct{ in, want string }{
		{`\\?\C:\a\b`, `C:\a\b`},
		{`\\?\C:\`, `C:\`},
		{`\\?\UNC\server\share\a`, `\\server\share\a`},
		{`\\?\UNC\server\share`, `\\server\share`},
		{`\\?\Volume{0a1b2c3d-0000-0000-0000-000000000000}\a`, `\\?\Volume{0a1b2c3d-0000-0000-0000-000000000000}\a`}, // ドライブ文字のないボリュームは戻せない
		{`C:\a`, `C:\a`},
	}
	for _, tt := range tests {
		if got := userPath(tt.in); got != tt.want {
			t.Errorf("userPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	for _, p := range []string{`C:\a\b`, `C:\`, `\\server\share\a`, `C:\a\foo.`} {
		s, err := sysPath(p)
		if err != nil || userPath(s) != p {
			t.Errorf("userPath(sysPath(%q)) = %q (%v)", p, userPath(s), err)
		}
	}
}

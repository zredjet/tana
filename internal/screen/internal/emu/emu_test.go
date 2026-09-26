package emu

import "testing"

// TestErrors は、screen が出力しないはずの制御文字・シーケンス・文字を、誤りとして記録することを確かめる
// （screen のテストの T2 の確認は、これに頼る）。
func TestErrors(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, out string
		wantErr   bool
	}{
		{"text", "abあ", false},
		{"cup", "\x1b[2;3Hx", false},
		{"cup home", "\x1b[Hx", false},
		{"sgr", "\x1b[0;1;2;4;7;31;42mx\x1b[39;49mx\x1b[mx", false},
		{"bright colors", "\x1b[97;107mx", false},
		{"clear and cursor", "\x1b[2J\x1b[?25l\x1b[?25h", false},
		{"control byte", "a\tb", true},
		{"newline", "a\r\nb", true},
		{"lone esc", "\x1b", true},
		{"osc", "\x1b]0;title\x07", true},
		{"incomplete csi", "\x1b[12", true},
		{"unknown csi", "\x1b[2K", true},
		{"bad cup", "\x1b[5H", true},
		{"cup outside", "\x1b[3;1H", true},
		{"bad sgr", "\x1b[1;xm", true},
		{"unknown sgr", "\x1b[38;5;1m", true},
		{"bidi control", "a\u202eb", true},
		{"invisible", "a\u200bb", true},
		{"invalid byte", "a\xffb", true},
	} {
		e := New(4, 2)
		e.Write([]byte(tt.out))
		if got := len(e.Errors) > 0; got != tt.wantErr {
			t.Errorf("%s: %q: errors %q, want error %v", tt.name, tt.out, e.Errors, tt.wantErr)
		}
	}
}

func TestCellsAndStyle(t *testing.T) {
	t.Parallel()
	e := New(4, 2)
	e.Write([]byte("\x1b[1;2H\x1b[0;1;31mあ\x1b[39mb\x1b[2;4Hcd"))
	if e.Row(0) != " あb" || e.Row(1) != "   c" {
		t.Errorf("rows %q %q", e.Row(0), e.Row(1))
	}
	if c := e.Cells[1]; c.Width != 2 || c.Style != (Style{FG: 31, Bold: true}) {
		t.Errorf("wide cell %+v", c)
	}
	if c := e.Cells[3]; c.Style != (Style{Bold: true}) {
		t.Errorf("after SGR 39: %+v", c)
	}
	// 幅 2 の文字の後半に書くと、前半も空白になる。
	e.Write([]byte("\x1b[0m\x1b[1;3Hz"))
	if e.Row(0) != "  zb" {
		t.Errorf("overwrite the second half: %q", e.Row(0))
	}
	// 画面の消去。
	e.Write([]byte("\x1b[2J"))
	if e.Row(0) != "    " || e.Row(1) != "    " {
		t.Errorf("after clear: %q %q", e.Row(0), e.Row(1))
	}
	if len(e.Errors) > 0 {
		t.Errorf("errors: %q", e.Errors)
	}
}

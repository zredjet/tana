package term

import (
	"encoding/binary"
	"slices"
	"testing"
)

// inputRecord は、テスト用に INPUT_RECORD の 20 バイトを作る。
func inputRecord(kind uint16, event ...uint16) []byte {
	b := make([]byte, inputRecordSize)
	binary.LittleEndian.PutUint16(b, kind)
	for i, v := range event {
		binary.LittleEndian.PutUint16(b[4+2*i:], v)
	}
	return b
}

func TestDecodeRecord(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   []byte
		want Record
	}{
		{
			// KEY_EVENT_RECORD: bKeyDown=1, wRepeatCount=1, VK_ESCAPE, scan 1, uChar 0x1b, LEFT_CTRL_PRESSED|ENHANCED_KEY
			name: "key down",
			in:   inputRecord(0x0001, 1, 0, 1, 0x1b, 1, 0x1b, 0x0108, 0),
			want: Record{Kind: KeyRecord, KeyDown: true, RepeatCount: 1, VirtualKey: 0x1b, ScanCode: 1, Char: 0x1b, ControlKeys: 0x0108},
		},
		{
			// 上位サロゲートだけの KEY_EVENT（VK_PACKET）。そのまま写す。
			name: "key up surrogate half",
			in:   inputRecord(0x0001, 0, 0, 1, 0xe7, 0, 0xd83d, 0, 0x0001),
			want: Record{Kind: KeyRecord, RepeatCount: 1, VirtualKey: 0xe7, Char: 0xd83d, ControlKeys: 0x10000},
		},
		{
			name: "window buffer size",
			in:   inputRecord(0x0004, 120, 30),
			want: Record{Kind: WindowSizeRecord, Width: 120, Height: 30},
		},
		{
			name: "focus",
			in:   inputRecord(0x0010, 1, 0),
			want: Record{Kind: FocusRecord, Focus: true},
		},
		{
			name: "menu",
			in:   inputRecord(0x0008, 0x1234, 0),
			want: Record{Kind: MenuRecord},
		},
	}
	for _, tt := range tests {
		got := decodeRecord(tt.in)
		want := tt.want
		copy(want.Raw[:], tt.in[4:])
		if got != want {
			t.Errorf("%s: decodeRecord = %+v, want %+v", tt.name, got, want)
		}
	}
}

func TestUTF16Encoder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		writes []string
		want   [][]uint16
	}{
		{"ascii and escape", []string{"a\x1b[6n"}, [][]uint16{{'a', 0x1b, '[', '6', 'n'}}},
		{"japanese", []string{"あ漢"}, [][]uint16{{0x3042, 0x6f22}}},
		{"surrogate pair", []string{"😀"}, [][]uint16{{0xd83d, 0xde00}}},
		// 書き込みの境界で切れた文字は、次の書き込みで続ける。
		{"split across writes", []string{"a\xe3\x81", "\x82b"}, [][]uint16{{'a'}, {0x3042, 'b'}}},
		{"split emoji", []string{"\xf0\x9f", "\x98", "\x80"}, [][]uint16{{}, {}, {0xd83d, 0xde00}}},
		// 不正な UTF-8 は 1 バイトずつ U+FFFD にする。WTF-8 のサロゲートも、コンソールに渡さない。
		{"invalid byte", []string{"a\xffb"}, [][]uint16{{'a', 0xfffd, 'b'}}},
		{"wtf-8 surrogate", []string{"\xed\xa0\x80"}, [][]uint16{{0xfffd, 0xfffd, 0xfffd}}},
		{"truncated then ascii", []string{"\xe3\x81", "x"}, [][]uint16{{}, {0xfffd, 0xfffd, 'x'}}},
	}
	for _, tt := range tests {
		var e utf16Encoder
		for i, w := range tt.writes {
			if got := e.encode([]byte(w)); !slices.Equal(got, tt.want[i]) {
				t.Errorf("%s: write %d (%q): got %#x, want %#x", tt.name, i, w, got, tt.want[i])
			}
		}
	}
}

func TestOutputMethodNames(t *testing.T) {
	t.Parallel()
	for _, m := range []OutputMethod{OutputWriteConsoleW, OutputUTF8CodePage, OutputWriteFile} {
		got, err := ParseOutputMethod(m.String())
		if err != nil || got != m {
			t.Errorf("ParseOutputMethod(%q) = %v, %v, want %v", m.String(), got, err, m)
		}
	}
	if _, err := ParseOutputMethod("bogus"); err == nil {
		t.Error("ParseOutputMethod(bogus): err = nil")
	}
}

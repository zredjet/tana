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

// TestRecordDecoder は、Windows の入力のレコードを keys に渡すバイト列に変えることを確かめる（T3。tui §8）。
func TestRecordDecoder(t *testing.T) {
	t.Parallel()
	key := func(down bool, rep uint16, c uint16) Record {
		return Record{Kind: KeyRecord, KeyDown: down, RepeatCount: rep, Char: c}
	}
	tests := []struct {
		name       string
		reads      [][]Record
		want       []string // 読み取りごとのバイト列
		wantResize []bool
	}{
		{
			name: "key down chars, repeat, key up, no char",
			reads: [][]Record{{
				key(true, 1, 'a'), key(false, 1, 'a'), // キーを離したレコードは使わない
				key(true, 3, 'b'),                                        // 繰り返し
				key(true, 0, 'c'),                                        // 繰り返しの回数 0 も 1 回
				key(true, 1, 0),                                          // 文字のないキー（Shift など）
				key(true, 1, 0x1b), key(true, 1, '['), key(true, 1, 'A'), // VT の入力モードの矢印
			}},
			want:       []string{"abbbc\x1b[A"},
			wantResize: []bool{false},
		},
		{
			name:       "surrogate pair in one read and across reads",
			reads:      [][]Record{{key(true, 1, 0xd83c), key(true, 1, 0xdf63), key(true, 1, 0xd83d)}, {key(true, 1, 0xde00)}},
			want:       []string{"🍣", "😀"},
			wantResize: []bool{false, false},
		},
		{
			name:       "unpaired surrogates",
			reads:      [][]Record{{key(true, 1, 0xdc00), key(true, 1, 'x'), key(true, 1, 0xd800)}, {key(true, 1, 'y')}},
			want:       []string{"�x", "�y"},
			wantResize: []bool{false, false},
		},
		{
			// conhost の貼り付け: Alt＋テンキーの並びの後の、Alt を離したレコードに文字が載る（docs/probe-results の conhost）。
			name: "conhost alt numpad paste",
			reads: [][]Record{{
				{Kind: KeyRecord, KeyDown: true, RepeatCount: 1, VirtualKey: vkMenu, ControlKeys: 0x2},
				{Kind: KeyRecord, KeyDown: true, RepeatCount: 1, VirtualKey: 0x66, ControlKeys: 0x2},
				{Kind: KeyRecord, KeyDown: false, RepeatCount: 1, VirtualKey: 0x66, ControlKeys: 0x2},
				{Kind: KeyRecord, KeyDown: false, RepeatCount: 1, VirtualKey: vkMenu, Char: 0xd83c},
				{Kind: KeyRecord, KeyDown: true, RepeatCount: 1, VirtualKey: vkMenu, ControlKeys: 0x2},
				{Kind: KeyRecord, KeyDown: false, RepeatCount: 1, VirtualKey: vkMenu, Char: 0xdf63},
			}},
			want:       []string{"🍣"},
			wantResize: []bool{false},
		},
		{
			name: "window size, focus and menu records",
			reads: [][]Record{
				{{Kind: FocusRecord, Focus: true}, {Kind: MenuRecord}},
				{key(true, 1, 'a'), {Kind: WindowSizeRecord, Width: 100, Height: 30}},
			},
			want:       []string{"", "a"},
			wantResize: []bool{false, true},
		},
	}
	for _, tt := range tests {
		var d recordDecoder
		for i, recs := range tt.reads {
			got, resize := d.decode(recs)
			if string(got) != tt.want[i] || resize != tt.wantResize[i] {
				t.Errorf("%s: read %d: %q resize %v, want %q resize %v", tt.name, i, got, resize, tt.want[i], tt.wantResize[i])
			}
		}
	}
}

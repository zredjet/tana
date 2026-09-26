package term

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// probeRecord は、docs/probe-results の keys の記録の、Windows の入力のレコード（cmd/tuiprobe の recordJSON）。
type probeRecord struct {
	Kind   string `json:"kind"`
	Down   *bool  `json:"down"`
	Repeat uint16 `json:"repeat"`
	VK     uint16 `json:"vk"`
	Char   uint16 `json:"char"`
	W      int    `json:"w"`
	H      int    `json:"h"`
}

type probeStep struct {
	ID    string `json:"id"`
	Hex   string `json:"hex"`
	Reads []struct {
		Records []probeRecord `json:"records"`
	} `json:"reads"`
}

func (p probeRecord) record() Record {
	switch p.Kind {
	case "key":
		return Record{Kind: KeyRecord, KeyDown: p.Down != nil && *p.Down, RepeatCount: p.Repeat, VirtualKey: p.VK, Char: p.Char}
	case "size":
		return Record{Kind: WindowSizeRecord, Width: p.W, Height: p.H}
	case "focus":
		return Record{Kind: FocusRecord}
	}
	return Record{Kind: MenuRecord}
}

// probeSection は、keys の記録の 1 つの節。
type probeSection struct {
	PasteText string      `json:"paste_text"` // 貼り付けの手順で貼り付けた文字列
	Steps     []probeStep `json:"steps"`
}

// windowsProbeSteps は、フェーズ12で記録した Windows Terminal と conhost のキーの節（レコードの読み取りの列）を返す。
func windowsProbeSteps(t testing.TB) map[string]probeSection {
	t.Helper()
	out := map[string]probeSection{}
	for _, name := range []string{"windows-terminal", "conhost"} {
		data, err := os.ReadFile("../../docs/probe-results/" + name + "-2026-09-26.json")
		if err != nil {
			t.Fatal(err)
		}
		var f map[string]json.RawMessage
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatal(err)
		}
		for _, sec := range []string{"keys", "keys_vtinput"} {
			var s probeSection
			if err := json.Unmarshal(f[sec], &s); err != nil {
				t.Fatalf("%s %s: %v", name, sec, err)
			}
			out[name+"/"+sec] = s
		}
	}
	return out
}

// TestRecordDecoderMatchesProbe は、フェーズ12で実測したレコードの列を、記録したときと同じバイト列にすることを確かめる（T3。tui §8）。
// 記録の hex は、手順ごとに読み取りを順に変換してつないだもの（最後に残った上位サロゲートは U+FFFD）。
// ただし貼り付けの手順は、貼り付けた文字列と比べる。conhost の記録の hex は、Alt を離したレコードの文字（🍣）を
// 含めるようにする前に作ったもので、🍣 が抜けているため（docs/probe-results の conhost）。
func TestRecordDecoderMatchesProbe(t *testing.T) {
	t.Parallel()
	n := 0
	for sec, ps := range windowsProbeSteps(t) {
		for _, st := range ps.Steps {
			var d recordDecoder
			var got []byte
			for _, rd := range st.Reads {
				var recs []Record
				for _, r := range rd.Records {
					recs = append(recs, r.record())
				}
				b, _ := d.decode(recs)
				got = append(got, b...)
			}
			if d.high != 0 {
				got = append(got, "\uFFFD"...)
			}
			if strings.HasPrefix(st.ID, "paste") {
				// 印を除き、改行を LF にそろえる（VT の入力モードでは CR、conhost の VT の入力モードでないときは LF で届く）。
				text := strings.TrimSuffix(strings.TrimPrefix(string(got), "\x1b[200~"), "\x1b[201~")
				if text = strings.ReplaceAll(text, "\r", "\n"); text != ps.PasteText {
					t.Errorf("%s %s: %q, want the pasted text %q", sec, st.ID, got, ps.PasteText)
				}
			} else if want, _ := hex.DecodeString(st.Hex); !bytes.Equal(got, want) {
				t.Errorf("%s %s: %q, want %q", sec, st.ID, got, want)
			}
			n++
		}
	}
	if n < 100 {
		t.Errorf("only %d probe steps", n)
	}
}

// fuzzRecords は、ファジングの入力を、レコードの読み取りの列にする。1 つのレコードは 5 バイト:
// 種類（下位 2 ビット: 0・1 はキー、2 は大きさ、3 はフォーカス。最上位ビットはこのレコードの後で読み取りを区切る）、
// 押したか（下位 1 ビット）と繰り返しの回数（上位 7 ビットを 4 で割った余り）、仮想キーコード（0x12 は Alt）、文字（リトルエンディアン）。
func fuzzRecords(data []byte) [][]Record {
	var reads [][]Record
	var cur []Record
	for len(data) >= 5 {
		b := data[:5]
		data = data[5:]
		var r Record
		switch b[0] & 3 {
		case 0, 1:
			r = Record{Kind: KeyRecord, KeyDown: b[1]&1 == 1, RepeatCount: uint16(b[1]>>1) % 4, VirtualKey: uint16(b[2]), Char: uint16(b[3]) | uint16(b[4])<<8}
		case 2:
			r = Record{Kind: WindowSizeRecord, Width: int(b[3]), Height: int(b[4])}
		case 3:
			r = Record{Kind: FocusRecord}
		}
		cur = append(cur, r)
		if b[0]&0x80 != 0 {
			reads = append(reads, cur)
			cur = nil
		}
	}
	return append(reads, cur)
}

// FuzzRecordDecoder は、Windows の入力のレコードをバイト列にするときの性質を確かめる（T3。tui §10）。
//   - 読み取りの区切り方によらず、つないだ結果は同じ（サロゲートの対が読み取りをまたいでも組み立てる）。
//   - 結果は正しい UTF-8。
//   - キーを押したレコードの、サロゲートでない文字は、繰り返しの回数だけ、順に結果に現れる（失わない）。
//   - 大きさの変更の印は、大きさのレコードがある読み取りでだけ立つ。
func FuzzRecordDecoder(f *testing.F) {
	// シードは testdata/fuzz にある。フェーズ12で実測した Windows Terminal と conhost の手順のレコードを、fuzzRecords の形にしたもの。
	f.Fuzz(func(t *testing.T, data []byte) {
		reads := fuzzRecords(data)
		var split recordDecoder
		var got []byte
		for _, recs := range reads {
			b, resize := split.decode(recs)
			if resize != slices.ContainsFunc(recs, func(r Record) bool { return r.Kind == WindowSizeRecord }) {
				t.Fatalf("resize = %v for %+v", resize, recs)
			}
			got = append(got, b...)
		}
		var whole recordDecoder
		all, _ := whole.decode(slices.Concat(reads...))
		if !bytes.Equal(got, all) || split.high != whole.high {
			t.Fatalf("split reads %q (held %#x), one read %q (held %#x)", got, split.high, all, whole.high)
		}
		if !utf8.Valid(got) {
			t.Fatalf("invalid UTF-8 %q", got)
		}
		// キーを押したレコードの、サロゲートでない文字が、順に現れる。
		rest := string(got)
		for _, r := range slices.Concat(reads...) {
			if r.Kind != KeyRecord || !r.KeyDown || r.Char == 0 || utf16.IsSurrogate(rune(r.Char)) {
				continue
			}
			for range max(r.RepeatCount, 1) {
				c := string(rune(r.Char))
				i := indexRune(rest, c)
				if i < 0 {
					t.Fatalf("char %U of a key-down record is missing from %q", r.Char, got)
				}
				rest = rest[i+len(c):]
			}
		}
	})
}

func indexRune(s, c string) int {
	for i := 0; i+len(c) <= len(s); {
		if s[i:i+len(c)] == c {
			return i
		}
		_, n := utf8.DecodeRuneInString(s[i:])
		i += n
	}
	return -1
}

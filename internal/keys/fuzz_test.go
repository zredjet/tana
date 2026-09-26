package keys

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// FuzzDecoder は、任意の入力で、次の性質を確かめる（tui §5 のファジング）。
//   - panic しない。
//   - T3: イベントの元の入力をつなぐと、受け取った入力と一致する。
//   - 有限の回数の確定の処理（待ち時間より後の時刻での Tick 1 回）で、保留中の入力がなくなる。
//   - 区切りに依存しない: 同じバイト列を任意の位置で分けて渡しても、分けた間隔が待ち時間を超えない限り、同じイベントの列になる。
//   - 押したキーを 1 つのイベントにまとめすぎない: 貼り付けと解釈できない入力のほかは、元の入力が ESC を 3 つ以上続けて含まない
//     （ESC ESC ESC は Esc と Alt＋Esc の 2 つ）。
//
// cuts の各バイトが、入力を分ける位置（入力の長さ＋1 で割った余り）を表す。
func FuzzDecoder(f *testing.F) {
	for _, s := range []string{
		"a", "\x1b", "\x1b[A", "\x1b[1;5A", "\x1bOP", "\x1b[15;2~", "\x1b\x1b[A", "\x1ba", "あ\x1bあ", "\x1b[12;34R",
		"\x1b[200~a\x1b[Ab\x1b[201~x", "\x1b[200~ab", "\x1b[201~", "\x1b[?1;2c", "\xe3\x81\x82\xff", "\x1b[1\x1b[B", "\x00\x08\x7f\x0d",
	} {
		f.Add([]byte(s), []byte{1, 3, 5}, false)
		f.Add([]byte(s), []byte{2}, true)
	}
	f.Fuzz(func(t *testing.T, data, cuts []byte, cpr bool) {
		var whole Decoder
		whole.SetCursorPositionExpected(cpr)
		w := whole.Feed(data, t0)
		w = append(w, whole.Tick(far)...)
		if raws(w) != string(data) {
			t.Fatalf("T3: raw of events %q, input %q", raws(w), data)
		}
		if _, ok := whole.Deadline(); ok {
			t.Fatalf("pending input remains after Tick(far) for %q", data)
		}
		for _, e := range w {
			if e.Kind != PasteEvent && e.Kind != UnknownEvent && strings.Contains(e.Raw, "\x1b\x1b\x1b") {
				t.Fatalf("event %s swallows a run of ESCs in %q", e, data)
			}
		}

		var pos []int
		for _, c := range cuts {
			pos = append(pos, int(c)%(len(data)+1))
		}
		slices.Sort(pos)
		pos = slices.Compact(pos)
		var split Decoder
		split.SetCursorPositionExpected(cpr)
		var s []Event
		prev := 0
		now := t0
		for _, p := range append(pos, len(data)) {
			s = append(s, split.Feed(data[prev:p], now)...)
			prev = p
			now = now.Add(time.Millisecond) // 待ち時間（50 ミリ秒）より短い間隔
		}
		s = append(s, split.Tick(far)...)
		if !slices.EqualFunc(w, s, func(a, b Event) bool { return a.String() == b.String() && a.Raw == b.Raw }) {
			t.Errorf("split at %v changes the events of %q:\n whole %q\n split %q", pos, data, strs(w), strs(s))
		}
	})
}

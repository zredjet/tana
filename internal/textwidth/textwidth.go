package textwidth

import (
	"iter"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Class は書記素クラスタの分類（tui §4）。
type Class uint8

const (
	// Normal は通常の文字。そのまま表示する。
	Normal Class = iota
	// Forbidden は端末に出してはいけない文字（制御文字、双方向の制御文字と方向の印、行区切り・段落区切り）。? で表示する。
	Forbidden
	// InvalidByte は不正な UTF-8 のバイト（1 バイトずつ）。? で表示する。
	InvalidByte
	// Invisible は見えない書記素クラスタ（既定で無視される文字だけのもの、結合文字で始まるもの、幅が 0 になるもの）。? で表示する。
	Invisible
	// Unstable は不安定な書記素クラスタ（端末によって幅と描き方が割れる絵文字の並びなど）。簡略な形で表示する。
	Unstable
)

func (c Class) String() string {
	switch c {
	case Normal:
		return "Normal"
	case Forbidden:
		return "Forbidden"
	case InvalidByte:
		return "InvalidByte"
	case Invisible:
		return "Invisible"
	case Unstable:
		return "Unstable"
	}
	return "Class(" + strconv.Itoa(int(c)) + ")"
}

// Cluster は 1 つの書記素クラスタ。
type Cluster struct {
	Text    string // 元のバイト列（入力の部分文字列）
	Class   Class
	Display string // 表示する形。Normal なら Text と同じ
	Width   int    // Display の表示幅（1 以上）
	Varies  bool   // 幅が端末によって違いうる（幅が曖昧な文字、Unicode 16.0 より新しい絵文字、幅が 3 以上）
}

// Next は、s の先頭の書記素クラスタと、残りの文字列を返す。s が空なら、ゼロ値の Cluster と空の文字列を返す。
func Next(s string) (Cluster, string) {
	n := clusterLen(s)
	if n == 0 {
		return Cluster{}, ""
	}
	return classify(s[:n]), s[n:]
}

// All は、s の書記素クラスタを先頭から順に返す。
func All(s string) iter.Seq[Cluster] {
	return func(yield func(Cluster) bool) {
		for s != "" {
			var c Cluster
			c, s = Next(s)
			if !yield(c) {
				return
			}
		}
	}
}

// Width は、s の表示幅（書記素クラスタの表示幅の和）を返す。
func Width(s string) int {
	w := 0
	for c := range All(s) {
		w += c.Width
	}
	return w
}

// replaced は、? で表示する書記素クラスタを返す。
func replaced(text string, class Class) Cluster {
	return Cluster{Text: text, Class: class, Display: "?", Width: 1}
}

// forbidden は、r が端末に出してはいけない文字（tui §4）かを返す。
func forbidden(r rune) bool {
	switch {
	case r < 0x20, 0x7f <= r && r <= 0x9f: // 制御文字
		return true
	case 0x202a <= r && r <= 0x202e, 0x2066 <= r && r <= 0x2069: // 双方向の制御文字
		return true
	case r == 0x200e, r == 0x200f, r == 0x061c: // 方向の印
		return true
	case r == 0x2028, r == 0x2029: // 行区切り・段落区切り
		return true
	}
	return false
}

const (
	zwj    = 0x200d
	vs16   = 0xfe0f
	keycap = 0x20e3
)

func isTag(r rune) bool { return 0xe0020 <= r && r <= 0xe007f }

// classify は、1 つの書記素クラスタ text（空でない）を分類し、表示する形と表示幅を決める（tui §4）。
func classify(text string) Cluster {
	r0, size := utf8.DecodeRuneInString(text)
	if r0 == utf8.RuneError && size == 1 {
		return replaced(text, InvalidByte)
	}
	var (
		allIgnorable              = true
		ri                        int
		hasTag, hasVS16, modifier bool
		zwjEmoji                       = false
		firstEmoji                rune = -1
		prev                      rune = -1
		last                      rune
		count                     int
	)
	for i, r := range text {
		if forbidden(r) {
			return replaced(text, Forbidden)
		}
		p := lookup(r)
		if p&defaultIgnorable == 0 {
			allIgnorable = false
		}
		switch {
		case p&gcbMask == gcbRI:
			ri++
		case isTag(r):
			hasTag = true
		case r == vs16:
			hasVS16 = true
		case p&emojiModifier != 0 && i > 0:
			modifier = true
		}
		if p&extPict != 0 {
			if firstEmoji < 0 {
				firstEmoji = r
			}
			if prev == zwj {
				zwjEmoji = true
			}
		}
		prev, last = r, r
		count++
	}
	if ri > 0 {
		// 地域指示記号は、対でも単独でも 1 つにつき ? にする。
		return Cluster{Text: text, Class: Unstable, Display: strings.Repeat("?", ri), Width: ri}
	}
	if allIgnorable || lookup(r0)&mark != 0 {
		return replaced(text, Invisible)
	}
	class, display := Normal, text
	switch {
	case hasTag || last == keycap && count > 1:
		// タグの並び・キーキャップは、最初の文字にする。
		class, display = Unstable, string(r0)
	case zwjEmoji:
		// ZWJ でつないだ絵文字の並びは、最初の絵文字にする。
		class, display = Unstable, string(firstEmoji)
	case modifier || hasVS16:
		// 肌の色の修飾と VS16 を除く。
		var b strings.Builder
		for i, r := range text {
			if r == vs16 || i > 0 && lookup(r)&emojiModifier != 0 {
				continue
			}
			b.WriteRune(r)
		}
		class, display = Unstable, b.String()
	}
	width, varies := 0, false
	for _, r := range display {
		p := lookup(r)
		w := runeWidth(p)
		width += w
		if w > 0 && p&ambiguous != 0 || p&newEmoji != 0 {
			varies = true
		}
	}
	if width == 0 {
		return replaced(text, Invisible)
	}
	return Cluster{Text: text, Class: class, Display: display, Width: width, Varies: varies || width >= 3}
}

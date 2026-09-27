package keymap

import (
	"strings"
	"unicode"

	"github.com/zredjet/tana/internal/keys"
)

// ModPolicy は、修飾キーの一致の仕方。今の割り当ての振る舞いを、キーごとにそのまま写す（filer §4）。
type ModPolicy int

const (
	ModExact       ModPolicy = iota // 修飾キーが Spec.Mod と同じ
	ModIgnoreShift                  // Shift を除いた修飾キーが Spec.Mod と同じ（文字のキー。大文字は Shift 付きで届くことがある）
	ModAny                          // 修飾キーを問わない（入力欄・操作の画面の Esc・矢印など）
)

// Spec は、1 つのキー。
type Spec struct {
	Key    keys.Key
	Rune   rune // Key が KeyRune のとき
	Mod    keys.Mod
	Policy ModPolicy
}

// Chord は、続けて押すキーの並び（例: g のあとに g）。v0.1 の表は、どれも 1 つのキー（filer §4。後の版で並びを使う）。
type Chord []Spec

// Match は、キーのイベント ev が s に当たるかを返す。貼り付けなど、キー以外のイベントには当たらない。
func (s Spec) Match(ev keys.Event) bool {
	if ev.Kind != keys.KeyEvent || ev.Key != s.Key || s.Key == keys.KeyRune && ev.Rune != s.Rune {
		return false
	}
	switch s.Policy {
	case ModIgnoreShift:
		return ev.Mod&^keys.ModShift == s.Mod
	case ModAny:
		return true
	}
	return ev.Mod == s.Mod
}

// Name は、キーの案内とヘルプに出す、キーの表示名（Up、PgUp、Space、Ctrl+R、y など）。
func (s Spec) Name() string {
	var name string
	switch s.Key {
	case keys.KeyRune:
		switch {
		case s.Rune == ' ':
			name = "Space"
		case s.Mod&keys.ModCtrl != 0:
			name = string(unicode.ToUpper(s.Rune))
		default:
			name = string(s.Rune)
		}
	case keys.KeyPageUp:
		name = "PgUp"
	case keys.KeyPageDown:
		name = "PgDn"
	default:
		name = s.Key.String()
	}
	if s.Policy == ModAny {
		return name // 修飾キーを問わないので、キーだけを出す
	}
	return (s.Mod &^ keys.ModShift).String() + name // Ctrl+R など。Shift は文字そのもので分かるので出さない
}

// Name は、並びの表示名（キーの表示名を空白でつなぐ）。
func (c Chord) Name() string {
	var parts []string
	for _, s := range c {
		parts = append(parts, s.Name())
	}
	return strings.Join(parts, " ")
}

// ---- 表を書くための短い形 ----

// r は、文字のキー（Shift は問わない）。
func r(c rune) Spec { return Spec{Key: keys.KeyRune, Rune: c, Policy: ModIgnoreShift} }

// rx は、修飾キーのない文字のキー（Shift も付かないもの）。
func rx(c rune) Spec { return Spec{Key: keys.KeyRune, Rune: c, Policy: ModExact} }

// ctrl は、Ctrl と文字のキー。
func ctrl(c rune) Spec { return Spec{Key: keys.KeyRune, Rune: c, Mod: keys.ModCtrl, Policy: ModExact} }

// k0 は、修飾キーのない特殊キー。
func k0(k keys.Key) Spec { return Spec{Key: k, Policy: ModExact} }

// kany は、修飾キーを問わない特殊キー。
func kany(k keys.Key) Spec { return Spec{Key: k, Policy: ModAny} }

// alt は、どれを押しても同じ操作になるキー（1 つのキーの並びを、選べるものとして並べる。先頭のキーを案内に出す）。
func alt(specs ...Spec) []Chord {
	out := make([]Chord, len(specs))
	for i, s := range specs {
		out[i] = Chord{s}
	}
	return out
}

package keymap

import (
	"strings"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/keys"
)

// Result は、キー入力を解決した結果。
type Result struct {
	Action  app.Action // 操作（Role は、キーを解決した役割）
	Local   Local      // tui の中だけの操作（LocalNone なら Action）
	OK      bool       // 操作になった
	Pending bool       // 並びの途中（v0.1 の表では起きない）
}

// Resolver は、キー入力を、フォーカスの道筋に沿って操作に解決する（filer §4）。
// 並びの途中の状態は Resolver が持つ（keys のエスケープシーケンスの途中と同じく、tui の側の状態）。
// v0.1 の表はどれも 1 つのキーなので、まだ途中の状態を持たない。並びを使うときに、ここに足す（道筋が変わったら途中の状態を捨てる）。
type Resolver struct{}

// Lookup は、フォーカスの道筋 path（app.FocusPath。内側から外側）で、キー入力 ev を解決する。
// 共通の表（Ctrl+L）を引き、次に道筋の内側の役割から、割り当て・どのキーでも・文字の入力の順に引く。
// 解決した役割を Action.Role に入れる（app はその役割の部品に届ける）。
func (*Resolver) Lookup(path []app.Role, ev keys.Event) Result {
	if b, ok := Global.find(ev); ok {
		return Result{Local: b.Local, OK: true}
	}
	for _, role := range path {
		km := Maps[role]
		if km == nil {
			continue
		}
		if b, ok := km.find(ev); ok {
			if b.Local != LocalNone {
				return Result{Local: b.Local, OK: true}
			}
			return Result{Action: action(b.Command, role), OK: true}
		}
		if km.AnyKey != "" && ev.Kind == keys.KeyEvent {
			return Result{Action: action(km.AnyKey, role), OK: true}
		}
		if text, ok := km.text(ev); ok {
			return Result{Action: app.Action{Kind: app.ActInsert, Text: text, Role: role}, OK: true}
		}
	}
	return Result{}
}

// find は、キー入力 ev に当たる割り当てを返す。
func (k *Keymap) find(ev keys.Event) (Binding, bool) {
	for _, b := range k.bindings() {
		for _, c := range b.Keys {
			if len(c) == 1 && c[0].Match(ev) {
				return b, true
			}
		}
	}
	return Binding{}, false
}

// text は、入力欄の役割で、文字のキーと貼り付けを、入れる文字列にする（Text が TextNone なら入れない）。
// 貼り付けは入力欄に入れるだけで、コマンドとして解釈しない（tui §5。filer U2）。
func (k *Keymap) text(ev keys.Event) (string, bool) {
	if k.Text == TextNone {
		return "", false
	}
	switch ev.Kind {
	case keys.PasteEvent:
		// 貼り付けの改行は、端末によって LF・CRLF・CR（Terminal.app・iTerm2 は LF を CR にして送る）のどれでも届く。
		if i := strings.IndexAny(ev.Text, "\r\n"); i >= 0 && k.Text == TextLine {
			return ev.Text[:i], true
		}
		return ev.Text, true
	case keys.KeyEvent:
		if ev.Key == keys.KeyRune && ev.Mod&^keys.ModShift == 0 {
			return string(ev.Rune), true
		}
	}
	return "", false
}

// action は、ID の操作に役割 role を付けたもの。表に書いた ID は、テストで Commands にあることを確かめる。
func action(id string, role app.Role) app.Action {
	c, _ := CommandByID(id)
	act := c.Action
	act.Role = role
	return act
}

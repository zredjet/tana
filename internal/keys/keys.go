package keys

import (
	"fmt"
	"strconv"
)

// Kind はイベントの種類。
type Kind uint8

const (
	KeyEvent            Kind = iota + 1 // キー
	PasteEvent                          // 貼り付け（bracketed paste）
	CursorPositionEvent                 // カーソル位置の報告（ESC[行;桁R。問い合わせを待っている間だけ）
	UnknownEvent                        // 解釈できない入力（Raw に元のバイト列）
)

func (k Kind) String() string {
	switch k {
	case KeyEvent:
		return "Key"
	case PasteEvent:
		return "Paste"
	case CursorPositionEvent:
		return "CursorPosition"
	case UnknownEvent:
		return "Unknown"
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// Key はキーの種類。文字のキーは KeyRune で、Event.Rune に文字を持つ。
type Key uint8

const (
	KeyRune Key = iota
	KeyEsc
	KeyEnter
	KeyTab
	KeyBackspace
	KeyInsert
	KeyDelete
	KeyUp
	KeyDown
	KeyRight
	KeyLeft
	KeyHome
	KeyEnd
	KeyPageUp
	KeyPageDown
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
	KeyF13
	KeyF14
	KeyF15
	KeyF16
	KeyF17
	KeyF18
	KeyF19
	KeyF20
)

var keyNames = [...]string{
	KeyRune: "Rune", KeyEsc: "Esc", KeyEnter: "Enter", KeyTab: "Tab", KeyBackspace: "Backspace",
	KeyInsert: "Insert", KeyDelete: "Delete", KeyUp: "Up", KeyDown: "Down", KeyRight: "Right", KeyLeft: "Left",
	KeyHome: "Home", KeyEnd: "End", KeyPageUp: "PageUp", KeyPageDown: "PageDown",
}

func (k Key) String() string {
	switch {
	case int(k) < len(keyNames):
		return keyNames[k]
	case KeyF1 <= k && k <= KeyF20:
		return "F" + strconv.Itoa(int(k-KeyF1)+1)
	}
	return "Key(" + strconv.Itoa(int(k)) + ")"
}

// Mod は修飾キー（xterm の修飾キーの値から 1 を引いたもののビット）。
type Mod uint8

const (
	ModShift Mod = 1 << iota
	ModAlt
	ModCtrl
	ModMeta
)

// String は、"Ctrl+Alt+" のように、修飾キーを Ctrl・Alt・Shift・Meta の順に + でつないだものを返す。
func (m Mod) String() string {
	var s string
	for _, x := range []struct {
		m    Mod
		name string
	}{{ModCtrl, "Ctrl+"}, {ModAlt, "Alt+"}, {ModShift, "Shift+"}, {ModMeta, "Meta+"}} {
		if m&x.m != 0 {
			s += x.name
		}
	}
	return s
}

// Event は、入力から作ったイベント。
type Event struct {
	Kind     Kind
	Key      Key    // KeyEvent のとき
	Mod      Mod    // KeyEvent のとき
	Rune     rune   // KeyEvent で Key が KeyRune のとき
	Text     string // PasteEvent のとき（印の間の元のバイト列）
	Row, Col int    // CursorPositionEvent のとき（1 から数える）
	Raw      string // このイベントになった元の入力（T3: すべてのイベントの Raw をつなぐと、受け取った入力と一致する）
}

// String は、テストとログのための短い表現を返す（例: Ctrl+'a'、Shift+Tab、Paste("x")、CPR(3,7)、Unknown("\x1b[?1c")）。
func (e Event) String() string {
	switch e.Kind {
	case KeyEvent:
		if e.Key == KeyRune {
			return e.Mod.String() + strconv.QuoteRune(e.Rune)
		}
		return e.Mod.String() + e.Key.String()
	case PasteEvent:
		return fmt.Sprintf("Paste(%q)", e.Text)
	case CursorPositionEvent:
		return fmt.Sprintf("CPR(%d,%d)", e.Row, e.Col)
	}
	return fmt.Sprintf("%v(%q)", e.Kind, e.Raw)
}

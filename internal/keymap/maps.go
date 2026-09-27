package keymap

import (
	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/keys"
)

// Local は、tui の中だけで行う操作（app に渡さない）。
type Local int

const (
	LocalNone   Local = iota
	LocalRedraw       // 画面の描き直し（Ctrl+L。どの画面でも）
	LocalView         // 表示形式の切り替え（v。filer §5.3）
)

// localIDs は、tui の中だけの操作の ID（案内・ヘルプで参照する）。
var localIDs = map[Local]string{LocalRedraw: "local:redraw", LocalView: "local:view"}

// Binding は、キーの割り当て 1 つ。Keys のどれを押しても同じ操作になる。先頭のキーを案内に出す。
type Binding struct {
	Keys    []Chord
	Command string // Commands の ID（Local のときは空）
	Local   Local
}

// id は、割り当ての操作の ID。
func (b Binding) id() string {
	if b.Local != LocalNone {
		return localIDs[b.Local]
	}
	return b.Command
}

// TextPolicy は、入力欄の役割で、文字のキーと貼り付けをどう入れるか。
type TextPolicy int

const (
	TextNone TextPolicy = iota // 入れない。貼り付けは捨てる（コマンドとして解釈しない。tui §5、filer U2）
	TextLine                   // 打った文字と、貼り付けの最初の行だけを入れる（パスの入力）
	TextRaw                    // 打った文字と、貼り付けをそのまま入れる（名前。改行などは lineedit が除く）
)

// Keymap は、1 つの役割のキーの表。
type Keymap struct {
	Role     app.Role
	Text     TextPolicy
	AnyKey   string    // キーのイベントなら何でもこの操作にする（ヘルプ。貼り付けは除く）
	Include  []*Keymap // 部品の種類の共通の表（入力欄の編集）
	Bindings []Binding
}

// bindings は、表の割り当てを、自分のものから、取り込んだ表の順に返す。
func (k *Keymap) bindings() []Binding {
	out := append([]Binding(nil), k.Bindings...)
	for _, inc := range k.Include {
		out = append(out, inc.bindings()...)
	}
	return out
}

// Global は、どの画面でも最初に引く表。
var Global = &Keymap{Bindings: []Binding{{Keys: alt(ctrl('l')), Local: LocalRedraw}}}

// edit は、入力欄の編集のキー（パスの入力・名前の変更・新しいフォルダで使う）。修飾キーは問わない。
var edit = &Keymap{Bindings: []Binding{
	{Keys: alt(kany(keys.KeyBackspace)), Command: "backspace"},
	{Keys: alt(kany(keys.KeyDelete)), Command: "delete-char"},
	{Keys: alt(kany(keys.KeyLeft)), Command: "left"},
	{Keys: alt(kany(keys.KeyRight)), Command: "right"},
	{Keys: alt(kany(keys.KeyHome), ctrl('a')), Command: "line-home"},
	{Keys: alt(kany(keys.KeyEnd), ctrl('e')), Command: "line-end"},
	{Keys: alt(kany(keys.KeyEnter)), Command: "submit"},
	{Keys: alt(kany(keys.KeyEsc)), Command: "cancel"},
}}

// listNav は、一覧のカーソルの移動のキー（衝突・結果）。
var listNav = &Keymap{Bindings: []Binding{
	{Keys: alt(kany(keys.KeyUp), rx('k')), Command: "up"},
	{Keys: alt(kany(keys.KeyDown), rx('j')), Command: "down"},
	{Keys: alt(kany(keys.KeyPageUp)), Command: "page-up"},
	{Keys: alt(kany(keys.KeyPageDown)), Command: "page-down"},
	{Keys: alt(kany(keys.KeyHome)), Command: "home"},
	{Keys: alt(kany(keys.KeyEnd)), Command: "end"},
}}

// Maps は、役割ごとのキーの表（filer §7 の割り当て）。
var Maps = map[app.Role]*Keymap{
	app.RoleWorkspace: {Role: app.RoleWorkspace, Bindings: []Binding{
		{Keys: alt(k0(keys.KeyTab)), Command: "next-pane"},
		{Keys: alt(r('.')), Command: "toggle-hidden"},
		{Keys: alt(ctrl('r')), Command: "reload"},
		{Keys: alt(r('?')), Command: "help"},
		{Keys: alt(r('q')), Command: "quit"},
		{Keys: alt(k0(keys.KeyEsc)), Command: "cancel"},
		{Keys: alt(r('L')), Command: "last-result"},
		{Keys: alt(rx('v')), Local: LocalView},
	}},
	app.RolePane: {Role: app.RolePane, Bindings: []Binding{
		{Keys: alt(k0(keys.KeyUp), r('k')), Command: "up"},
		{Keys: alt(k0(keys.KeyDown), r('j')), Command: "down"},
		{Keys: alt(k0(keys.KeyPageUp)), Command: "page-up"},
		{Keys: alt(k0(keys.KeyPageDown)), Command: "page-down"},
		{Keys: alt(k0(keys.KeyHome)), Command: "home"},
		{Keys: alt(k0(keys.KeyEnd)), Command: "end"},
		{Keys: alt(k0(keys.KeyEnter)), Command: "enter"},
		{Keys: alt(r('h'), k0(keys.KeyLeft), k0(keys.KeyBackspace)), Command: "parent"},
		{Keys: alt(r('l'), k0(keys.KeyRight)), Command: "enter-dir"},
		{Keys: alt(r(' ')), Command: "mark"},
		{Keys: alt(r('a')), Command: "mark-all"},
		{Keys: alt(r('g')), Command: "go-path"},
		{Keys: alt(r('=')), Command: "sync-other"},
		{Keys: alt(r('y')), Command: "yank"},
		{Keys: alt(r('p')), Command: "paste-copy"},
		{Keys: alt(r('P')), Command: "paste-move"},
		{Keys: alt(r('d')), Command: "trash"},
		{Keys: alt(r('D')), Command: "purge"},
		{Keys: alt(r('r')), Command: "rename"},
		{Keys: alt(r('n')), Command: "new-dir"},
	}},
	app.RoleHelp:   {Role: app.RoleHelp, AnyKey: "cancel"},
	app.RolePath:   {Role: app.RolePath, Text: TextLine, Include: []*Keymap{edit}},
	app.RoleRename: {Role: app.RoleRename, Text: TextRaw, Include: []*Keymap{edit}},
	app.RoleNewDir: {Role: app.RoleNewDir, Text: TextRaw, Include: []*Keymap{edit}},
	app.RoleExec: {Role: app.RoleExec, Bindings: []Binding{
		{Keys: alt(rx('y')), Command: "yes"},
		{Keys: alt(rx('n')), Command: "no"},
		{Keys: alt(kany(keys.KeyEsc)), Command: "cancel"},
	}},
	// 計画を作っている間は、Esc で中止するほかは何もしない（filer §8.1）。表示形式は切り替えられる。
	app.RolePlanning: {Role: app.RolePlanning, Bindings: []Binding{
		{Keys: alt(k0(keys.KeyEsc)), Command: "cancel"},
		{Keys: alt(rx('v')), Local: LocalView},
	}},
	app.RoleConfirm: {Role: app.RoleConfirm, Bindings: []Binding{
		{Keys: alt(k0(keys.KeyEnter)), Command: "submit"},
		{Keys: alt(kany(keys.KeyEsc)), Command: "cancel"},
		{Keys: alt(r('D')), Command: "purge"},
	}},
	app.RoleConflicts: {Role: app.RoleConflicts, Include: []*Keymap{listNav}, Bindings: []Binding{
		{Keys: alt(k0(keys.KeyEnter)), Command: "submit"},
		{Keys: alt(kany(keys.KeyEsc)), Command: "cancel"},
		{Keys: alt(r('s')), Command: "decide:skip"},
		{Keys: alt(r('o')), Command: "decide:overwrite"},
		{Keys: alt(r('r')), Command: "decide:auto-rename"},
		{Keys: alt(r('m')), Command: "decide:merge"},
		{Keys: alt(r('S')), Command: "decide-all:skip"},
		{Keys: alt(r('O')), Command: "decide-all:overwrite"},
		{Keys: alt(r('R')), Command: "decide-all:auto-rename"},
		{Keys: alt(r('M')), Command: "decide-all:merge"},
		{Keys: alt(r('N')), Command: "newer-only"},
		{Keys: alt(r(' ')), Command: "toggle"},
		{Keys: alt(r('u')), Command: "unset-only"},
	}},
	// 実行中の Ctrl+C は、Esc と同じく中止の確認を出す（filer §7）。
	app.RoleProgress: {Role: app.RoleProgress, Bindings: []Binding{
		{Keys: alt(kany(keys.KeyEsc), ctrl('c')), Command: "cancel"},
		{Keys: alt(r('Q')), Command: "force-quit"},
	}},
	app.RoleCancelAsk: {Role: app.RoleCancelAsk, Bindings: []Binding{
		{Keys: alt(r('y')), Command: "yes"},
		{Keys: alt(r('n')), Command: "no"},
		{Keys: alt(kany(keys.KeyEsc), ctrl('c')), Command: "cancel"},
	}},
	app.RoleResult: {Role: app.RoleResult, Include: []*Keymap{listNav}, Bindings: []Binding{
		{Keys: alt(k0(keys.KeyEnter)), Command: "submit"},
		{Keys: alt(kany(keys.KeyEsc)), Command: "cancel"},
		{Keys: alt(r(' ')), Command: "toggle"},
		{Keys: alt(r('e')), Command: "english"},
		{Keys: alt(r('D')), Command: "purge"},
	}},
	// 完全削除の確認: 確定は y だけ。Enter・n・Esc はやめる（filer §8.6。U2）。
	app.RoleDelete: {Role: app.RoleDelete, Bindings: []Binding{
		{Keys: alt(k0(keys.KeyEnter)), Command: "submit"},
		{Keys: alt(kany(keys.KeyEsc)), Command: "cancel"},
		{Keys: alt(r('y')), Command: "yes"},
		{Keys: alt(r('n')), Command: "no"},
	}},
}

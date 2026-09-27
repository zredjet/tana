package keymap

import (
	"strings"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

// キーの案内とヘルプ（filer §4）。キーの名前は表から作るので、割り当てを変えても案内とヘルプはずれない。
// どの案内を出すか（状態で変わるもの）は、tui の描き方が選ぶ。

// keyNames は、役割 role（RoleNone なら共通の表）の、ID id の操作のキーの表示名を、表の順に返す。
func keyNames(role app.Role, id string) []string {
	km := Global
	if role != app.RoleNone {
		km = Maps[role]
	}
	if km == nil {
		return nil
	}
	for _, b := range km.bindings() {
		if b.id() == id {
			var out []string
			for _, c := range b.Keys {
				out = append(out, c.Name())
			}
			return out
		}
	}
	return nil
}

// KeyName は、役割 role の、ID id の操作の先頭のキーの表示名（案内に出すキー）。割り当てがなければ空。
func KeyName(role app.Role, id string) string {
	if names := keyNames(role, id); len(names) > 0 {
		return names[0]
	}
	return ""
}

// Item は、案内の項目（「キー 説明」）。IDs の操作の先頭のキーを「・」でつないで出す（「n・Esc・Enter やめる」）。
type Item struct {
	Role  app.Role
	IDs   []string
	Label string
}

// Keys は、項目のキーの表示。
func (it Item) Keys() string {
	var names []string
	for _, id := range it.IDs {
		names = append(names, KeyName(it.Role, id))
	}
	return strings.Join(names, "・")
}

// Text は、項目の表示（「キー 説明」）。
func (it Item) Text() string { return it.Keys() + " " + it.Label }

// WithLabel は、説明を label にした項目（状態で説明が変わるもの）。
func (it Item) WithLabel(label string) Item {
	it.Label = label
	return it
}

// Guide は、1 行の案内。Prefix の後に、項目を Sep でつなぐ。
type Guide struct {
	Prefix string
	Sep    string
	Items  []Item
}

// Render は、案内の文字列。
func (g Guide) Render() string {
	var parts []string
	for _, it := range g.Items {
		parts = append(parts, it.Text())
	}
	return g.Prefix + strings.Join(parts, g.Sep)
}

// Join は、項目 items を、区切り sep でつないだ案内（状態で項目を選ぶ案内。結果の画面など）。
func Join(sep string, items ...Item) string { return Guide{Sep: sep, Items: items}.Render() }

// 区切り: ダイアログの案内は空白 3 つ、閲覧の画面と衝突の画面の案内は空白 2 つ。
const (
	SepDialog = "   "
	SepWide   = "  "
)

func item(role app.Role, label string, ids ...string) Item {
	return Item{Role: role, IDs: ids, Label: label}
}

// 案内の定義。テストで、どの項目の操作にもキーがあることを確かめる（Guides）。
var (
	// 閲覧の画面の最下行（filer §5.1）。80 桁に収める（ほかのキーはヘルプで示す）。
	MainGuide = Guide{Sep: SepWide, Items: []Item{
		item(app.RolePane, msg.GuideParent, "parent"), item(app.RoleWorkspace, msg.GuideSwitch, "next-pane"),
		item(app.RolePane, msg.GuideMark, "mark"), item(app.RolePane, msg.GuideYank, "yank"),
		item(app.RolePane, msg.GuidePaste, "paste-copy"), item(app.RoleWorkspace, msg.GuideView, "local:view"),
		item(app.RoleWorkspace, msg.GuideHelp, "help"), item(app.RoleWorkspace, msg.GuideQuit, "quit"),
	}}

	ExecGuide = Guide{Sep: SepDialog, Items: []Item{item(app.RoleExec, msg.GuideExecYes, "yes"), item(app.RoleExec, msg.GuideStop, "no")}}

	ConfirmGuide          = Guide{Sep: SepDialog, Items: []Item{item(app.RoleConfirm, msg.GuideRun, "submit"), item(app.RoleConfirm, msg.GuideStop, "cancel")}}
	ConfirmConflictsGuide = Guide{Sep: SepDialog, Items: []Item{item(app.RoleConfirm, msg.GuideToConflicts, "submit"), item(app.RoleConfirm, msg.GuideStop, "cancel")}}
	ConfirmNoneGuide      = Guide{Sep: SepDialog, Items: []Item{item(app.RoleConfirm, msg.GuideClose, "cancel")}}
	ConfirmPurgeGuide     = Guide{Sep: SepDialog, Items: []Item{item(app.RoleConfirm, msg.GuideToPurge, "purge"), item(app.RoleConfirm, msg.GuideStop, "cancel")}}

	DeleteGuide     = Guide{Sep: SepDialog, Items: []Item{item(app.RoleDelete, msg.GuideDeleteYes, "yes"), item(app.RoleDelete, msg.GuideStop, "no", "cancel", "submit")}}
	DeleteNoneGuide = Guide{Sep: SepDialog, Items: []Item{item(app.RoleDelete, msg.GuideClose, "no", "cancel", "submit")}}

	RenameGuide = Guide{Sep: SepDialog, Items: []Item{item(app.RoleRename, msg.GuideRename, "submit"), item(app.RoleRename, msg.GuideStop, "cancel")}}
	NewDirGuide = Guide{Sep: SepDialog, Items: []Item{item(app.RoleNewDir, msg.GuideCreate, "submit"), item(app.RoleNewDir, msg.GuideStop, "cancel")}}

	CancelAskGuide = Guide{Sep: SepDialog, Items: []Item{item(app.RoleCancelAsk, msg.GuideCancelYes, "yes"), item(app.RoleCancelAsk, msg.GuideContinue, "no")}}
	ProgressGuide  = Guide{Sep: SepDialog, Items: []Item{item(app.RoleProgress, msg.GuideAbort, "cancel")}}
	ForceQuitGuide = Guide{Sep: SepDialog, Items: []Item{item(app.RoleProgress, msg.GuideForceQuit, "force-quit")}}

	// 結果の画面の項目。どれを出すかは状態で決まる（ごみ箱に入らなかった項目、カーソル行の中の結果）。
	ResultPurge   = item(app.RoleResult, msg.GuideToPurge, "purge")
	ResultInside  = item(app.RoleResult, "", "toggle") // 説明は WithLabel(msg.GuideInside(...)) で付ける
	ResultEnglish = item(app.RoleResult, msg.GuideEnglish, "english")
	ResultClose   = item(app.RoleResult, msg.GuideClose, "submit")

	// 衝突の画面（filer §8.3）。この行の決定は、その行で使えないものを暗く出すので、tui が項目ごとに描く。
	ConflictRowGuide = Guide{Prefix: msg.ConflictRowTitle, Sep: SepWide, Items: []Item{
		item(app.RoleConflicts, msg.Decision(fsops.DecisionSkip), "decide:skip"),
		item(app.RoleConflicts, msg.Decision(fsops.DecisionOverwrite), "decide:overwrite"),
		item(app.RoleConflicts, msg.Decision(fsops.DecisionAutoRename), "decide:auto-rename"),
		item(app.RoleConflicts, msg.Decision(fsops.DecisionMerge), "decide:merge"),
	}}
	ConflictAllGuide = Guide{Prefix: msg.GuideConflictsAll, Sep: SepWide, Items: []Item{
		item(app.RoleConflicts, msg.Decision(fsops.DecisionSkip), "decide-all:skip"),
		item(app.RoleConflicts, msg.Decision(fsops.DecisionOverwrite), "decide-all:overwrite"),
		item(app.RoleConflicts, msg.GuideNewerOnly, "newer-only"),
		item(app.RoleConflicts, msg.Decision(fsops.DecisionAutoRename), "decide-all:auto-rename"),
		item(app.RoleConflicts, msg.Decision(fsops.DecisionMerge), "decide-all:merge"),
	}}
	ConflictKeysGuide = Guide{Sep: SepWide, Items: []Item{
		item(app.RoleConflicts, msg.GuideRun, "submit"), item(app.RoleConflicts, msg.GuideStop, "cancel"),
		item(app.RoleConflicts, msg.GuideFold, "toggle"), item(app.RoleConflicts, msg.GuideUnsetOnly, "unset-only"),
	}}
)

// Guides は、すべての案内（テストで、項目の操作にキーがあることを確かめる）。
var Guides = []Guide{MainGuide, ExecGuide, ConfirmGuide, ConfirmConflictsGuide, ConfirmNoneGuide, ConfirmPurgeGuide,
	DeleteGuide, DeleteNoneGuide, RenameGuide, NewDirGuide, CancelAskGuide, ProgressGuide, ForceQuitGuide,
	{Items: []Item{ResultPurge, ResultInside, ResultEnglish, ResultClose}}, ConflictRowGuide, ConflictAllGuide, ConflictKeysGuide}

// Ref は、ヘルプの行が説明する操作（役割と ID。RoleNone は共通の表）。
type Ref struct {
	Role app.Role
	ID   string
}

// HelpRow は、ヘルプの 1 行。キーの欄は、操作ごとにキーを空白 1 つでつなぎ、操作の間を空白 2 つでつなぐ（「Up k  Down j」。フェーズ23で決めた）。
type HelpRow struct {
	Refs  []Ref
	Label string
}

// Keys は、ヘルプの行のキーの欄。
func (h HelpRow) Keys() string {
	var parts []string
	for _, r := range h.Refs {
		parts = append(parts, strings.Join(keyNames(r.Role, r.ID), " "))
	}
	return strings.Join(parts, "  ")
}

// HelpRows は、ヘルプの行（閲覧の画面のキー。? はヘルプそのものなので載せない）。
var HelpRows = func() []HelpRow {
	pane := func(ids ...string) []Ref {
		var out []Ref
		for _, id := range ids {
			out = append(out, Ref{app.RolePane, id})
		}
		return out
	}
	ws := func(id string) []Ref { return []Ref{{app.RoleWorkspace, id}} }
	return []HelpRow{
		{pane("up", "down"), msg.HelpMove},
		{pane("page-up", "page-down", "home", "end"), msg.HelpPage},
		{pane("enter"), msg.HelpEnter},
		{pane("parent"), msg.HelpParent},
		{pane("enter-dir"), msg.HelpEnterDir},
		{ws("next-pane"), msg.HelpNextPane},
		{pane("mark"), msg.HelpMark},
		{pane("yank"), msg.HelpYank},
		{pane("paste-copy", "paste-move"), msg.HelpPaste},
		{pane("trash", "purge"), msg.HelpTrash(KeyName(app.RoleConfirm, "submit"), KeyName(app.RoleDelete, "yes"))},
		{pane("rename", "new-dir"), msg.HelpName},
		{ws("last-result"), msg.HelpLast},
		{pane("mark-all"), msg.HelpMarkAll},
		{pane("go-path"), msg.HelpGoPath},
		{pane("sync-other"), msg.HelpSyncOther},
		{ws("toggle-hidden"), msg.HelpHidden},
		{ws("local:view"), msg.HelpView},
		{ws("reload"), msg.HelpReload},
		{[]Ref{{app.RoleNone, "local:redraw"}}, msg.HelpRedraw},
		{ws("cancel"), msg.HelpCancel},
		{ws("quit"), msg.HelpQuit},
	}
}()

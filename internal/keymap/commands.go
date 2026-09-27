package keymap

import (
	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/fsops"
)

// Command は、キーに割り当てる操作と、その安定した名前（ID）。ID は、キーの表・案内・ヘルプと、後の版の設定ファイル・パレットで使う（filer §4）。
type Command struct {
	ID     string
	Action app.Action // Role は入れない（キーを解決した役割を Resolver が入れる）
}

// Commands は、キーに割り当てられる操作の一覧。文字の入力（app.ActInsert）は、表の Text で扱うので含めない。
var Commands = func() []Command {
	cmd := func(id string, k app.ActionKind) Command { return Command{ID: id, Action: app.Action{Kind: k}} }
	decide := func(id string, k app.ActionKind, d fsops.Decision) Command {
		return Command{ID: id, Action: app.Action{Kind: k, Decision: d}}
	}
	return []Command{
		cmd("up", app.ActUp), cmd("down", app.ActDown), cmd("page-up", app.ActPageUp), cmd("page-down", app.ActPageDown),
		cmd("home", app.ActHome), cmd("end", app.ActEnd),
		cmd("enter", app.ActEnter), cmd("enter-dir", app.ActEnterDir), cmd("parent", app.ActParent), cmd("next-pane", app.ActNextPane),
		cmd("mark", app.ActMark), cmd("mark-all", app.ActMarkAll), cmd("toggle-hidden", app.ActToggleHidden), cmd("reload", app.ActReload),
		cmd("go-path", app.ActGoPath), cmd("sync-other", app.ActSyncOther), cmd("help", app.ActHelp), cmd("quit", app.ActQuit),
		cmd("cancel", app.ActCancel), cmd("yes", app.ActYes), cmd("no", app.ActNo),
		cmd("backspace", app.ActBackspace), cmd("delete-char", app.ActDelete), cmd("left", app.ActLeft), cmd("right", app.ActRight),
		cmd("line-home", app.ActLineHome), cmd("line-end", app.ActLineEnd), cmd("submit", app.ActSubmit),
		cmd("yank", app.ActYank), cmd("paste-copy", app.ActPasteCopy), cmd("paste-move", app.ActPasteMove),
		decide("decide:skip", app.ActDecide, fsops.DecisionSkip), decide("decide:overwrite", app.ActDecide, fsops.DecisionOverwrite),
		decide("decide:auto-rename", app.ActDecide, fsops.DecisionAutoRename), decide("decide:merge", app.ActDecide, fsops.DecisionMerge),
		decide("decide-all:skip", app.ActDecideAll, fsops.DecisionSkip), decide("decide-all:overwrite", app.ActDecideAll, fsops.DecisionOverwrite),
		decide("decide-all:auto-rename", app.ActDecideAll, fsops.DecisionAutoRename), decide("decide-all:merge", app.ActDecideAll, fsops.DecisionMerge),
		cmd("newer-only", app.ActNewerOnly), cmd("toggle", app.ActToggle), cmd("unset-only", app.ActUnsetOnly),
		cmd("english", app.ActEnglish), cmd("last-result", app.ActLastResult), cmd("force-quit", app.ActForceQuit),
		cmd("trash", app.ActTrash), cmd("purge", app.ActPurge), cmd("rename", app.ActRename), cmd("new-dir", app.ActNewDir),
	}
}()

// CommandByID は、ID の操作を返す。
func CommandByID(id string) (Command, bool) {
	for _, c := range Commands {
		if c.ID == id {
			return c, true
		}
	}
	return Command{}, false
}

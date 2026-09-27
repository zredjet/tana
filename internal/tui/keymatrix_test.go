package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/keymap"
	"github.com/zredjet/tana/internal/keys"
)

// キーの行列: 画面の状態ごとに、どのキー入力が、どの操作になるかを表にして、ゴールデンファイルと比べる。
// キーの割り当てを作り直すとき（filer §4 の「UI の骨格」。フェーズ22・23）に、振る舞いが変わっていないことを確かめる。

// actionNames は、行列に書く操作の名前。操作を足したら、ここにも足す（TestKeyMatrix が確かめる）。
var actionNames = map[app.ActionKind]string{
	app.ActUp: "Up", app.ActDown: "Down", app.ActPageUp: "PageUp", app.ActPageDown: "PageDown", app.ActHome: "Home", app.ActEnd: "End",
	app.ActEnter: "Enter", app.ActEnterDir: "EnterDir", app.ActParent: "Parent", app.ActNextPane: "NextPane",
	app.ActMark: "Mark", app.ActMarkAll: "MarkAll", app.ActToggleHidden: "ToggleHidden", app.ActReload: "Reload",
	app.ActGoPath: "GoPath", app.ActSyncOther: "SyncOther", app.ActHelp: "Help", app.ActQuit: "Quit", app.ActCancel: "Cancel",
	app.ActYes: "Yes", app.ActNo: "No", app.ActInsert: "Insert", app.ActBackspace: "Backspace", app.ActDelete: "Delete",
	app.ActLeft: "Left", app.ActRight: "Right", app.ActLineHome: "LineHome", app.ActLineEnd: "LineEnd", app.ActSubmit: "Submit",
	app.ActYank: "Yank", app.ActPasteCopy: "PasteCopy", app.ActPasteMove: "PasteMove", app.ActDecide: "Decide",
	app.ActDecideAll: "DecideAll", app.ActNewerOnly: "NewerOnly", app.ActToggle: "Toggle", app.ActUnsetOnly: "UnsetOnly",
	app.ActEnglish: "English", app.ActLastResult: "LastResult", app.ActForceQuit: "ForceQuit", app.ActTrash: "Trash",
	app.ActPurge: "Purge", app.ActRename: "Rename", app.ActNewDir: "NewDir",
}

// matrixState は、行列の列（画面の状態）とその作り方。role は、作り方が意図した画面になったかを確かめるための、一番上の重ねる部品の役割。
type matrixState struct {
	name  string
	role  app.Role
	build func(t *testing.T) *scene
}

// failedResult は、結果の画面を出す実行（1 項目が失敗する）。
func failedResult(plan **opPlan) func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
	return func(context.Context, fsops.ExecOptions) (*fsops.Result, error) {
		var items []fsops.ItemResult
		for _, s := range (*plan).req.Sources {
			items = append(items, fsops.ItemResult{Src: s, Outcome: fsops.OutcomeFailed, Err: &fsops.OpError{Kind: fsops.KindNotFound}})
		}
		return &fsops.Result{Status: fsops.StatusCompletedWithErrors, Items: items}, nil
	}
}

var matrixStates = []matrixState{
	{"browse", app.RoleNone, func(t *testing.T) *scene { return newScene(t) }},
	{"planning", app.RolePlanning, func(t *testing.T) *scene {
		sc, _ := newOpScene(t, fsops.OpCopy, nil, nil)
		sc.keys(key(keys.KeyEsc)) // 確認をやめ、計画を作っている途中で止める
		sc.hold = true
		sc.keys(char('p'))
		return sc
	}},
	{"help", app.RoleHelp, func(t *testing.T) *scene { sc := newScene(t); sc.keys(char('?')); return sc }},
	{"path", app.RolePath, func(t *testing.T) *scene { sc := newScene(t); sc.keys(char('g')); return sc }},
	{"rename", app.RoleRename, func(t *testing.T) *scene { sc := newScene(t); sc.moveTo("README.md"); sc.keys(char('r')); return sc }},
	{"newdir", app.RoleNewDir, func(t *testing.T) *scene { sc := newScene(t); sc.keys(char('n')); return sc }},
	{"exec", app.RoleExec, func(t *testing.T) *scene {
		sc := newScene(t)
		sc.moveTo("setup.exe")
		sc.keys(key(keys.KeyEnter))
		return sc
	}},
	{"confirm", app.RoleConfirm, func(t *testing.T) *scene { sc, _ := newOpScene(t, fsops.OpCopy, nil, nil); return sc }},
	{"conflicts", app.RoleConflicts, func(t *testing.T) *scene {
		sc, _ := newOpScene(t, fsops.OpCopy, nil, nil)
		sc.draw(80, 24)
		sc.keys(key(keys.KeyEnter))
		return sc
	}},
	{"progress", app.RoleProgress, func(t *testing.T) *scene {
		sc, _ := newOpScene(t, fsops.OpCopy, nil, nil)
		sc.draw(80, 24)
		sc.keys(key(keys.KeyEnter))
		sc.draw(80, 24)
		sc.hold = true
		sc.keys(key(keys.KeyEnter))
		return sc
	}},
	{"cancelask", app.RoleCancelAsk, func(t *testing.T) *scene {
		sc, _ := newOpScene(t, fsops.OpCopy, nil, nil)
		sc.draw(80, 24)
		sc.keys(key(keys.KeyEnter))
		sc.draw(80, 24)
		sc.hold = true
		sc.keys(key(keys.KeyEnter), key(keys.KeyEsc))
		return sc
	}},
	{"result", app.RoleResult, func(t *testing.T) *scene {
		var plan *opPlan
		sc, p := newOpScene(t, fsops.OpCopy, failedResult(&plan), nil)
		plan = p
		sc.draw(80, 24)
		sc.keys(key(keys.KeyEnter))
		sc.draw(80, 24)
		sc.keys(key(keys.KeyEnter))
		return sc
	}},
	{"delete", app.RoleDelete, func(t *testing.T) *scene {
		sc, _ := newOpScene(t, fsops.OpCopy, nil, nil)
		sc.keys(key(keys.KeyEsc), key(keys.KeyDown), char('D')) // .. の次の項目を完全削除する
		return sc
	}},
}

// matrixEvents は、行列の行（キー入力）。修飾キーは、割り当ての規則を見分けるのに足りる組み合わせにする
// （なし、Shift だけ、Alt、Ctrl、Meta、Ctrl+Shift）。
func matrixEvents() []keys.Event {
	mods := []keys.Mod{0, keys.ModShift, keys.ModAlt, keys.ModCtrl, keys.ModMeta, keys.ModCtrl | keys.ModShift}
	var evs []keys.Event
	for k := keys.KeyEsc; k <= keys.KeyF20; k++ {
		for _, m := range mods {
			evs = append(evs, keys.Event{Kind: keys.KeyEvent, Key: k, Mod: m})
		}
	}
	runes := []rune{'あ', 'é'}
	for r := rune(0x20); r <= 0x7e; r++ {
		runes = append(runes, r)
	}
	for _, r := range runes {
		for _, m := range mods {
			evs = append(evs, keys.Event{Kind: keys.KeyEvent, Key: keys.KeyRune, Rune: r, Mod: m})
		}
	}
	for _, text := range []string{"", "q", "D", "y", "\r", "abc", "a\nb", "x\r\ny", "z\rw"} {
		evs = append(evs, keys.Event{Kind: keys.PasteEvent, Text: text})
	}
	return append(evs, keys.Event{Kind: keys.CursorPositionEvent, Row: 1, Col: 1}, keys.Event{Kind: keys.UnknownEvent, Raw: "\x1b[?1c"})
}

// cell は、1 つの状態で、キー入力 ev がどの操作になるかを書く。操作にならなければ "-"。
func cell(t *testing.T, sc *scene, ev keys.Event) string {
	t.Helper()
	act, loc, ok := sc.f.resolve(ev)
	switch {
	case !ok:
		return "-"
	case loc == keymap.LocalRedraw:
		return "local:redraw"
	case loc == keymap.LocalView:
		return "local:view"
	}
	name, known := actionNames[act.Kind]
	if !known {
		t.Fatalf("action %d has no name in actionNames", act.Kind)
	}
	switch act.Kind {
	case app.ActInsert:
		name += ":" + fmt.Sprintf("%q", act.Text)
	case app.ActDecide, app.ActDecideAll:
		name += ":" + act.Decision.String()
	}
	return name
}

// TestKeyMatrix は、状態ごとのキーの割り当てを、ゴールデンファイル testdata/keymatrix.golden と比べる。
// 操作にならないキー入力は、どの状態でも操作にならない行を省く（行の数を抑えるため）。-update で書き直す。
func TestKeyMatrix(t *testing.T) {
	t.Parallel()
	for k := app.ActUp; k <= app.ActNewDir; k++ {
		if _, ok := actionNames[k]; !ok {
			t.Fatalf("action %d has no name in actionNames", k)
		}
	}
	scenes := make([]*scene, len(matrixStates))
	header := []string{"# event"}
	for i, st := range matrixStates {
		sc := st.build(t)
		if top(sc.a) != st.role {
			t.Fatalf("%s: top %v, want %v", st.name, top(sc.a), st.role)
		}
		scenes[i] = sc
		header = append(header, st.name)
	}
	lines := []string{strings.Join(header, "\t")}
	for _, ev := range matrixEvents() {
		row := []string{ev.String()}
		hit := false
		for _, sc := range scenes {
			c := cell(t, sc, ev)
			hit = hit || c != "-"
			row = append(row, c)
		}
		if hit {
			lines = append(lines, strings.Join(row, "\t"))
		}
	}
	got := strings.Join(lines, "\n") + "\n"
	path := filepath.Join("testdata", "keymatrix.golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/tui -run TestKeyMatrix -update to create it)", err)
	}
	if got == string(want) {
		return
	}
	gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
	for i := range max(len(gl), len(wl)) {
		var g, w string
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if g != w {
			t.Errorf("%s differs at line %d:\n got: %s\nwant: %s", path, i+1, g, w)
			return
		}
	}
}

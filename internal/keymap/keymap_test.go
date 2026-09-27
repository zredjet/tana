package keymap

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/keys"
	"github.com/zredjet/tana/internal/msg"
)

// events は、表を確かめるためのキー入力（特殊キー・文字のキーと修飾キーの組み合わせ、貼り付け、そのほか）。
func events() []keys.Event {
	var mods []keys.Mod
	for m := keys.Mod(0); m < 16; m++ {
		mods = append(mods, m)
	}
	var evs []keys.Event
	for k := keys.KeyEsc; k <= keys.KeyF20; k++ {
		for _, m := range mods {
			evs = append(evs, keys.Event{Kind: keys.KeyEvent, Key: k, Mod: m})
		}
	}
	runes := []rune{'あ', 'é'}
	for c := rune(0x20); c <= 0x7e; c++ {
		runes = append(runes, c)
	}
	for _, c := range runes {
		for _, m := range mods {
			evs = append(evs, keys.Event{Kind: keys.KeyEvent, Key: keys.KeyRune, Rune: c, Mod: m})
		}
	}
	for _, text := range []string{"", "q", "D", "y", "\r", "a\nb", "x\r\ny", "z\rw", "\x1b[A"} {
		evs = append(evs, keys.Event{Kind: keys.PasteEvent, Text: text})
	}
	return append(evs, keys.Event{Kind: keys.CursorPositionEvent, Row: 1, Col: 1}, keys.Event{Kind: keys.UnknownEvent, Raw: "\x1b[?1c"})
}

// TestTablesComplete は、すべての役割に表があり、表の役割が合っていることを確かめる。
func TestTablesComplete(t *testing.T) {
	t.Parallel()
	for _, r := range app.AllRoles() {
		km, ok := Maps[r]
		if !ok || km.Role != r {
			t.Errorf("role %d: no key map (or a wrong Role)", r)
		}
	}
	if len(Maps) != len(app.AllRoles()) {
		t.Errorf("%d key maps for %d roles", len(Maps), len(app.AllRoles()))
	}
}

// TestCommands は、操作の ID が重ならず、表・案内・ヘルプに書いた ID がどれも操作の一覧にあることを確かめる。
func TestCommands(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, c := range Commands {
		if seen[c.ID] || c.ID == "" {
			t.Errorf("command ID %q is empty or duplicated", c.ID)
		}
		seen[c.ID] = true
		if c.Action.Kind == app.ActInsert || c.Action.Role != app.RoleNone {
			t.Errorf("command %q: %+v (insert is not a command; the resolver sets Role)", c.ID, c.Action)
		}
	}
	for _, id := range localIDs {
		if seen[id] {
			t.Errorf("local ID %q is also a command ID", id)
		}
	}
	locals := slices.Collect(maps.Values(localIDs))
	check := func(where, id string) {
		if _, ok := CommandByID(id); !ok && !slices.Contains(locals, id) {
			t.Errorf("%s: unknown ID %q", where, id)
		}
	}
	for r, km := range Maps {
		for _, b := range km.bindings() {
			if b.Local == LocalNone {
				check("key map", b.Command)
			}
			if len(b.Keys) == 0 {
				t.Errorf("role %d: binding %q has no key", r, b.id())
			}
		}
		if km.AnyKey != "" {
			check("any key", km.AnyKey)
		}
	}
}

// TestRoleCommands は、表の操作がどれもその役割の部品の受け付ける操作であること（app.RoleCommands）と、
// 部品の受け付ける操作にはどれもキーがあること（文字の入力は Text）を確かめる。
func TestRoleCommands(t *testing.T) {
	t.Parallel()
	for _, r := range app.AllRoles() {
		km := Maps[r]
		accepts := app.RoleCommands(r)
		bound := map[app.ActionKind]bool{}
		for _, b := range km.bindings() {
			if b.Local != LocalNone {
				continue
			}
			c, _ := CommandByID(b.Command)
			bound[c.Action.Kind] = true
			if !slices.Contains(accepts, c.Action.Kind) {
				t.Errorf("role %d: key for %q, which the component does not accept", r, b.Command)
			}
		}
		if km.AnyKey != "" {
			c, _ := CommandByID(km.AnyKey)
			bound[c.Action.Kind] = true
		}
		if km.Text != TextNone {
			bound[app.ActInsert] = true
		}
		for _, k := range accepts {
			if !bound[k] {
				t.Errorf("role %d: action %d has no key", r, k)
			}
		}
	}
}

// TestNoOverlap は、同じ役割の中で、1 つのキー入力に 2 つの割り当てが当たらないことと、
// v0.1 の表がどれも 1 つのキーの並びであること（Resolver はまだ並びの途中を扱わない）を確かめる。
func TestNoOverlap(t *testing.T) {
	t.Parallel()
	evs := events()
	for _, km := range append(slices.Collect(maps.Values(Maps)), Global) {
		for _, b := range km.bindings() {
			for _, c := range b.Keys {
				if len(c) != 1 {
					t.Errorf("role %d: %q has a key sequence (not handled in v0.1)", km.Role, b.id())
				}
			}
		}
		for _, ev := range evs {
			var hit []string
			for _, b := range km.bindings() {
				for _, c := range b.Keys {
					if c[0].Match(ev) {
						hit = append(hit, b.id())
					}
				}
			}
			if len(hit) > 1 {
				t.Errorf("role %d: %v matches %q", km.Role, ev, hit)
			}
		}
	}
}

// TestPasteOnlyInserts は、どの道筋でも、貼り付けが入力欄への文字の入力にしかならないことを確かめる（tui §5。filer U2）。
func TestPasteOnlyInserts(t *testing.T) {
	t.Parallel()
	var res Resolver
	for _, r := range app.AllRoles() {
		path := []app.Role{r}
		if r == app.RolePane {
			path = append(path, app.RoleWorkspace)
		}
		for _, ev := range events() {
			if ev.Kind != keys.PasteEvent {
				continue
			}
			if got := res.Lookup(path, ev); got.OK && (got.Local != LocalNone || got.Action.Kind != app.ActInsert) {
				t.Errorf("role %d: paste %q became %+v", r, ev.Text, got)
			}
		}
	}
}

// TestForbiddenKeys は、割り当てに使わないキー（filer §7: F11、Alt、Ctrl+I・Ctrl+M・Ctrl+[）がないことを確かめる。
func TestForbiddenKeys(t *testing.T) {
	t.Parallel()
	for _, km := range Maps {
		for _, b := range append(km.bindings(), Global.bindings()...) {
			for _, c := range b.Keys {
				s := c[0]
				bad := s.Key == keys.KeyF11 || s.Mod&keys.ModAlt != 0 ||
					s.Key == keys.KeyRune && s.Mod&keys.ModCtrl != 0 && strings.ContainsRune("iImM[", s.Rune)
				if bad {
					t.Errorf("role %d: %q uses a key not to be assigned: %+v", km.Role, b.id(), s)
				}
			}
		}
	}
}

// TestGuideItemsBound は、案内とヘルプの項目の操作に、どれもキーがあることを確かめる。
func TestGuideItemsBound(t *testing.T) {
	t.Parallel()
	for _, g := range Guides {
		for _, it := range g.Items {
			for _, id := range it.IDs {
				if KeyName(it.Role, id) == "" {
					t.Errorf("guide item %q: role %d has no key for %q", it.Label, it.Role, id)
				}
			}
		}
	}
	for _, h := range HelpRows {
		for _, r := range h.Refs {
			if len(keyNames(r.Role, r.ID)) == 0 {
				t.Errorf("help %q: role %d has no key for %q", h.Label, r.Role, r.ID)
			}
		}
	}
}

// TestHelpCoversBrowse は、閲覧の画面のキー（作業場とペインと共通の表）が、ヘルプのどれか 1 行にだけ載っていることを確かめる（? はヘルプそのもの）。
func TestHelpCoversBrowse(t *testing.T) {
	t.Parallel()
	count := map[Ref]int{}
	for _, h := range HelpRows {
		for _, r := range h.Refs {
			count[r]++
		}
	}
	for _, role := range []app.Role{app.RoleWorkspace, app.RolePane, app.RoleNone} {
		km := Global
		if role != app.RoleNone {
			km = Maps[role]
		}
		for _, b := range km.bindings() {
			ref := Ref{role, b.id()}
			if ref == (Ref{app.RoleWorkspace, "help"}) {
				continue
			}
			if count[ref] != 1 {
				t.Errorf("%+v is in %d help rows, want 1", ref, count[ref])
			}
		}
	}
}

// TestHelpKeys は、ヘルプのキーの欄の形を確かめる（操作ごとにキーを空白 1 つ、操作の間は空白 2 つ。フェーズ23で決めた）。
func TestHelpKeys(t *testing.T) {
	t.Parallel()
	want := []string{"Up k  Down j", "PgUp  PgDn  Home  End", "Enter", "h Left Backspace", "l Right", "Tab", "Space", "y", "p  P", "d  D",
		"r  n", "L", "a", "g", "=", ".", "v", "Ctrl+R", "Ctrl+L", "Esc", "q"}
	var got []string
	for _, h := range HelpRows {
		got = append(got, h.Keys())
	}
	if !slices.Equal(got, want) {
		t.Errorf("help keys:\n got %q\nwant %q", got, want)
	}
	if got := HelpRows[9].Label; got != "ごみ箱へ（Enter で実行）・完全削除（y で確定）" {
		t.Errorf("trash row %q", got)
	}
}

// TestGuidesMatchTexts は、表から作った案内が、フェーズ22までの文言と同じであることを確かめる（案内を表から作るように移すため）。
func TestGuidesMatchTexts(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		got, want string
	}{
		{MainGuide.Render(), msg.KeyGuide},
		{ExecGuide.Render(), msg.ExecChoices},
		{ConfirmGuide.Render(), msg.ConfirmKeys},
		{ConfirmConflictsGuide.Render(), msg.ConfirmKeysConflict},
		{ConfirmNoneGuide.Render(), msg.ConfirmKeysNone},
		{ConfirmPurgeGuide.Render(), msg.ConfirmKeysPurge},
		{DeleteGuide.Render(), msg.DeleteChoices},
		{DeleteNoneGuide.Render(), msg.DeleteChoicesNone},
		{RenameGuide.Render(), msg.RenameKeys},
		{NewDirGuide.Render(), msg.NewDirKeys},
		{CancelAskGuide.Render(), msg.CancelChoices},
		{ProgressGuide.Render(), msg.ProgressKeys},
		{ForceQuitGuide.Render(), msg.ForceQuitKey},
		{ResultPurge.Text(), msg.PurgeKey},
		{ResultInside.WithLabel(msg.GuideInside(2, false)).Text(), msg.InsideKey(2, false)},
		{ResultInside.WithLabel(msg.GuideInside(2, true)).Text(), msg.InsideKey(2, true)},
		{Join(SepDialog, ResultEnglish, ResultClose), msg.ResultKeys},
		{ConflictAllGuide.Render(), msg.ConflictKeys[0]},
		{ConflictKeysGuide.Render(), msg.ConflictKeys[1]},
	} {
		if tt.got != tt.want {
			t.Errorf("guide %q, want %q", tt.got, tt.want)
		}
	}
	for i, it := range ConflictRowGuide.Items {
		c, _ := CommandByID(it.IDs[0])
		if k := msg.ConflictRowKeys[i]; it.Text() != k.Text || c.Action.Decision != k.Decision {
			t.Errorf("conflict row item %q (%v), want %q (%v)", it.Text(), c.Action.Decision, k.Text, k.Decision)
		}
	}
}

// TestAppTexts は、app が作る文言に書いたキーの名前が、表と合っていることを確かめる（割り当ての変更は後の版。そのときキーの名前を app に渡す）。
func TestAppTexts(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		text string
		role app.Role
		id   string
	}{
		{msg.NothingYanked, app.RolePane, "yank"},
		{msg.Yanked(1), app.RolePane, "paste-copy"},
		{msg.Yanked(1), app.RolePane, "paste-move"},
		{msg.Done(fsops.OpCopy, 1, 0, true), app.RoleWorkspace, "last-result"},
		{msg.InnerCollapsed, app.RoleConflicts, "toggle"},
	} {
		if key := KeyName(tt.role, tt.id); !strings.Contains(tt.text, key+" で") {
			t.Errorf("%q does not name the key %q for %s", tt.text, key, tt.id)
		}
	}
}

// TestLookup は、道筋に沿った解決の性質を確かめる: 内側の役割から引き、解決した役割を Action.Role に入れる。
func TestLookup(t *testing.T) {
	t.Parallel()
	var res Resolver
	browse := []app.Role{app.RolePane, app.RoleWorkspace}
	key := func(k keys.Key) keys.Event { return keys.Event{Kind: keys.KeyEvent, Key: k} }
	char := func(c rune) keys.Event { return keys.Event{Kind: keys.KeyEvent, Key: keys.KeyRune, Rune: c} }
	for _, tt := range []struct {
		path []app.Role
		ev   keys.Event
		want Result
	}{
		{browse, char('j'), Result{Action: app.Action{Kind: app.ActDown, Role: app.RolePane}, OK: true}},
		{browse, char('q'), Result{Action: app.Action{Kind: app.ActQuit, Role: app.RoleWorkspace}, OK: true}},
		{browse, char('v'), Result{Local: LocalView, OK: true}},
		{browse, keys.Event{Kind: keys.KeyEvent, Key: keys.KeyRune, Rune: 'l', Mod: keys.ModCtrl}, Result{Local: LocalRedraw, OK: true}},
		{browse, char('x'), Result{}},
		{[]app.Role{app.RolePath}, char('j'), Result{Action: app.Action{Kind: app.ActInsert, Text: "j", Role: app.RolePath}, OK: true}},
		{[]app.Role{app.RolePath}, key(keys.KeyEnter), Result{Action: app.Action{Kind: app.ActSubmit, Role: app.RolePath}, OK: true}},
		{[]app.Role{app.RoleHelp}, char('q'), Result{Action: app.Action{Kind: app.ActCancel, Role: app.RoleHelp}, OK: true}},
		{[]app.Role{app.RoleConflicts}, char('O'), Result{Action: app.Action{Kind: app.ActDecideAll, Decision: fsops.DecisionOverwrite, Role: app.RoleConflicts}, OK: true}},
	} {
		if got := res.Lookup(tt.path, tt.ev); got != tt.want {
			t.Errorf("%v on %v: %+v, want %+v", tt.ev, tt.path, got, tt.want)
		}
	}
}

// FuzzLookup は、どんな入力のバイト列でも、解決が落ちず、貼り付けが文字の入力にしかならないことを確かめる。
func FuzzLookup(f *testing.F) {
	for _, s := range []string{"q", "\x1b", "\x1b[A", "\x1b[200~D\x1b[201~", "\x0c", "あ", "\x1b[1;5A"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		var d keys.Decoder
		now := time.Unix(0, 0)
		evs := append(d.Feed(b, now), d.Tick(now.Add(time.Hour))...)
		var res Resolver
		for _, r := range app.AllRoles() {
			for _, ev := range evs {
				got := res.Lookup([]app.Role{r, app.RoleWorkspace}, ev)
				if ev.Kind == keys.PasteEvent && got.OK && got.Action.Kind != app.ActInsert {
					t.Fatalf("role %d: paste %q became %+v", r, ev.Text, got)
				}
			}
		}
	})
}

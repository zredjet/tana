package tui

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/keymap"
	"github.com/zredjet/tana/internal/keys"
	"github.com/zredjet/tana/internal/screen"
)

// TestDrawersComplete は、重ねる部品のすべての役割に、配置と描き方があることを確かめる（作業場とペインは Draw が描く）。
func TestDrawersComplete(t *testing.T) {
	t.Parallel()
	for _, r := range app.AllRoles() {
		if r == app.RoleWorkspace || r == app.RolePane {
			continue
		}
		d, ok := drawers[r]
		if !ok || d.draw == nil || d.place == placeDialog && d.size == nil {
			t.Errorf("role %d has no placement or drawer", r)
		}
	}
}

// TestCursorOnlyWhenFocused は、本物のカーソル（IME の変換中の文字が出る所）を、道筋の一番内側の部品の描き方だけが置くことを確かめる（G8）。
func TestCursorOnlyWhenFocused(t *testing.T) {
	t.Parallel()
	for _, open := range []rune{'g', 'n'} {
		sc := newScene(t)
		sc.keys(char(open))
		v := sc.a.Modals()[0]
		d := drawers[v.Role()]
		for _, focused := range []bool{true, false} {
			s := screen.New(80, 24)
			s.SetCursor(0, 0, false)
			d.draw(sc.f, s, sc.f.region(s, d, v), v, &frame{focused: focused})
			if _, _, visible := s.Cursor(); visible != focused {
				t.Errorf("%c: focused %v, cursor visible %v", open, focused, visible)
			}
		}
	}
}

// TestThemeComplete は、見た目の表のすべての意味の名前が、既定の値を持つことを確かめる（G15）。
func TestThemeComplete(t *testing.T) {
	t.Parallel()
	v := reflect.ValueOf(defaultTheme)
	for i := range v.NumField() {
		if v.Field(i).IsZero() {
			t.Errorf("theme.%s has no default", v.Type().Field(i).Name)
		}
	}
}

// TestDialogKeepsKeys は、ダイアログの高さが画面に足りないときも、キーの案内の行（最後の行）を残すことを確かめる。
// 残さないと、確定のキー（完全削除の y）が見えないまま、確定できる状態になる（U2）。本文は切り詰め、残りの行数を示す。
func TestDialogKeepsKeys(t *testing.T) {
	t.Parallel()
	sc, plan := newOpScene(t, fsops.OpCopy, nil, nil)
	for i := range 20 { // 警告の行で、完全削除の確認を 24 行に収まらなくする
		plan.warnings = append(plan.warnings, &fsops.OpError{Kind: fsops.KindPermission, Path: colHome + "/w" + strconv.Itoa(i)})
	}
	sc.keys(key(keys.KeyEsc), key(keys.KeyDown), char('D'))
	if top(sc.a) != app.RoleDelete {
		t.Fatalf("top %v", top(sc.a))
	}
	s := sc.draw(80, 24)
	out := render(s)
	if !strings.Contains(out, keymap.DeleteGuide.Render()) {
		t.Errorf("the delete keys are not on the screen:\n%s", out)
	}
	if !strings.Contains(out, "ほか ") || !strings.Contains(out, " 行") {
		t.Errorf("no note of the cut lines:\n%s", out)
	}
}

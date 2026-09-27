package tui

import (
	"reflect"
	"testing"

	"github.com/zredjet/tana/internal/app"
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

package tui

import (
	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/keymap"
	"github.com/zredjet/tana/internal/msg"
	"github.com/zredjet/tana/internal/screen"
)

// 重ねる部品の配置と描き方（filer §4 の「UI の骨格」。G4・G8）。
// 配置（どこに、どの大きさで）は役割ごとの表 drawers で決め、描き方は与えられた領域の中だけに描く。
// 後の版で、同じ部品をパネル（寄せる領域）に置くときは、配置を足すだけで、描き方は変えない。

// placement は、重ねる部品の置き場所。
type placement int

const (
	placeDialog      placement = iota // 画面の中央のダイアログ
	placeFull                         // 画面全体
	placeMessageLine                  // メッセージ行（計画を作成中の表示）
)

// frame は、1 回の描画で、描き方に渡す情報と、描き方から集めるもの。
type frame struct {
	focused bool // 道筋の一番内側の部品。本物のカーソル（IME）は、これだけが置く（G8）
	rows    int  // 描いた一覧の行数（ページ単位の移動に使う。app.SetListRows に渡す）。一覧がなければ 0
}

// drawer は、役割の描き方。
type drawer struct {
	place placement
	size  func(f *Filer, s *screen.Screen, v app.View) (w, h int) // placeDialog: 枠を含む大きさ（幅は画面に収める）
	draw  func(f *Filer, s *screen.Screen, r screen.Region, v app.View, fr *frame)
}

// linesDrawer は、行を並べるダイアログの描き方（確認・進捗・中止の確認・完全削除の確認）。
// content は、題名・幅・行を返す。高さは行の数に合わせ、画面に収める。
func linesDrawer(content func(f *Filer, s *screen.Screen, v app.View) (string, int, []line)) drawer {
	return drawer{
		place: placeDialog,
		size: func(f *Filer, s *screen.Screen, v app.View) (int, int) {
			_, w, lines := content(f, s, v)
			_, rows := s.Size()
			return w, min(len(lines)+2, rows-2)
		},
		draw: func(f *Filer, s *screen.Screen, r screen.Region, v app.View, _ *frame) {
			title, _, lines := content(f, s, v)
			drawLinesIn(s, r, title, lines)
		},
	}
}

// fixed は、大きさの決まったダイアログの大きさ。
func fixed(w, h int) func(*Filer, *screen.Screen, app.View) (int, int) {
	return func(*Filer, *screen.Screen, app.View) (int, int) { return w, h }
}

// drawers は、重ねる部品の役割ごとの配置と描き方。テストで、すべての役割（作業場とペインを除く）にあることを確かめる。
var drawers = map[app.Role]drawer{
	app.RoleHelp:      {placeDialog, func(*Filer, *screen.Screen, app.View) (int, int) { return 76, len(keymap.HelpRows) + 2 }, (*Filer).drawHelp},
	app.RolePath:      {placeDialog, fixed(76, 3), (*Filer).drawPathInput},
	app.RoleRename:    {placeDialog, fixed(64, 9), (*Filer).drawName},
	app.RoleNewDir:    {placeDialog, fixed(64, 9), (*Filer).drawName},
	app.RoleExec:      {placeDialog, fixed(60, 5), (*Filer).drawExec},
	app.RolePlanning:  {place: placeMessageLine, draw: (*Filer).drawPlanning},
	app.RoleConfirm:   linesDrawer((*Filer).confirmLines),
	app.RoleConflicts: {place: placeFull, draw: (*Filer).drawConflicts},
	app.RoleProgress:  linesDrawer((*Filer).progressLines),
	app.RoleCancelAsk: linesDrawer((*Filer).cancelAskLines),
	app.RoleResult:    {place: placeFull, draw: (*Filer).drawResult},
	app.RoleDelete:    linesDrawer((*Filer).deleteLines),
}

// region は、描き方 d の部品 v を置く領域。
func (f *Filer) region(s *screen.Screen, d drawer, v app.View) screen.Region {
	cols, rows := s.Size()
	switch d.place {
	case placeFull:
		return screen.Region{W: cols, H: rows}
	case placeMessageLine:
		return screen.Region{X: 0, Y: rows - 2, W: cols, H: 1}
	}
	w, h := d.size(f, s, v)
	return dialogRegion(s, w, h)
}

// drawModals は、作業場の上に重ねた部品を、下から順に描く。一番上の部品が、道筋の一番内側（v0.1 の重ねる部品は子を持たない）。
func (f *Filer) drawModals(s *screen.Screen) {
	views := f.app.Modals()
	for i, v := range views {
		d := drawers[v.Role()]
		fr := &frame{focused: i == len(views)-1}
		d.draw(f, s, f.region(s, d, v), v, fr)
		if fr.rows != 0 {
			f.app.SetListRows(v.Role(), fr.rows)
		}
	}
}

// drawPlanning は、計画を作っている間、0.2 秒を超えたら、メッセージ行 r に「計画を作成中」を出す（filer §8.1）。
func (f *Filer) drawPlanning(s *screen.Screen, r screen.Region, view app.View, _ *frame) {
	if !view.(app.PlanningView).Slow {
		return
	}
	s.Fill(r, screen.Style{})
	s.Put(r, 1, 0, msg.Planning(keymap.KeyName(app.RolePlanning, "cancel")), styleWarn)
}

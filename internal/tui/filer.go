package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/keymap"
	"github.com/zredjet/tana/internal/keys"
	"github.com/zredjet/tana/internal/listing"
	"github.com/zredjet/tana/internal/msg"
	"github.com/zredjet/tana/internal/screen"
	"github.com/zredjet/tana/internal/textfmt"
	"github.com/zredjet/tana/internal/textwidth"
)

// signalWait は、シグナルで終わるとき、実行中のファイル操作が中止されるのを待つ時間。
const signalWait = 3 * time.Second

// 端末の大きさの下限（filer §3）。これより小さいときは「端末が小さすぎます」とだけ出す。
const (
	minCols = 80
	minRows = 24
)

// Filer は、ファイラーのメイン画面（filer §5）。app の状態を描き、キー入力を app の操作に変える。
// 表示形式（2 ペインと Yazi 風）は Filer が持ち、v で切り替える（filer §4・§5.3）。app は表示形式を知らない。
type Filer struct {
	app    *app.App
	view   view
	redraw bool            // 次に描くとき、画面全体を端末に書き直す（Ctrl+L）
	keys   keymap.Resolver // キーの表（filer §4）。零値のまま使う
}

// view は表示形式。
type view int

const (
	viewPanes   view = iota // 2 ペイン（ペインを横に並べる。§5.1）
	viewColumns             // Yazi 風（親フォルダ、操作中のペイン、プレビュー。§5.3）
)

// needs は、表示形式が app に求めるもの。
func (f *Filer) needs() app.Needs {
	if f.view == viewColumns {
		return app.Needs{Parent: true, Preview: true}
	}
	return app.Needs{}
}

// RunFiler は、App a の画面をイベントループ l で動かす。cmds は app.New が返した最初の処理。
func RunFiler(l *Loop, a *app.App, cmds []app.Cmd) error {
	a.SetWake(l.Wake) // 実行中の進捗は、最新の値を置いて Wake で知らせる（filer §10）
	f := &Filer{app: a}
	f.run(l, cmds)
	return l.Run(f)
}

// run は、app の Cmd を作業用の goroutine で動かし、結果をイベントループに送る（filer §10）。
func (f *Filer) run(l *Loop, cmds []app.Cmd) {
	for _, c := range cmds {
		if c.Delay > 0 {
			time.AfterFunc(c.Delay, func() { l.Go(func() { l.Post(c.Run()) }) })
			continue
		}
		l.Go(func() { l.Post(c.Run()) })
	}
}

// Handle は、イベントを app に渡す。
func (f *Filer) Handle(l *Loop, ev Event) bool {
	switch ev.Kind {
	case KindKey:
		f.run(l, f.key(ev.Key))
	case KindMessage:
		f.run(l, f.app.Update(ev.Msg))
	case KindWake:
		f.app.Refresh()
	case KindSignal:
		// ファイル操作の実行中なら中止し、短い時間だけ Execute が戻るのを待つ（filer §10。Windows は約 5 秒で強制終了される）。
		f.app.Abort(signalWait)
		return false
	}
	return !f.app.Quit()
}

// key は、キー入力を処理する。キーは keymap の表で、フォーカスの道筋に沿って操作に解決する（filer §4）。
// v は表示形式を切り替え（Filer が持つ）、Ctrl+L は画面全体を描き直す。ほかは app の操作に変える。
// どのキー入力でも、メッセージ行を消す（操作にならないキーでも。「計画を作成中」の間は除く。filer §5.1）。
func (f *Filer) key(ev keys.Event) []app.Cmd {
	f.app.KeyPressed()
	act, loc, ok := f.resolve(ev)
	switch {
	case !ok:
		return nil
	case loc == keymap.LocalRedraw:
		f.redraw = true
		return nil
	case loc == keymap.LocalView:
		f.view = 1 - f.view
		return f.app.SetNeeds(f.needs())
	}
	return f.app.Do(act)
}

// resolve は、キー入力を app の操作か、tui の中だけで行う操作に変える。どちらでもなければ ok が偽。状態は変えない。
func (f *Filer) resolve(ev keys.Event) (act app.Action, loc keymap.Local, ok bool) {
	r := f.keys.Lookup(f.app.FocusPath(), ev)
	return r.Action, r.Local, r.OK
}

// ---- 描画 ----

// box は枠の文字（罫線は 4 端末とも 1 桁だった。filer §9.1）。
type box struct{ h, v, tl, tr, bl, br string }

var (
	boxSingle = box{"─", "│", "┌", "┐", "└", "┘"}
	boxDouble = box{"═", "║", "╔", "╗", "╚", "╝"} // 操作中のペイン
)

// 欄の幅（filer §5.1）。
const (
	sizeW    = 5  // サイズ（3.1K、<DIR>。msg.LabelDir などはこの幅）
	dateW    = 11 // 更新日時（09-24 11:19）
	minNameW = 16 // これより名前の欄が狭くなるなら、更新日時の欄を隠す
)

// Draw は、画面の全体を描く。
func (f *Filer) Draw(s *screen.Screen) {
	cols, rows := s.Size()
	s.SetCursor(0, 0, false)
	if f.redraw {
		s.Invalidate() // 差分でなく、画面全体を書く
		f.redraw = false
	}
	if cols < minCols || rows < minRows {
		s.Put(screen.Region{W: cols, H: rows}, 0, 0, msg.TooSmall, screen.Style{})
		return
	}
	a := f.app
	// 作業場の配置: 下端にキーの案内・メッセージ行・状態行を寄せ、残りにペイン（表示形式ごと）を置く（G13）。
	d := dock{rest: screen.Region{W: cols, H: rows}}
	guide, message, status := d.bottom(1), d.bottom(1), d.bottom(1)
	if f.view == viewColumns {
		f.drawColumns(s, d.rest)
	} else {
		for i, r := range splitCols(d.rest, len(a.Panes())) {
			f.drawPane(s, r, a.Panes()[i], i == a.Active())
		}
	}
	f.drawStatus(s, status)
	if n := a.Yanked(); n > 0 { // 覚えている項目の数（y。filer §7）は、状態行の右に出す
		text := " " + msg.YankedIndicator(n) + " "
		w := textwidth.Width(text)
		s.Put(screen.Region{X: status.X + status.W - w - 1, Y: status.Y, W: w, H: 1}, 0, 0, text, f.th().indicator)
	}
	if text, isErr := a.Message(); text != "" {
		st := screen.Style{}
		if isErr {
			st = f.th().err
		}
		s.Put(message, 1, 0, text, st)
	}
	s.Put(guide, 1, 0, keymap.MainGuide.Render(), f.th().dim)
	f.drawModals(s)
	a.Drawn() // 確認のダイアログは、描いた後に届いたキーで確定する（filer U2）
}

// drawBox は、領域 r に見た目 st の枠を描き、中を空白で塗る。title は上の枠の中に出す。
func drawBox(s *screen.Screen, r screen.Region, b box, title string, st screen.Style) {
	s.Fill(r, screen.Style{})
	s.Put(r, 0, 0, b.tl+strings.Repeat(b.h, r.W-2)+b.tr, st)
	for y := 1; y < r.H-1; y++ {
		s.Put(r, 0, y, b.v, st)
		s.Put(r, r.W-1, y, b.v, st)
	}
	s.Put(r, 0, r.H-1, b.bl+strings.Repeat(b.h, r.W-2)+b.br, st)
	if title != "" {
		s.Put(screen.Region{X: r.X + 2, Y: r.Y, W: r.W - 4, H: 1}, 0, 0, " "+title+" ", st)
	}
}

// drawPane は、ペイン p を領域 r に描く。枠の上にフォルダのパス、下に項目数を出す（filer §5.1）。
func (f *Filer) drawPane(s *screen.Screen, r screen.Region, p *app.Pane, active bool) {
	b, st := boxSingle, screen.Style{}
	if active {
		b, st = boxDouble, f.th().bold
	}
	drawBox(s, r, b, textfmt.TruncPath(p.Dir(), r.W-6), st)
	drawFooter(s, r, 2, p, st)
	inner := screen.Region{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}
	top := p.Window(inner.H)
	for row := 0; row < inner.H && top+row < p.Len(); row++ {
		f.drawItem(s, screen.Region{X: inner.X, Y: inner.Y + row, W: inner.W, H: 1}, p, top+row, active)
	}
}

// drawFooter は、枠 r の下の枠の x 桁目から、ペイン p の項目数と、読み込み中の表示を出す（filer §5.1・§6）。
func drawFooter(s *screen.Screen, r screen.Region, x int, p *app.Pane, st screen.Style) {
	footer := ""
	switch {
	case p.Loading():
		footer = msg.Loading(keymap.KeyName(app.RoleWorkspace, "cancel"))
	case p.Loaded():
		footer = msg.Items(p.Counts())
	}
	if footer != "" {
		s.Put(screen.Region{X: r.X + x, Y: r.Y + r.H - 1, W: r.W - x - 2, H: 1}, 0, 0, " "+footer+" ", st)
	}
}

// columns は、ペインの内側の幅 w での名前の欄の幅と、更新日時の欄を出すかを返す。
// 行は「マーク 1 桁、名前、空白、サイズ 5 桁、空白、更新日時 11 桁」。狭ければ更新日時を隠して名前に回す（filer §5.1）。
func columns(w int) (nameW int, date bool) {
	if _, ws := rowColumns(1, w-1, 1, flex, sizeW, dateW); ws[0] >= minNameW {
		return ws[0], true
	}
	_, ws := rowColumns(1, w-1, 1, flex, sizeW)
	return max(ws[0], 1), false
}

// drawItem は、ペイン p の i 番目の項目を 1 行の領域 r に描く。
func (f *Filer) drawItem(s *screen.Screen, r screen.Region, p *app.Pane, i int, active bool) {
	it := p.Item(i)
	marked := !it.Parent && p.Marked(it.Name)
	st := f.itemStyle(it, marked)
	if i == p.Cursor() {
		if active {
			st.Attr |= f.th().cursor
		} else {
			st.Attr |= f.th().cursorInactive
		}
	}
	s.Fill(r, st)
	if marked {
		s.Put(r, 0, 0, "*", st)
	}
	nameW, date := columns(r.W)
	f.putName(s, screen.Region{X: r.X + 1, Y: r.Y, W: nameW, H: 1}, textfmt.TruncName(it.Name, nameW), st)
	x := 1 + nameW + 1
	s.Put(r, x, 0, fmt.Sprintf("%*s", sizeW, sizeText(it)), st)
	if date && !it.Parent && it.Err == nil {
		s.Put(r, x+sizeW+1, 0, textfmt.Time(it.Info.ModTime, f.app.Now()), st)
	}
}

// putName は、名前を置く。表示で置き換える書記素クラスタ（制御文字など）は色を変える（filer §9.3）。置き換えは screen が行う（T2）。
func (f *Filer) putName(s *screen.Screen, r screen.Region, name string, st screen.Style) {
	x, start, replaced := 0, 0, false
	flush := func(end int) {
		if end > start {
			sty := st
			if replaced {
				sty.FG, sty.Attr = f.th().replaced.FG, sty.Attr|screen.AttrBold
			}
			x += s.Put(r, x, 0, name[start:end], sty)
		}
		start = end
	}
	pos := 0
	for c := range textwidth.All(name) {
		if rep := c.Class != textwidth.Normal; rep != replaced {
			flush(pos)
			replaced = rep
		}
		pos += len(c.Text)
	}
	flush(pos)
}

// itemStyle は、項目の種類とマークの見た目を返す（filer §9.5）。
func (f *Filer) itemStyle(it listing.Item, marked bool) screen.Style {
	var st screen.Style
	switch {
	case it.Err != nil:
		st = f.th().err
	case it.IsDir() && it.Info.Type != fsops.TypeJunction:
		st = f.th().dir
	case it.Info.Type == fsops.TypeJunction:
		st = f.th().junction
	case it.Info.Type == fsops.TypeSymlink:
		st = f.th().link
	case it.Info.Type == fsops.TypeSpecial:
		st = f.th().special
	}
	if marked {
		st = f.th().marked
	}
	if it.Hidden {
		st.Attr |= screen.AttrDim
	}
	return st
}

// sizeText は、サイズの欄の文字列（filer §5.1）。ファイルならサイズ、ほかは種類。
func sizeText(it listing.Item) string {
	switch {
	case it.Parent:
		return msg.LabelDir
	case it.Err != nil:
		return msg.LabelError
	}
	switch it.Info.Type {
	case fsops.TypeDir:
		return msg.LabelDir
	case fsops.TypeJunction:
		return msg.LabelJunction
	case fsops.TypeSymlink:
		return msg.LabelSymlink
	case fsops.TypeSpecial:
		return msg.LabelSpecial
	}
	return textfmt.Size(it.Info.Size)
}

// drawStatus は、状態行に、操作中のペインのカーソル行の項目の詳細を出す（filer §5.1）。
// 名前とリンク先は、置き換える文字を \x1b や ‮ の形で示す（filer §9.3）。
func (f *Filer) drawStatus(s *screen.Screen, r screen.Region) {
	p := f.app.Panes()[f.app.Active()]
	if !p.Loaded() || p.Len() == 0 {
		return
	}
	it := p.Item(p.Cursor())
	var info []string
	switch {
	case it.Parent:
		info = append(info, msg.TypeParent)
	case it.Err != nil:
		info = append(info, msg.Error(it.Err))
	default:
		switch it.Info.Type {
		case fsops.TypeFile:
			info = append(info, textfmt.Bytes(it.Info.Size)+" "+msg.UnitBytes)
		case fsops.TypeDir:
			info = append(info, msg.TypeDir)
		case fsops.TypeJunction, fsops.TypeSymlink:
			kind := msg.TypeSymlink
			if it.Info.Type == fsops.TypeJunction {
				kind = msg.TypeJunction
			}
			if target, ok := p.LinkTarget(it.Name); ok {
				kind += msg.LinkArrow + textfmt.Escape(target)
			}
			info = append(info, kind)
		case fsops.TypeSpecial:
			info = append(info, msg.TypeSpecial)
		}
		info = append(info, textfmt.FullTime(it.Info.ModTime))
	}
	rest := "   " + strings.Join(info, "   ")
	name := textfmt.Escape(it.Name)
	avail := max(r.W-2-textwidth.Width(rest), r.W/3)
	st := screen.Style{}
	if textfmt.HasReplaced(it.Name) {
		st = f.th().replaced
	}
	x := 1 + s.Put(r, 1, 0, textfmt.TruncName(name, avail), st)
	s.Put(r, x, 0, rest, screen.Style{})
}

// dialogRegion は、画面の中央に置くダイアログの領域。
func dialogRegion(s *screen.Screen, w, h int) screen.Region {
	cols, rows := s.Size()
	w = min(w, cols-4)
	return screen.Region{X: (cols - w) / 2, Y: max((rows-h)/2-1, 0), W: w, H: h}
}

// drawPathInput は、パスの入力欄を領域 r に描き、本物のカーソルを入力の位置に置く（IME の変換中の文字はここに出る。filer VU4）。
func (f *Filer) drawPathInput(s *screen.Screen, r screen.Region, view app.View, fr *frame) {
	drawBox(s, r, boxDouble, msg.PathInputTitle(keymap.KeyName(app.RolePath, "submit"), keymap.KeyName(app.RolePath, "cancel")), screen.Style{})
	field := screen.Region{X: r.X + 2, Y: r.Y + 1, W: r.W - 4, H: 1}
	e := view.(app.PathView).Edit
	v := e.View(field.W)
	s.Put(field, 0, 0, e.Text()[v.Start:v.End], screen.Style{})
	if fr.focused {
		s.SetCursor(field.X+v.CursorCol, field.Y, true)
	}
}

// drawName は、名前の変更・新しいフォルダの入力欄を描く（filer §8.7）。本物のカーソルを入力の位置に置く（IME。filer VU4）。
// 入力欄の文字は元のバイト列で、表示する形への置き換えは screen が描くときに行う（U4・U6）。
func (f *Filer) drawName(s *screen.Screen, r screen.Region, view app.View, fr *frame) {
	v := view.(app.NameView)
	rename := v.Role() == app.RoleRename
	title, keys, busy := msg.RenameTitle, keymap.RenameGuide.Render(), msg.RenameBusy(keymap.KeyName(app.RoleRename, "cancel"))
	if !rename {
		title, keys, busy = msg.NewDirTitle, keymap.NewDirGuide.Render(), msg.NewDirBusy(keymap.KeyName(app.RoleNewDir, "cancel"))
	}
	drawBox(s, r, boxDouble, title, f.th().bold)
	in := screen.Region{X: r.X + 2, Y: r.Y + 1, W: r.W - 4, H: r.H - 2}
	head := screen.Region{X: in.X, Y: in.Y + 1, W: in.W, H: 1}
	if rename {
		f.putName(s, head, textfmt.TruncName(v.Name, in.W), screen.Style{}) // 今の名前（置き換えた文字は色を変える）
	} else {
		s.Put(head, 0, 0, msg.Place(textfmt.TruncPath(v.Dir, in.W-textwidth.Width(msg.Place("")))), screen.Style{})
	}
	s.Put(in, 0, 2, "[", screen.Style{})
	s.Put(in, in.W-1, 2, "]", screen.Style{})
	field := screen.Region{X: in.X + 1, Y: in.Y + 2, W: in.W - 2, H: 1}
	fv := v.Edit.View(field.W)
	s.Put(field, 0, 0, v.Edit.Text()[fv.Start:fv.End], screen.Style{})
	switch {
	case v.Busy:
		s.Put(in, 0, 4, busy, f.th().dim)
	case v.Err != "":
		s.Put(in, 0, 4, "! "+v.Err, f.th().err)
	}
	s.Put(in, 0, 6, keys, f.th().bold)
	if !v.Busy && fr.focused {
		s.SetCursor(field.X+fv.CursorCol, field.Y, true)
	}
}

// drawExec は、実行ファイルを開く前の確認を領域 r に描く（filer §7）。
func (f *Filer) drawExec(s *screen.Screen, r screen.Region, view app.View, _ *frame) {
	drawBox(s, r, boxDouble, msg.ExecConfirm, f.th().bold)
	in := screen.Region{X: r.X + 2, Y: r.Y + 1, W: r.W - 4, H: 3}
	f.putName(s, screen.Region{X: in.X, Y: in.Y, W: in.W, H: 1}, textfmt.TruncName(view.(app.ExecView).Name, in.W), screen.Style{})
	s.Put(in, 0, 2, keymap.ExecGuide.Render(), screen.Style{})
}

// drawHelp は、キー操作の一覧を領域 r に描く。
func (f *Filer) drawHelp(s *screen.Screen, r screen.Region, _ app.View, _ *frame) {
	// キーの欄はキーの表から作る（操作ごとにキーを空白 1 つ、操作の間は空白 2 つ）。説明はキーの欄の最も広いものの 2 桁後から。
	keyW := 0
	for _, h := range keymap.HelpRows {
		keyW = max(keyW, textwidth.Width(h.Keys()))
	}
	drawBox(s, r, boxDouble, msg.HelpTitle, screen.Style{})
	in := screen.Region{X: r.X + 2, Y: r.Y + 1, W: r.W - 4, H: r.H - 2}
	for i, h := range keymap.HelpRows {
		s.Put(in, 0, i, h.Keys(), f.th().bold)
		s.Put(in, keyW+2, i, h.Label, screen.Style{})
	}
}

// drawColumns は、Yazi 風の表示（左に親フォルダ、中央に操作中のペイン、右にプレビュー。filer §5.3）を領域 r に描く。
// 列の幅の比は 1:4:3。列の間には縦の罫線を置く（隣の列の書記素クラスタと結合させない。§9.1）。
func (f *Filer) drawColumns(s *screen.Screen, r screen.Region) {
	a := f.app
	i := a.Active()
	p := a.Panes()[i]
	st := screen.Style{}
	drawBox(s, r, boxSingle, "", st)
	inner := screen.Region{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 2}
	ws := ratioWidths(inner.W-2, 1, 4, 3) // 列の間の罫線 2 本を除いた幅を、親フォルダ・ペイン・プレビューに 1:4:3 で分ける
	pw, cw := ws[0], ws[1]
	sep1 := inner.X + pw
	sep2 := sep1 + 1 + cw
	for _, x := range []int{sep1, sep2} {
		s.Put(r, x-r.X, 0, "┬", st)
		s.Put(r, x-r.X, r.H-1, "┴", st)
		for y := 1; y < r.H-1; y++ {
			s.Put(r, x-r.X, y, boxSingle.v, st)
		}
	}
	// 見出し（今のフォルダのパス）は中央の列の上に置く。
	ind := msg.PaneIndicator(i+1, len(a.Panes()))
	titleX := sep1 - r.X + 2
	titleW := r.W - titleX - len(ind) - 6
	s.Put(screen.Region{X: r.X + titleX, Y: r.Y, W: titleW + 2, H: 1}, 0, 0, " "+textfmt.TruncPath(p.Dir(), titleW)+" ",
		f.th().bold)
	s.Put(r, r.W-4-len(ind), 0, " "+ind+" ", st)
	drawFooter(s, r, sep1-r.X+1, p, st) // 中央の列（操作中のペイン）の下

	f.drawParentColumn(s, screen.Region{X: inner.X, Y: inner.Y, W: pw, H: inner.H}, p)
	top := p.Window(inner.H)
	for row := 0; row < inner.H && top+row < p.Len(); row++ {
		f.drawItem(s, screen.Region{X: sep1 + 1, Y: inner.Y + row, W: cw, H: 1}, p, top+row, true)
	}
	pv := a.Preview()
	f.drawPreview(s, screen.Region{X: sep2 + 1, Y: inner.Y, W: inner.X + inner.W - sep2 - 1, H: inner.H}, pv)
	if pv.Kind == app.PreviewText {
		s.Put(screen.Region{X: sep2 + 2, Y: r.Y + r.H - 1, W: inner.X + inner.W - sep2 - 3, H: 1}, 0, 0, " "+pv.Encoding+" ", st)
	}
}

// shown は、隠しファイルの規則（filer §6）で、項目 it を表示するかを返す。keep の名前の項目は、隠しファイルでも表示する。
func (f *Filer) shown(it listing.Item, keep string) bool {
	return f.app.ShowHidden() || !it.Hidden || keep != "" && it.Name == keep
}

// drawParentColumn は、親フォルダの一覧を描き、今のフォルダの行を反転する。今のフォルダが見えるように送る。
// 今のフォルダは、隠しフォルダでも表示する（どこにいるかを示すため）。描くたびに一覧を写さない（大きなフォルダでも描画を重くしない）。
func (f *Filer) drawParentColumn(s *screen.Screen, r screen.Region, p *app.Pane) {
	items, current, err, ok := p.Parent()
	if !ok {
		return
	}
	if err != nil {
		for row, l := range textfmt.Wrap(msg.Error(err), r.W-1) {
			s.Put(screen.Region{X: r.X + 1, Y: r.Y, W: r.W - 1, H: r.H}, 0, row, l, withAttr(f.th().err, screen.AttrDim))
		}
		return
	}
	at, n := -1, 0 // 今のフォルダの位置と、表示する項目の数
	for _, it := range items {
		if !f.shown(it, current) {
			continue
		}
		if at < 0 && it.Name == current {
			at = n
		}
		n++
	}
	top := 0
	if at >= r.H {
		top = min(at-r.H/2, n-r.H)
	}
	k, row := 0, 0
	for _, it := range items {
		if row >= r.H {
			break
		}
		if !f.shown(it, current) {
			continue
		}
		if k++; k-1 < top {
			continue
		}
		st := f.itemStyle(it, false)
		if k-1 == at {
			st.Attr |= f.th().cursor
		}
		line := screen.Region{X: r.X, Y: r.Y + row, W: r.W, H: 1}
		s.Fill(line, st)
		f.putName(s, screen.Region{X: r.X + 1, Y: line.Y, W: r.W - 1, H: 1}, textfmt.TruncName(it.Name, r.W-1), st)
		row++
	}
}

// drawPreview は、カーソル行の項目のプレビューを描く（filer §6）。テキストは折り返さずに列の幅で切る。
// 制御文字などは screen が置き換える（U6・T2）。
func (f *Filer) drawPreview(s *screen.Screen, r screen.Region, pv app.Preview) {
	in := screen.Region{X: r.X + 1, Y: r.Y, W: r.W - 1, H: r.H}
	row := 0
	put := func(text string, st screen.Style) { // 案内の文は列の幅で折り返す
		for _, l := range textfmt.Wrap(text, in.W) {
			s.Put(in, 0, row, l, st)
			row++
		}
	}
	dim := f.th().dim
	switch pv.Kind {
	case app.PreviewDir:
		for _, it := range pv.Items { // 見える行の分だけ描く（一覧を写さない）
			if row >= in.H {
				break
			}
			if f.shown(it, "") {
				f.putName(s, screen.Region{X: in.X, Y: in.Y + row, W: in.W, H: 1}, textfmt.TruncName(it.Name, in.W), f.itemStyle(it, false))
				row++
			}
		}
		if row == 0 {
			put(msg.PreviewEmpty, dim)
		}
	case app.PreviewText:
		for k := 0; k < in.H && k < len(pv.Lines); k++ {
			s.Put(in, 0, k, pv.Lines[k], screen.Style{})
		}
	case app.PreviewBinary:
		put(msg.PreviewBinary, dim)
		put(textfmt.Bytes(pv.Size)+" "+msg.UnitBytes, dim)
	case app.PreviewNotLocal:
		put(msg.PreviewNotLocal, dim)
		put(textfmt.Bytes(pv.Size)+" "+msg.UnitBytes, dim)
	case app.PreviewSpecial:
		put(msg.TypeSpecial, dim)
	case app.PreviewError:
		put(msg.Error(pv.Err), f.th().err)
	}
}

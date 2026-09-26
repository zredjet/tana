package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/keys"
	"github.com/zredjet/tana/internal/listing"
	"github.com/zredjet/tana/internal/msg"
	"github.com/zredjet/tana/internal/screen"
	"github.com/zredjet/tana/internal/textfmt"
	"github.com/zredjet/tana/internal/textwidth"
)

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
	redraw bool // 次に描くとき、画面全体を端末に書き直す（Ctrl+L）
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
	case KindSignal:
		return false
	}
	return !f.app.Quit()
}

// key は、キー入力を処理する。v は表示形式を切り替え（Filer が持つ）、ほかは app の操作に変える。
func (f *Filer) key(ev keys.Event) []app.Cmd {
	if ev.Kind == keys.KeyEvent && ev.Key == keys.KeyRune && ev.Mod == keys.ModCtrl && ev.Rune == 'l' {
		f.redraw = true // どの画面でも、端末に何かが残ったときの描き直し
		return nil
	}
	if f.app.Dialog() == app.DialogNone && ev.Kind == keys.KeyEvent && ev.Key == keys.KeyRune && ev.Mod == 0 && ev.Rune == 'v' {
		f.view = 1 - f.view
		return f.app.SetNeeds(f.needs())
	}
	if act, ok := f.action(ev); ok {
		return f.app.Do(act)
	}
	return nil
}

// action は、キー入力を app の操作に変える（filer §7。割り当ては仮）。対応しないキーは false。
func (f *Filer) action(ev keys.Event) (app.Action, bool) {
	act := func(k app.ActionKind) (app.Action, bool) { return app.Action{Kind: k}, true }
	switch f.app.Dialog() {
	case app.DialogHelp:
		if ev.Kind == keys.KeyEvent {
			return act(app.ActCancel)
		}
		return app.Action{}, false
	case app.DialogExec:
		// 確定は y だけ（Enter では確定しない）。貼り付けは受け付けない（filer U2）。
		switch {
		case ev.Kind != keys.KeyEvent:
		case ev.Key == keys.KeyRune && ev.Mod == 0 && ev.Rune == 'y':
			return act(app.ActYes)
		case ev.Key == keys.KeyRune && ev.Mod == 0 && ev.Rune == 'n':
			return act(app.ActNo)
		case ev.Key == keys.KeyEsc:
			return act(app.ActCancel)
		}
		return app.Action{}, false
	case app.DialogPath:
		return pathAction(ev)
	}
	if ev.Kind != keys.KeyEvent { // 貼り付けはコマンドとして解釈しない（tui §5）
		return app.Action{}, false
	}
	if ev.Key == keys.KeyRune {
		if ev.Mod == keys.ModCtrl && ev.Rune == 'r' {
			return act(app.ActReload)
		}
		if ev.Mod&^keys.ModShift != 0 {
			return app.Action{}, false
		}
		switch ev.Rune {
		case 'k':
			return act(app.ActUp)
		case 'j':
			return act(app.ActDown)
		case 'h':
			return act(app.ActParent)
		case 'l':
			return act(app.ActEnterDir)
		case ' ':
			return act(app.ActMark)
		case 'a':
			return act(app.ActMarkAll)
		case '.':
			return act(app.ActToggleHidden)
		case 'g':
			return act(app.ActGoPath)
		case '=':
			return act(app.ActSyncOther)
		case '?':
			return act(app.ActHelp)
		case 'q':
			return act(app.ActQuit)
		case 'c', 'm', 'd', 'D', 'r', 'n', 'L':
			return act(app.ActNotYet)
		}
		return app.Action{}, false
	}
	if ev.Mod != 0 {
		return app.Action{}, false
	}
	switch ev.Key {
	case keys.KeyUp:
		return act(app.ActUp)
	case keys.KeyDown:
		return act(app.ActDown)
	case keys.KeyPageUp:
		return act(app.ActPageUp)
	case keys.KeyPageDown:
		return act(app.ActPageDown)
	case keys.KeyHome:
		return act(app.ActHome)
	case keys.KeyEnd:
		return act(app.ActEnd)
	case keys.KeyEnter:
		return act(app.ActEnter)
	case keys.KeyBackspace:
		return act(app.ActParent)
	case keys.KeyTab:
		return act(app.ActNextPane)
	case keys.KeyLeft: // どちらの表示形式でも h と同じ（ペインの切り替えは Tab だけ。filer §7）
		return act(app.ActParent)
	case keys.KeyRight: // l と同じ
		return act(app.ActEnterDir)
	case keys.KeyEsc:
		return act(app.ActCancel)
	}
	return app.Action{}, false
}

// pathAction は、パスの入力欄のキーを操作に変える。貼り付けは最初の行だけを入れる（コマンドとして解釈しない。tui §5）。
// 貼り付けの改行は、端末によって LF・CRLF・CR（Terminal.app・iTerm2 は LF を CR にして送る）のどれでも届く。
func pathAction(ev keys.Event) (app.Action, bool) {
	act := func(k app.ActionKind) (app.Action, bool) { return app.Action{Kind: k}, true }
	switch ev.Kind {
	case keys.PasteEvent:
		line := ev.Text
		if i := strings.IndexAny(line, "\r\n"); i >= 0 {
			line = line[:i]
		}
		return app.Action{Kind: app.ActInsert, Text: line}, true
	case keys.KeyEvent:
	default:
		return app.Action{}, false
	}
	switch ev.Key {
	case keys.KeyRune:
		switch {
		case ev.Mod == keys.ModCtrl && ev.Rune == 'a':
			return act(app.ActLineHome)
		case ev.Mod == keys.ModCtrl && ev.Rune == 'e':
			return act(app.ActLineEnd)
		case ev.Mod&^keys.ModShift == 0:
			return app.Action{Kind: app.ActInsert, Text: string(ev.Rune)}, true
		}
	case keys.KeyBackspace:
		return act(app.ActBackspace)
	case keys.KeyDelete:
		return act(app.ActDelete)
	case keys.KeyLeft:
		return act(app.ActLeft)
	case keys.KeyRight:
		return act(app.ActRight)
	case keys.KeyHome:
		return act(app.ActLineHome)
	case keys.KeyEnd:
		return act(app.ActLineEnd)
	case keys.KeyEnter:
		return act(app.ActSubmit)
	case keys.KeyEsc:
		return act(app.ActCancel)
	}
	return app.Action{}, false
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

// 見た目（filer §9.5）。色だけで区別しないので、種類はサイズの欄、マークは * でも分かる。
var (
	styleDir      = screen.Style{FG: screen.ColorBlue, Attr: screen.AttrBold}
	styleJunction = screen.Style{FG: screen.ColorMagenta, Attr: screen.AttrBold}
	styleLink     = screen.Style{FG: screen.ColorCyan}
	styleSpecial  = screen.Style{FG: screen.ColorYellow}
	styleError    = screen.Style{FG: screen.ColorRed}
	styleMarked   = screen.Style{FG: screen.ColorYellow, Attr: screen.AttrBold}
	styleReplaced = screen.Style{FG: screen.ColorBrightRed} // 表示で置き換えた文字（filer §9.3）
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
	panes := a.Panes()
	paneH := rows - 3
	if f.view == viewColumns {
		f.drawColumns(s, screen.Region{W: cols, H: paneH})
	} else {
		for i, p := range panes {
			x0, x1 := cols*i/len(panes), cols*(i+1)/len(panes)
			f.drawPane(s, screen.Region{X: x0, Y: 0, W: x1 - x0, H: paneH}, p, i == a.Active())
		}
	}
	line := func(y int) screen.Region { return screen.Region{X: 0, Y: y, W: cols, H: 1} }
	f.drawStatus(s, line(rows-3))
	if text, isErr := a.Message(); text != "" {
		st := screen.Style{}
		if isErr {
			st = styleError
		}
		s.Put(line(rows-2), 1, 0, text, st)
	}
	s.Put(line(rows-1), 1, 0, msg.KeyGuide, screen.Style{Attr: screen.AttrDim})
	switch a.Dialog() {
	case app.DialogPath:
		f.drawPathInput(s)
	case app.DialogExec:
		f.drawExec(s)
	case app.DialogHelp:
		f.drawHelp(s)
	}
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
		b, st = boxDouble, screen.Style{Attr: screen.AttrBold}
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
		footer = msg.Loading
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
	if nameW = w - 1 - 1 - sizeW - 1 - dateW; nameW >= minNameW {
		return nameW, true
	}
	return max(w-1-1-sizeW, 1), false
}

// drawItem は、ペイン p の i 番目の項目を 1 行の領域 r に描く。
func (f *Filer) drawItem(s *screen.Screen, r screen.Region, p *app.Pane, i int, active bool) {
	it := p.Item(i)
	marked := !it.Parent && p.Marked(it.Name)
	st := itemStyle(it, marked)
	if i == p.Cursor() {
		if active {
			st.Attr |= screen.AttrReverse
		} else {
			st.Attr |= screen.AttrUnderline
		}
	}
	s.Fill(r, st)
	if marked {
		s.Put(r, 0, 0, "*", st)
	}
	nameW, date := columns(r.W)
	putName(s, screen.Region{X: r.X + 1, Y: r.Y, W: nameW, H: 1}, textfmt.TruncName(it.Name, nameW), st)
	x := 1 + nameW + 1
	s.Put(r, x, 0, fmt.Sprintf("%*s", sizeW, sizeText(it)), st)
	if date && !it.Parent && it.Err == nil {
		s.Put(r, x+sizeW+1, 0, textfmt.Time(it.Info.ModTime, f.app.Now()), st)
	}
}

// putName は、名前を置く。表示で置き換える書記素クラスタ（制御文字など）は色を変える（filer §9.3）。置き換えは screen が行う（T2）。
func putName(s *screen.Screen, r screen.Region, name string, st screen.Style) {
	x, start, replaced := 0, 0, false
	flush := func(end int) {
		if end > start {
			sty := st
			if replaced {
				sty.FG, sty.Attr = styleReplaced.FG, sty.Attr|screen.AttrBold
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
func itemStyle(it listing.Item, marked bool) screen.Style {
	var st screen.Style
	switch {
	case it.Err != nil:
		st = styleError
	case it.IsDir() && it.Info.Type != fsops.TypeJunction:
		st = styleDir
	case it.Info.Type == fsops.TypeJunction:
		st = styleJunction
	case it.Info.Type == fsops.TypeSymlink:
		st = styleLink
	case it.Info.Type == fsops.TypeSpecial:
		st = styleSpecial
	}
	if marked {
		st = styleMarked
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
		st = styleReplaced
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

// drawPathInput は、パスの入力欄を描き、本物のカーソルを入力の位置に置く（IME の変換中の文字はここに出る。filer VU4）。
func (f *Filer) drawPathInput(s *screen.Screen) {
	r := dialogRegion(s, 76, 3)
	drawBox(s, r, boxDouble, msg.PathInputTitle, screen.Style{})
	field := screen.Region{X: r.X + 2, Y: r.Y + 1, W: r.W - 4, H: 1}
	e := f.app.PathEditor()
	v := e.View(field.W)
	s.Put(field, 0, 0, e.Text()[v.Start:v.End], screen.Style{})
	s.SetCursor(field.X+v.CursorCol, field.Y, true)
}

// drawExec は、実行ファイルを開く前の確認を描く（filer §7）。
func (f *Filer) drawExec(s *screen.Screen) {
	r := dialogRegion(s, 60, 5)
	drawBox(s, r, boxDouble, msg.ExecConfirm, screen.Style{Attr: screen.AttrBold})
	in := screen.Region{X: r.X + 2, Y: r.Y + 1, W: r.W - 4, H: 3}
	putName(s, screen.Region{X: in.X, Y: in.Y, W: in.W, H: 1}, textfmt.TruncName(f.app.ExecName(), in.W), screen.Style{})
	s.Put(in, 0, 2, msg.ExecChoices, screen.Style{})
}

// drawHelp は、キー操作の一覧を描く。
func (f *Filer) drawHelp(s *screen.Screen) {
	r := dialogRegion(s, 72, len(msg.Help)+2)
	drawBox(s, r, boxDouble, msg.HelpTitle, screen.Style{})
	in := screen.Region{X: r.X + 2, Y: r.Y + 1, W: r.W - 4, H: r.H - 2}
	for i, h := range msg.Help {
		s.Put(in, 0, i, h[0], screen.Style{Attr: screen.AttrBold})
		s.Put(in, 20, i, h[1], screen.Style{})
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
	total := inner.W - 2
	pw, cw := total/8, total*4/8
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
		screen.Style{Attr: screen.AttrBold})
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

// visible は、隠しファイルを表示しないときに隠しファイルを除いた項目を返す（filer §6）。
func (f *Filer) visible(items []listing.Item) []listing.Item {
	if f.app.ShowHidden() {
		return items
	}
	out := make([]listing.Item, 0, len(items))
	for _, it := range items {
		if !it.Hidden {
			out = append(out, it)
		}
	}
	return out
}

// drawParentColumn は、親フォルダの一覧を描き、今のフォルダの行を反転する。今のフォルダが見えるように送る。
func (f *Filer) drawParentColumn(s *screen.Screen, r screen.Region, p *app.Pane) {
	items, current, ok := p.Parent()
	if !ok {
		return
	}
	items = f.visible(items)
	at := -1
	for k, it := range items {
		if it.Name == current {
			at = k
			break
		}
	}
	top := 0
	if at >= r.H {
		top = min(at-r.H/2, len(items)-r.H)
	}
	for row := 0; row < r.H && top+row < len(items); row++ {
		it := items[top+row]
		st := itemStyle(it, false)
		if top+row == at {
			st.Attr |= screen.AttrReverse
		}
		line := screen.Region{X: r.X, Y: r.Y + row, W: r.W, H: 1}
		s.Fill(line, st)
		putName(s, screen.Region{X: r.X + 1, Y: line.Y, W: r.W - 1, H: 1}, textfmt.TruncName(it.Name, r.W-1), st)
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
	dim := screen.Style{Attr: screen.AttrDim}
	switch pv.Kind {
	case app.PreviewDir:
		items := f.visible(pv.Items)
		if len(items) == 0 {
			put(msg.PreviewEmpty, dim)
		}
		for row := 0; row < in.H && row < len(items); row++ {
			it := items[row]
			putName(s, screen.Region{X: in.X, Y: in.Y + row, W: in.W, H: 1}, textfmt.TruncName(it.Name, in.W), itemStyle(it, false))
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
		put(msg.Error(pv.Err), styleError)
	}
}

package tui

import (
	"strconv"
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

// ファイル操作の画面（確認・衝突の決定・進捗・結果。filer §8.2〜§8.5）のキーと描画。

// opAction は、ファイル操作の画面のキーを app の操作に変える。貼り付けはコマンドとして解釈しない（tui §5。filer U2）。
func opAction(s app.Screen, ev keys.Event) (app.Action, bool) {
	act := func(k app.ActionKind) (app.Action, bool) { return app.Action{Kind: k}, true }
	if ev.Kind != keys.KeyEvent {
		return app.Action{}, false
	}
	nav := func() (app.Action, bool) {
		switch {
		case ev.Key == keys.KeyUp || ev.Key == keys.KeyRune && ev.Mod == 0 && ev.Rune == 'k':
			return act(app.ActUp)
		case ev.Key == keys.KeyDown || ev.Key == keys.KeyRune && ev.Mod == 0 && ev.Rune == 'j':
			return act(app.ActDown)
		case ev.Key == keys.KeyPageUp:
			return act(app.ActPageUp)
		case ev.Key == keys.KeyPageDown:
			return act(app.ActPageDown)
		case ev.Key == keys.KeyHome:
			return act(app.ActHome)
		case ev.Key == keys.KeyEnd:
			return act(app.ActEnd)
		}
		return app.Action{}, false
	}
	switch {
	case ev.Key == keys.KeyEnter && ev.Mod == 0 && s != app.ScreenProgress:
		return act(app.ActSubmit)
	case ev.Key == keys.KeyEsc:
		return act(app.ActCancel)
	case ev.Key == keys.KeyRune && ev.Mod == keys.ModCtrl && ev.Rune == 'c' && s == app.ScreenProgress:
		return act(app.ActCancel) // 実行中の Ctrl+C は Esc と同じ（filer §7）
	}
	if a, ok := nav(); ok && (s == app.ScreenConflicts || s == app.ScreenResult) {
		return a, true
	}
	if ev.Key != keys.KeyRune || ev.Mod&^keys.ModShift != 0 {
		return app.Action{}, false
	}
	decide := func(k app.ActionKind, d fsops.Decision) (app.Action, bool) {
		return app.Action{Kind: k, Decision: d}, true
	}
	switch s {
	case app.ScreenConflicts:
		switch ev.Rune {
		case 's':
			return decide(app.ActDecide, fsops.DecisionSkip)
		case 'o':
			return decide(app.ActDecide, fsops.DecisionOverwrite)
		case 'r':
			return decide(app.ActDecide, fsops.DecisionAutoRename)
		case 'm':
			return decide(app.ActDecide, fsops.DecisionMerge)
		case 'S':
			return decide(app.ActDecideAll, fsops.DecisionSkip)
		case 'O':
			return decide(app.ActDecideAll, fsops.DecisionOverwrite)
		case 'R':
			return decide(app.ActDecideAll, fsops.DecisionAutoRename)
		case 'M':
			return decide(app.ActDecideAll, fsops.DecisionMerge)
		case 'N':
			return act(app.ActNewerOnly)
		case ' ':
			return act(app.ActToggle)
		case 'u':
			return act(app.ActUnsetOnly)
		}
	case app.ScreenProgress:
		switch ev.Rune {
		case 'y':
			return act(app.ActYes)
		case 'n':
			return act(app.ActNo)
		case 'Q':
			return act(app.ActForceQuit)
		}
	case app.ScreenResult:
		switch ev.Rune {
		case ' ':
			return act(app.ActToggle)
		case 'e':
			return act(app.ActEnglish)
		}
	}
	return app.Action{}, false
}

var (
	styleDim     = screen.Style{Attr: screen.AttrDim}
	styleBold    = screen.Style{Attr: screen.AttrBold}
	styleWarn    = screen.Style{FG: screen.ColorYellow, Attr: screen.AttrBold}
	styleProblem = screen.Style{FG: screen.ColorRed, Attr: screen.AttrBold}
)

// drawOp は、ファイル操作の画面を描く（一覧の上に重ねる）。
func (f *Filer) drawOp(s *screen.Screen) {
	switch f.app.Screen() {
	case app.ScreenConfirm:
		f.drawConfirm(s)
	case app.ScreenConflicts:
		f.drawConflicts(s)
	case app.ScreenProgress:
		f.drawProgress(s)
	case app.ScreenResult:
		f.drawResult(s)
	}
}

// line は、ダイアログの中の 1 行（文字と見た目）。
type line struct {
	text string
	st   screen.Style
}

// drawLines は、題名 title のダイアログに行を並べて、画面の中央に描く。高さは行の数に合わせる（画面に収める）。
func drawLines(s *screen.Screen, width int, title string, lines []line) {
	_, rows := s.Size()
	h := min(len(lines)+2, rows-2)
	r := dialogRegion(s, width, h)
	drawBox(s, r, boxDouble, title, styleBold)
	in := screen.Region{X: r.X + 2, Y: r.Y + 1, W: r.W - 4, H: r.H - 2}
	for i, l := range lines {
		s.Put(in, 0, i, l.text, l.st)
	}
}

// drawConfirm は、確認画面を描く（filer §8.2）。実行されない項目・衝突・警告は ! を付けて示す。
func (f *Filer) drawConfirm(s *screen.Screen) {
	cols, _ := s.Size()
	v := f.app.Confirm()
	w := min(cols-4, 68)
	in := w - 4
	lines := []line{{}, {text: msg.ConfirmSummary(v.Op, v.Count, textfmt.TruncPath(v.Dest, in-20))}}
	size := ""
	if v.Bytes > 0 {
		size = textfmt.Size(v.Bytes)
	}
	lines = append(lines, line{text: msg.Totals(v.Files, size)}, line{})
	if n := len(v.NotRunnable); n > 0 {
		lines = append(lines, line{text: "! " + msg.NotRunnable(n), st: styleWarn})
		for i, it := range v.NotRunnable {
			if i == 3 {
				lines = append(lines, line{text: "    " + msg.Count(n-3), st: styleDim})
				break
			}
			lines = append(lines, line{text: "    " + textfmt.TruncName(it.Name, 20) + "  " + it.Reason})
		}
	}
	if v.Conflicts > 0 {
		lines = append(lines, line{text: "! " + msg.Conflicts(v.Conflicts, v.TopLvl), st: styleWarn})
	}
	for _, w := range v.Warnings {
		lines = append(lines, line{text: "! " + w, st: styleWarn})
	}
	keys := msg.ConfirmKeys
	switch {
	case v.Runnable == 0:
		lines = append(lines, line{text: msg.NothingRunnable, st: styleProblem})
		keys = msg.ConfirmKeysNone
	case v.Conflicts > 0:
		keys = msg.ConfirmKeysConflict
	}
	lines = append(lines, line{}, line{text: keys, st: styleBold})
	drawLines(s, w, msg.Op(v.Op), lines)
}

// 衝突の一覧の欄の幅（filer §8.3）。コピー元・コピー先は「サイズ 5 桁、空白、日時 11 桁、空白、新 2 桁」。
const (
	infoW     = 5 + 1 + 11 + 1 + 2
	decisionW = 12 // 自動リネーム
)

// drawConflicts は、衝突の決定の画面を描く（画面全体。filer §8.3）。
func (f *Filer) drawConflicts(s *screen.Screen) {
	cols, rows := s.Size()
	s.Fill(screen.Region{W: cols, H: rows}, screen.Style{})
	v := f.app.Conflicts()
	full := screen.Region{W: cols, H: rows}
	from, to := fitPaths(cols, msg.ConflictHeader(v.Op, "", ""), v.From, v.To)
	s.Put(full, 1, 0, msg.ConflictHeader(v.Op, from, to), styleBold)
	x := 1 + s.Put(full, 1, 1, msg.Conflicts(v.All, v.Top)+"  ", screen.Style{})
	st := screen.Style{}
	if v.Unset > 0 {
		st = styleWarn
	}
	s.Put(full, x, 1, msg.Unset(v.Unset), st) // 未選択はスキップになることを、常に出しておく（U1）
	header := 2
	if len(v.Warnings) > 0 {
		s.Put(full, 1, 2, "! "+strings.Join(v.Warnings, "  "), styleWarn)
		header = 3
	}
	rule := strings.Repeat(boxSingle.h, cols)
	s.Put(full, 0, header, rule, styleDim)
	nameW := cols - 2 - infoW - 1 - infoW - 1 - decisionW
	colX := [4]int{2, 2 + nameW + 1, 2 + nameW + 1 + infoW + 1, 2 + nameW + 1 + 2*(infoW+1)}
	for i, h := range msg.ConflictColumns {
		s.Put(full, colX[i], header+1, h, styleDim)
	}
	listY := header + 2
	listH := rows - listY - 4
	f.app.SetConflictRows(listH)
	top := max(min(v.Cursor-listH/2, len(v.Rows)-listH), 0)
	now := f.app.Now()
	var cur app.ConflictRow
	for i := 0; i < listH && top+i < len(v.Rows); i++ {
		r := v.Rows[top+i]
		y := listY + i
		rowSt := screen.Style{}
		if top+i == v.Cursor {
			cur = r
			rowSt.Attr |= screen.AttrReverse
			s.Fill(screen.Region{X: 0, Y: y, W: cols, H: 1}, rowSt)
			s.Put(full, 0, y, ">", rowSt)
		}
		indent := strings.Repeat("  ", r.Depth)
		nameR := screen.Region{X: colX[0], Y: y, W: nameW, H: 1}
		if r.ID == 0 { // 件数だけの行は、欄に分けずに行の幅を使う
			s.Put(screen.Region{X: colX[0], Y: y, W: cols - colX[0], H: 1}, 0, 0, indent+msg.Inner(r.Inner, r.InnerHow), withAttr(rowSt, screen.AttrDim))
			continue
		}
		name := r.Name
		if r.Dir {
			name += "/"
		}
		putName(s, nameR, textfmt.TruncName(indent+name, nameW), rowSt)
		s.Put(full, colX[1], y, infoText(r.Src, r.SrcNewer, now), rowSt)
		s.Put(full, colX[2], y, infoText(r.Dst, r.DstNewer, now), rowSt)
		dst := rowSt
		if r.Decision == fsops.DecisionUnset {
			dst = withAttr(dst, screen.AttrDim) // 未選択は暗く（filer §8.3）
		}
		s.Put(full, colX[3], y, msg.Decision(r.Decision), dst)
	}
	s.Put(full, 0, rows-4, rule, styleDim)
	// この行のキー: その行で使えない決定は暗くする（filer §8.3）。
	lx := 1 + s.Put(full, 1, rows-3, "この行: ", screen.Style{})
	for _, k := range []struct {
		d    fsops.Decision
		text string
	}{{fsops.DecisionSkip, "s スキップ"}, {fsops.DecisionOverwrite, "o 上書き"}, {fsops.DecisionAutoRename, "r 自動リネーム"}, {fsops.DecisionMerge, "m マージ"}} {
		st := screen.Style{}
		if cur.ID == 0 || !cur.Allowed[k.d] {
			st = styleDim
		}
		lx += s.Put(full, lx, rows-3, k.text, st) + 2
	}
	s.Put(full, 1, rows-2, msg.ConflictKeys[1], screen.Style{})
	s.Put(full, 1, rows-1, msg.ConflictKeys[2], screen.Style{})
	if text, isErr := f.app.Message(); text != "" { // 使えない決定の理由などは、キーの案内の上に重ねて出す
		st := screen.Style{}
		if isErr {
			st = styleError
		}
		s.Fill(screen.Region{X: 0, Y: rows - 4, W: cols, H: 1}, screen.Style{})
		s.Put(full, 1, rows-4, text, st)
	}
}

// fitPaths は、見出し frame（2 つのパスを空にしたもの）に、パス from・to を収める（1 桁目から描く）。
// 収まらなければ、to に半分まで（短ければその幅）を、from に残りを割り当てて、先頭側を切り詰める（filer §9.2）。
func fitPaths(cols int, frame, from, to string) (string, string) {
	avail := max(cols-2-textwidth.Width(frame), 16)
	if textwidth.Width(from)+textwidth.Width(to) <= avail {
		return from, to
	}
	tw := min(textwidth.Width(to), avail/2)
	return textfmt.TruncPath(from, avail-tw), textfmt.TruncPath(to, tw)
}

// infoText は、衝突の一覧のコピー元・コピー先の欄（サイズか種類、更新日時、新しい方の印）。種類は一覧と同じ表記（<DIR> など）。
func infoText(info fsops.EntryInfo, newer bool, now time.Time) string {
	size := sizeText(listing.Item{Info: info})
	mark := ""
	if newer {
		mark = " " + msg.NewerMark
	}
	return padRight(size, sizeW) + " " + textfmt.Time(info.ModTime, now) + mark
}

// padRight は、s を表示幅 w の右寄せにする（s が広ければそのまま）。
func padRight(s string, w int) string {
	if n := textwidth.Width(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

// drawProgress は、進捗の画面を描く（filer §8.4）。中止の確認は、その上に重ねる。
func (f *Filer) drawProgress(s *screen.Screen) {
	cols, _ := s.Size()
	p := f.app.Progress()
	w := min(cols-4, 64)
	in := w - 4
	percent := 0
	switch {
	case p.TotalBytes > 0:
		percent = int(p.DoneBytes * 100 / p.TotalBytes)
	case p.TotalFiles > 0:
		percent = p.DoneFiles * 100 / p.TotalFiles
	}
	barW := in - 8
	filled := barW * percent / 100
	bar := "[" + strings.Repeat("#", filled) + strings.Repeat("-", barW-filled) + "]  " + strconv.Itoa(percent) + "%"
	totalSize, doneSize := "", ""
	if p.TotalBytes > 0 {
		totalSize, doneSize = textfmt.Size(p.TotalBytes), textfmt.Size(p.DoneBytes)
	}
	speed, remaining := "", ""
	if p.Speed > 0 {
		speed = textfmt.Size(int64(p.Speed))
	}
	if p.Remaining >= 0 {
		remaining = msg.Duration(int(p.Remaining.Seconds() + 0.5))
	}
	lines := []line{{},
		{text: textfmt.TruncPath(p.Current, in)},
		{text: bar},
		{text: msg.ProgressFiles(p.DoneFiles, p.TotalFiles, doneSize, totalSize)},
		{text: msg.ProgressTimes(speed, remaining, msg.Duration(int(p.Elapsed.Seconds())))},
		{}}
	switch {
	case p.Unresponsive:
		lines = append(lines, line{text: msg.Unresponsive, st: styleProblem})
		for _, l := range msg.UnresponsiveLeftovers {
			lines = append(lines, line{text: l})
		}
		lines = append(lines, line{text: msg.ForceQuitKey, st: styleBold})
	case p.Canceling:
		lines = append(lines, line{text: msg.Canceling, st: styleWarn})
	default:
		lines = append(lines, line{text: msg.ProgressKeys, st: styleBold})
	}
	drawLines(s, w, msg.Stage(p.Stage), lines)
	if p.AskCancel {
		drawLines(s, 52, msg.CancelTitle, []line{{}, {text: msg.CancelQuestion, st: styleBold}, {text: msg.CancelNoPartial},
			{text: msg.CancelDoneKept}, {}, {text: msg.CancelChoices, st: styleBold}})
	}
}

// drawResult は、結果の画面を描く（画面全体。filer §8.5）。
func (f *Filer) drawResult(s *screen.Screen) {
	cols, rows := s.Size()
	s.Fill(screen.Region{W: cols, H: rows}, screen.Style{})
	v := f.app.Result()
	full := screen.Region{W: cols, H: rows}
	from, to := fitPaths(cols, msg.ResultTitle(v.Op, msg.Status(v.Status), "", ""), v.From, v.To)
	titleSt := styleBold
	if v.Status != fsops.StatusCompleted {
		titleSt = styleProblem
	}
	s.Put(full, 1, 0, msg.ResultTitle(v.Op, msg.Status(v.Status), from, to), titleSt)
	var counts []string
	for _, c := range v.Counts {
		counts = append(counts, msg.OutcomeCount(c.Outcome, c.N))
	}
	s.Put(full, 1, 1, strings.Join(counts, "   "), screen.Style{})
	rule := strings.Repeat(boxSingle.h, cols)
	s.Put(full, 0, 2, rule, styleDim)
	// 結果の欄の幅は、出す結果の文言の幅に合わせる（「ごみ箱に入ったか確かめられない」は長い）。
	outW := 0
	for _, r := range v.Rows {
		if !r.Detail {
			outW = max(outW, textwidth.Width(msg.Outcome(r.Outcome)))
		}
	}
	nameW := min(24, cols/4)
	// 英語の詳細（e）は、カーソル行のものを画面の下に折り返して出す（行の中に出すと、深い階層のパスで Kind が見えなくなる）。
	var english []string
	if v.English && v.Cursor < len(v.Rows) {
		english = englishLines(v.Rows[v.Cursor].English, cols-2, max(rows/3, 3))
	}
	paneH := 0
	if len(english) > 0 {
		paneH = len(english) + 2 // 区切りと題名
	}
	listY, listH := 3, rows-5-paneH
	f.app.SetResultRows(listH)
	top := max(min(v.Cursor-listH/2, len(v.Rows)-listH), 0)
	for i := 0; i < listH && top+i < len(v.Rows); i++ {
		r := v.Rows[top+i]
		y := listY + i
		st := screen.Style{}
		if top+i == v.Cursor {
			st.Attr |= screen.AttrReverse
			s.Fill(screen.Region{X: 0, Y: y, W: cols, H: 1}, st)
			s.Put(full, 0, y, ">", st)
		}
		x := 2
		if !r.Detail {
			ost := st
			if r.Outcome != fsops.OutcomeDone && r.Outcome != fsops.OutcomeSkipped {
				ost.FG, ost.Attr = screen.ColorRed, ost.Attr|screen.AttrBold
			}
			s.Put(full, x, y, msg.Outcome(r.Outcome), ost)
		}
		x += outW + 1
		indent := ""
		if r.Detail {
			indent = boxSingle.bl + " " // フォルダの中の結果（上の項目の中）
		}
		putName(s, screen.Region{X: x, Y: y, W: nameW, H: 1}, textfmt.TruncName(indent+r.Name, nameW), st)
		s.Put(screen.Region{X: x + nameW + 1, Y: y, W: cols - x - nameW - 2, H: 1}, 0, 0, r.Reason, st)
	}
	if paneH > 0 {
		py := rows - 2 - paneH
		s.Put(full, 0, py, rule, styleDim)
		s.Put(full, 1, py+1, msg.EnglishTitle, styleBold)
		for i, l := range english {
			s.Put(full, 1, py+2+i, l, screen.Style{})
		}
	}
	s.Put(full, 0, rows-2, rule, styleDim)
	// Space で何が起きるかは、カーソル行に合わせて書く（中の結果がある行だけ）。
	keysText := msg.ResultKeys
	if v.Cursor < len(v.Rows) {
		if r := v.Rows[v.Cursor]; r.Expandable {
			keysText = msg.InsideKey(r.Details, r.Expanded) + "   " + keysText
		}
	}
	s.Put(full, 1, rows-1, keysText, screen.Style{})
}

// withAttr は、見た目 st に属性 a を加える。
func withAttr(st screen.Style, a screen.Attr) screen.Style {
	st.Attr |= a
	return st
}

// englishLines は、英語の詳細を幅 w で折り返し、最大 n 行にする。入らなければ、先頭と末尾を残して間を ... にする
// （先頭に操作とパス、末尾に Kind と OS のエラーがある）。詳細がなければ、そのことを書く。
func englishLines(details []string, w, n int) []string {
	if len(details) == 0 {
		return []string{msg.NoEnglish}
	}
	var lines []string
	for _, d := range details {
		lines = append(lines, textfmt.Wrap(d, w)...)
	}
	if len(lines) <= n {
		return lines
	}
	head := (n - 1) / 2
	return append(append(lines[:head:head], "..."), lines[len(lines)-(n-1-head):]...)
}

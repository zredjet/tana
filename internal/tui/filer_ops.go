package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/keymap"
	"github.com/zredjet/tana/internal/listing"
	"github.com/zredjet/tana/internal/msg"
	"github.com/zredjet/tana/internal/screen"
	"github.com/zredjet/tana/internal/textfmt"
	"github.com/zredjet/tana/internal/textwidth"
)

// ファイル操作の画面（確認・衝突の決定・進捗・結果・完全削除の確認。filer §8.2〜§8.6）のキーと描画。

// line は、ダイアログの中の 1 行（文字と見た目）。
type line struct {
	text string
	st   screen.Style
}

// drawLinesIn は、領域 r に、題名 title の枠を描き、行を並べる（行を並べるダイアログ。領域は配置が決める）。
func (f *Filer) drawLinesIn(s *screen.Screen, r screen.Region, title string, lines []line) {
	drawBox(s, r, boxDouble, title, f.th().bold)
	in := screen.Region{X: r.X + 2, Y: r.Y + 1, W: r.W - 4, H: r.H - 2}
	for i, l := range lines {
		s.Put(in, 0, i, l.text, l.st)
	}
}

// confirmLines は、確認画面の題名・幅・行（filer §8.2）。実行されない項目・衝突・警告は ! を付けて示す。
func (f *Filer) confirmLines(s *screen.Screen, view app.View) (string, int, []line) {
	cols, _ := s.Size()
	v := view.(app.ConfirmView)
	w := min(cols-4, 68)
	in := w - 4
	lines := []line{{}, {text: msg.ConfirmSummary(v.Op, v.Count, textfmt.TruncPath(v.Dest, in-20))}}
	if v.Op != fsops.OpTrash { // ごみ箱では fsops が中身を数えないので、合計は出さない（filer §8.2）
		size := ""
		if v.Bytes > 0 {
			size = sizeUnit(v.Bytes)
		}
		lines = append(lines, line{text: msg.Totals(v.Files, size)})
	}
	lines = append(lines, line{})
	lines = append(lines, f.notRunnableLines(v.NotRunnable)...)
	if v.Conflicts > 0 {
		lines = append(lines, line{text: "! " + msg.Conflicts(v.Conflicts, v.TopLvl), st: f.th().warn})
	}
	for _, w := range v.Warnings {
		lines = append(lines, line{text: "! " + w, st: f.th().warn})
	}
	keys := keymap.ConfirmGuide
	switch {
	case v.Runnable == 0:
		lines = append(lines, line{text: msg.NothingRunnable, st: f.th().problem})
		keys = keymap.ConfirmNoneGuide
		if v.Untrashable > 0 {
			keys = keymap.ConfirmPurgeGuide // ごみ箱に入らない項目は、利用者が選べば完全削除の確認へ（フェーズ20で決めた）
		}
	case v.Conflicts > 0:
		keys = keymap.ConfirmConflictsGuide
	}
	lines = append(lines, line{}, line{text: keys.Render(), st: f.th().bold})
	return msg.Op(v.Op), w, lines
}

// notRunnableLines は、実行されない項目の行（「! n 項目は実行しません」と、先頭の 3 件とその理由、残りの件数）。項目がなければ空。
func (f *Filer) notRunnableLines(notes []app.ItemNote) []line {
	n := len(notes)
	if n == 0 {
		return nil
	}
	lines := []line{{text: "! " + msg.NotRunnable(n), st: f.th().warn}}
	for i, it := range notes {
		if i == 3 {
			return append(lines, line{text: "    " + msg.Count(n-3), st: f.th().dim})
		}
		lines = append(lines, line{text: "    " + textfmt.TruncName(it.Name, 20) + "  " + it.Reason})
	}
	return lines
}

// 衝突の一覧の欄の幅（filer §8.3）。コピー元・コピー先は「サイズ 5 桁、空白、日時 11 桁、空白、新 2 桁」。
const (
	infoW     = 5 + 1 + 11 + 1 + 2
	decisionW = 12 // 自動リネーム
)

// drawConflicts は、衝突の決定の画面を、画面全体の領域 r に描く（filer §8.3）。
func (f *Filer) drawConflicts(s *screen.Screen, r screen.Region, view app.View, fr *frame) {
	cols, rows := r.W, r.H
	s.Fill(r, screen.Style{})
	v := view.(app.ConflictsView)
	full := r
	from, to := fitPaths(cols, msg.ConflictHeader(v.Op, "", ""), v.From, v.To)
	s.Put(full, 1, 0, msg.ConflictHeader(v.Op, from, to), f.th().bold)
	x := 1 + s.Put(full, 1, 1, msg.Conflicts(v.All, v.Top)+"  ", screen.Style{})
	st := screen.Style{}
	if v.Unset > 0 {
		st = f.th().warn
	}
	s.Put(full, x, 1, msg.Unset(v.Unset), st) // 未選択はスキップになることを、常に出しておく（U1）
	header := 2
	if len(v.Warnings) > 0 {
		s.Put(full, 1, 2, "! "+strings.Join(v.Warnings, "  "), f.th().warn)
		header = 3
	}
	rule := strings.Repeat(boxSingle.h, cols)
	s.Put(full, 0, header, rule, f.th().dim)
	nameW := cols - 2 - (infoW + 1) - (infoW + 1) - 1 - decisionW // 左端の 2 桁と、欄の間の 3 つの空白
	colX := [4]int{2, 2 + nameW + 1, 2 + nameW + 1 + infoW + 1, 2 + nameW + 1 + 2*(infoW+1)}
	for i, h := range msg.ConflictColumns {
		s.Put(full, colX[i], header+1, h, f.th().dim)
	}
	listY := header + 2
	listH := rows - listY - 4
	fr.rows = listH
	top := max(min(v.Cursor-listH/2, len(v.Rows)-listH), 0)
	now := f.app.Now()
	var cur app.ConflictRow
	for i := 0; i < listH && top+i < len(v.Rows); i++ {
		r := v.Rows[top+i]
		y := listY + i
		rowSt := screen.Style{}
		if top+i == v.Cursor {
			cur = r
			rowSt.Attr |= f.th().cursor
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
		f.putName(s, nameR, textfmt.TruncName(indent+name, nameW), rowSt)
		s.Put(full, colX[1], y, infoText(r.Src, r.SrcNewer, now), rowSt)
		s.Put(full, colX[2], y, infoText(r.Dst, r.DstNewer, now), rowSt)
		dst := rowSt
		if r.Decision == fsops.DecisionUnset {
			dst = withAttr(dst, screen.AttrDim) // 未選択は暗く（filer §8.3）
		}
		s.Put(full, colX[3], y, msg.Decision(r.Decision), dst)
	}
	s.Put(full, 0, rows-4, rule, f.th().dim)
	// この行のキー: その行で使えない決定は暗くする（filer §8.3）。
	g := keymap.ConflictRowGuide
	lx := 1 + s.Put(full, 1, rows-3, g.Prefix, screen.Style{})
	for _, it := range g.Items {
		c, _ := keymap.CommandByID(it.IDs[0])
		st := screen.Style{}
		if cur.ID == 0 || !cur.Allowed[c.Action.Decision] {
			st = f.th().dim
		}
		lx += s.Put(full, lx, rows-3, it.Text(), st) + textwidth.Width(g.Sep)
	}
	s.Put(full, 1, rows-2, keymap.ConflictAllGuide.Render(), screen.Style{})
	s.Put(full, 1, rows-1, keymap.ConflictKeysGuide.Render(), screen.Style{})
	if text, isErr := f.app.Message(); text != "" { // 使えない決定の理由などは、キーの案内の上に重ねて出す
		st := screen.Style{}
		if isErr {
			st = f.th().err
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

// progressLines は、進捗の画面の題名・幅・行（filer §8.4）。中止の確認は、別の部品としてその上に重ねる。
func (f *Filer) progressLines(s *screen.Screen, view app.View) (string, int, []line) {
	cols, _ := s.Size()
	p := view.(app.ProgressView)
	w := min(cols-4, 64)
	in := w - 4
	percent := 0
	switch {
	case p.TotalBytes > 0:
		percent = int(p.DoneBytes * 100 / p.TotalBytes)
	case p.TotalFiles > 0:
		percent = p.DoneFiles * 100 / p.TotalFiles
	}
	percent = max(min(percent, 100), 0) // 計画の後にファイルが大きくなると、済んだ量が合計を超える
	barW := in - 8
	filled := barW * percent / 100
	bar := "[" + strings.Repeat("#", filled) + strings.Repeat("-", barW-filled) + "]  " + strconv.Itoa(percent) + "%"
	totalSize, doneSize := "", ""
	if p.TotalBytes > 0 {
		totalSize, doneSize = sizeUnit(p.TotalBytes), sizeUnit(p.DoneBytes)
	}
	speed, remaining := "", ""
	if p.Speed > 0 {
		speed = sizeUnit(int64(p.Speed))
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
	if p.TrashDialog { // Windows のごみ箱の確認ダイアログ（filer §8.4）
		for _, l := range textfmt.Wrap(msg.TrashDialog, in) {
			lines = append(lines, line{text: l, st: f.th().warn})
		}
		lines = append(lines, line{})
	}
	switch {
	case p.Unresponsive:
		for _, w := range textfmt.Wrap(msg.Unresponsive(keymap.KeyName(app.RoleProgress, "force-quit")), in) {
			lines = append(lines, line{text: w, st: f.th().problem})
		}
		for _, l := range msg.Leftovers(p.Op) {
			for _, w := range textfmt.Wrap(l, in) { // 長い行は、ダイアログの幅で折り返す
				lines = append(lines, line{text: w})
			}
		}
		lines = append(lines, line{text: keymap.ForceQuitGuide.Render(), st: f.th().bold})
	case p.Canceling:
		lines = append(lines, line{text: msg.Canceling, st: f.th().warn})
	default:
		lines = append(lines, line{text: keymap.ProgressGuide.Render(), st: f.th().bold})
	}
	return msg.Stage(p.Stage), w, lines
}

// cancelAskLines は、中止の確認の題名・幅・行（filer §8.4）。
func (f *Filer) cancelAskLines(_ *screen.Screen, view app.View) (string, int, []line) {
	v := view.(app.CancelAskView)
	lines := []line{{}, {text: msg.CancelQuestion, st: f.th().bold}}
	for _, l := range msg.CancelNotes(v.Op) {
		lines = append(lines, line{text: l})
	}
	return msg.CancelTitle, 52, append(lines, line{}, line{text: keymap.CancelAskGuide.Render(), st: f.th().bold})
}

// drawResult は、結果の画面を、画面全体の領域 r に描く（filer §8.5）。
func (f *Filer) drawResult(s *screen.Screen, r screen.Region, view app.View, fr *frame) {
	cols, rows := r.W, r.H
	s.Fill(r, screen.Style{})
	v := view.(app.ResultView)
	full := r
	from, to := fitPaths(cols, msg.ResultTitle(v.Op, msg.Status(v.Status), "", ""), v.From, v.To)
	titleSt := f.th().bold
	if v.Status != fsops.StatusCompleted {
		titleSt = f.th().problem
	}
	s.Put(full, 1, 0, msg.ResultTitle(v.Op, msg.Status(v.Status), from, to), titleSt)
	var counts []string
	for _, c := range v.Counts {
		counts = append(counts, msg.OutcomeCount(c.Outcome, c.N))
	}
	s.Put(full, 1, 1, strings.Join(counts, "   "), screen.Style{})
	rule := strings.Repeat(boxSingle.h, cols)
	s.Put(full, 0, 2, rule, f.th().dim)
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
	fr.rows = listH
	top := max(min(v.Cursor-listH/2, len(v.Rows)-listH), 0)
	for i := 0; i < listH && top+i < len(v.Rows); i++ {
		r := v.Rows[top+i]
		y := listY + i
		st := screen.Style{}
		if top+i == v.Cursor {
			st.Attr |= f.th().cursor
			s.Fill(screen.Region{X: 0, Y: y, W: cols, H: 1}, st)
			s.Put(full, 0, y, ">", st)
		}
		x := 2
		if !r.Detail {
			ost := st
			if r.Outcome != fsops.OutcomeDone && r.Outcome != fsops.OutcomeSkipped {
				ost.FG, ost.Attr = f.th().problem.FG, ost.Attr|f.th().problem.Attr
			}
			s.Put(full, x, y, msg.Outcome(r.Outcome), ost)
		}
		x += outW + 1
		indent := ""
		if r.Detail {
			indent = boxSingle.bl + " " // フォルダの中の結果（上の項目の中）
		}
		f.putName(s, screen.Region{X: x, Y: y, W: nameW, H: 1}, textfmt.TruncName(indent+r.Name, nameW), st)
		s.Put(screen.Region{X: x + nameW + 1, Y: y, W: cols - x - nameW - 2, H: 1}, 0, 0, r.Reason, st)
	}
	if paneH > 0 {
		py := rows - 2 - paneH
		s.Put(full, 0, py, rule, f.th().dim)
		s.Put(full, 1, py+1, msg.EnglishTitle(keymap.KeyName(app.RoleResult, "english")), f.th().bold)
		for i, l := range english {
			s.Put(full, 1, py+2+i, l, screen.Style{})
		}
	}
	s.Put(full, 0, rows-2, rule, f.th().dim)
	// Space で何が起きるかは、カーソル行に合わせて書く（中の結果がある行だけ）。
	var items []keymap.Item
	if v.Untrashable > 0 { // ごみ箱に入らなかった項目があれば、完全削除の確認へ進めることを案内する（filer §8.5）
		items = append(items, keymap.ResultPurge)
	}
	if v.Cursor < len(v.Rows) {
		if r := v.Rows[v.Cursor]; r.Expandable {
			items = append(items, keymap.ResultInside.WithLabel(msg.GuideInside(r.Details, r.Expanded)))
		}
	}
	items = append(items, keymap.ResultEnglish, keymap.ResultClose)
	s.Put(full, 1, rows-1, keymap.Join(keymap.SepDialog, items...), screen.Style{})
}

// sizeUnit は、確認画面・進捗画面のサイズを単位付きで書く（6.1 GB、500 バイト。filer §9.4）。一覧の欄は 5 桁の textfmt.Size のまま。
func sizeUnit(n int64) string {
	if n < 1024 {
		return textfmt.Bytes(n) + " " + msg.UnitBytes
	}
	return textfmt.SizeUnit(n)
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

// 完全削除の確認に出す項目の数。多いときは、先頭のこの数だけと「ほか n 項目」を出す（filer §8.6）。
const deleteShown = 5

// deleteLines は、完全削除の確認の題名・幅・行（filer §8.6）。確定は y だけ（U2）。
// 項目は、場所（フォルダ）と名前で示す。fsops の計画に項目ごとの合計はないので、ファイルかフォルダかと、ファイルのサイズだけを出す（filer §11 の F5）。
func (f *Filer) deleteLines(s *screen.Screen, view app.View) (string, int, []line) {
	cols, _ := s.Size()
	v := view.(app.DeleteView)
	w := min(cols-4, 68)
	in := w - 4
	lines := []line{{}}
	if v.Runnable > 0 { // 実行できる項目がなければ、「削除します」とは出さない（下に「実行できる項目がありません」と出す）
		second := msg.DeleteIrreversible
		if v.FromTrash {
			second = msg.DeleteQuestion
		}
		lines = append(lines, line{text: msg.DeleteLead(v.Runnable, v.FromTrash), st: f.th().bold}, line{text: second, st: f.th().problem}, line{})
	}
	lines = append(lines, line{text: msg.Place(textfmt.TruncPath(v.Dir, in-textwidth.Width(msg.Place(""))))})
	const infoW = 14 // 「ジャンクション」
	nameW := in - 4 - 2 - infoW
	for i, it := range v.Items {
		if i == deleteShown && len(v.Items) > deleteShown+1 {
			lines = append(lines, line{text: "    " + msg.More(len(v.Items)-deleteShown), st: f.th().dim})
			break
		}
		name := it.Name
		info := sizeUnit(it.Info.Size)
		if it.Info.Type != fsops.TypeFile {
			info = msg.Type(it.Info.Type)
		}
		if it.Info.Type == fsops.TypeDir {
			name += "/"
		}
		name = textfmt.TruncName(name, nameW)
		lines = append(lines, line{text: "    " + name + strings.Repeat(" ", nameW-textwidth.Width(name)) + "  " + info})
	}
	if v.Runnable > 0 {
		size := ""
		if v.Bytes > 0 {
			size = sizeUnit(v.Bytes)
		}
		lines = append(lines, line{}, line{text: msg.Totals(v.Files, size)})
	}
	if len(v.NotRunnable) > 0 {
		lines = append(append(lines, line{}), f.notRunnableLines(v.NotRunnable)...)
	}
	for _, w := range v.Warnings {
		lines = append(lines, line{text: "! " + w, st: f.th().warn})
	}
	keys := keymap.DeleteGuide
	if v.Runnable == 0 {
		lines = append(lines, line{text: msg.NothingRunnable, st: f.th().problem})
		keys = keymap.DeleteNoneGuide
	}
	lines = append(lines, line{}, line{text: keys.Render(), st: f.th().bold})
	return msg.DeleteTitle, w, lines
}

package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zredjet/tana/internal/textwidth"
)

// modeSeq は、設定と解除の制御シーケンスが文字として表示されないかを確かめるもの（VT5。tui §9）。
// 代替画面（?1049）は、測っている最中に切り替えると画面が変わるので、ここでは測らない（終わった後の撮影で確かめる）。
var modeSeqs = []struct{ id, seq string }{
	{"2026-sync-output", "\x1b[?2026h\x1b[?2026l"},
	{"7-autowrap", "\x1b[?7h\x1b[?7l"},
	{"25-cursor", "\x1b[?25l\x1b[?25h\x1b[?25l"},
	{"2004-bracketed-paste", "\x1b[?2004h\x1b[?2004l"},
}

// modeQueries は、DECRQM の問い合わせ（VT5）。Terminal.app は応答せず、問い合わせの一部を文字として表示する（フェーズ12）ので、
// -decrqm=false で送らない。
var modeQueries = []struct{ id, seq string }{
	{"decrqm-2026-sync-output", "\x1b[?2026$p"},
	{"decrqm-1049-alt-screen", "\x1b[?1049$p"},
	{"decrqm-7-autowrap", "\x1b[?7$p"},
	{"decrqm-25-cursor", "\x1b[?25$p"},
	{"decrqm-2004-bracketed-paste", "\x1b[?2004$p"},
}

// joinCase は、続けて書くと端末が 1 つの書記素クラスタにまとめうる 2 つの書記素クラスタ（VT7）。
// screen は、欄の境目などでこれらが隣り合うとき、b の前で位置を指定し直す（tui §6）。
type joinCase struct {
	id, a, b, note string
}

var joinCases = []joinCase{
	{"jamo-l+syllable", "ᄀ", "가", "ハングル字母 L の後の音節"},
	{"jamo-l+jamo-l", "ᄀ", "ᄀ", "ハングル字母 L＋L"},
	{"virama+consonant", "क्", "ष", "デーヴァナーガリーのヴィラーマの後の子音（GB9c）"},
	{"consonant+spacing-mark", "क", "ि", "子音の後の母音記号（GB9a。単独でも幅 1 の通常の文字）"},
	{"zwj+emoji", "😀‍", "😀", "ZWJ で終わる絵文字の後の絵文字（GB11）"},
	{"prepend+letter", "؀", "a", "プリペンドの後の文字（GB9b）"},
	{"ascii+ascii", "a", "b", "比較用（結合しない）"},
}

type modeCheck struct {
	ID      string `json:"id"`
	Seq     string `json:"seq"`
	Col     int    `json:"col"`     // 書いた後のカーソルの桁（1 なら文字として表示されていない）
	Printed bool   `json:"printed"` // 文字として表示された
	Error   string `json:"error,omitempty"`
}

type autowrapCheck struct {
	Cols        int    `json:"cols"`
	OffRow      int    `json:"off_row"` // 自動改行を切って右端を越えて書いた後のカーソル（行は変わらないはず）
	OffCol      int    `json:"off_col"`
	OnRow       int    `json:"on_row"` // 自動改行を付けて右端を越えて書いた後のカーソル（次の行に移るはず）
	OnCol       int    `json:"on_col"`
	WrapsWhenOn bool   `json:"wraps_when_on"`
	StaysOff    bool   `json:"stays_when_off"`
	Error       string `json:"error,omitempty"`
}

type joinResult struct {
	ID          string `json:"id"`
	A           string `json:"a"`
	B           string `json:"b"`
	CodePoints  string `json:"codepoints"`
	Note        string `json:"note,omitempty"`
	WidthA      int    `json:"width_a"` // textwidth の幅
	WidthB      int    `json:"width_b"`
	Row         int    `json:"row"`
	ColCUP      int    `json:"col_cup"`      // a の後で位置を指定し直して b を書いた後の桁
	ColDirect   int    `json:"col_direct"`   // a と b を続けて書いた後の桁（比較用）
	ExpectedCol int    `json:"expected_col"` // 分かれて描かれたときの桁（1＋幅 a＋幅 b）
	Separated   bool   `json:"separated"`    // 位置を指定し直すと、端末が進めた桁が 2 つの幅の和になった
	// ColRTL は、右から書いた後の桁: a の場所を空白にしてから b を書き、その後で a を書く（端末が左のセルとだけ結合するなら、分かれて描かれる）。
	// 期待する桁は、a の後ろ（列の始め＋幅 a）。分かれて描かれたかは、撮影で確かめる。
	ColRTL int    `json:"col_rtl"`
	Error  string `json:"error,omitempty"`
}

type modesSection struct {
	sectionHeader
	SetReset  []modeCheck   `json:"set_reset"`
	AutoWrap  autowrapCheck `json:"autowrap"`
	Queries   []queryResult `json:"queries,omitempty"`
	Joins     []joinResult  `json:"joins"`
	JoinRow   int           `json:"join_row"` // 撮影用の画面で、結合の確かめを描いた最初の行
	Completed bool          `json:"completed"`
}

// runModes は、VT5（制御シーケンスが使えるか）と VT7（位置の指定で結合が切れるか）を測り、撮影用の画面を hold の間だけ出す。
// timeout は、カーソル位置の問い合わせの応答を待つ時間。
func runModes(c console, box *inbox, sec sectionHeader, decrqm bool, hold, timeout time.Duration) (*modesSection, error) {
	res := &modesSection{sectionHeader: sec}
	r := &cprReader{box: box}
	fmt.Fprint(c, "\x1b[2J\x1b[1;1Htuiprobe modes: 制御シーケンスと、位置の指定による結合の切れ方を測ります")
	var err error
	if res.SetReset, err = measureSetReset(c, r, timeout); err != nil {
		return res, err
	}
	if res.AutoWrap, err = measureAutoWrap(c, r, sec.Cols, timeout); err != nil {
		return res, err
	}
	if decrqm {
		if res.Queries, err = runModeQueries(c, r, timeout); err != nil {
			return res, err
		}
	}
	res.JoinRow = 4
	if res.Joins, err = measureJoins(c, r, res.JoinRow, timeout); err != nil {
		return res, err
	}
	if err := holdJoins(c, r, res, hold); err != nil {
		return res, err
	}
	res.Completed = true
	return res, nil
}

// cursorAfter は、s を書いた後のカーソルの位置を問い合わせる。
func cursorAfter(c console, r *cprReader, s string, timeout time.Duration) (row, col int, err error) {
	if _, err := c.Write([]byte(s)); err != nil {
		return 0, 0, err
	}
	if err := c.QueryCursorPosition(); err != nil {
		return 0, 0, err
	}
	row, col, _, _, err = r.read(timeout)
	return row, col, err
}

func errString(err error) string {
	if errors.Is(err, errTimeout) {
		return "no cursor position report"
	}
	return err.Error()
}

func measureSetReset(c console, r *cprReader, timeout time.Duration) ([]modeCheck, error) {
	var out []modeCheck
	for _, m := range modeSeqs {
		res := modeCheck{ID: m.id, Seq: fmt.Sprintf("%q", m.seq)}
		_, col, err := cursorAfter(c, r, fmt.Sprintf("\x1b[%d;1H\x1b[2K%s", measureRow, m.seq), timeout)
		switch {
		case errors.Is(err, errTimeout):
			res.Error = errString(err)
		case err != nil:
			return out, err
		default:
			res.Col, res.Printed = col, col != 1
		}
		out = append(out, res)
	}
	// 描くときの設定に戻す（カーソルは隠す。自動改行は切る）。
	_, err := c.Write([]byte("\x1b[?25l\x1b[?7l"))
	return out, err
}

// measureAutoWrap は、右端を越えて書いたときに、自動改行を切ると行が変わらず、付けると次の行に移ることを確かめる（DECAWM）。
func measureAutoWrap(c console, r *cprReader, cols int, timeout time.Duration) (autowrapCheck, error) {
	res := autowrapCheck{Cols: cols}
	if cols < 10 {
		res.Error = "terminal too narrow"
		return res, nil
	}
	var err error
	res.OffRow, res.OffCol, err = cursorAfter(c, r, fmt.Sprintf("\x1b[?7l\x1b[%d;%dHabcdef", measureRow, cols-2), timeout)
	if err == nil {
		res.OnRow, res.OnCol, err = cursorAfter(c, r, fmt.Sprintf("\x1b[?7h\x1b[%d;1H\x1b[2K\x1b[%d;1H\x1b[2K\x1b[%d;%dHabcdef", measureRow, measureRow+1, measureRow, cols-2), timeout)
	}
	if _, werr := c.Write([]byte(fmt.Sprintf("\x1b[?7l\x1b[%d;1H\x1b[2K\x1b[%d;1H\x1b[2K", measureRow, measureRow+1))); werr != nil {
		return res, werr
	}
	switch {
	case errors.Is(err, errTimeout):
		res.Error = errString(err)
	case err != nil:
		return res, err
	default:
		res.StaysOff = res.OffRow == measureRow && res.OffCol == cols
		res.WrapsWhenOn = res.OnRow == measureRow+1
	}
	return res, nil
}

func runModeQueries(c console, r *cprReader, timeout time.Duration) ([]queryResult, error) {
	var out []queryResult
	for _, q := range modeQueries {
		fmt.Fprintf(c, "\x1b[%d;1H\x1b[2K%s", measureRow, q.seq)
		if err := c.QueryCursorPosition(); err != nil {
			return out, err
		}
		_, col, before, _, err := r.read(timeout)
		res := queryResult{ID: q.id, Query: fmt.Sprintf("%q", q.seq), Reply: fmt.Sprintf("%q", before), ReplyHex: fmt.Sprintf("%x", before), Col: col}
		if errors.Is(err, errTimeout) {
			res.Error = errString(err)
		} else if err != nil {
			return out, err
		}
		out = append(out, res)
	}
	return out, r.drain(300 * time.Millisecond)
}

// measureJoins は、joinCases を 3 行ずつ描いて測る。1 行目は a の後で位置を指定し直して b を書き、2 行目は続けて書き、
// 3 行目は右から書く（a の場所を空白にして b を書き、その後で a を書く）。
// どの行も、分かれて描かれたときの b の後ろの桁に | を描く（撮影で、ずれを見る）。
func measureJoins(c console, r *cprReader, firstRow int, timeout time.Duration) ([]joinResult, error) {
	var out []joinResult
	row := firstRow
	for _, jc := range joinCases {
		wa, wb := textwidth.Width(jc.a), textwidth.Width(jc.b)
		res := joinResult{ID: jc.id, A: jc.a, B: jc.b, CodePoints: codePoints(jc.a) + " + " + codePoints(jc.b), Note: jc.note, WidthA: wa, WidthB: wb, Row: row}
		const col0 = 24 // 左に ID を書く
		res.ExpectedCol = col0 + wa + wb
		label := fmt.Sprintf("\x1b[%d;1H\x1b[2K%-22s", row, jc.id)
		_, colCUP, err := cursorAfter(c, r, label+fmt.Sprintf("\x1b[%d;%dH%s\x1b[%d;%dH%s", row, col0, jc.a, row, col0+wa, jc.b), timeout)
		if err == nil {
			res.ColCUP = colCUP
			_, res.ColDirect, err = cursorAfter(c, r, fmt.Sprintf("\x1b[%d;1H\x1b[2K%-22s\x1b[%d;%dH%s%s", row+1, "  (続けて書く)", row+1, col0, jc.a, jc.b), timeout)
		}
		if err == nil {
			_, res.ColRTL, err = cursorAfter(c, r, fmt.Sprintf("\x1b[%d;1H\x1b[2K%-22s\x1b[%d;%dH%s\x1b[%d;%dH%s\x1b[%d;%dH%s",
				row+2, "  (右から書く)", row+2, col0, strings.Repeat(" ", wa), row+2, col0+wa, jc.b, row+2, col0, jc.a), timeout)
		}
		switch {
		case errors.Is(err, errTimeout):
			res.Error = errString(err)
		case err != nil:
			return out, err
		default:
			res.Separated = res.ColCUP == res.ExpectedCol
		}
		// 撮影用に、分かれて描かれたときの b の後ろに | を描く。
		fmt.Fprintf(c, "\x1b[%d;%dH|\x1b[%d;%dH|\x1b[%d;%dH|", row, res.ExpectedCol, row+1, res.ExpectedCol, row+2, res.ExpectedCol)
		out = append(out, res)
		row += 3
	}
	return out, nil
}

// holdJoins は、測った結果の表を画面の下に書き足し、撮影のために hold の間（0 ならキーを押すまで）出しておく。
func holdJoins(c console, r *cprReader, res *modesSection, hold time.Duration) error {
	var b strings.Builder
	row := res.JoinRow + 3*len(res.Joins) + 1
	fmt.Fprintf(&b, "\x1b[1;1H\x1b[2Ktuiprobe modes: 各行の | の手前で b が終わっていれば、a と b は分かれて描かれている")
	fmt.Fprintf(&b, "\x1b[2;1H\x1b[2K位置を指定し直した行・続けて書いた行・右から書いた行の組。右の数字は、端末が進めた桁（期待する桁）")
	for i, j := range res.Joins {
		fmt.Fprintf(&b, "\x1b[%d;60H%d(%d) / %d / %d(%d)", res.JoinRow+3*i, j.ColCUP, j.ExpectedCol, j.ColDirect, j.ColRTL, j.ExpectedCol-j.WidthB)
	}
	if hold > 0 {
		fmt.Fprintf(&b, "\x1b[%d;1H撮影のため %s 表示します。キーを押すと終わります。", row, hold)
	} else {
		fmt.Fprintf(&b, "\x1b[%d;1H撮影が終わったら、キーを押してください。", row)
	}
	if _, err := c.Write([]byte(b.String())); err != nil {
		return err
	}
	return holdUntilKey(r.box, hold)
}

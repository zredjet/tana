package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// widthCase は、幅を測る文字列（filer §12.1）。
// 制御文字と双方向の制御文字（端末に出してはいけない文字。tui §4）は含めない。
type widthCase struct {
	id, category, text, note string
}

// 分類:
//
//	ascii       基準
//	wide        East Asian Width が W・F
//	halfwidth   半角カナ（H）
//	ambiguous   East Asian Width が A（罫線、記号、ギリシャ文字、ラテン文字の一部、私用領域）
//	narrow      N・Na の記号（比較用）
//	nfd         結合文字（macOS の NFD の名前など）
//	emoji       絵文字（単独、VS16・VS15、ZWJ、肌の色、国旗、キーキャップ、新しい版の絵文字）
//	format      幅のない文字（ZWSP、ZWJ など）
//	variation   異体字セレクタ（IVS）
//	script      ハングル字母、タイ文字、デーヴァナーガリー、アラビア文字、未割り当て
//	invalid     不正な UTF-8
var widthCases = []widthCase{
	{"ascii-a", "ascii", "a", ""},
	{"ascii-abc", "ascii", "abc", ""},

	{"hira-a", "wide", "あ", "U+3042"},
	{"kanji", "wide", "漢", "U+6F22"},
	{"kata-ka", "wide", "カ", "U+30AB"},
	{"fullwidth-a", "wide", "Ａ", "U+FF21"},
	{"ideographic-space", "wide", "\u3000", ""},
	{"chouon", "wide", "ー", "U+30FC"},
	{"kata-middle-dot", "wide", "・", "U+30FB"},
	{"wave-dash", "wide", "〜", "U+301C"},
	{"fullwidth-tilde", "wide", "～", "U+FF5E"},
	{"kanji-ext-b", "wide", "𠮷", "U+20BB7（サロゲートの対）"},
	{"hangul-ga", "wide", "가", "U+AC00"},
	{"nfc-ga", "wide", "が", "U+304C（NFD の比較用）"},
	{"spacing-dakuten", "wide", "゛", "U+309B"},

	{"hw-kana-a", "halfwidth", "ｱ", "U+FF71"},
	{"hw-kana-ga", "halfwidth", "ｶﾞ", "U+FF76 U+FF9E（UAX #29 では 1 つの書記素クラスタ）"},
	{"hw-dakuten", "halfwidth", "ﾞ", "U+FF9E"},

	{"box-h", "ambiguous", "─", "U+2500"},
	{"box-v", "ambiguous", "│", "U+2502"},
	{"box-dr", "ambiguous", "┌", "U+250C"},
	{"box-cross", "ambiguous", "┼", "U+253C"},
	{"box-heavy-h", "ambiguous", "━", "U+2501"},
	{"box-double-h", "ambiguous", "═", "U+2550"},
	{"box-arc", "ambiguous", "╭", "U+256D"},
	{"block-full", "ambiguous", "█", "U+2588"},
	{"shade-light", "ambiguous", "░", "U+2591"},
	{"ellipsis", "ambiguous", "…", "U+2026"},
	{"circle", "ambiguous", "○", "U+25CB"},
	{"black-circle", "ambiguous", "●", "U+25CF"},
	{"bullseye", "ambiguous", "◎", "U+25CE"},
	{"square", "ambiguous", "□", "U+25A1"},
	{"black-square", "ambiguous", "■", "U+25A0"},
	{"triangle", "ambiguous", "△", "U+25B3"},
	{"black-triangle", "ambiguous", "▲", "U+25B2"},
	{"black-down-triangle", "ambiguous", "▼", "U+25BC"},
	{"black-diamond", "ambiguous", "◆", "U+25C6"},
	{"black-star", "ambiguous", "★", "U+2605"},
	{"white-star", "ambiguous", "☆", "U+2606"},
	{"kome", "ambiguous", "※", "U+203B"},
	{"circled-1", "ambiguous", "①", "U+2460"},
	{"roman-1", "ambiguous", "Ⅰ", "U+2160"},
	{"greek-alpha", "ambiguous", "α", "U+03B1"},
	{"greek-omega", "ambiguous", "Ω", "U+03A9"},
	{"cyrillic-de", "ambiguous", "Д", "U+0414"},
	{"degree", "ambiguous", "°", "U+00B0"},
	{"plus-minus", "ambiguous", "±", "U+00B1"},
	{"times", "ambiguous", "×", "U+00D7"},
	{"divide", "ambiguous", "÷", "U+00F7"},
	{"section", "ambiguous", "§", "U+00A7"},
	{"pilcrow", "ambiguous", "¶", "U+00B6"},
	{"middle-dot", "ambiguous", "·", "U+00B7"},
	{"registered", "ambiguous", "®", "U+00AE"},
	{"arrow-right", "ambiguous", "→", "U+2192"},
	{"arrow-double", "ambiguous", "⇒", "U+21D2"},
	{"forall", "ambiguous", "∀", "U+2200"},
	{"sqrt", "ambiguous", "√", "U+221A"},
	{"infinity", "ambiguous", "∞", "U+221E"},
	{"approx", "ambiguous", "≒", "U+2252"},
	{"music-note", "ambiguous", "♪", "U+266A"},
	{"quote-left", "ambiguous", "“", "U+201C"},
	{"quote-right", "ambiguous", "”", "U+201D"},
	{"single-quote-left", "ambiguous", "‘", "U+2018"},
	{"hyphen", "ambiguous", "‐", "U+2010"},
	{"em-dash", "ambiguous", "—", "U+2014"},
	{"horizontal-bar", "ambiguous", "―", "U+2015"},
	{"euro", "ambiguous", "€", "U+20AC"},
	{"celsius", "ambiguous", "℃", "U+2103"},
	{"numero", "ambiguous", "№", "U+2116"},
	{"trademark", "ambiguous", "™", "U+2122"},
	{"e-acute", "ambiguous", "é", "U+00E9（NFC）"},
	{"a-umlaut", "ambiguous", "ä", "U+00E4（NFC）"},
	{"sharp-s", "ambiguous", "ß", "U+00DF"},
	{"pua", "ambiguous", "\ue000", "U+E000（私用領域）"},
	{"pua-plane15", "ambiguous", "\U000F0000", "U+F0000（私用領域）"},

	{"yen", "narrow", "¥", "U+00A5"},
	{"cent", "narrow", "¢", "U+00A2"},
	{"copyright", "narrow", "©", "U+00A9"},

	{"nfd-ga", "nfd", "か\u3099", "U+304B U+3099"},
	{"nfd-pa", "nfd", "は\u309a", "U+306F U+309A"},
	{"nfd-gagigu", "nfd", "か\u3099き\u3099く\u3099", ""},
	{"nfd-kata-ga", "nfd", "カ\u3099", "U+30AB U+3099"},
	{"nfd-e-acute", "nfd", "e\u0301", "U+0065 U+0301"},
	{"nfd-a-umlaut", "nfd", "a\u0308", "U+0061 U+0308"},
	{"combining-dakuten-alone", "nfd", "\u3099", "先頭の結合文字"},
	{"combining-acute-alone", "nfd", "\u0301", "先頭の結合文字"},
	{"combining-stack", "nfd", "e\u0301\u0302\u0303", ""},

	{"emoji-grin", "emoji", "😀", "U+1F600"},
	{"emoji-thumbs", "emoji", "👍", "U+1F44D"},
	{"emoji-thumbs-skin", "emoji", "👍🏽", "U+1F44D U+1F3FD"},
	{"emoji-skin-alone", "emoji", "🏽", "U+1F3FD"},
	{"emoji-family", "emoji", "👨\u200d👩\u200d👧", "ZWJ"},
	{"emoji-technologist", "emoji", "🧑\u200d💻", "ZWJ"},
	{"emoji-tech-skin", "emoji", "👩🏽\u200d💻", "肌の色＋ZWJ"},
	{"emoji-rainbow-flag", "emoji", "🏳\ufe0f\u200d🌈", "U+1F3F3 U+FE0F U+200D U+1F308"},
	{"heart-text", "emoji", "❤", "U+2764（既定は文字の表示）"},
	{"heart-vs16", "emoji", "❤\ufe0f", "U+2764 U+FE0F"},
	{"smile-text", "emoji", "☺", "U+263A"},
	{"smile-vs16", "emoji", "☺\ufe0f", "U+263A U+FE0F"},
	{"scissors-text", "emoji", "✂", "U+2702"},
	{"scissors-vs16", "emoji", "✂\ufe0f", "U+2702 U+FE0F"},
	{"sun-text", "emoji", "☀", "U+2600"},
	{"sun-vs16", "emoji", "☀\ufe0f", "U+2600 U+FE0F"},
	{"watch", "emoji", "⌚", "U+231A（W）"},
	{"star-emoji", "emoji", "⭐", "U+2B50（W）"},
	{"check-emoji", "emoji", "✅", "U+2705（W）"},
	{"keycap-hash", "emoji", "#\ufe0f\u20e3", "U+0023 U+FE0F U+20E3"},
	{"keycap-1", "emoji", "1\ufe0f\u20e3", "U+0031 U+FE0F U+20E3"},
	{"flag-jp", "emoji", "🇯🇵", "地域指示記号の対"},
	{"ri-alone", "emoji", "🇯", "対にならない地域指示記号"},
	{"flags-two", "emoji", "🇯🇵🇺🇸", ""},
	{"grin-vs15", "emoji", "😀\ufe0e", "U+1F600 U+FE0E"},
	{"emoji-u15", "emoji", "🫨", "U+1FAE8（Unicode 15.0）"},
	{"emoji-u16", "emoji", "🪾", "U+1FABE（Unicode 16.0）"},
	{"flag-england", "emoji", "🏴\U000e0067\U000e0062\U000e0065\U000e006e\U000e0067\U000e007f", "タグの並び"},
	{"arrow-lr-vs16", "emoji", "↔\ufe0f", "U+2194（A）U+FE0F"},
	{"play-vs16", "emoji", "▶\ufe0f", "U+25B6（A）U+FE0F"},
	{"copyright-vs16", "emoji", "©\ufe0f", "U+00A9 U+FE0F"},
	// textwidth が簡略な形にしたときの形（tui §4。フェーズ13で追加）
	{"emoji-man", "emoji", "\U0001f468", "U+1F468（ZWJ の並びの簡略な形）"},
	{"emoji-person", "emoji", "\U0001f9d1", "U+1F9D1（ZWJ の並びの簡略な形）"},
	{"emoji-woman", "emoji", "\U0001f469", "U+1F469（ZWJ の並びの簡略な形）"},
	{"white-flag", "emoji", "\U0001f3f3", "U+1F3F3（虹の旗の簡略な形）"},
	{"black-flag", "emoji", "\U0001f3f4", "U+1F3F4（タグの並びの簡略な形）"},
	{"arrow-lr", "ambiguous", "\u2194", "U+2194（↔️ の簡略な形）"},
	{"play", "ambiguous", "\u25b6", "U+25B6（▶️ の簡略な形）"},
	// Unicode 17.0・18.0 で加わった絵文字（フェーズ13で追加。幅が端末によって違いうるか）
	{"emoji-u17", "emoji", "\U0001fac8", "U+1FAC8（Unicode 17.0）"},
	{"emoji-u17b", "emoji", "\U0001f6d8", "U+1F6D8（Unicode 17.0）"},
	{"emoji-u18", "emoji", "\U0001faf9", "U+1FAF9（Unicode 18.0）"},
	{"emoji-u18b", "emoji", "\U0001f6d9", "U+1F6D9（Unicode 18.0）"},

	{"zwsp", "format", "\u200b", "U+200B"},
	{"zwj", "format", "\u200d", "U+200D"},
	{"word-joiner", "format", "\u2060", "U+2060"},
	{"bom", "format", "\ufeff", "U+FEFF"},
	{"soft-hyphen", "format", "\u00ad", "U+00AD"},
	{"cgj", "format", "\u034f", "U+034F"},
	{"a-zwsp-b", "format", "a\u200bb", ""},

	{"ivs", "variation", "葛\U000e0100", "U+845B U+E0100"},
	{"vs16-on-kanji", "variation", "漢\ufe0f", "U+6F22 U+FE0F"},

	{"jamo-lv", "script", "가", "ハングル字母 L＋V"},
	{"jamo-l", "script", "ᄀ", "U+1100（W）"},
	{"jamo-ll", "script", "\u1100\u1100", "ハングル字母 L＋L（1 つの書記素クラスタ。フェーズ13で追加）"},
	{"jamo-v-alone", "script", "\u1161", "前に初声のない中声字母（フェーズ13で追加）"},
	{"thai-am", "script", "กำ", ""},
	{"thai-i", "script", "ก\u0e34", ""},
	{"devanagari-kssi", "script", "क\u094dषि", ""},
	{"arabic-ain", "script", "ع", "右から左の文字"},
	{"unassigned", "script", "\u0378", "未割り当て"},

	{"invalid-ff", "invalid", "\xff", ""},
	{"invalid-c3", "invalid", "\xc3", "途中で切れた 2 バイトの文字"},
	{"invalid-e3-81", "invalid", "\xe3\x81", "途中で切れた「あ」"},
	{"invalid-surrogate", "invalid", "\xed\xa0\x80", "WTF-8 の U+D800"},
	{"invalid-overlong", "invalid", "\xc0\xaf", ""},
	{"invalid-a-ff-b", "invalid", "a\xffb", ""},
}

// driftIDs は、ずれの広がりを見る画面に出す文字列。
var driftIDs = []string{
	"ascii-abc", "hira-a", "circle", "circled-1", "box-h", "ellipsis", "heart-text", "heart-vs16", "smile-vs16",
	"emoji-family", "emoji-thumbs-skin", "flag-jp", "nfd-ga", "hw-kana-ga", "ivs", "jamo-lv", "devanagari-kssi", "invalid-ff",
}

// termQueries は、参考に記録する問い合わせ（VT5 の下調べ）。応答のない端末があるので、カーソル位置の問い合わせで区切る。
var termQueries = []struct{ id, seq string }{
	{"da1", "\x1b[c"},
	{"da2", "\x1b[>c"},
	{"xtversion", "\x1b[>0q"},
	{"decrqm-2026-sync-output", "\x1b[?2026$p"},
	{"decrqm-2004-bracketed-paste", "\x1b[?2004$p"},
	{"decrqm-1049-alt-screen", "\x1b[?1049$p"},
	{"decrqm-7-autowrap", "\x1b[?7$p"},
	{"decrqm-25-cursor", "\x1b[?25$p"},
}

type widthResult struct {
	ID         string `json:"id"`
	Category   string `json:"category"`
	Text       string `json:"text,omitempty"` // 正しい UTF-8 のときだけ
	Hex        string `json:"hex"`
	CodePoints string `json:"codepoints"`
	Note       string `json:"note,omitempty"`
	Advance    *int   `json:"advance"`         // 端末が進めた桁数（応答がなければ null）
	Row        int    `json:"row,omitempty"`   // 報告された行（折り返していないことの確認）
	Stray      string `json:"stray,omitempty"` // 報告の前に届いたもの（16 進）
	Error      string `json:"error,omitempty"`
}

type queryResult struct {
	ID       string `json:"id"`
	Query    string `json:"query"`
	Reply    string `json:"reply"` // Go の文字列リテラルの形
	ReplyHex string `json:"reply_hex,omitempty"`
	Col      int    `json:"col,omitempty"` // 問い合わせの後のカーソルの桁。1 でなければ、端末が問い合わせを文字として表示した
	Error    string `json:"error,omitempty"`
}

type widthSection struct {
	sectionHeader
	MeasureRow int           `json:"measure_row"`
	Cases      []widthResult `json:"cases"`
	Queries    []queryResult `json:"queries"`
	DriftIDs   []string      `json:"drift_ids"`
	Completed  bool          `json:"completed"`
}

// measureRow は、測る文字列を書く行。
const measureRow = 3

const cprTimeout = 2 * time.Second

// runWidth は、widthCases の幅を測り、問い合わせの応答を記録し、ずれの広がりを見る画面を hold の間だけ出す（hold が 0 ならキーを押すまで）。
// 途中で失敗しても、そこまでの結果を返す。
func runWidth(c console, box *inbox, sec sectionHeader, hold time.Duration) (*widthSection, error) {
	res := &widthSection{sectionHeader: sec, MeasureRow: measureRow, DriftIDs: driftIDs}
	r := &cprReader{box: box}
	fmt.Fprintf(c, "\x1b[2J\x1b[1;1Htuiprobe width: %d 件の文字列の幅を測ります", len(widthCases))
	var err error
	res.Cases, err = measureCases(c, r, widthCases, cprTimeout)
	if err != nil {
		return res, err
	}
	res.Queries, err = runQueries(c, r, cprTimeout)
	if err != nil {
		return res, err
	}
	if err := showDrift(c, r, hold); err != nil {
		return res, err
	}
	res.Completed = true
	return res, nil
}

func measureCases(c console, r *cprReader, cases []widthCase, timeout time.Duration) ([]widthResult, error) {
	var out []widthResult
	misses := 0
	for i, wc := range cases {
		res := widthResult{ID: wc.id, Category: wc.category, Hex: hex.EncodeToString([]byte(wc.text)), CodePoints: codePoints(wc.text), Note: wc.note}
		if utf8.ValidString(wc.text) {
			res.Text = wc.text
		}
		fmt.Fprintf(c, "\x1b[1;1H\x1b[2Ktuiprobe width: %d/%d %s\x1b[%d;1H\x1b[2K%s", i+1, len(cases), wc.id, measureRow, wc.text)
		if err := c.QueryCursorPosition(); err != nil {
			return out, err
		}
		row, col, before, _, err := r.read(timeout)
		res.Stray = hex.EncodeToString(before)
		switch {
		case errors.Is(err, errTimeout):
			res.Error = "no cursor position report"
			if misses++; misses >= 3 {
				return append(out, res), errors.New("端末がカーソル位置の問い合わせに応答しません")
			}
		case err != nil:
			return out, err
		default:
			misses = 0
			adv := col - 1
			res.Advance = &adv
			res.Row = row
		}
		out = append(out, res)
	}
	return out, nil
}

func runQueries(c console, r *cprReader, timeout time.Duration) ([]queryResult, error) {
	var out []queryResult
	for _, q := range termQueries {
		fmt.Fprintf(c, "\x1b[1;1H\x1b[2Ktuiprobe width: 問い合わせ %s\x1b[%d;1H\x1b[2K%s", q.id, measureRow, q.seq)
		if err := c.QueryCursorPosition(); err != nil {
			return out, err
		}
		_, col, before, _, err := r.read(timeout)
		res := queryResult{ID: q.id, Query: fmt.Sprintf("%q", q.seq), Reply: fmt.Sprintf("%q", before), ReplyHex: hex.EncodeToString(before), Col: col}
		if errors.Is(err, errTimeout) {
			res.Error = "no cursor position report"
		} else if err != nil {
			return out, err
		}
		out = append(out, res)
	}
	// 遅れて届いた応答が、ほかの記録に混ざらないように捨てる。
	return out, r.drain(300 * time.Millisecond)
}

// showDrift は、幅がずれうる文字列の後ろに、位置を指定しない文字と、位置を指定した欄を並べた画面を出す（filer §12.1）。
func showDrift(c console, r *cprReader, hold time.Duration) error {
	var b strings.Builder
	b.WriteString("\x1b[2J\x1b[1;1H[文字列を3回]x の後ろの | は、50 桁目に位置を指定して描いた欄")
	row := 3
	for _, id := range driftIDs {
		wc := caseByID(id)
		fmt.Fprintf(&b, "\x1b[%d;1H%s\x1b[%d;20H[%s%s%s]x\x1b[%d;50H|50 %s", row, id, row, wc.text, wc.text, wc.text, row, codePoints(wc.text))
		row++
	}
	if hold > 0 {
		fmt.Fprintf(&b, "\x1b[%d;1H撮影のため %s 表示します。キーを押すと終わります。", row+1, hold)
	} else {
		fmt.Fprintf(&b, "\x1b[%d;1H撮影が終わったら、キーを押してください。", row+1)
	}
	if _, err := c.Write([]byte(b.String())); err != nil {
		return err
	}
	return holdUntilKey(r.box, hold)
}

// holdUntilKey は、キーが押されるか hold が過ぎるまで待つ（hold が 0 ならキーを押すまで）。撮影のための画面を出しておくのに使う。
// フォーカス・大きさの変更・キーを離したレコードでは終わらない（撮影のためにウィンドウを触っても消えない）。
func holdUntilKey(box *inbox, hold time.Duration) error {
	if hold <= 0 {
		hold = 24 * time.Hour
	}
	deadline := time.Now().Add(hold)
	for left := hold; left > 0; left = time.Until(deadline) {
		x, err := box.next(left)
		if errors.Is(err, errTimeout) {
			return nil
		}
		if err != nil {
			return err
		}
		if hasKey(x) {
			return nil
		}
	}
	return nil
}

func caseByID(id string) widthCase {
	for _, wc := range widthCases {
		if wc.id == id {
			return wc
		}
	}
	panic("tuiprobe: unknown width case " + id)
}

// codePoints は、s の符号位置を "U+304B U+3099" の形で返す。不正な UTF-8 のバイトは "0xFF" の形にする。
func codePoints(s string) string {
	var parts []string
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			parts = append(parts, fmt.Sprintf("0x%02X", s[i]))
		} else {
			parts = append(parts, fmt.Sprintf("U+%04X", r))
		}
		i += size
	}
	return strings.Join(parts, " ")
}

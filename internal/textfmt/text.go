package textfmt

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"

	"github.com/zredjet/tana/internal/textwidth"
)

// DecodeText は、ファイルの先頭 data がテキストなら、UTF-8 にした文字列と文字コードの名前を返す（プレビュー。filer §6）。
// 判定するのは UTF-8（BOM の有無を問わない。BOM は除く）、BOM 付きの UTF-16、Shift_JIS。
// NUL を含むもの、どの文字コードでもないもの、制御文字が多いもの（5% を超える。ESC・タブ・改行などは数えない）はテキストでない（ok が偽）。
// truncated は data がファイルの途中で切れていること。末尾の途中で切れた文字は除く。
func DecodeText(data []byte, truncated bool) (text, encoding string, ok bool) {
	switch {
	case bytes.HasPrefix(data, []byte("\xef\xbb\xbf")):
		data = data[3:]
	case bytes.HasPrefix(data, []byte("\xff\xfe")) || bytes.HasPrefix(data, []byte("\xfe\xff")):
		if len(data)%2 == 1 {
			data = data[:len(data)-1] // 途中で切れた符号単位
		}
		b, err := unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM).NewDecoder().Bytes(data)
		if err != nil {
			return "", "", false
		}
		return textOK(string(b), "UTF-16")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", "", false
	}
	if truncated {
		data = trimPartialUTF8(data)
	}
	if utf8.Valid(data) {
		return textOK(string(data), "UTF-8")
	}
	// Shift_JIS（Windows の古いテキスト）。末尾の 2 バイト文字の 1 バイト目だけが残っていれば除く。
	if truncated && len(data) > 0 && isSJISLead(data[len(data)-1]) && !secondOfPair(data) {
		data = data[:len(data)-1]
	}
	b, err := japanese.ShiftJIS.NewDecoder().Bytes(data)
	if err != nil || bytes.ContainsRune(b, utf8.RuneError) {
		return "", "", false
	}
	return textOK(string(b), "Shift_JIS")
}

// textOK は、制御文字が多すぎなければ s をテキストとして返す。
func textOK(s, encoding string) (string, string, bool) {
	controls, n := 0, 0
	for _, r := range s {
		n++
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' && r != '\f' && r != '\v' && r != 0x1b || r == 0x7f {
			controls++
		}
	}
	if controls*20 > n {
		return "", "", false
	}
	return s, encoding, true
}

// trimPartialUTF8 は、末尾の途中で切れた UTF-8 の文字（3 バイトまで）を除く。
func trimPartialUTF8(b []byte) []byte {
	for i := 1; i <= 3 && i <= len(b); i++ {
		c := b[len(b)-i]
		if c < 0x80 {
			return b
		}
		if c >= 0xc0 { // 文字の始め
			if !utf8.FullRune(b[len(b)-i:]) {
				return b[:len(b)-i]
			}
			return b
		}
	}
	return b
}

// isSJISLead は、c が Shift_JIS の 2 バイト文字の 1 バイト目になりうるかを返す。
func isSJISLead(c byte) bool { return 0x81 <= c && c <= 0x9f || 0xe0 <= c && c <= 0xfc }

// secondOfPair は、b の最後のバイトが、Shift_JIS の 2 バイト文字の 2 バイト目かを返す（前から数えて決める）。
func secondOfPair(b []byte) bool {
	for i := 0; i < len(b); i++ {
		if isSJISLead(b[i]) {
			if i+1 == len(b)-1 {
				return true
			}
			i++
		}
	}
	return false
}

// ExpandTabs は、タブを tab 桁ごとの空白に広げる。桁は textwidth の表示幅で数える（T4）。
func ExpandTabs(s string, tab int) string {
	if !strings.Contains(s, "\t") {
		return s
	}
	var b strings.Builder
	col := 0
	for c := range textwidth.All(s) {
		if c.Text == "\t" {
			n := tab - col%tab
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		}
		b.WriteString(c.Text)
		col += c.Width
	}
	return b.String()
}

// PreviewLines は、テキストを行（\n・\r\n の区切り）に分け、タブを 4 桁ごとに広げて、先頭の n 行を返す。最後の改行の後は行にしない。
func PreviewLines(text string, n int) []string {
	if text == "" {
		return nil
	}
	var lines []string
	for line := range strings.SplitSeq(strings.TrimSuffix(text, "\n"), "\n") {
		if len(lines) == n {
			break
		}
		lines = append(lines, ExpandTabs(strings.TrimSuffix(line, "\r"), 4))
	}
	return lines
}

// Wrap は、s を表示幅 width ごとの行に分ける（書記素クラスタの途中では分けない。短い案内の文を狭い列に出すため）。
// width に入らない 1 つの書記素クラスタは、それだけで 1 行にする。
func Wrap(s string, width int) []string {
	if width <= 0 || s == "" {
		return nil
	}
	var lines []string
	var b strings.Builder
	col := 0
	for c := range textwidth.All(s) {
		if col+c.Width > width && col > 0 {
			lines = append(lines, b.String())
			b.Reset()
			col = 0
		}
		b.WriteString(c.Text)
		col += c.Width
	}
	return append(lines, b.String())
}

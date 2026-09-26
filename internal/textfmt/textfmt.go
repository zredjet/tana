package textfmt

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/zredjet/tana/internal/textwidth"
)

// Size は、ファイルの大きさを 1024 で割って K・M・G・T を付け、5 桁以内にする（filer §9.4）。
// 10 未満は小数第 1 位まで（3.1K）、それ以上は整数（48K、128M）。1024 未満はバイト数のまま。
func Size(n int64) string {
	if n < 1024 {
		return strconv.FormatInt(n, 10)
	}
	v := float64(n)
	unit := ""
	for _, u := range []string{"K", "M", "G", "T"} {
		v /= 1024
		unit = u
		if v < 1024 || u == "T" {
			break
		}
	}
	if s := strconv.FormatFloat(v, 'f', 1, 64); v < 10 && s != "10.0" {
		return s + unit
	}
	return strconv.FormatFloat(v, 'f', 0, 64) + unit
}

// SizeUnit は、確認画面・進捗画面のサイズ（6.1 GB、128 MB、3.1 KB。filer §9.4）。値は Size と同じく 1024 で割ったもの。
// 1024 未満は数だけを返す（単位の「バイト」は、人向けの文言なので呼ぶ側で付ける）。
func SizeUnit(n int64) string {
	s := Size(n)
	if n < 1024 {
		return s
	}
	return s[:len(s)-1] + " " + s[len(s)-1:] + "B"
}

// Bytes は、バイト数を 3 桁ごとにコンマで区切る（状態行。filer §9.4）。
func Bytes(n int64) string {
	s := strconv.FormatInt(n, 10)
	sign := ""
	if n < 0 {
		sign, s = "-", s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return sign + b.String()
}

// Time は、一覧の更新日時を書く。now と同じ年なら MM-DD HH:MM、それ以外は YYYY-MM-DD（filer §9.4）。
func Time(t, now time.Time) string {
	if t.Year() == now.Year() {
		return t.Format("01-02 15:04")
	}
	return t.Format("2006-01-02")
}

// FullTime は、状態行の更新日時を秒まで書く（filer §9.4）。
func FullTime(t time.Time) string { return t.Format("2006-01-02 15:04:05") }

// clusters は、s の書記素クラスタの元の文字列と表示幅を返す。
func clusters(s string) (texts []string, widths []int) {
	for c := range textwidth.All(s) {
		texts = append(texts, c.Text)
		widths = append(widths, c.Width)
	}
	return texts, widths
}

// headFit は、s の先頭から、幅 width に収まるだけの書記素クラスタをつないだ文字列を返す。
func headFit(s string, width int) string {
	n, w := 0, 0
	for c := range textwidth.All(s) {
		if w+c.Width > width {
			break
		}
		w += c.Width
		n += len(c.Text)
	}
	return s[:n]
}

// tailFit は、s の後ろから、幅 width に収まるだけの書記素クラスタをつないだ文字列を返す。
func tailFit(s string, width int) string {
	texts, widths := clusters(s)
	w, i := 0, len(texts)
	for i > 0 && w+widths[i-1] <= width {
		i--
		w += widths[i]
	}
	return strings.Join(texts[i:], "")
}

// TruncName は、名前を表示幅 width に収める。収まらなければ、拡張子を残して途中を ~ にする（見積書_2026~.xlsx。filer §9.2）。
// 拡張子は最後の . から後ろ（先頭の . は区切りとみなさない）で、幅が width の半分以下のときだけ残す。書記素クラスタの途中では切らない。
// 表示のためだけの文字列で、パスを作ってはいけない（filer U4）。
func TruncName(name string, width int) string {
	if width <= 0 {
		return ""
	}
	if textwidth.Width(name) <= width {
		return name
	}
	head, ext := name, ""
	if dot := strings.LastIndexByte(name, '.'); dot > 0 && textwidth.Width(name[dot:]) <= width/2 {
		head, ext = name[:dot], name[dot:]
	}
	// つないだときに ~ が前の書記素クラスタと結合して幅が変わることがある（プリペンドなど）ので、収まるまで頭を縮める。
	for avail := width - 1 - textwidth.Width(ext); avail >= 0; avail-- {
		if s := headFit(head, avail) + "~" + ext; textwidth.Width(s) <= width {
			return s
		}
	}
	return headFit("~", width)
}

// TruncPath は、パスを表示幅 width に収める。収まらなければ、ボリュームのルートの後ろの先頭側の要素を ~ にする（C:\~\projects\tana。filer §9.2）。
// 最後の要素も入らなければ、~ とその後ろ側を残す。表示のためだけの文字列で、パスを作ってはいけない（filer U4）。
func TruncPath(path string, width int) string {
	if width <= 0 {
		return ""
	}
	if textwidth.Width(path) <= width {
		return path
	}
	sep := string(filepath.Separator)
	vol := filepath.VolumeName(path)
	rest := path[len(vol):]
	root := vol
	if strings.HasPrefix(rest, sep) || strings.HasPrefix(rest, "/") {
		root, rest = vol+rest[:1], rest[1:]
	}
	parts := strings.FieldsFunc(rest, func(r rune) bool { return r == filepath.Separator || r == '/' })
	for i := 1; i < len(parts); i++ {
		if s := root + "~" + sep + strings.Join(parts[i:], sep); textwidth.Width(s) <= width {
			return s
		}
	}
	last := path
	if len(parts) > 0 {
		last = parts[len(parts)-1]
	}
	return "~" + tailFit(last, width-1)
}

// Escape は、状態行のための表記を返す（filer §9.3）。一覧で ? や簡略な形に置き換える書記素クラスタを、
// 符号位置（\x1b、\u202e、\u{1f468}）と不正なバイト（\xff）の形にする。ほかの書記素クラスタはそのまま。
// 置き換えるものがあるときは、もとの \ を \\ にする（\u202e という文字列と、置き換えた U+202E を区別するため）。
func Escape(name string) string {
	if !HasReplaced(name) {
		return name
	}
	var b strings.Builder
	for c := range textwidth.All(name) {
		if c.Class == textwidth.Normal {
			b.WriteString(strings.ReplaceAll(c.Text, `\`, `\\`))
			continue
		}
		for i := 0; i < len(c.Text); {
			r, size := utf8.DecodeRuneInString(c.Text[i:])
			switch {
			case r == utf8.RuneError && size <= 1:
				fmt.Fprintf(&b, `\x%02x`, c.Text[i])
				size = 1
			case r >= 0x20 && r < 0x7f:
				b.WriteRune(r) // 印刷できる ASCII（キーキャップの数字など）はそのまま
			case r < 0x80:
				fmt.Fprintf(&b, `\x%02x`, r)
			case r <= 0xffff:
				fmt.Fprintf(&b, `\u%04x`, r)
			default:
				fmt.Fprintf(&b, `\u{%x}`, r)
			}
			i += size
		}
	}
	return b.String()
}

// HasReplaced は、一覧で ? や簡略な形に置き換える書記素クラスタを含むかを返す（色を変えて出すため。filer §9.3）。
func HasReplaced(name string) bool {
	for c := range textwidth.All(name) {
		if c.Class != textwidth.Normal {
			return true
		}
	}
	return false
}

package tui

import "github.com/zredjet/tana/internal/screen"

// theme は、見た目を意味の名前で持つ表（filer §4。G15）。描き方は、色や属性を直接書かずに、ここから引く。
// 後の版で色を変えるときは、この表を差し替える（設定ファイル。filer §14）。
// 色だけで区別しないので、種類はサイズの欄、マークは * でも分かる（filer §9.5）。
type theme struct {
	dir, junction, link, special screen.Style // 項目の種類（filer §9.5）
	err                          screen.Style // エラー（読めなかった項目、エラーのメッセージ）
	marked                       screen.Style // マークした項目
	replaced                     screen.Style // 表示で置き換えた文字（filer §9.3）
	dim, bold                    screen.Style
	warn                         screen.Style // 注意（警告、計画を作成中）
	problem                      screen.Style // 問題（実行できない、応答がない、完了でない結果）
	indicator                    screen.Style // 状態行の右の印（覚えている項目の数）
	cursor                       screen.Attr  // カーソル行に重ねる属性
	cursorInactive               screen.Attr  // 操作中でないペインのカーソル行に重ねる属性
}

// defaultTheme は、既定の見た目。
var defaultTheme = theme{
	dir:      screen.Style{FG: screen.ColorBlue, Attr: screen.AttrBold},
	junction: screen.Style{FG: screen.ColorMagenta, Attr: screen.AttrBold},
	link:     screen.Style{FG: screen.ColorCyan},
	special:  screen.Style{FG: screen.ColorYellow},
	err:      screen.Style{FG: screen.ColorRed},
	marked:   screen.Style{FG: screen.ColorYellow, Attr: screen.AttrBold},
	replaced: screen.Style{FG: screen.ColorBrightRed},
	dim:      screen.Style{Attr: screen.AttrDim},
	bold:     screen.Style{Attr: screen.AttrBold},
	warn:     screen.Style{FG: screen.ColorYellow, Attr: screen.AttrBold},
	problem:  screen.Style{FG: screen.ColorRed, Attr: screen.AttrBold},

	indicator:      screen.Style{Attr: screen.AttrReverse},
	cursor:         screen.AttrReverse,
	cursorInactive: screen.AttrUnderline,
}

// th は、描くときに使う見た目の表（今は既定のもの）。
func (f *Filer) th() *theme { return &defaultTheme }

package msg

import "strconv"

// 閲覧の画面の文言（filer §5・§6・§7）。幅が曖昧な文字（…・→ など）は使わない（conhost で 2 桁になる。filer §9.1）。
const (
	LoadCanceled   = "読み込みを中止しました"
	CannotOpenName = "この名前のファイルは開けません"
	ExecConfirm    = "実行しますか"
	TooSmall       = "端末が小さすぎます"
	HelpTitle      = "キー操作（何かキーを押すと閉じます）"
)

// 文の中のキーの名前は、keymap の表から渡す（割り当てを変えても、文がずれないように。filer §4）。

// Loading は、読み込み中の表示（filer §6）。esc は、読み込みを中止するキー。
func Loading(esc string) string { return "読み込み中...（" + esc + " で中止）" }

// PathInputTitle は、パスの入力欄の題名。enter・esc は、移動するキーと中止するキー。
func PathInputTitle(enter, esc string) string {
	return "移動先のパス（" + enter + " で移動、" + esc + " で中止）"
}

// CannotOpenDir は、フォルダに入れなかったときの文言（filer §6）。reason は Error の文言。
func CannotOpenDir(reason string) string { return "このフォルダを開けません: " + reason }

// ShowingAncestor は、表示するはずのフォルダを開けず、祖先のフォルダを表示したときの文言（filer §6）。
func ShowingAncestor(reason string) string {
	return "フォルダを開けないため、開ける親のフォルダを表示しました: " + reason
}

// Opened は、関連付けで開いたときの文言。
func Opened(name string) string { return "開きました: " + name }

// OpenFailed は、関連付けで開けなかったときの文言。英語の詳細はログに書く（filer §10）。
func OpenFailed(name string) string { return "開けませんでした: " + name }

// Items は、ペインの枠の下に出す項目数（.. は数えない）とマークの数と、隠している項目の数（filer §5.1）。
func Items(n, marks, hidden int) string {
	s := strconv.Itoa(n) + " 項目"
	if marks > 0 {
		s += "  マーク " + strconv.Itoa(marks)
	}
	if hidden > 0 {
		s += "  隠し " + strconv.Itoa(hidden)
	}
	return s
}

// 状態行の種類（filer §5.1）。
const (
	TypeParent   = "親のフォルダ"
	TypeDir      = "フォルダ"
	TypeJunction = "ジャンクション"
	TypeSymlink  = "リンク"
	TypeSpecial  = "特殊なファイル"
	UnitBytes    = "バイト"
	LinkArrow    = " -> "
)

// 一覧のサイズの欄に出す種類（filer §5.1）。どれも 5 桁（tui のサイズの欄の幅）。
const (
	LabelDir      = "<DIR>"
	LabelJunction = "<JCT>"
	LabelSymlink  = "<LNK>"
	LabelSpecial  = "<SPC>"
	LabelError    = "<ERR>" // 列挙はできたが調べられなかった項目
)

// YankedIndicator は、覚えている項目の数（状態行の右に出す）。
func YankedIndicator(n int) string { return "覚えた項目 " + strconv.Itoa(n) }

// PaneIndicator は、Yazi 風の表示で、どちらのペインを表示しているかの印（[1/2]。filer §5.3）。
func PaneIndicator(i, n int) string { return "[" + strconv.Itoa(i) + "/" + strconv.Itoa(n) + "]" }

// プレビューの文言（filer §6）。
const (
	PreviewEmpty    = "（空のフォルダ）"
	PreviewBinary   = "テキストではないファイルです"
	PreviewNotLocal = "中身が手元にないファイルです（読むと取得が始まるので、プレビューしません）"
)

// 起動（cmd/tana）の文言。
const (
	Usage = "使い方: tana [フォルダ [フォルダ]]\n" +
		"  フォルダを省略すると、今いるフォルダを表示します。\n" +
		"  環境変数 TANA_LOG にファイルのパスを指定すると、エラーの詳細（英語）をそこに書きます。NO_COLOR を設定すると色を使いません。"
	CannotOpenTerminal = "端末を開けません: "
	CannotOpenLog      = "ログのファイルを開けません: "
	InvalidFolder      = "フォルダの指定が正しくありません: "
)

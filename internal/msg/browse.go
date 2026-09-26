package msg

import "strconv"

// 閲覧の画面の文言（filer §5・§6・§7）。幅が曖昧な文字（…・→ など）は使わない（conhost で 2 桁になる。filer §9.1）。
const (
	Loading        = "読み込み中...（Esc で中止）"
	LoadCanceled   = "読み込みを中止しました"
	CannotOpenName = "この名前のファイルは開けません"
	ExecConfirm    = "実行しますか"
	ExecChoices    = "y 実行する   n やめる"
	PathInputTitle = "移動先のパス（Enter で移動、Esc で中止）"
	NotYet         = "この操作はまだ使えません"
	TooSmall       = "端末が小さすぎます"
	HelpTitle      = "キー操作（何かキーを押すと閉じます）"
)

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

// KeyGuide は、画面の最下行のキーの案内（filer §5.1。キーの割り当ては仮。§15）。
const KeyGuide = "Enter 開く  BS 親へ  Tab 切替  Space マーク  g パス  . 隠し  ? ヘルプ  q 終了"

// Help は、ヘルプの画面の行（キー、説明）。
var Help = [][2]string{
	{"Up Down  j k", "カーソルの移動"},
	{"PgUp PgDn Home End", "ページ単位の移動、先頭、末尾"},
	{"Enter", "フォルダに入る。ファイルは関連付けで開く"},
	{"Backspace", "親のフォルダへ"},
	{"Tab", "操作するペインの切り替え"},
	{"Left Right", "左・右のペインへ。すでにその側なら親のフォルダへ"},
	{"Space", "マークの切り替え"},
	{"a", "すべてマークする・すべて外す"},
	{"g", "パスを入力して移動（ドライブの切り替えも）"},
	{"=", "反対側のペインを同じフォルダにする"},
	{".", "隠しファイルの表示の切り替え"},
	{"Ctrl+R", "再読み込み"},
	{"Esc", "読み込みの中止"},
	{"q", "終了"},
	{"c m d D r n L", "（フェーズ19・20で作る）"},
}

// 起動（cmd/tana）の文言。
const (
	Usage = "使い方: tana [フォルダ [フォルダ]]\n" +
		"  フォルダを省略すると、今いるフォルダを表示します。\n" +
		"  環境変数 TANA_LOG にファイルのパスを指定すると、エラーの詳細（英語）をそこに書きます。NO_COLOR を設定すると色を使いません。"
	CannotOpenTerminal = "端末を開けません: "
	CannotOpenLog      = "ログのファイルを開けません: "
	InvalidFolder      = "フォルダの指定が正しくありません: "
)

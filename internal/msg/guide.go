package msg

import "strconv"

// キーの案内とヘルプの説明（filer §4 の「キーの表」）。キーの名前は含めない。キーの名前は keymap の表から作り、
// 「キーの名前 説明」の形で並べる（割り当てを変えても、案内とヘルプがずれないように）。
// 幅が曖昧な文字（…・→ など）は使わない（filer §9.1）。

// 閲覧の画面の最下行の案内（filer §5.1・§7）。
const (
	GuideParent = "親へ"
	GuideSwitch = "切替"
	GuideMark   = "マーク"
	GuideYank   = "覚える"
	GuidePaste  = "貼る"
	GuideView   = "表示"
	GuideHelp   = "ヘルプ"
	GuideQuit   = "終了"
)

// ダイアログとファイル操作の画面の案内（filer §7・§8）。
const (
	GuideExecYes      = "実行する"
	GuideRun          = "実行"
	GuideToConflicts  = "衝突の確認へ"
	GuideStop         = "やめる"
	GuideClose        = "閉じる"
	GuideToPurge      = "完全削除の確認へ"
	GuideDeleteYes    = "完全に削除する"
	GuideRename       = "変更"
	GuideCreate       = "作成"
	GuideCancelYes    = "中止する"
	GuideContinue     = "続ける"
	GuideAbort        = "中止"
	GuideForceQuit    = "終了"
	GuideEnglish      = "英語の詳細"
	GuideNewerOnly    = "新しいときだけ上書き"
	GuideFold         = "展開・折りたたみ"
	GuideUnsetOnly    = "未選択だけ表示"
	GuideConflictsAll = "すべて: "
)

// GuideInside は、結果の画面で、カーソル行のフォルダの中の結果を表示・隠す案内の説明（filer §8.5）。
func GuideInside(n int, shown bool) string {
	if shown {
		return "中の " + strconv.Itoa(n) + " 件を隠す"
	}
	return "中の " + strconv.Itoa(n) + " 件を表示"
}

// ヘルプの行の説明（filer §7）。
const (
	HelpMove      = "カーソルの移動"
	HelpPage      = "ページ単位の移動、先頭、末尾"
	HelpEnter     = "フォルダに入る。ファイルは関連付けで開く"
	HelpParent    = "親のフォルダへ"
	HelpEnterDir  = "フォルダに入る（ファイルでは何もしない）"
	HelpNextPane  = "ペインの切り替え（Yazi 風では表示するペイン）"
	HelpMark      = "マークの切り替え"
	HelpYank      = "対象を覚える（マークした項目か、カーソル行）"
	HelpPaste     = "覚えた項目を、このフォルダへコピー・移動"
	HelpName      = "名前の変更・新しいフォルダ"
	HelpLast      = "直前の操作の結果をもう一度見る"
	HelpMarkAll   = "すべてマークする・すべて外す"
	HelpGoPath    = "パスを入力して移動（ドライブの切り替えも）"
	HelpSyncOther = "反対側のペインを同じフォルダにする"
	HelpHidden    = "隠しファイルの表示の切り替え"
	HelpView      = "表示形式の切り替え（2 ペイン・Yazi 風）"
	HelpReload    = "再読み込み"
	HelpRedraw    = "画面の描き直し（表示が崩れたとき）"
	HelpCancel    = "読み込みの中止"
	HelpQuit      = "終了"
)

// HelpTrash は、ヘルプのごみ箱・完全削除の行の説明。enter は確認画面の実行のキー、y は完全削除の確定のキー。
func HelpTrash(enter, y string) string {
	return "ごみ箱へ（" + enter + " で実行）・完全削除（" + y + " で確定）"
}

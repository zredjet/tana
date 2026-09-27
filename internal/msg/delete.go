package msg

import (
	"errors"
	"strconv"

	"github.com/zredjet/tana/internal/fsops"
)

// ごみ箱・完全削除（filer §8.2・§8.5・§8.6）と、名前の変更・新しいフォルダ（filer §8.7）の文言。
// 幅が曖昧な文字（…・→ など）は使わない（filer §9.1）。
const (
	NoTarget             = "対象の項目がありません"
	TrashUnavailableSkip = "ごみ箱に入らないため実行しません"
	TrashDialog          = "Windows の確認ダイアログが開いている可能性があります（ほかのウィンドウを確認してください）。" +
		"残すには「いいえ」を選んでください。「はい」は完全に削除します"
	DeleteTitle        = "完全削除の確認"
	DeleteQuestion     = "完全に削除しますか？ 元に戻せません。"
	DeleteIrreversible = "元に戻せません。"

	RenameTitle = "名前の変更"
	NewDirTitle = "新しいフォルダ"
)

// RenameBusy は、名前の変更を待っている間の表示（filer §8.7）。esc は、待つのをやめるキー。
func RenameBusy(esc string) string {
	return "変更しています...（" + esc + " で待つのをやめる）"
}

// NewDirBusy は、フォルダの作成を待っている間の表示。
func NewDirBusy(esc string) string {
	return "作成しています...（" + esc + " で待つのをやめる）"
}

// DeleteLead は、完全削除の確認の 1 行目（filer §8.6）。ごみ箱に入らなかった項目から進んだときと、D キーで直接行うときで変える。
func DeleteLead(n int, fromTrash bool) string {
	if fromTrash {
		return "次の " + strconv.Itoa(n) + " 項目はごみ箱に入れられません。"
	}
	return "次の " + strconv.Itoa(n) + " 項目を完全に削除します。"
}

// Place は、項目のあるフォルダ（「場所: D:\backup」）。
func Place(dir string) string { return "場所: " + dir }

// More は、出しきれなかった項目の数（「ほか 3 項目」）。
func More(n int) string { return "ほか " + strconv.Itoa(n) + " 項目" }

// MoreLines は、ダイアログの高さが足りないときに、切り詰めた行の数（キーの案内の行は残す）。
func MoreLines(n int) string { return "ほか " + strconv.Itoa(n) + " 行" }

// NameError は、名前の変更・フォルダの作成のエラーの文言（入力欄の下に出す）。
// フォルダの作成のエラーは移動先の側（OnDest）だが、「コピー先・移動先で: 」は付けない（その画面で作ろうとしている場所なので）。
func NameError(err error) string {
	if oe, ok := errors.AsType[*fsops.OpError](err); ok && oe != nil {
		return Kind(oe.Kind)
	}
	return Kind(fsops.KindUnknown)
}

// NameFailed は、待つのをやめた後に届いた、名前の変更（rename が真）・フォルダの作成のエラーの文言（メッセージ行に出す）。
func NameFailed(rename bool, reason string) string {
	if rename {
		return "名前を変更できませんでした: " + reason
	}
	return "フォルダを作れませんでした: " + reason
}

package msg

import (
	"errors"

	"github.com/zredjet/tana/internal/fsops"
)

// Kind は、fsops のエラーの分類の文言を返す（filer §8.8）。
func Kind(k fsops.Kind) string {
	switch k {
	case fsops.KindNotFound:
		return "見つかりません"
	case fsops.KindExist:
		return "同じ名前のものがあります"
	case fsops.KindPermission:
		return "アクセス権がありません"
	case fsops.KindLocked:
		return "ほかのアプリが使用中です"
	case fsops.KindReadOnly:
		return "読み取り専用です"
	case fsops.KindNoSpace:
		return "空き容量が足りません"
	case fsops.KindNotEmpty:
		return "フォルダが空でないため削除できません"
	case fsops.KindLinkUnsupported:
		return "この場所にはリンクを作れません"
	case fsops.KindUnsupportedType:
		return "この種類のファイルは扱えません"
	case fsops.KindTrashUnavailable:
		return "ごみ箱に入れられません"
	case fsops.KindDestInsideSource:
		return "コピー先がコピー元のフォルダの中にあります"
	case fsops.KindSameFile:
		return "移動元と移動先が同じです"
	case fsops.KindSourceChanged:
		return "処理中に変更されました"
	case fsops.KindDestChanged:
		return "処理中にコピー先・移動先のフォルダやファイルが変更されました"
	case fsops.KindInvalidName:
		return "この名前は使えません"
	case fsops.KindNameForm:
		return "文字の表現（NFC・NFD）の違いで、このボリュームでは扱えない名前です"
	case fsops.KindInvalidRequest:
		return "指定が正しくありません"
	case fsops.KindCanceled:
		return "中止しました"
	case fsops.KindMetadata:
		return "更新日時などを引き継げませんでした（データは無事です）"
	case fsops.KindFileTooLarge:
		return "コピー先のファイルシステムには大きすぎるファイルです"
	case fsops.KindMountPoint:
		return "別のボリュームがマウントされたフォルダです（中は削除しません）"
	case fsops.KindVerifyFailed:
		return "コピーした内容が元と一致しませんでした"
	case fsops.KindSyncFailed:
		return "ディスクへの書き込みを確定できませんでした"
	case fsops.KindLinkSkipped:
		return "リンクは設定によりコピーしませんでした"
	case fsops.KindUnreachable:
		return "場所に接続できません（ネットワークやドライブが応答しません）"
	}
	// KindUnknown と、結果に現れない KindCrossDevice（filer §8.8）。
	return "予期しないエラーです"
}

// Error は、エラーの文言を返す。fsops の OpError なら Kind の文言にし、コピー先・移動先の側で起きたものには印を付ける（filer §8.8）。
func Error(err error) string {
	oe, ok := errors.AsType[*fsops.OpError](err)
	if !ok || oe == nil {
		return Kind(fsops.KindUnknown)
	}
	s := Kind(oe.Kind)
	if oe.OnDest {
		s = "コピー先・移動先で: " + s
	}
	return s
}

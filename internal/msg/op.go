package msg

import (
	"strconv"

	"github.com/zredjet/tana/internal/fsops"
)

// 操作・段階・結果・衝突の決定の文言（filer §8）。fsops はメッセージを作らないので、ここで作る（fsops §17）。
// 幅が曖昧な文字（…・→ など）は使わない（filer §9.1）。

// Op は、操作の名前（コピー・移動など）。
func Op(op fsops.OpKind) string {
	switch op {
	case fsops.OpCopy:
		return "コピー"
	case fsops.OpMove:
		return "移動"
	case fsops.OpTrash:
		return "ごみ箱へ"
	case fsops.OpDelete:
		return "完全削除"
	}
	return ""
}

// Stage は、進捗の段階（進捗の画面の題名。filer §8.4）。
func Stage(s fsops.Stage) string {
	switch s {
	case fsops.StageCopy:
		return "コピー中"
	case fsops.StageMove:
		return "移動中"
	case fsops.StageVerify:
		return "検証中"
	case fsops.StageRemoveSource:
		return "移動元を削除中"
	case fsops.StageTrash:
		return "ごみ箱へ移動中"
	case fsops.StageDelete:
		return "削除中"
	}
	return ""
}

// Outcome は、項目の結果（filer §8.5）。
func Outcome(o fsops.Outcome) string {
	switch o {
	case fsops.OutcomeDone:
		return "完了"
	case fsops.OutcomeSkipped:
		return "スキップ"
	case fsops.OutcomeFailed:
		return "失敗"
	case fsops.OutcomePartial:
		return "一部だけ"
	case fsops.OutcomeCopiedSourceKept:
		return "移動元を残した"
	case fsops.OutcomeTrashUnconfirmed:
		return "ごみ箱に入ったか確かめられない"
	}
	return ""
}

// Status は、操作の全体の結果（filer §8.5）。
func Status(s fsops.Status) string {
	switch s {
	case fsops.StatusCompleted:
		return "完了"
	case fsops.StatusCompletedWithErrors:
		return "一部にエラーがあります"
	case fsops.StatusCanceled:
		return "中止しました"
	}
	return ""
}

// Decision は、衝突の決定（filer §8.3）。
func Decision(d fsops.Decision) string {
	switch d {
	case fsops.DecisionUnset:
		return "未選択"
	case fsops.DecisionSkip:
		return "スキップ"
	case fsops.DecisionOverwrite:
		return "上書き"
	case fsops.DecisionAutoRename:
		return "自動リネーム"
	case fsops.DecisionMerge:
		return "マージ"
	}
	return ""
}

// Method は、項目の処理の方式。
func Method(m fsops.Method) string {
	switch m {
	case fsops.MethodRename:
		return "同じボリューム内の移動"
	case fsops.MethodCopy:
		return "コピー"
	case fsops.MethodCopyThenRemove:
		return "ボリュームをまたぐ移動"
	case fsops.MethodTrash:
		return "ごみ箱"
	case fsops.MethodRemove:
		return "完全削除"
	}
	return ""
}

// Type は、エントリの種類。
func Type(t fsops.EntryType) string {
	switch t {
	case fsops.TypeFile:
		return "ファイル"
	case fsops.TypeDir:
		return TypeDir
	case fsops.TypeSymlink:
		return TypeSymlink
	case fsops.TypeJunction:
		return TypeJunction
	case fsops.TypeSpecial:
		return TypeSpecial
	}
	return ""
}

// Partial は、一部だけ（OutcomePartial）と移動元を残した（OutcomeCopiedSourceKept）が表す状態を、実際に使った方式に合わせて説明する
// （filer §8.5、fsops §7.4）。それ以外は空。
func Partial(m fsops.Method, o fsops.Outcome) string {
	switch {
	case o == fsops.OutcomeCopiedSourceKept:
		return "移動先は完成しています。移動元の一部を残しました（データは無事です）"
	case o != fsops.OutcomePartial:
		return ""
	case m == fsops.MethodCopy:
		return "コピー先に途中までの結果があります。コピー元は変わっていません"
	case m == fsops.MethodRename:
		return "一部は移動先へ移り、下の一覧のものは移動元に残っています"
	case m == fsops.MethodCopyThenRemove:
		return "移動元はすべて残っています。移動先に途中までコピーしたものがあります"
	case m == fsops.MethodRemove:
		return "一部は削除され、下の一覧のものは残っています"
	}
	return ""
}

// ResultError は、実行の結果のエラーの文言。結果に固有の言い方がある Kind は、それを使う（filer §8.8 の補足）。
func ResultError(o fsops.Outcome, err *fsops.OpError) string {
	if err == nil {
		return SkippedByChoice
	}
	switch {
	case err.Kind == fsops.KindExist:
		return "計画の後に同じ名前のものができたため、スキップしました"
	case err.Kind == fsops.KindNoSpace && o == fsops.OutcomeSkipped:
		return "空き容量が足りないため、実行しませんでした"
	case err.Kind == fsops.KindSourceChanged && o == fsops.OutcomeCopiedSourceKept:
		return "コピーの後に変更されたため残しました"
	case o == fsops.OutcomeTrashUnconfirmed:
		return "元の場所から消えましたが、ごみ箱に入ったことを確かめられません（完全に削除された可能性があります）"
	}
	return Error(err)
}

// SkippedByChoice は、衝突の決定によるスキップ（Err が nil）の文言。エラーと区別する（filer §8.5）。
const SkippedByChoice = "選択によりスキップ"

// Count は、件数の後に「件」を付ける。
func Count(n int) string { return strconv.Itoa(n) + " 件" }

package main

import (
	"errors"
	"fmt"

	"github.com/zredjet/tana/internal/fsops"
)

// 表示用の日本語。fsops はメッセージを作らないので（SPEC §17）、Kind などから CLI が作る。

func opName(op fsops.OpKind) string {
	switch op {
	case fsops.OpCopy:
		return "copy"
	case fsops.OpMove:
		return "move"
	case fsops.OpTrash:
		return "trash"
	case fsops.OpDelete:
		return "delete"
	}
	return "unknown"
}

func opText(op fsops.OpKind) string {
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
	return op.String()
}

func methodText(m fsops.Method) string {
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
	return m.String()
}

func typeText(t fsops.EntryType) string {
	switch t {
	case fsops.TypeFile:
		return "ファイル"
	case fsops.TypeDir:
		return "フォルダ"
	case fsops.TypeSymlink:
		return "シンボリックリンク"
	case fsops.TypeJunction:
		return "ジャンクション"
	case fsops.TypeSpecial:
		return "特殊なファイル"
	}
	return t.String()
}

func conflictKindText(c fsops.Conflict) string {
	if c.Self {
		return "同じフォルダへのコピー"
	}
	return typeText(c.SrcInfo.Type) + " → 既存の" + typeText(c.DstInfo.Type)
}

func decisionText(d fsops.Decision) string {
	switch d {
	case fsops.DecisionUnset:
		return "未設定（スキップ）"
	case fsops.DecisionSkip:
		return "スキップ"
	case fsops.DecisionOverwrite:
		return "上書き"
	case fsops.DecisionAutoRename:
		return "名前を変えて残す"
	case fsops.DecisionMerge:
		return "マージ"
	}
	return d.String()
}

func stageText(s fsops.Stage) string {
	switch s {
	case fsops.StageCopy:
		return "コピー"
	case fsops.StageMove:
		return "移動"
	case fsops.StageVerify:
		return "検証"
	case fsops.StageRemoveSource:
		return "移動元の削除"
	case fsops.StageTrash:
		return "ごみ箱"
	case fsops.StageDelete:
		return "完全削除"
	}
	return s.String()
}

func statusText(s fsops.Status) string {
	switch s {
	case fsops.StatusCompleted:
		return "完了"
	case fsops.StatusCompletedWithErrors:
		return "完了（一部にエラーあり）"
	case fsops.StatusCanceled:
		return "キャンセルしました"
	}
	return s.String()
}

func outcomeText(o fsops.Outcome) string {
	switch o {
	case fsops.OutcomeDone:
		return "完了"
	case fsops.OutcomeSkipped:
		return "スキップ"
	case fsops.OutcomeFailed:
		return "失敗"
	case fsops.OutcomePartial:
		return "一部"
	case fsops.OutcomeCopiedSourceKept:
		return "移動元を残した"
	}
	return o.String()
}

// kindText は、エラーの分類を利用者向けの文にする。
func kindText(k fsops.Kind) string {
	switch k {
	case fsops.KindNotFound:
		return "見つかりません"
	case fsops.KindExist:
		return "同じ名前のものが既にあります（上書きしませんでした）"
	case fsops.KindPermission:
		return "権限がありません"
	case fsops.KindLocked:
		return "ほかのプログラムが使用中です"
	case fsops.KindReadOnly:
		return "読み取り専用です"
	case fsops.KindNoSpace:
		return "空き容量が足りません"
	case fsops.KindNotEmpty:
		return "フォルダが空ではありません"
	case fsops.KindCrossDevice:
		return "別のボリュームです"
	case fsops.KindLinkUnsupported:
		return "リンクを作成・複製できません"
	case fsops.KindUnsupportedType:
		return "この種類のファイルは扱えません"
	case fsops.KindTrashUnavailable:
		return "ごみ箱に入れられません"
	case fsops.KindDestInsideSource:
		return "コピー先・移動先が、コピー元・移動元の中にあります"
	case fsops.KindSameFile:
		return "移動元と移動先が同じ場所です"
	case fsops.KindSourceChanged:
		return "処理中に元のファイル・フォルダが変更されました"
	case fsops.KindInvalidName:
		return "名前が使えないか、長すぎます"
	case fsops.KindInvalidRequest:
		return "指定が正しくありません"
	case fsops.KindCanceled:
		return "キャンセルしました"
	case fsops.KindMetadata:
		return "更新日時などの情報を保持できませんでした（データは無事です）"
	case fsops.KindFileTooLarge:
		return "コピー先のファイルシステムには大きすぎるファイルです（FAT32 は 4 GB 未満まで）"
	}
	return "原因不明のエラーです"
}

// errorText は、エラーを「分類の文（元のエラー）」の形にする。
func errorText(err error) string {
	var oe *fsops.OpError
	if !errors.As(err, &oe) || oe == nil {
		return err.Error()
	}
	s := kindText(oe.Kind)
	if oe.Err != nil {
		s += fmt.Sprintf("（%v）", oe.Err)
	}
	return s
}

// sizeText は、バイト数を読みやすい単位で返す。
func sizeText(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	f, suffix := float64(n), ""
	for _, s := range []string{"KiB", "MiB", "GiB", "TiB"} {
		f /= unit
		suffix = s
		if f < unit {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", f, suffix)
}

package msg

import (
	"strconv"
	"strings"

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
		return "移動先は完成。移動元の一部を残しました"
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

// コピー・移動の流れの文言（filer §8.1〜§8.5）。
const (
	Planning            = "計画を作成中...（Esc で中止）"
	NothingToYank       = "覚える項目がありません"
	NothingYanked       = "覚えた項目がありません（y で覚えます）"
	DestNotFound        = "コピー先のフォルダが見つかりません"
	SpaceWarning        = "空き容量が足りない見込みです"
	NothingRunnable     = "実行できる項目がありません"
	ConfirmKeys         = "Enter 実行   Esc やめる"
	ConfirmKeysConflict = "Enter 衝突の確認へ   Esc やめる"
	ConfirmKeysNone     = "Esc 閉じる"
	UnsetIsSkip         = "未選択はスキップします"
	InnerHidden         = "マージを選ぶと表示します"
	InnerCollapsed      = "Space で表示します"
	CancelTitle         = "中止の確認"
	CancelQuestion      = "中止しますか？"
	CancelNoPartial     = "書きかけのファイルは残りません。"
	CancelDoneKept      = "完了した項目はそのまま残ります。"
	CancelChoices       = "y 中止する   n 続ける"
	Canceling           = "中止しています..."
	Unresponsive        = "応答がありません。Q で終了できますが、次のものが残る場合があります:"
	ProgressKeys        = "Esc 中止"
	ForceQuitKey        = "Q 終了"
	ResultKeys          = "e 英語の詳細   Enter 閉じる"
	EnglishTitle        = "英語の詳細（ログと同じ。e で閉じる）"
	NoEnglish           = "この行にはエラーがありません"
	NewerMark           = "新"
	MetadataWarning     = "一部の属性を引き継げませんでした"
)

// UnresponsiveLeftovers は、応答がなくなったまま終了したときに残りうるもの（fsops の doc.go の「保証すること」）。
var UnresponsiveLeftovers = []string{
	"・移動元と移動先の両方に同じものがある、または一部だけ移動されている",
	"・.fsops-<16 進>.tmp という一時ファイル",
}

// Yanked は、y で覚えたときの文言。
func Yanked(n int) string {
	return strconv.Itoa(n) + " 項目を覚えました（p でコピー、P で移動）"
}

// CannotPlan は、計画を作れなかったとき（リクエスト全体の問題。filer §8.1）の文言。
func CannotPlan(reason string) string { return "計画を作れません: " + reason }

// CannotExecute は、実行できなかったときの文言。
func CannotExecute(reason string) string { return "実行できません: " + reason }

// ConfirmSummary は、確認画面の 1 行目（「3 項目を D:\backup へコピーします」）。
func ConfirmSummary(op fsops.OpKind, n int, dest string) string {
	return strconv.Itoa(n) + " 項目を " + dest + " へ" + Op(op) + "します"
}

// Totals は、合計のファイル数とバイト数（バイト数が 0 なら出さない。filer §8.2）。size はサイズの書式にしたもの。
func Totals(files int, size string) string {
	s := "合計 " + strconv.Itoa(files) + " ファイル"
	if size != "" {
		s += "、" + size
	}
	return s
}

// NotRunnable は、実行されない項目の数。
func NotRunnable(n int) string { return strconv.Itoa(n) + " 項目は実行しません" }

// Conflicts は、衝突の件数（「衝突 125 件（トップレベル 3 件）」）。
func Conflicts(all, top int) string {
	return "衝突 " + strconv.Itoa(all) + " 件（トップレベル " + strconv.Itoa(top) + " 件）"
}

// Unset は、未選択の件数（「未選択 122 件（未選択はスキップします）」。U1）。
func Unset(n int) string { return "未選択 " + strconv.Itoa(n) + " 件（" + UnsetIsSkip + "）" }

// Inner は、折りたたんだ内側の衝突の行（「（内側の衝突 120 件。マージを選ぶと表示します）」）。
func Inner(n int, how string) string {
	return "（内側の衝突 " + strconv.Itoa(n) + " 件。" + how + "）"
}

// NotChanged は、まとめての決定で、種類が合わずに変えなかった件数。
func NotChanged(n int) string {
	return strconv.Itoa(n) + " 件は種類が合わないため変更しませんでした"
}

// DecisionNotAllowed は、その衝突で使えない決定を選んだときの理由（filer §8.3）。
func DecisionNotAllowed(d fsops.Decision) string {
	switch d {
	case fsops.DecisionOverwrite:
		return "上書きは、ファイル同士の衝突でだけ使えます"
	case fsops.DecisionMerge:
		return "マージは、フォルダ同士の衝突でだけ使えます"
	}
	return "この決定はこの衝突には使えません"
}

// ConflictHeader は、衝突の画面の 1 行目（「衝突の確認   コピー  C:\x -> D:\y」）。
func ConflictHeader(op fsops.OpKind, from, to string) string {
	return "衝突の確認   " + Op(op) + "  " + from + " -> " + to
}

// ConflictColumns は、衝突の一覧の見出し。
var ConflictColumns = [4]string{"名前", "コピー元", "コピー先", "決定"}

// ConflictKeys は、衝突の画面のキーの案内（3 行）。
var ConflictKeys = [3]string{
	"この行: s スキップ  o 上書き  r 自動リネーム  m マージ",
	"すべて: S スキップ  O 上書き  N 新しいときだけ上書き  R 自動リネーム  M マージ",
	"Enter 実行  Esc やめる  Space 展開・折りたたみ  u 未選択だけ表示",
}

// ProgressFiles は、進捗のファイル数とバイト数（「ファイル 26 / 58      597M / 1.3G」）。
func ProgressFiles(done, total int, doneSize, totalSize string) string {
	s := "ファイル " + strconv.Itoa(done) + " / " + strconv.Itoa(total)
	if totalSize != "" {
		s += "      " + doneSize + " / " + totalSize
	}
	return s
}

// ProgressTimes は、速度・残り時間・経過時間（「毎秒 85M   残り 約 9 秒   経過 7 秒」）。空の値は出さない。
func ProgressTimes(speed, remaining, elapsed string) string {
	var parts []string
	if speed != "" {
		parts = append(parts, "毎秒 "+speed)
	}
	if remaining != "" {
		parts = append(parts, "残り 約 "+remaining)
	}
	parts = append(parts, "経過 "+elapsed)
	return strings.Join(parts, "   ")
}

// Duration は、時間を「9 秒」「2 分 5 秒」「1 時間 3 分」の形にする。
func Duration(sec int) string {
	switch {
	case sec < 60:
		return strconv.Itoa(sec) + " 秒"
	case sec < 3600:
		return strconv.Itoa(sec/60) + " 分 " + strconv.Itoa(sec%60) + " 秒"
	}
	return strconv.Itoa(sec/3600) + " 時間 " + strconv.Itoa(sec%3600/60) + " 分"
}

// ResultTitle は、結果の画面の 1 行目（「移動の結果: 一部にエラーがあります   C:\x -> D:\y」）。
func ResultTitle(op fsops.OpKind, status string, from, to string) string {
	return Op(op) + "の結果: " + status + "   " + from + " -> " + to
}

// OutcomeCount は、結果の件数の 1 つ（「失敗 1」）。
func OutcomeCount(o fsops.Outcome, n int) string { return Outcome(o) + " " + strconv.Itoa(n) }

// InsideKey は、カーソル行のフォルダの中の結果を、Space で表示・隠す案内（結果の画面のキーの案内。filer §8.5）。
func InsideKey(n int, shown bool) string {
	if shown {
		return "Space 中の " + strconv.Itoa(n) + " 件を隠す"
	}
	return "Space 中の " + strconv.Itoa(n) + " 件を表示"
}

// SkippedInside は、フォルダの中の選択によるスキップの件数（「（選択によるスキップ 1 件）」）。
func SkippedInside(n int) string {
	return "（選択によるスキップ " + strconv.Itoa(n) + " 件）"
}

// Done は、結果の画面を出さないときのメッセージ（「3 項目をコピーしました」「（選択によるスキップ 1 件）」「一部の属性を…（L で詳細）」）。
func Done(op fsops.OpKind, done, skipped int, warned bool) string {
	s := strconv.Itoa(done) + " 項目を" + Op(op) + "しました"
	if skipped > 0 {
		s += SkippedInside(skipped)
	}
	if warned {
		s += "。" + MetadataWarning + "（L で詳細）"
	}
	return s
}

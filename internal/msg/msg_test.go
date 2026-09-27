package msg

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops"
)

// allKinds は、fsops の Kind をすべて返す（String が "Kind(n)" になる最初の値の手前まで）。
func allKinds() []fsops.Kind {
	var ks []fsops.Kind
	for k := fsops.KindUnknown; !strings.HasPrefix(k.String(), "Kind("); k++ {
		ks = append(ks, k)
	}
	return ks
}

// TestKindTextComplete は、すべての Kind に文言があることを確かめる（filer §8.8）。
func TestKindTextComplete(t *testing.T) {
	t.Parallel()
	ks := allKinds()
	if len(ks) < 25 {
		t.Fatalf("only %d kinds found", len(ks))
	}
	unknown := Kind(fsops.KindUnknown)
	for _, k := range ks {
		text := Kind(k)
		if text == "" {
			t.Errorf("%v: no text", k)
		}
		// KindCrossDevice は結果に現れないので、KindUnknown と同じに扱う（filer §8.8）。それ以外は固有の文言を持つ。
		if (text == unknown) != (k == fsops.KindUnknown || k == fsops.KindCrossDevice) {
			t.Errorf("%v: text %q (unknown %q)", k, text, unknown)
		}
	}
}

// TestError は、エラーの文言と、コピー先・移動先の印（filer §8.8）を確かめる。
func TestError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  error
		want string
	}{
		{&fsops.OpError{Kind: fsops.KindPermission}, "アクセス権がありません"},
		{&fsops.OpError{Kind: fsops.KindNoSpace, OnDest: true}, "コピー先・移動先で: 空き容量が足りません"},
		{fmt.Errorf("wrapped: %w", &fsops.OpError{Kind: fsops.KindNotFound}), "見つかりません"},
		{errors.New("plain"), "予期しないエラーです"},
	}
	for _, tt := range tests {
		if got := Error(tt.err); got != tt.want {
			t.Errorf("Error(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

// TestBrowseTextsWidth は、画面の文言に幅が曖昧な文字（conhost で 2 桁になる）を使っていないことを確かめる（filer §9.1）。
func TestBrowseTextsWidth(t *testing.T) {
	// キーの案内とヘルプは、キーの名前と、ここのラベルから keymap が作る（keymap のテストでも確かめる）。
	texts := []string{Loading("Esc"), LoadCanceled, CannotOpenName, ExecConfirm, PathInputTitle("Enter", "Esc"), TooSmall, HelpTitle,
		CannotOpenDir(""), ShowingAncestor(""), Opened(""), OpenFailed(""), Items(1, 2, 3),
		TypeParent, TypeDir, TypeJunction, TypeSymlink, TypeSpecial, UnitBytes, LinkArrow, YankedIndicator(3),
		Planning("Esc"), NothingToYank, NothingYanked, DestNotFound, SpaceWarning, NothingRunnable,
		UnsetIsSkip, InnerHidden, InnerCollapsed, CancelTitle, CancelQuestion,
		Canceling, Unresponsive("Q"), NewerMark, MetadataWarning, Yanked(2),
		EnglishTitle("e"), NoEnglish, ConflictRowTitle, ConflictColumns[0], ConflictColumns[1], ConflictColumns[2], ConflictColumns[3],
		ConflictHeader(fsops.OpCopy, "a", "b"), ResultTitle(fsops.OpMove, "x", "a", "b"), Done(fsops.OpCopy, 1, 2, true),
		PaneIndicator(1, 2), PreviewEmpty, PreviewBinary, PreviewNotLocal,
		LabelDir, LabelJunction, LabelSymlink, LabelSpecial, LabelError,
		NoTarget, TrashUnavailableSkip, TrashDialog, DeleteTitle,
		DeleteQuestion, DeleteIrreversible, RenameTitle, RenameBusy("Esc"), NewDirTitle, NewDirBusy("Esc"),
		DeleteLead(2, true), DeleteLead(2, false), Place("a"), More(3), NameFailed(true, ""), NameFailed(false, ""),
		GuideParent, GuideSwitch, GuideMark, GuideYank, GuidePaste, GuideView, GuideHelp, GuideQuit,
		GuideExecYes, GuideRun, GuideToConflicts, GuideStop, GuideClose, GuideToPurge, GuideDeleteYes, GuideRename, GuideCreate,
		GuideCancelYes, GuideContinue, GuideAbort, GuideForceQuit, GuideEnglish, GuideNewerOnly, GuideFold, GuideUnsetOnly,
		GuideConflictsAll, GuideInside(2, false), GuideInside(2, true),
		HelpMove, HelpPage, HelpEnter, HelpParent, HelpEnterDir, HelpNextPane, HelpMark, HelpYank, HelpPaste, HelpTrash("Enter", "y"),
		HelpName, HelpLast, HelpMarkAll, HelpGoPath, HelpSyncOther, HelpHidden, HelpView, HelpReload, HelpRedraw, HelpCancel, HelpQuit}
	for op := fsops.OpCopy; op <= fsops.OpDelete; op++ {
		texts = append(texts, Did(op), ConfirmSummary(op, 2, "a"), ResultTitle(op, "x", "a", ""))
		texts = append(texts, CancelNotes(op)...)
		texts = append(texts, Leftovers(op)...)
	}
	for _, s := range texts {
		if i := strings.IndexAny(s, "…→←↑↓○●※×①②③◆■□△▲"); i >= 0 {
			t.Errorf("%q contains an ambiguous-width character at %d (filer §9.1)", s, i)
		}
	}
}

// TestLabelsWidth は、サイズの欄の種類がどれも 5 桁（ASCII の 5 文字）であることを確かめる（欄の幅は tui が 5 桁で取る）。
func TestLabelsWidth(t *testing.T) {
	for _, l := range []string{LabelDir, LabelJunction, LabelSymlink, LabelSpecial, LabelError} {
		if len(l) != 5 || strings.ContainsFunc(l, func(r rune) bool { return r < 0x20 || r > 0x7e }) {
			t.Errorf("%q is not 5 printable ASCII characters", l)
		}
	}
}

// TestOpTextsComplete は、すべての操作・段階・結果・状態・決定・方式・種類に文言があることを確かめる（filer §8.8）。
// 値の並びは、String() が "型(n)" の形を返すところで終わる。
func TestOpTextsComplete(t *testing.T) {
	check := func(name string, first, last int, str func(int) string, text func(int) string) {
		t.Helper()
		n := 0
		for v := first; ; v++ {
			if s := str(v); strings.Contains(s, "(") {
				break
			}
			n++
			if text(v) == "" {
				t.Errorf("%s: no text for %s", name, str(v))
			}
		}
		if n != last-first+1 {
			t.Errorf("%s: %d values, want %d", name, n, last-first+1)
		}
	}
	check("Op", int(fsops.OpCopy), int(fsops.OpDelete),
		func(v int) string { return fsops.OpKind(v).String() }, func(v int) string { return Op(fsops.OpKind(v)) })
	check("Stage", int(fsops.StageCopy), int(fsops.StageDelete),
		func(v int) string { return fsops.Stage(v).String() }, func(v int) string { return Stage(fsops.Stage(v)) })
	check("Outcome", int(fsops.OutcomeDone), int(fsops.OutcomeTrashUnconfirmed),
		func(v int) string { return fsops.Outcome(v).String() }, func(v int) string { return Outcome(fsops.Outcome(v)) })
	check("Status", int(fsops.StatusCompleted), int(fsops.StatusCanceled),
		func(v int) string { return fsops.Status(v).String() }, func(v int) string { return Status(fsops.Status(v)) })
	check("Decision", int(fsops.DecisionUnset), int(fsops.DecisionMerge),
		func(v int) string { return fsops.Decision(v).String() }, func(v int) string { return Decision(fsops.Decision(v)) })
	check("Method", int(fsops.MethodRename), int(fsops.MethodRemove),
		func(v int) string { return fsops.Method(v).String() }, func(v int) string { return Method(fsops.Method(v)) })
	check("EntryType", int(fsops.TypeFile), int(fsops.TypeSpecial),
		func(v int) string { return fsops.EntryType(v).String() }, func(v int) string { return Type(fsops.EntryType(v)) })
	for _, m := range []fsops.Method{fsops.MethodRename, fsops.MethodCopy, fsops.MethodCopyThenRemove, fsops.MethodRemove} {
		if Partial(m, fsops.OutcomePartial) == "" {
			t.Errorf("Partial(%v): no text", m)
		}
	}
	if Partial(fsops.MethodCopy, fsops.OutcomeDone) != "" || Partial(fsops.MethodCopyThenRemove, fsops.OutcomeCopiedSourceKept) == "" {
		t.Error("Partial for Done / CopiedSourceKept")
	}
}

// TestResultError は、結果に固有の言い方と、衝突の決定によるスキップを確かめる（filer §8.5・§8.8）。
func TestResultError(t *testing.T) {
	for _, tt := range []struct {
		o    fsops.Outcome
		err  *fsops.OpError
		want string
	}{
		{fsops.OutcomeSkipped, nil, SkippedByChoice},
		{fsops.OutcomeSkipped, &fsops.OpError{Kind: fsops.KindExist}, "計画の後に同じ名前のものができたため、スキップしました"},
		{fsops.OutcomeSkipped, &fsops.OpError{Kind: fsops.KindNoSpace}, "空き容量が足りないため、実行しませんでした"},
		{fsops.OutcomeFailed, &fsops.OpError{Kind: fsops.KindNoSpace}, "空き容量が足りません"},
		{fsops.OutcomeCopiedSourceKept, &fsops.OpError{Kind: fsops.KindSourceChanged}, "コピーの後に変更されたため残しました"},
		{fsops.OutcomeFailed, &fsops.OpError{Kind: fsops.KindLocked, OnDest: true}, "コピー先・移動先で: ほかのアプリが使用中です"},
		// ジャンクションはコピーしない（Skipped）。リンクを作れなかったもの（Failed）とは言い方を分ける（filer §8.8）。
		{fsops.OutcomeSkipped, &fsops.OpError{Kind: fsops.KindLinkUnsupported}, "ジャンクションはコピーしません"},
		{fsops.OutcomeFailed, &fsops.OpError{Kind: fsops.KindLinkUnsupported, OnDest: true}, "コピー先・移動先で: この場所にはリンクを作れません"},
	} {
		if got := ResultError(tt.o, tt.err); got != tt.want {
			t.Errorf("ResultError(%v, %v) = %q, want %q", tt.o, tt.err, got, tt.want)
		}
	}
}

// TestOpWording は、操作ごとの言い方（ごみ箱・完全削除は「〜へコピーします」の形にしない）を確かめる。
func TestOpWording(t *testing.T) {
	for _, tt := range []struct{ got, want string }{
		{Done(fsops.OpCopy, 3, 0, false), "3 項目をコピーしました"},
		{Done(fsops.OpTrash, 3, 0, false), "3 項目をごみ箱に入れました"},
		{Done(fsops.OpDelete, 2, 0, false), "2 項目を完全に削除しました"},
		{ConfirmSummary(fsops.OpTrash, 3, ""), "3 項目をごみ箱に入れます"},
		{ConfirmSummary(fsops.OpMove, 3, "D:"), "3 項目を D: へ移動します"},
		{ResultTitle(fsops.OpTrash, "完了", "C:\\x", ""), "ごみ箱に入れた結果: 完了   C:\\x"},
		{ResultTitle(fsops.OpCopy, "完了", "a", "b"), "コピーの結果: 完了   a -> b"},
	} {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
}

// TestNameError は、名前の変更・フォルダの作成のエラーに「コピー先・移動先で: 」を付けないことを確かめる（fsops.Mkdir のエラーは OnDest）。
func TestNameError(t *testing.T) {
	if got := NameError(&fsops.OpError{Op: "mkdir", Kind: fsops.KindExist, OnDest: true}); got != Kind(fsops.KindExist) {
		t.Errorf("NameError = %q", got)
	}
	if got := NameError(nil); got != Kind(fsops.KindUnknown) {
		t.Errorf("NameError(nil) = %q", got)
	}
}

// TestLeftovers は、応答がないまま終わるときに残りうるものの案内が、操作に合っていることを確かめる（filer §8.4、fsops の doc.go）。
func TestLeftovers(t *testing.T) {
	has := func(op fsops.OpKind, sub string) bool {
		for _, l := range Leftovers(op) {
			if strings.Contains(l, sub) {
				return true
			}
		}
		return false
	}
	if has(fsops.OpCopy, "移動元") {
		t.Error("the copy leftovers mention the move source")
	}
	for _, op := range []fsops.OpKind{fsops.OpCopy, fsops.OpMove} {
		if !has(op, ".tmp") || !has(op, "空のファイル") {
			t.Errorf("%v: leftovers %q, want the temporary files and the empty files that reserved the names", op, Leftovers(op))
		}
	}
	if !has(fsops.OpMove, "移動元") {
		t.Error("the move leftovers do not mention the source and the destination")
	}
}

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
	texts := []string{Loading, LoadCanceled, CannotOpenName, ExecConfirm, ExecChoices, PathInputTitle, NotYet, TooSmall, HelpTitle,
		CannotOpenDir(""), ShowingAncestor(""), Opened(""), OpenFailed(""), Items(1, 2, 3),
		TypeParent, TypeDir, TypeJunction, TypeSymlink, TypeSpecial, UnitBytes, LinkArrow, KeyGuide,
		PaneIndicator(1, 2), PreviewEmpty, PreviewBinary, PreviewNotLocal,
		LabelDir, LabelJunction, LabelSymlink, LabelSpecial, LabelError}
	for _, h := range Help {
		texts = append(texts, h[0], h[1])
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
	} {
		if got := ResultError(tt.o, tt.err); got != tt.want {
			t.Errorf("ResultError(%v, %v) = %q, want %q", tt.o, tt.err, got, tt.want)
		}
	}
}

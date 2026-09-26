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

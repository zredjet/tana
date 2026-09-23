package fsops

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// checkStrings は、列挙型の各値の String() が prefix で始まる一意の識別子であり、
// 範囲外の値が "<型名>(<数値>)" になることを確かめる。
func checkStrings[T interface {
	~int
	fmt.Stringer
}](t *testing.T, typeName, prefix string, values []T, outOfRange T) {
	t.Helper()
	seen := map[string]T{}
	for _, v := range values {
		s := v.String()
		if !strings.HasPrefix(s, prefix) || strings.Contains(s, "(") {
			t.Errorf("%s(%d).String() = %q, want an identifier starting with %q", typeName, int(v), s, prefix)
		}
		if prev, ok := seen[s]; ok {
			t.Errorf("%s(%d) and %s(%d) have the same String() %q", typeName, int(prev), typeName, int(v), s)
		}
		seen[s] = v
	}
	want := fmt.Sprintf("%s(%d)", typeName, int(outOfRange))
	if got := outOfRange.String(); got != want {
		t.Errorf("%s(%d).String() = %q, want %q", typeName, int(outOfRange), got, want)
	}
}

func TestStrings(t *testing.T) {
	t.Parallel()
	var kinds []Kind
	for k := KindUnknown; k <= KindMetadata; k++ {
		kinds = append(kinds, k)
	}
	checkStrings(t, "Kind", "Kind", kinds, KindMetadata+1)
	checkStrings(t, "Outcome", "Outcome",
		[]Outcome{OutcomeDone, OutcomeSkipped, OutcomeFailed, OutcomePartial, OutcomeCopiedSourceKept}, 0)
	checkStrings(t, "Status", "Status",
		[]Status{StatusCompleted, StatusCompletedWithErrors, StatusCanceled}, 0)
	checkStrings(t, "Stage", "Stage",
		[]Stage{StageCopy, StageMove, StageVerify, StageRemoveSource, StageTrash, StageDelete}, 0)
	checkStrings(t, "OpKind", "Op", []OpKind{OpCopy, OpMove, OpTrash, OpDelete}, 0)
	checkStrings(t, "Method", "Method",
		[]Method{MethodRename, MethodCopy, MethodCopyThenRemove, MethodTrash, MethodRemove}, 0)
	checkStrings(t, "EntryType", "Type",
		[]EntryType{TypeFile, TypeDir, TypeSymlink, TypeJunction, TypeSpecial}, 0)
	checkStrings(t, "Decision", "Decision",
		[]Decision{DecisionUnset, DecisionSkip, DecisionOverwrite, DecisionAutoRename, DecisionMerge}, -1)
	checkStrings(t, "LinkPolicy", "Link", []LinkPolicy{LinkKeep, LinkSkip}, 2)
	checkStrings(t, "VerifyMode", "Verify", []VerifyMode{VerifySize, VerifyHash}, 2)
	checkStrings(t, "SyncMode", "Sync", []SyncMode{SyncMoveOnly, SyncAlways}, 2)
}

func TestKindOf(t *testing.T) {
	t.Parallel()
	oe := &OpError{Op: "copy", Path: "/a", Kind: KindExist, Err: fs.ErrExist}
	tests := []struct {
		name string
		err  error
		want Kind
	}{
		{"nil", nil, KindUnknown},
		{"plain error", errors.New("x"), KindUnknown},
		{"OpError", oe, KindExist},
		{"wrapped OpError", fmt.Errorf("outer: %w", oe), KindExist},
		{"joined OpError", errors.Join(errors.New("x"), oe), KindExist},
		{"outermost OpError wins", &OpError{Op: "move", Kind: KindNoSpace, Err: oe}, KindNoSpace},
		// KindOf は *OpError だけを見る。生のエラーは分類しない（SPEC §17）。
		{"raw context.Canceled", context.Canceled, KindUnknown},
		{"raw fs.ErrNotExist", fs.ErrNotExist, KindUnknown},
		// ItemResult.Err などの nil の *OpError を error として渡した場合
		{"typed nil OpError", (*OpError)(nil), KindUnknown},
	}
	for _, tt := range tests {
		if got := KindOf(tt.err); got != tt.want {
			t.Errorf("%s: KindOf = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestOpError(t *testing.T) {
	t.Parallel()
	inner := &fs.PathError{Op: "open", Path: "/a", Err: fs.ErrNotExist}
	tests := []struct {
		err  *OpError
		want string
	}{
		{&OpError{Op: "copy", Path: "/a", Dest: "/b", Kind: KindNotFound, Err: inner},
			"fsops: copy /a -> /b: KindNotFound: open /a: file does not exist"},
		{&OpError{Op: "remove", Path: "/a", Kind: KindNotEmpty},
			"fsops: remove /a: KindNotEmpty"},
		{&OpError{Op: "plan", Kind: KindInvalidRequest, Err: errors.New("no sources")},
			"fsops: plan: KindInvalidRequest: no sources"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}

	oe := &OpError{Op: "copy", Path: "/a", Kind: KindNotFound, Err: inner}
	if !errors.Is(oe, fs.ErrNotExist) {
		t.Error("errors.Is(OpError, fs.ErrNotExist) = false, want true (Unwrap)")
	}
	if pe, ok := errors.AsType[*fs.PathError](oe); !ok || pe != inner {
		t.Error("errors.AsType[*fs.PathError](OpError) did not find the wrapped error")
	}

	// nil の *OpError を error として扱っても panic しない。
	var nilErr error = (*OpError)(nil)
	if got := nilErr.Error(); got != "fsops: <nil>" {
		t.Errorf("nil Error() = %q", got)
	}
	if errors.Is(nilErr, fs.ErrNotExist) {
		t.Error("errors.Is(nil OpError, fs.ErrNotExist) = true, want false")
	}

	if (*OpError)(nil).clone() != nil {
		t.Error("nil.clone() != nil")
	}
	c := oe.clone()
	if c == oe || *c != *oe {
		t.Errorf("clone() = %p %+v, want a distinct copy of %p %+v", c, c, oe, oe)
	}
}

func TestClassifyGeneric(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want Kind
	}{
		{"nil", nil, KindUnknown},
		{"plain error", errors.New("x"), KindUnknown},
		{"OpError keeps its kind", &OpError{Kind: KindSourceChanged, Err: fs.ErrNotExist}, KindSourceChanged},
		{"wrapped OpError keeps its kind", fmt.Errorf("w: %w", &OpError{Kind: KindLocked}), KindLocked},
		{"context.Canceled", context.Canceled, KindCanceled},
		{"context.DeadlineExceeded", context.DeadlineExceeded, KindCanceled},
		{"wrapped context.Canceled", fmt.Errorf("w: %w", context.Canceled), KindCanceled},
		{"context cause", func() error {
			ctx, cancel := context.WithCancelCause(context.Background())
			cancel(errors.New("cause"))
			return ctx.Err()
		}(), KindCanceled},
		{"fs.ErrNotExist", fs.ErrNotExist, KindNotFound},
		{"fs.ErrExist", fs.ErrExist, KindExist},
		{"fs.ErrPermission", fs.ErrPermission, KindPermission},
		{"PathError with fs.ErrNotExist", &fs.PathError{Op: "open", Path: "/a", Err: fs.ErrNotExist}, KindNotFound},
		{"typed nil OpError", (*OpError)(nil), KindUnknown},
	}
	for _, tt := range tests {
		if got := classify(tt.err, classifyOpts{}); got != tt.want {
			t.Errorf("%s: classify = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestClassifyRealErrors は、実際の OS のエラーが期待どおりに分類されることを確かめる。
func TestClassifyRealErrors(t *testing.T) {
	t.Parallel()
	dir := testfs.TempDir(t)
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(dir, "full")
	if err := os.Mkdir(full, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(full, "a"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing")

	tests := []struct {
		name string
		op   func() error
		want Kind
	}{
		{"Lstat missing", func() error { _, err := os.Lstat(missing); return err }, KindNotFound},
		{"Lstat missing parent", func() error {
			_, err := os.Lstat(filepath.Join(missing, "child"))
			return err
		}, KindNotFound},
		{"Lstat under a file", func() error {
			_, err := os.Lstat(filepath.Join(file, "child"))
			return err
		}, KindNotFound},
		{"Remove missing", func() error { return os.Remove(missing) }, KindNotFound},
		{"Mkdir existing", func() error { return os.Mkdir(full, 0o755) }, KindExist},
		{"Mkdir over a file", func() error { return os.Mkdir(file, 0o755) }, KindExist},
		{"OpenFile O_EXCL existing", func() error {
			f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err == nil {
				f.Close()
			}
			return err
		}, KindExist},
		{"Remove non-empty dir", func() error { return os.Remove(full) }, KindNotEmpty},
	}
	for _, tt := range tests {
		err := tt.op()
		if err == nil {
			t.Errorf("%s: err = nil, want an error of %v", tt.name, tt.want)
			continue
		}
		if got := classify(err, classifyOpts{}); got != tt.want {
			t.Errorf("%s: classify(%v) = %v, want %v", tt.name, err, got, tt.want)
		}
	}
}

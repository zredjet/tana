package app

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

// 名前の変更と新しいフォルダ（filer §8.7）のテスト。

// renameCall は、名前の変更の呼び出しの記録。
type renameCall struct{ path, name string }

// spyRename は、本物の fsops.Rename を呼び、呼び出しを calls に記録する設定を返す。
func spyRename(calls *[]renameCall) func(*Config) {
	return func(c *Config) {
		c.Rename = func(path, name string) error {
			*calls = append(*calls, renameCall{path, name})
			return fsops.Rename(path, name)
		}
	}
}

// TestRenameUnchanged は、名前を変えずに Enter を押したら何もしないことを確かめる（filer §8.7。U4）。
// 表示で置き換える文字（制御文字）や、NFD の名前でも、入力欄は元のバイト列を持ち、表示用の文字列と比べて判断しない。
func TestRenameUnchanged(t *testing.T) {
	t.Parallel()
	root := tree(t)
	names := []string{"がぎ.txt"} // NFD
	if runtime.GOOS != "windows" {
		names = append(names, "e\x1b[31mx.txt") // Windows では名前に制御文字を使えない
	}
	for _, n := range names {
		write(t, filepath.Join(root, n))
	}
	var calls []renameCall
	h := newHarness(t, spyRename(&calls), root)
	for _, n := range names {
		h.moveTo(n)
		h.do(ActRename)
		if h.a.Dialog() != DialogRename {
			t.Fatalf("%q: dialog %v", n, h.a.Dialog())
		}
		v := h.a.NameDialog()
		if v.Edit.Text() != n || v.Name != n || v.Edit.Cursor() != len(n)-len(".txt") {
			t.Errorf("%q: editor %q cursor %d, name %q (want the enumerated bytes, the cursor before the extension)", n, v.Edit.Text(), v.Edit.Cursor(), v.Name)
		}
		h.do(ActLeft)
		h.do(ActRight) // カーソルを動かすだけでは変更にならない
		h.do(ActSubmit)
		if h.a.Dialog() != DialogNone {
			t.Errorf("%q: the dialog stays open after Enter without a change", n)
		}
		if !exists(t, filepath.Join(root, n)) {
			t.Errorf("%q: renamed without a change (U4)", n)
		}
	}
	if len(calls) != 0 {
		t.Errorf("Rename was called without a change: %q", calls)
	}
}

// TestRenameFromEnumeratedName は、変更の対象のパスを列挙で得た名前から作り、新しい名前を入力のバイト列のまま渡すことを確かめる（U4）。
// 変えた後は一覧を読み直し、カーソルを新しい名前に置く。
func TestRenameFromEnumeratedName(t *testing.T) {
	t.Parallel()
	root := tree(t)
	old := "がぎ.txt" // NFD（NFC に変えない。fsops I6）
	write(t, filepath.Join(root, old))
	var calls []renameCall
	h := newHarness(t, spyRename(&calls), root)
	h.moveTo(old)
	h.do(ActRename)
	h.act(Action{Kind: ActInsert, Text: "x"})
	h.do(ActSubmit)
	want := "がぎx.txt"
	if len(calls) != 1 || calls[0] != (renameCall{filepath.Join(root, old), want}) {
		t.Fatalf("calls %q", calls)
	}
	if h.a.Dialog() != DialogNone {
		t.Error("the dialog stays open after renaming")
	}
	if !slices.Contains(h.names(0), want) || h.cursorName(0) != want {
		t.Errorf("names %q, cursor %q; want %q (the same bytes) under the cursor", h.names(0), h.cursorName(0), want)
	}
}

// TestRenameErrors は、fsops.Rename のエラーを入力欄の下に出し、入力欄を閉じないことを確かめる（filer §8.7）。
func TestRenameErrors(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.moveTo("a.txt")
	h.do(ActRename)
	h.do(ActLineHome)
	h.do(ActDelete)
	h.act(Action{Kind: ActInsert, Text: "b"}) // b.txt はある
	h.do(ActSubmit)
	if h.a.Dialog() != DialogRename {
		t.Fatal("the dialog was closed on an error")
	}
	if v := h.a.NameDialog(); v.Err != msg.Kind(fsops.KindExist) || v.Edit.Text() != "b.txt" {
		t.Errorf("error %q, text %q", v.Err, v.Edit.Text())
	}
	if readFile(t, filepath.Join(root, "b.txt")) != "x" || !exists(t, filepath.Join(root, "a.txt")) {
		t.Error("the files changed")
	}
	h.act(Action{Kind: ActInsert, Text: "/"}) // 区切りを含む名前
	if v := h.a.NameDialog(); v.Err != "" {
		t.Errorf("the error remains after editing: %q", v.Err)
	}
	h.do(ActSubmit)
	if v := h.a.NameDialog(); v.Err != msg.Kind(fsops.KindInvalidName) {
		t.Errorf("error %q, want %q", v.Err, msg.Kind(fsops.KindInvalidName))
	}
	h.do(ActCancel)
	if h.a.Dialog() != DialogNone || !exists(t, filepath.Join(root, "a.txt")) {
		t.Error("Esc")
	}
}

// TestRenameCaseOnly は、大文字小文字だけが違う名前へ変えられることを確かめる（filer §8.7、fsops §11.3）。
func TestRenameCaseOnly(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.moveTo("a.txt")
	h.do(ActRename)
	h.do(ActLineHome)
	h.do(ActDelete)
	h.act(Action{Kind: ActInsert, Text: "A"})
	h.do(ActSubmit)
	if got := h.names(0); !slices.Contains(got, "A.txt") || slices.Contains(got, "a.txt") {
		t.Errorf("names %q, want A.txt instead of a.txt", got)
	}
	if v := h.a.NameDialog(); h.a.Dialog() != DialogNone {
		t.Errorf("dialog open: %q", v.Err)
	}
}

// TestRenameParentOrEmpty は、.. と空のフォルダでは名前の変更を始めないことを確かめる。
func TestRenameParentOrEmpty(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.do(ActHome)
	h.do(ActRename)
	if h.a.Dialog() != DialogNone {
		t.Error("rename started on ..")
	}
}

// TestNewDir は、新しいフォルダを作り、カーソルをそこに置くことと、エラーを入力欄の下に出すことを確かめる（filer §8.7）。
// fsops.Mkdir のエラーは移動先の側（OnDest）だが、「コピー先・移動先で: 」は付けない（フォルダを作る画面なので）。
func TestNewDir(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.do(ActNewDir)
	if h.a.Dialog() != DialogNewDir {
		t.Fatalf("dialog %v", h.a.Dialog())
	}
	if v := h.a.NameDialog(); v.Dir != root || v.Edit.Text() != "" {
		t.Errorf("NameDialog = %+v", v)
	}
	h.act(Action{Kind: ActInsert, Text: "新しいフォルダ"})
	h.do(ActSubmit)
	if fi, err := os.Stat(filepath.Join(root, "新しいフォルダ")); err != nil || !fi.IsDir() {
		t.Fatalf("not created: %v", err)
	}
	if h.a.Dialog() != DialogNone || h.cursorName(0) != "新しいフォルダ" {
		t.Errorf("dialog %v, cursor %q", h.a.Dialog(), h.cursorName(0))
	}
	h.do(ActNewDir)
	h.act(Action{Kind: ActInsert, Text: "sub"})
	h.do(ActSubmit)
	if v := h.a.NameDialog(); h.a.Dialog() != DialogNewDir || v.Err != msg.Kind(fsops.KindExist) {
		t.Errorf("dialog %v, error %q (want %q without the destination prefix)", h.a.Dialog(), v.Err, msg.Kind(fsops.KindExist))
	}
}

// TestNameAsync は、名前の変更とフォルダの作成を作業用の goroutine で行い、その間も UI が止まらないことを確かめる（filer U5）。
// 待っている間は入力を受け付けず（結果が別の名前のものにならないように）、Esc で待つのをやめられる。結果は届いたときに反映する。
func TestNameAsync(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.moveTo("a.txt")
	h.do(ActRename)
	h.act(Action{Kind: ActInsert, Text: "z"})
	h.hold = true
	h.do(ActSubmit)
	if v := h.a.NameDialog(); !v.Busy || len(h.held) != 1 {
		t.Fatalf("busy %v, held %d", v.Busy, len(h.held))
	}
	h.act(Action{Kind: ActInsert, Text: "q"})
	h.do(ActSubmit)
	if v := h.a.NameDialog(); v.Edit.Text() != "az.txt" || len(h.held) != 1 {
		t.Errorf("input accepted while busy: %q, held %d", v.Edit.Text(), len(h.held))
	}
	h.do(ActCancel)
	if h.a.Dialog() != DialogNone {
		t.Fatal("Esc did not stop waiting")
	}
	h.release()
	if !slices.Contains(h.names(0), "az.txt") {
		t.Errorf("the pane was not reloaded after the rename finished: %q", h.names(0))
	}
	// 待つのをやめた後に届いたエラーは、メッセージ行に出す。
	h.do(ActNewDir)
	h.act(Action{Kind: ActInsert, Text: "sub"})
	h.hold = true
	h.do(ActSubmit)
	h.do(ActCancel)
	h.release()
	if text, isErr := h.a.Message(); !isErr || text != msg.NameFailed(false, msg.Kind(fsops.KindExist)) {
		t.Errorf("message %q (error %v)", text, isErr)
	}
}

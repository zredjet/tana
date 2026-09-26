package app

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

// harness は、App に操作を与え、返った Cmd をその場で動かす（Delay のあるものは delayed に貯める）。
// hold のときは、Delay のない Cmd も held に貯める（読み込みの途中の状態を作るため）。
type harness struct {
	t       *testing.T
	a       *App
	delayed []Cmd
	held    []Cmd
	hold    bool
	opened  []string
}

func tempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func newHarness(t *testing.T, mod func(*Config), dirs ...string) *harness {
	t.Helper()
	h := &harness{t: t}
	cfg := DefaultConfig(dirs)
	cfg.DotFilesHidden = true
	cfg.Open = func(p string) error { h.opened = append(h.opened, p); return nil } // テストでアプリを起動しない
	cfg.Now = func() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local) }
	if mod != nil {
		mod(&cfg)
	}
	a, cmds := New(cfg)
	h.a = a
	h.run(cmds)
	return h
}

func (h *harness) run(cmds []Cmd) {
	for _, c := range cmds {
		switch {
		case c.Delay > 0:
			h.delayed = append(h.delayed, c)
		case h.hold:
			h.held = append(h.held, c)
		default:
			h.run(h.a.Update(c.Run()))
		}
	}
}

func (h *harness) do(k ActionKind) { h.act(Action{Kind: k}) }

func (h *harness) act(act Action) { h.run(h.a.Do(act)) }

// release は、貯めた Cmd を動かす。
func (h *harness) release() {
	held := h.held
	h.held, h.hold = nil, false
	h.run(held)
}

func (h *harness) pane(i int) *Pane { return h.a.Panes()[i] }

// names は、ペインの表示する項目の名前。
func (h *harness) names(i int) []string {
	p := h.pane(i)
	var out []string
	for k := range p.Len() {
		out = append(out, p.Item(k).Name)
	}
	return out
}

// cursorName は、ペインのカーソル行の名前。
func (h *harness) cursorName(i int) string {
	p := h.pane(i)
	if p.Len() == 0 {
		return ""
	}
	return p.Item(p.Cursor()).Name
}

// moveTo は、操作中のペインのカーソルを名前 name の項目に動かす（Up・Down で）。
func (h *harness) moveTo(name string) {
	h.t.Helper()
	i := slices.Index(h.names(h.a.Active()), name)
	if i < 0 {
		h.t.Fatalf("%q not in %q", name, h.names(h.a.Active()))
	}
	h.do(ActHome)
	for range i {
		h.do(ActDown)
	}
}

func (h *harness) wantMessage(want string) {
	h.t.Helper()
	if got, _ := h.a.Message(); got != want {
		h.t.Errorf("message = %q, want %q", got, want)
	}
}

func write(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

// tree は、次のものを作る: sub/inner.txt、a.txt、b.txt、.hidden。
func tree(t *testing.T) string {
	root := tempDir(t)
	mkdir(t, filepath.Join(root, "sub"))
	write(t, filepath.Join(root, "sub", "inner.txt"))
	for _, n := range []string{"a.txt", "b.txt", ".hidden"} {
		write(t, filepath.Join(root, n))
	}
	return root
}

// TestBrowse は、一覧、フォルダに入る、親へ戻る（カーソルを来たフォルダに置く）ことを確かめる（filer §6・§7）。
func TestBrowse(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root, root)
	if got, want := h.names(0), []string{"..", "sub", "a.txt", "b.txt"}; !slices.Equal(got, want) {
		t.Fatalf("names = %q, want %q", got, want)
	}
	if items, marks, hidden := h.pane(0).Counts(); items != 3 || marks != 0 || hidden != 1 {
		t.Errorf("Counts = %d, %d, %d, want 3, 0, 1", items, marks, hidden)
	}
	h.moveTo("sub")
	h.do(ActEnter)
	if got := h.pane(0).Dir(); got != filepath.Join(root, "sub") {
		t.Fatalf("Dir = %q after Enter", got)
	}
	if got := h.names(0); !slices.Equal(got, []string{"..", "inner.txt"}) || h.cursorName(0) != ".." {
		t.Errorf("in sub: %q, cursor %q", got, h.cursorName(0))
	}
	if h.pane(1).Dir() != root {
		t.Error("the other pane changed")
	}
	h.do(ActParent)
	if h.pane(0).Dir() != root || h.cursorName(0) != "sub" {
		t.Errorf("after Parent: %q, cursor %q, want %q and sub", h.pane(0).Dir(), h.cursorName(0), root)
	}
	h.moveTo("..")
	h.do(ActEnter) // .. でも親へ
	if h.pane(0).Dir() != filepath.Dir(root) || h.cursorName(0) != filepath.Base(root) {
		t.Errorf("after Enter on ..: %q, cursor %q", h.pane(0).Dir(), h.cursorName(0))
	}
}

// TestCursorMoves は、カーソルの移動が端で止まり、ページ単位の移動が描いた行数を使うことを確かめる。
func TestCursorMoves(t *testing.T) {
	t.Parallel()
	root := tempDir(t)
	for i := range 30 {
		write(t, filepath.Join(root, "f"+strings.Repeat("x", i)))
	}
	h := newHarness(t, nil, root)
	p := h.pane(0)
	h.do(ActUp)
	if p.Cursor() != 0 {
		t.Errorf("Up at the top: cursor %d", p.Cursor())
	}
	p.Window(10)
	h.do(ActPageDown)
	if p.Cursor() != 10 {
		t.Errorf("PageDown: cursor %d, want 10", p.Cursor())
	}
	if top := p.Window(10); top != 1 {
		t.Errorf("Window after PageDown = %d, want 1", top)
	}
	h.do(ActEnd)
	if p.Cursor() != 30 || p.Window(10) != 21 {
		t.Errorf("End: cursor %d, top %d, want 30 and 21", p.Cursor(), p.Window(10))
	}
	h.do(ActDown)
	if p.Cursor() != 30 {
		t.Errorf("Down at the end: cursor %d", p.Cursor())
	}
	h.do(ActHome)
	if p.Cursor() != 0 || p.Window(10) != 0 {
		t.Errorf("Home: cursor %d, top %d", p.Cursor(), p.Window(10))
	}
}

// TestMarks は、マークの切り替え（.. はマークしない）と、すべてマークする・外すことを確かめる。
func TestMarks(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	p := h.pane(0)
	h.do(ActMark) // .. はマークせず、カーソルだけ動く
	if _, marks, _ := p.Counts(); marks != 0 || h.cursorName(0) != "sub" {
		t.Errorf("Mark on ..: %d marks, cursor %q", marks, h.cursorName(0))
	}
	h.do(ActMark)
	if !p.Marked("sub") || h.cursorName(0) != "a.txt" {
		t.Errorf("Mark on sub: marked %v, cursor %q", p.Marked("sub"), h.cursorName(0))
	}
	h.do(ActMarkAll)
	if _, marks, _ := p.Counts(); marks != 3 {
		t.Errorf("MarkAll: %d marks, want 3 (not .. nor the hidden file)", marks)
	}
	h.do(ActMarkAll)
	if _, marks, _ := p.Counts(); marks != 0 {
		t.Errorf("MarkAll again: %d marks, want 0", marks)
	}
}

// TestHiddenToggle は、隠しファイルの表示の切り替えで、隠れた項目のマークを外すことを確かめる（見えない項目を操作の対象にしない。filer §6）。
func TestHiddenToggle(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root, root)
	h.do(ActToggleHidden)
	if got := h.names(0); !slices.Contains(got, ".hidden") || !slices.Contains(h.names(1), ".hidden") {
		t.Fatalf("hidden files not shown in every pane: %q", got)
	}
	h.moveTo(".hidden")
	h.do(ActMark)
	h.moveTo("a.txt")
	h.do(ActMark)
	h.moveTo(".hidden")
	h.do(ActToggleHidden)
	p := h.pane(0)
	if p.Marked(".hidden") || !p.Marked("a.txt") {
		t.Errorf("after hiding: .hidden marked %v (want false), a.txt marked %v (want true)", p.Marked(".hidden"), p.Marked("a.txt"))
	}
	// .hidden は a.txt の前に並ぶ（. は英字より前）。隠れたら次に見える項目へ。
	if got := h.cursorName(0); got != "a.txt" {
		t.Errorf("cursor after hiding = %q, want a.txt", got)
	}
}

// TestHiddenAttribute は、名前の . で隠さない設定（Windows）では . で始まる名前を出すことを確かめる。
func TestHiddenAttribute(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, func(c *Config) { c.DotFilesHidden = false }, root)
	if !slices.Contains(h.names(0), ".hidden") {
		t.Errorf("names = %q, want .hidden shown", h.names(0))
	}
}

// TestReload は、再読み込みでマークを名前で保ち、カーソルを同じ名前の項目に置くことを確かめる（filer §6）。
func TestReload(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	p := h.pane(0)
	h.moveTo("a.txt")
	h.do(ActMark)
	h.do(ActMark) // b.txt
	h.moveTo("b.txt")
	write(t, filepath.Join(root, "0.txt"))
	if err := os.Remove(filepath.Join(root, "a.txt")); err != nil {
		t.Fatal(err)
	}
	h.do(ActReload)
	if got, want := h.names(0), []string{"..", "sub", "0.txt", "b.txt"}; !slices.Equal(got, want) {
		t.Fatalf("after reload: %q, want %q", got, want)
	}
	if h.cursorName(0) != "b.txt" || !p.Marked("b.txt") || p.Marked("a.txt") {
		t.Errorf("cursor %q, marks b.txt %v a.txt %v", h.cursorName(0), p.Marked("b.txt"), p.Marked("a.txt"))
	}
	// カーソルの項目が消えたら、同じ行の位置に置く。
	if err := os.Remove(filepath.Join(root, "b.txt")); err != nil {
		t.Fatal(err)
	}
	h.do(ActReload)
	if h.cursorName(0) != "0.txt" {
		t.Errorf("cursor after the item vanished = %q, want 0.txt (the last row)", h.cursorName(0))
	}
}

// TestReloadVanished は、表示中のフォルダが消えていたら、存在する祖先のうち最も近いフォルダを表示することを確かめる（filer §6）。
func TestReloadVanished(t *testing.T) {
	t.Parallel()
	root := tree(t)
	deep := filepath.Join(root, "sub", "x", "y")
	mkdir(t, deep)
	h := newHarness(t, nil, deep)
	if err := os.RemoveAll(filepath.Join(root, "sub", "x")); err != nil { // テストが作ったもの
		t.Fatal(err)
	}
	h.do(ActReload)
	if got := h.pane(0).Dir(); got != filepath.Join(root, "sub") {
		t.Errorf("Dir = %q, want %q", got, filepath.Join(root, "sub"))
	}
	h.wantMessage(msg.ShowingAncestor(msg.Kind(fsops.KindNotFound)))
}

// TestInitialMissing は、起動時のフォルダがなければ、開ける祖先を表示することを確かめる。
func TestInitialMissing(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, filepath.Join(root, "missing", "x"))
	if got := h.pane(0).Dir(); got != root || !h.pane(0).Loaded() {
		t.Errorf("Dir = %q (loaded %v), want %q", got, h.pane(0).Loaded(), root)
	}
	if text, isErr := h.a.Message(); !isErr || !strings.HasPrefix(text, msg.ShowingAncestor("")) {
		t.Errorf("message = %q", text)
	}
}

// TestEnterFails は、フォルダに入れなかったとき、元のフォルダに留まり、理由を出すことを確かめる（filer §6）。
func TestEnterFails(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.moveTo("sub")
	if err := os.RemoveAll(filepath.Join(root, "sub")); err != nil { // 一覧の後で消えた
		t.Fatal(err)
	}
	h.do(ActEnter)
	if h.pane(0).Dir() != root || h.cursorName(0) != "sub" {
		t.Errorf("Dir = %q, cursor %q, want to stay", h.pane(0).Dir(), h.cursorName(0))
	}
	h.wantMessage(msg.CannotOpenDir("見つかりません"))
	h.do(ActDown) // 次のキー入力でメッセージを消す
	h.wantMessage("")
}

// TestEnterNoPermission は、権限のないフォルダで、アクセス権がないことを出して留まることを確かめる。
func TestEnterNoPermission(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Unix permissions")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores the permission of folders")
	}
	root := tree(t)
	locked := filepath.Join(root, "sub")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	h := newHarness(t, nil, root)
	h.moveTo("sub")
	h.do(ActEnter)
	if h.pane(0).Dir() != root {
		t.Errorf("Dir = %q, want to stay", h.pane(0).Dir())
	}
	h.wantMessage(msg.CannotOpenDir("アクセス権がありません"))
}

// TestLoadCancel は、読み込み中の表示と、Esc で中止して元のフォルダに留まることを確かめる（filer §6、U5）。
// 読み込み中は、そのペインの操作を受け付けない。中止した読み込みの結果は捨てる。
func TestLoadCancel(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.moveTo("sub")
	h.hold = true
	h.do(ActEnter)
	p := h.pane(0)
	if p.Loading() {
		t.Error("the loading notice appears before slowLoad")
	}
	if len(h.delayed) == 0 || h.delayed[len(h.delayed)-1].Delay != slowLoad {
		t.Fatalf("no loadSlow timer: %+v", h.delayed)
	}
	h.run(h.a.Update(h.delayed[len(h.delayed)-1].Run()))
	if !p.Loading() {
		t.Error("the loading notice is not shown after slowLoad")
	}
	h.do(ActUp) // 読み込み中のペインの操作は受け付けない
	if h.cursorName(0) != "sub" {
		t.Errorf("cursor moved while loading: %q", h.cursorName(0))
	}
	h.do(ActCancel)
	if p.Loading() || p.load != nil {
		t.Error("still loading after Esc")
	}
	h.wantMessage(msg.LoadCanceled)
	h.release() // 中止した読み込みの結果が届く
	if p.Dir() != root || h.cursorName(0) != "sub" {
		t.Errorf("after the canceled result: Dir %q, cursor %q, want to stay", p.Dir(), h.cursorName(0))
	}
}

// TestNewerLoadWins は、読み込み中に別の読み込みを始めたら、古い結果を捨てることを確かめる。
func TestNewerLoadWins(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root, root)
	h.hold = true
	h.moveTo("sub")
	h.do(ActEnter)
	h.do(ActNextPane)
	h.do(ActNextPane)
	h.do(ActReload) // ペイン 0 の読み込みを置き換える
	h.release()
	if got := h.pane(0).Dir(); got != root {
		t.Errorf("Dir = %q, want %q (the reload replaced the enter)", got, root)
	}
}

// TestOpenFile は、ファイルで Enter を押すと関連付けで開くことを確かめる（filer §7）。
func TestOpenFile(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.moveTo("a.txt")
	h.do(ActEnter)
	if want := []string{filepath.Join(root, "a.txt")}; !slices.Equal(h.opened, want) {
		t.Errorf("opened %q, want %q", h.opened, want)
	}
	h.wantMessage(msg.Opened("a.txt"))
}

// TestOpenExecutable は、実行ファイルは確認してから開き、確認を描く前に届いたキーでは確定しないことを確かめる（filer §7、U2）。
func TestOpenExecutable(t *testing.T) {
	t.Parallel()
	root := tempDir(t)
	name := "run"
	if runtime.GOOS == "windows" {
		name = "run.bat"
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, nil, root)
	h.moveTo(name)
	h.do(ActEnter)
	if h.a.Dialog() != DialogExec || h.a.ExecName() != name {
		t.Fatalf("dialog %v %q, want the exec confirmation", h.a.Dialog(), h.a.ExecName())
	}
	h.do(ActYes) // 描く前に届いた y（先行入力）
	if len(h.opened) != 0 || h.a.Dialog() != DialogExec {
		t.Fatalf("confirmed by a key that came before the dialog was drawn (U2): opened %q", h.opened)
	}
	h.a.Drawn()
	h.act(Action{Kind: ActInsert, Text: "y"}) // 貼り付け・文字の入力では確定しない
	h.do(ActNo)
	if len(h.opened) != 0 || h.a.Dialog() != DialogNone {
		t.Fatalf("n: opened %q, dialog %v", h.opened, h.a.Dialog())
	}
	h.do(ActEnter)
	h.a.Drawn()
	h.do(ActYes)
	if want := []string{filepath.Join(root, name)}; !slices.Equal(h.opened, want) {
		t.Errorf("opened %q, want %q", h.opened, want)
	}
}

// TestCannotOpenName は、開けない名前（Windows の VU10）なら開かずに理由を出すことを確かめる（filer §7）。
func TestCannotOpenName(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, func(c *Config) { c.CanOpen = func(string) bool { return false } }, root)
	h.moveTo("a.txt")
	h.do(ActEnter)
	if len(h.opened) != 0 {
		t.Errorf("opened %q", h.opened)
	}
	h.wantMessage(msg.CannotOpenName)
}

// TestGoPath は、パスの入力で移動することを確かめる。入力を中止すれば何もしない。
func TestGoPath(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	h.do(ActGoPath)
	if h.a.Dialog() != DialogPath || h.a.PathEditor().Text() != root {
		t.Fatalf("dialog %v, text %q, want the path input with the current folder", h.a.Dialog(), h.a.PathEditor().Text())
	}
	h.act(Action{Kind: ActInsert, Text: string(filepath.Separator) + "sub"})
	h.do(ActSubmit)
	if got := h.pane(0).Dir(); got != filepath.Join(root, "sub") || h.a.Dialog() != DialogNone {
		t.Errorf("Dir = %q, dialog %v", got, h.a.Dialog())
	}
	h.do(ActGoPath)
	h.act(Action{Kind: ActInsert, Text: "zzz"})
	h.do(ActCancel)
	if h.a.Dialog() != DialogNone || h.pane(0).Dir() != filepath.Join(root, "sub") {
		t.Error("Esc in the path input")
	}
	h.do(ActGoPath)
	h.do(ActLineHome)
	for range len(h.a.PathEditor().Text()) {
		h.do(ActDelete)
	}
	h.act(Action{Kind: ActInsert, Text: filepath.Join(root, "missing")})
	h.do(ActSubmit)
	if h.pane(0).Dir() != filepath.Join(root, "sub") {
		t.Errorf("moved to a missing folder: %q", h.pane(0).Dir())
	}
	h.wantMessage(msg.CannotOpenDir("見つかりません"))
}

// TestResolve は、入力されたパスの解釈を確かめる。
func TestResolve(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		for in, want := range map[string]string{`D:`: `D:\`, `D:\x\..\y`: `D:\y`, `sub`: `C:\a\sub`, `..`: `C:\`, `\\srv\share\x`: `\\srv\share\x`} {
			if got := resolve(`C:\a`, in); got != want {
				t.Errorf("resolve(%q) = %q, want %q", in, got, want)
			}
		}
		return
	}
	for in, want := range map[string]string{"/x/../y": "/y", "sub": "/a/sub", "..": "/", "a b/": "/a/a b"} {
		if got := resolve("/a", in); got != want {
			t.Errorf("resolve(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPanes は、ペインの切り替え、次のペインを同じフォルダにすること、指定したペインへの移動（すでにそこなら親へ）を確かめる。
func TestPanes(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root, root)
	h.moveTo("sub")
	h.do(ActEnter)
	h.do(ActSyncOther)
	if h.pane(1).Dir() != filepath.Join(root, "sub") || h.a.Active() != 0 {
		t.Errorf("SyncOther: pane 1 at %q, active %d", h.pane(1).Dir(), h.a.Active())
	}
	h.act(Action{Kind: ActFocusOrUp, Pane: 1})
	if h.a.Active() != 1 || h.pane(1).Dir() != filepath.Join(root, "sub") {
		t.Errorf("FocusOrUp(1) from 0: active %d, dir %q", h.a.Active(), h.pane(1).Dir())
	}
	h.act(Action{Kind: ActFocusOrUp, Pane: 1})
	if h.pane(1).Dir() != root {
		t.Errorf("FocusOrUp(1) on 1: dir %q, want the parent", h.pane(1).Dir())
	}
	h.do(ActNextPane)
	if h.a.Active() != 0 {
		t.Errorf("NextPane: active %d", h.a.Active())
	}
	h.do(ActQuit)
	if !h.a.Quit() {
		t.Error("Quit")
	}
}

// TestLinks は、リンクのフォルダにはリンクのパスのまま入り、リンク先がファイルなら開くことを確かめる（filer §6・§7）。
// 状態行に出すリンク先も読む。
func TestLinks(t *testing.T) {
	t.Parallel()
	root := tree(t)
	for _, l := range []struct{ target, name string }{{"sub", "dirlink"}, {"a.txt", "filelink"}} {
		if err := os.Symlink(l.target, filepath.Join(root, l.name)); err != nil {
			t.Skipf("cannot create a symbolic link: %v", err)
		}
	}
	h := newHarness(t, nil, root)
	h.moveTo("dirlink")
	if target, ok := h.pane(0).LinkTarget("dirlink"); !ok || target != "sub" {
		t.Errorf("LinkTarget = %q, %v, want sub", target, ok)
	}
	h.do(ActEnter)
	if got := h.pane(0).Dir(); got != filepath.Join(root, "dirlink") || !slices.Contains(h.names(0), "inner.txt") {
		t.Errorf("in the link: Dir %q (want the link's path), names %q", got, h.names(0))
	}
	h.do(ActParent)
	if h.pane(0).Dir() != root || h.cursorName(0) != "dirlink" {
		t.Errorf("after Parent: %q, cursor %q", h.pane(0).Dir(), h.cursorName(0))
	}
	h.moveTo("filelink")
	h.do(ActEnter)
	if want := []string{filepath.Join(root, "filelink")}; !slices.Equal(h.opened, want) || h.pane(0).Dir() != root {
		t.Errorf("opened %q (want %q), Dir %q", h.opened, want, h.pane(0).Dir())
	}
}

// TestNamesKept は、表示で置き換える名前（制御文字、NFD）でも、列挙で得た名前からパスを作ることを確かめる（U4）。
func TestNamesKept(t *testing.T) {
	t.Parallel()
	root := tempDir(t)
	names := []string{"か\u3099", "x\u202etxt.exe"}
	if runtime.GOOS != "windows" {
		names = append(names, "esc\x1b[31m")
	}
	for _, n := range names {
		mkdir(t, filepath.Join(root, n, "in-"+n))
	}
	h := newHarness(t, nil, root)
	for _, n := range names {
		h.moveTo(n)
		h.do(ActEnter)
		if got := h.names(0); !slices.Contains(got, "in-"+n) {
			t.Errorf("entered %q: %q", n, got)
		}
		h.do(ActParent)
	}
}

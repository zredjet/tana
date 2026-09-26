package tui

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/app"
	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/keys"
	"github.com/zredjet/tana/internal/msg"
	"github.com/zredjet/tana/internal/screen"
)

var update = flag.Bool("update", false, "update the golden files in testdata/filer")

// 見本のフォルダ（filer §5.1 のモック）。パスは Windows の形の文字列のまま使い、どの OS でも同じ画面にする（枠に収まるので切り詰めない）。
const (
	leftDir  = `C:\Users\hiro\Documents`
	rightDir = `D:\backup`
)

var now = time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

func at(month time.Month, day, hour, minute, sec int) time.Time {
	return time.Date(2026, month, day, hour, minute, sec, 0, time.UTC)
}

func file(name string, size int64, t time.Time) fsops.Entry {
	return fsops.Entry{Name: name, Info: fsops.EntryInfo{Type: fsops.TypeFile, Size: size, ModTime: t}}
}

func typed(name string, typ fsops.EntryType, t time.Time) fsops.Entry {
	return fsops.Entry{Name: name, Info: fsops.EntryInfo{Type: typ, ModTime: t}}
}

// fakeFS は、見本のフォルダの列挙の結果。
func fakeFS() map[string][]fsops.Entry {
	hidden := file("desktop.ini", 282, at(1, 5, 9, 0, 0))
	hidden.Hidden = true
	return map[string][]fsops.Entry{
		leftDir: {
			typed("old-docs", fsops.TypeJunction, time.Date(2025, 3, 1, 10, 0, 0, 0, time.UTC)),
			typed("projects", fsops.TypeDir, at(9, 24, 10, 40, 0)),
			typed("写真", fsops.TypeDir, at(9, 12, 9, 15, 0)),
			typed("議事録", fsops.TypeDir, at(9, 20, 18, 2, 0)),
			typed("latest", fsops.TypeSymlink, at(9, 22, 8, 0, 0)),
			file("data.csv", 134217728, at(9, 18, 14, 30, 0)),
			file("README.md", 3174, at(9, 23, 22, 10, 0)),
			file("報告書.docx", 1234567, at(9, 24, 11, 19, 5)),
			file("見積書_2026年度版.xlsx", 49152, at(9, 10, 16, 5, 0)),
			file("bad\x1b[31mname\u202etxt.exe", 10, at(9, 1, 0, 0, 0)),
			file("か\u3099き\u3099.txt", 2048, at(8, 1, 12, 0, 0)),
			file("👨\u200d👩\u200d👧family.jpg", 5<<20, at(8, 2, 12, 0, 0)),
			file("setup.exe", 7<<20, at(7, 7, 7, 7, 0)),
			typed("fifo", fsops.TypeSpecial, at(6, 1, 0, 0, 0)),
			{Name: "unreadable", Err: &fsops.OpError{Op: "lstat", Kind: fsops.KindPermission}},
			hidden,
		},
		rightDir: {
			typed("2025", fsops.TypeDir, time.Date(2025, 12, 28, 10, 0, 0, 0, time.UTC)),
			typed("写真", fsops.TypeDir, at(8, 2, 12, 0, 0)),
			typed("議事録", fsops.TypeDir, at(9, 1, 10, 0, 0)),
			file("archive.zip", 2469606195, at(8, 30, 19, 45, 0)),
			file("報告書.docx", 1153434, at(9, 20, 9, 0, 0)),
		},
	}
}

// scene は、見本の状態の App と、Cmd をその場で動かすもの（app のテストの harness と同じ）。
type scene struct {
	t       *testing.T
	a       *app.App
	f       *Filer
	delayed []app.Cmd
	held    []app.Cmd
	hold    bool
}

func newScene(t *testing.T) *scene {
	t.Helper()
	fs := fakeFS()
	cfg := app.Config{
		Dirs:           []string{leftDir, rightDir},
		DotFilesHidden: false,
		ReadDir: func(dir string) ([]fsops.Entry, error) {
			if e, ok := fs[dir]; ok {
				return e, nil
			}
			if strings.HasSuffix(dir, "old-docs") {
				return nil, &fsops.OpError{Op: "readdir", Path: dir, Kind: fsops.KindPermission}
			}
			return nil, &fsops.OpError{Op: "readdir", Path: dir, Kind: fsops.KindNotFound}
		},
		Readlink:     func(string) (string, error) { return `D:\backup\latest`, nil },
		Open:         func(string) error { return nil },
		IsExecutable: func(p string, dir bool) bool { return strings.HasSuffix(p, ".exe") },
		CanOpen:      func(string) bool { return true },
		Now:          func() time.Time { return now },
	}
	a, cmds := app.New(cfg)
	sc := &scene{t: t, a: a, f: &Filer{app: a}}
	sc.run(cmds)
	return sc
}

func (sc *scene) run(cmds []app.Cmd) {
	for _, c := range cmds {
		switch {
		case c.Delay > 0:
			sc.delayed = append(sc.delayed, c)
		case sc.hold:
			sc.held = append(sc.held, c)
		default:
			sc.run(sc.a.Update(c.Run()))
		}
	}
}

// keys は、キーの列を画面のキーの割り当てで app に渡す。
func (sc *scene) keys(evs ...keys.Event) {
	for _, ev := range evs {
		if act, ok := sc.f.action(ev); ok {
			sc.run(sc.a.Do(act))
		}
	}
}

func key(k keys.Key) keys.Event { return keys.Event{Kind: keys.KeyEvent, Key: k} }
func char(r rune) keys.Event    { return keys.Event{Kind: keys.KeyEvent, Key: keys.KeyRune, Rune: r} }
func ctrl(r rune) keys.Event {
	return keys.Event{Kind: keys.KeyEvent, Key: keys.KeyRune, Rune: r, Mod: keys.ModCtrl}
}
func paste(s string) keys.Event { return keys.Event{Kind: keys.PasteEvent, Text: s} }
func repeat(ev keys.Event, n int) []keys.Event {
	out := make([]keys.Event, n)
	for i := range out {
		out[i] = ev
	}
	return out
}

// moveTo は、操作中のペインのカーソルを名前 name の項目に、Home と ↓ で動かす。
func (sc *scene) moveTo(name string) {
	sc.t.Helper()
	p := sc.a.Panes()[sc.a.Active()]
	for i := range p.Len() {
		if p.Item(i).Name == name {
			sc.keys(key(keys.KeyHome))
			sc.keys(repeat(key(keys.KeyDown), i)...)
			return
		}
	}
	sc.t.Fatalf("%q not in the pane", name)
}

// mainState は、モックの状態にする: 写真・議事録・報告書.docx をマークし、カーソルを報告書.docx に置く。
func (sc *scene) mainState() {
	sc.moveTo("写真")
	sc.keys(char(' '), char(' ')) // 写真・議事録
	sc.moveTo("報告書.docx")
	sc.keys(char(' '), key(keys.KeyUp))
}

// draw は、cols×rows の画面に描く（Loop と同じく、空白に戻してから描く）。
func (sc *scene) draw(cols, rows int) *screen.Screen {
	s := screen.New(cols, rows)
	sc.f.Draw(s)
	return s
}

var colorNames = []string{"default", "black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
	"brightblack", "brightred", "brightgreen", "brightyellow", "brightblue", "brightmagenta", "brightcyan", "brightwhite"}

func styleString(st screen.Style) string {
	var attrs []string
	for _, a := range []struct {
		a    screen.Attr
		name string
	}{{screen.AttrBold, "bold"}, {screen.AttrDim, "dim"}, {screen.AttrUnderline, "underline"}, {screen.AttrReverse, "reverse"}} {
		if st.Attr&a.a != 0 {
			attrs = append(attrs, a.name)
		}
	}
	return fmt.Sprintf("fg=%s bg=%s %s", colorNames[st.FG], colorNames[st.BG], strings.Join(attrs, ","))
}

// render は、格子を文字列にする。各行の文字（行末に | を付ける）、セルごとの見た目の記号と凡例、本物のカーソル。
func render(s *screen.Screen) string {
	cols, rows := s.Size()
	var b strings.Builder
	for y := range rows {
		b.WriteString(s.Row(y))
		b.WriteString("|\n")
	}
	b.WriteString("-- styles --\n")
	legend := map[screen.Style]byte{{}: '.'}
	var order []screen.Style
	next := byte('a')
	for y := range rows {
		for x := range cols {
			st := s.Cell(x, y).Style
			l, ok := legend[st]
			if !ok {
				l, next = next, next+1
				legend[st] = l
				order = append(order, st)
			}
			b.WriteByte(l)
		}
		b.WriteByte('\n')
	}
	for _, st := range order {
		fmt.Fprintf(&b, "%c = %s\n", legend[st], strings.TrimSpace(styleString(st)))
	}
	x, y, visible := s.Cursor()
	fmt.Fprintf(&b, "-- cursor --\n%d %d %v\n", x, y, visible)
	return b.String()
}

// golden は、描いた画面をゴールデンファイルと比べる（filer §10）。-update で書き直す。
func golden(t *testing.T, name string, s *screen.Screen) {
	t.Helper()
	got := render(s)
	path := filepath.Join("testdata", "filer", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/tui -run TestGolden -update to create it)", err)
	}
	if got == string(want) {
		return
	}
	gl, wl := strings.Split(got, "\n"), strings.Split(string(want), "\n")
	for i := range max(len(gl), len(wl)) {
		var g, w string
		if i < len(gl) {
			g = gl[i]
		}
		if i < len(wl) {
			w = wl[i]
		}
		if g != w {
			t.Errorf("%s differs at line %d:\n got: %s\nwant: %s", path, i+1, g, w)
			return
		}
	}
}

// TestGoldenFilesLF は、ゴールデンファイルの改行が LF のままであることを確かめる。
// Windows のチェックアウト（core.autocrlf）で CRLF に変わると、すべての比較が 1 行目で食い違う（.gitattributes で防ぐ）。
func TestGoldenFilesLF(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("testdata", "filer", "*.golden"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden files: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "\r") {
			t.Errorf("%s has CR (checked out with CRLF; see .gitattributes)", f)
		}
	}
}

// TestGoldenMain は、メイン画面を 80×24 と 120×40 で描く（filer §5.1 のモックが出発点）。
func TestGoldenMain(t *testing.T) {
	t.Parallel()
	sc := newScene(t)
	sc.mainState()
	golden(t, "main-80x24", sc.draw(80, 24))
	golden(t, "main-120x40", sc.draw(120, 40))
	// 隠しファイルを出して、右のペインに移る（更新日時を隠す幅は TestColumns で確かめる）。
	sc.keys(char('.'))
	sc.keys(key(keys.KeyTab))
	golden(t, "main-hidden-right-80x24", sc.draw(80, 24))
}

// TestGoldenStatus は、状態行（リンク先、置き換えた名前、エラー）を描く。
func TestGoldenStatus(t *testing.T) {
	t.Parallel()
	sc := newScene(t)
	sc.moveTo("latest") // リンク。リンク先を読む
	golden(t, "status-link-80x24", sc.draw(80, 24))
	sc.moveTo("bad\x1b[31mname\u202etxt.exe")
	golden(t, "status-replaced-80x24", sc.draw(80, 24))
	sc.moveTo("unreadable") // 調べられなかった項目
	golden(t, "status-error-80x24", sc.draw(80, 24))
}

// TestGoldenDialogs は、パスの入力・実行の確認・ヘルプを描く。
func TestGoldenDialogs(t *testing.T) {
	t.Parallel()
	sc := newScene(t)
	sc.keys(char('g'), key(keys.KeyBackspace))
	sc.keys(paste(`\新しい場所` + "\nignored"))
	golden(t, "path-80x24", sc.draw(80, 24))
	sc.keys(key(keys.KeyEsc))

	sc.moveTo("setup.exe")
	sc.keys(key(keys.KeyEnter))
	if sc.a.Dialog() != app.DialogExec {
		t.Fatalf("dialog %v, want the exec confirmation", sc.a.Dialog())
	}
	golden(t, "exec-80x24", sc.draw(80, 24))
	sc.keys(char('n'))

	sc.keys(char('?'))
	golden(t, "help-80x24", sc.draw(80, 24))
	golden(t, "help-120x40", sc.draw(120, 40))
}

// TestGoldenLoadingAndErrors は、読み込み中の表示と、フォルダに入れなかったときのメッセージを描く（filer §6）。
func TestGoldenLoadingAndErrors(t *testing.T) {
	t.Parallel()
	sc := newScene(t)
	sc.moveTo("old-docs") // 開けない
	sc.keys(key(keys.KeyEnter))
	golden(t, "error-80x24", sc.draw(80, 24))
	sc.moveTo("projects")
	sc.hold = true
	sc.keys(key(keys.KeyEnter))
	sc.run(sc.a.Update(sc.delayed[len(sc.delayed)-1].Run())) // 0.2 秒が過ぎた
	golden(t, "loading-80x24", sc.draw(80, 24))
	sc.keys(key(keys.KeyEsc))
	if text, _ := sc.a.Message(); text != msg.LoadCanceled {
		t.Errorf("message after Esc = %q", text)
	}
}

// TestGoldenTooSmall は、80×24 より小さい端末では「端末が小さすぎます」だけを出すことを確かめる（filer §3）。
func TestGoldenTooSmall(t *testing.T) {
	t.Parallel()
	sc := newScene(t)
	golden(t, "small-79x24", sc.draw(79, 24))
	s := sc.draw(80, 23)
	if got := strings.TrimSpace(s.Row(0)); got != msg.TooSmall {
		t.Errorf("80x23: %q", got)
	}
}

// TestKeyMap は、キーの割り当てと、貼り付けをコマンドとして解釈しないことを確かめる（filer §7、tui §5）。
func TestKeyMap(t *testing.T) {
	t.Parallel()
	sc := newScene(t)
	for _, tt := range []struct {
		ev   keys.Event
		want app.ActionKind
	}{
		{key(keys.KeyUp), app.ActUp}, {char('k'), app.ActUp}, {key(keys.KeyDown), app.ActDown}, {char('j'), app.ActDown},
		{key(keys.KeyPageUp), app.ActPageUp}, {key(keys.KeyPageDown), app.ActPageDown},
		{key(keys.KeyHome), app.ActHome}, {key(keys.KeyEnd), app.ActEnd},
		{key(keys.KeyEnter), app.ActEnter}, {key(keys.KeyBackspace), app.ActParent}, {key(keys.KeyTab), app.ActNextPane},
		{key(keys.KeyLeft), app.ActFocusOrUp}, {key(keys.KeyRight), app.ActFocusOrUp},
		{char(' '), app.ActMark}, {char('a'), app.ActMarkAll}, {char('.'), app.ActToggleHidden}, {ctrl('r'), app.ActReload},
		{char('g'), app.ActGoPath}, {char('='), app.ActSyncOther}, {char('?'), app.ActHelp}, {char('q'), app.ActQuit},
		{key(keys.KeyEsc), app.ActCancel}, {char('c'), app.ActNotYet}, {char('D'), app.ActNotYet},
	} {
		if act, ok := sc.f.action(tt.ev); !ok || act.Kind != tt.want {
			t.Errorf("%v: %v %v, want %v", tt.ev, act.Kind, ok, tt.want)
		}
	}
	for _, ev := range []keys.Event{paste("q"), paste("\r"), ctrl('c'), char('x'), {Kind: keys.UnknownEvent, Raw: "\x1b[?1c"}} {
		if act, ok := sc.f.action(ev); ok {
			t.Errorf("%v: %v, want no action", ev, act)
		}
	}
	if act, _ := sc.f.action(key(keys.KeyRight)); act.Pane != 1 {
		t.Errorf("Right: pane %d, want 1", act.Pane)
	}
}

// TestPastePath は、パスの入力欄への貼り付けは最初の行だけを入れることを確かめる。改行は LF・CRLF・CR のどれでも届く。
func TestPastePath(t *testing.T) {
	t.Parallel()
	sc := newScene(t)
	sc.keys(char('g'))
	for _, tt := range []struct{ text, want string }{
		{"C:\\x\nnext", "C:\\x"}, {"/a b\r\nnext", "/a b"}, {"/tmp\rnext", "/tmp"}, {"one", "one"}, {"\rnext", ""},
	} {
		act, ok := sc.f.action(paste(tt.text))
		if !ok || act.Kind != app.ActInsert || act.Text != tt.want {
			t.Errorf("paste %q: %v %q %v, want insert %q", tt.text, act.Kind, act.Text, ok, tt.want)
		}
	}
}

// TestColumns は、ペインの内側の幅による欄の割り当てを確かめる。狭ければ更新日時を隠して名前に回す（filer §5.1）。
func TestColumns(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		w, nameW int
		date     bool
	}{
		{38, 19, true},  // 80 桁の 2 ペイン
		{58, 39, true},  // 120 桁の 2 ペイン
		{35, 16, true},  // 名前の欄が minNameW ちょうど
		{34, 27, false}, // 名前の欄が minNameW より狭くなるので、更新日時を隠す
		{8, 1, false},
		{1, 1, false},
	} {
		nameW, date := columns(tt.w)
		if nameW != tt.nameW || date != tt.date {
			t.Errorf("columns(%d) = %d, %v, want %d, %v", tt.w, nameW, date, tt.nameW, tt.date)
		}
		if used := 1 + nameW + 1 + sizeW; date && used+1+dateW > tt.w || !date && tt.w >= 7 && used > tt.w {
			t.Errorf("columns(%d): %d columns do not fit", tt.w, used)
		}
	}
}

// TestRunFiler は、端末（偽物）の上でファイラーを動かし、本物の fsops で一覧を描いて、q で終わることを確かめる。
func TestRunFiler(t *testing.T) {
	t.Parallel()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newFakeTerm(80, 24)
	sent := false
	f.onWrite = func(out string) {
		if !sent && strings.Contains(out, "hello.txt") { // 一覧を描いたら終える
			sent = true
			f.send("q")
		}
	}
	cfg := app.DefaultConfig([]string{dir, dir})
	cfg.Open = func(p string) error { t.Errorf("opened %s", p); return nil } // アプリを起動しない
	a, cmds := app.New(cfg)
	if err := RunFiler(New(f), a, cmds); err != nil {
		t.Fatalf("RunFiler: %v", err)
	}
	if !sent || f.restoreCount() != 1 {
		t.Errorf("listing drawn %v, restored %d times", sent, f.restoreCount())
	}
}

// BenchmarkDraw100k は、10 万件のフォルダで、カーソルを 1 行動かして描き、差分を書く時間を測る（filer VU8、VU1 の「スクロールが遅れない」）。
func BenchmarkDraw100k(b *testing.B) {
	entries := make([]fsops.Entry, 100_000)
	for i := range entries {
		entries[i] = file(fmt.Sprintf("報告書_%06d.docx", i), int64(i)*1000, at(9, 1+i%28, i%24, i%60, 0))
	}
	cfg := app.Config{
		Dirs:    []string{leftDir, rightDir},
		ReadDir: func(string) ([]fsops.Entry, error) { return entries, nil },
		Now:     func() time.Time { return now },
	}
	a, cmds := app.New(cfg)
	for _, c := range cmds {
		if c.Delay == 0 {
			a.Update(c.Run())
		}
	}
	f := &Filer{app: a}
	s := screen.New(120, 40)
	for b.Loop() {
		a.Do(app.Action{Kind: app.ActDown})
		s.Fill(screen.Region{W: 120, H: 40}, screen.Style{})
		f.Draw(s)
		if err := s.Flush(io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}

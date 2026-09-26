package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/lineedit"
	"github.com/zredjet/tana/internal/listing"
	"github.com/zredjet/tana/internal/msg"
	"github.com/zredjet/tana/internal/platform"
)

// slowLoad は、読み込み中の表示を出すまでの時間（filer §6）。
const slowLoad = 200 * time.Millisecond

// Cmd は、作業用の goroutine で動かす処理。Delay の後に Run を動かし、戻り値を Update に渡す（filer §10）。
// Run は App の状態に触れない（必要な値は Cmd を作るときに写す）。
type Cmd struct {
	Delay time.Duration
	Run   func() any
}

// Config は、App の設定と、OS・ファイルシステムへの入口（テストで差し替える）。
type Config struct {
	Dirs           []string // 各ペインの最初のフォルダ（絶対パス）。数がペインの数になる
	DotFilesHidden bool     // 名前が . で始まるものを隠しファイルとして扱う（filer §6）

	ReadDir      func(dir string) ([]fsops.Entry, error)
	Readlink     func(path string) (string, error)
	ReadHead     func(path string, max int) (fsops.Head, error) // プレビュー（filer §6）
	NewPlan      func(ctx context.Context, req fsops.Request) (Plan, error)
	Rename       func(path, newName string) error // 名前の変更（filer §8.7）
	Mkdir        func(parent, name string) error  // 新しいフォルダ（filer §8.7）
	Wake         func()                           // 実行中の進捗が届いたことをイベントループに知らせる（tui が設定する）。nil なら知らせない
	Open         func(path string) error
	IsExecutable func(path string, isDir bool) bool
	CanOpen      func(path string) bool
	Now          func() time.Time
	Log          func(err error) // 英語の詳細の記録（TANA_LOG。filer §10）。nil なら記録しない

	// TrashMayAsk は、ごみ箱へ入れるときに OS が完全削除の確認ダイアログを出しうるか（Windows。filer §8.4、fsops §12.2）。
	TrashMayAsk bool
}

// DefaultConfig は、本物の fsops と platform を使う設定を返す。
func DefaultConfig(dirs []string) Config {
	return Config{
		Dirs:           dirs,
		DotFilesHidden: platform.DotFilesHidden,
		ReadDir:        fsops.ReadDir,
		Readlink:       fsops.Readlink,
		ReadHead:       fsops.ReadHead,
		NewPlan:        newFsopsPlan,
		Rename:         fsops.Rename,
		Mkdir:          fsops.Mkdir,
		Open:           platform.Open,
		IsExecutable:   platform.IsExecutable,
		CanOpen:        platform.CanOpen,
		Now:            time.Now,
		TrashMayAsk:    platform.TrashMayAsk,
	}
}

// DialogKind は、開いているダイアログの種類。
type DialogKind int

const (
	DialogNone   DialogKind = iota
	DialogPath              // パスの入力（g）
	DialogExec              // 実行ファイルを開く前の確認（filer §7）
	DialogHelp              // ヘルプ
	DialogRename            // 名前の変更（filer §8.7）
	DialogNewDir            // 新しいフォルダ（filer §8.7）
)

type dialog struct {
	kind  DialogKind
	edit  *lineedit.Editor // DialogPath・DialogRename・DialogNewDir
	path  string           // DialogExec: 開くパス。DialogRename: 変える項目のパス（列挙で得た名前から作る。U4）
	name  string           // DialogExec: 表示する名前。DialogRename: 今の名前
	frame int              // 開いたときの a.frames。これより後に描いてから届いたキーだけで確定する（filer U2）

	// DialogRename・DialogNewDir
	dir  string // 項目のあるフォルダ、フォルダを作る場所
	pane int    // 始めたペイン
	err  string // fsops のエラーの文言（入力欄の下に出す）
	busy int    // 変更・作成を待っている処理の世代（0 なら待っていない）
}

// App は、画面の状態。
type App struct {
	cfg        Config
	panes      []*Pane
	active     int
	showHidden bool
	message    string
	messageErr bool
	dialog     dialog
	gen        int // 読み込み・開く処理の世代。古い結果を捨てる
	opening    int // 関連付けで開く前の確認をしている世代（0 ならしていない）
	frames     int // 描いた回数（Drawn）
	quit       bool

	needs        Needs        // 表示形式が求めるもの（SetNeeds）
	yanked       []string     // 覚えた項目のパス（y。filer §7）
	yankDir      string       // 覚えたときのペインのフォルダ（見出しに出す）
	op           *operation   // 進めているファイル操作（計画から結果まで。filer §8）
	result       *resultState // 直前の操作の結果（L でもう一度出す）
	preview      Preview      // 操作中のペインのカーソル行のプレビュー（読んでいる途中なら Kind が PreviewNone）
	previewGen   int          // プレビューの世代。カーソルが動いたら古い読み込みの結果を捨てる
	previewStale bool         // 一覧を読み直したので、同じ項目でもプレビューを読み直す
}

// New は、App を作り、各ペインの最初の読み込みを返す。
func New(cfg Config) (*App, []Cmd) {
	a := &App{cfg: cfg}
	var cmds []Cmd
	for i, dir := range cfg.Dirs {
		p := &Pane{dir: dir, marks: map[string]struct{}{}, targets: map[string]string{}}
		a.panes = append(a.panes, p)
		cmds = append(cmds, a.load(i, dir, loadInitial, "")...)
	}
	return a, cmds
}

// ---- tui が読む状態 ----

// Panes は、ペインの並びを返す。
func (a *App) Panes() []*Pane { return a.panes }

// Active は、操作中のペインの番号を返す。
func (a *App) Active() int { return a.active }

// ShowHidden は、隠しファイルを表示しているかを返す。
func (a *App) ShowHidden() bool { return a.showHidden }

// Message は、メッセージ行の文言と、それがエラーかを返す。
func (a *App) Message() (text string, isErr bool) { return a.message, a.messageErr }

// Dialog は、開いているダイアログの種類を返す。
func (a *App) Dialog() DialogKind { return a.dialog.kind }

// PathEditor は、パスの入力欄を返す（DialogPath のとき）。
func (a *App) PathEditor() *lineedit.Editor { return a.dialog.edit }

// NameView は、名前の変更・新しいフォルダの画面の内容（filer §8.7）。
type NameView struct {
	Edit *lineedit.Editor // 入力欄（元のバイト列を持つ。表示する形への置き換えは描くときに行う。U4）
	Name string           // 名前の変更: 今の名前（列挙で得たもの）
	Dir  string           // 項目のあるフォルダ、フォルダを作る場所
	Err  string           // fsops のエラーの文言（入力欄の下に出す）
	Busy bool             // 変更・作成を待っている
}

// NameDialog は、名前の変更・新しいフォルダの画面の内容を返す（DialogRename・DialogNewDir のとき）。
func (a *App) NameDialog() NameView {
	d := a.dialog
	return NameView{Edit: d.edit, Name: d.name, Dir: d.dir, Err: d.err, Busy: d.busy != 0}
}

// ExecName は、実行の確認で表示する名前を返す（DialogExec のとき）。
func (a *App) ExecName() string { return a.dialog.name }

// Now は、日時の表示に使う現在の時刻を返す。
func (a *App) Now() time.Time { return a.cfg.Now() }

// Quit は、終了するかを返す。
func (a *App) Quit() bool { return a.quit }

// Drawn は、tui が画面を描いた後に呼ぶ。確認のダイアログは、描いた後に届いたキーでだけ確定する（先行入力を捨てる。filer U2）。
func (a *App) Drawn() { a.frames++ }

// ---- 操作 ----

// ActionKind は、利用者の操作の種類。キーとの対応は tui が決める（filer §7・§15）。
type ActionKind int

const (
	ActUp ActionKind = iota + 1
	ActDown
	ActPageUp
	ActPageDown
	ActHome
	ActEnd
	ActEnter        // フォルダに入る、ファイルを開く
	ActEnterDir     // フォルダに入る（ファイルでは何もしない。h・l の l）
	ActParent       // 親のフォルダへ
	ActNextPane     // 次のペインへ
	ActMark         // マークの切り替え
	ActMarkAll      // すべてマークする・すべて外す
	ActToggleHidden // 隠しファイルの表示の切り替え
	ActReload       // 再読み込み
	ActGoPath       // パスを入力して移動
	ActSyncOther    // 次のペインを同じフォルダにする
	ActHelp
	ActQuit
	ActCancel // Esc。読み込みの中止、ダイアログを閉じる

	// ダイアログの中の操作。
	ActYes
	ActNo
	ActInsert // Action.Text を入れる（打った文字、貼り付け）
	ActBackspace
	ActDelete
	ActLeft
	ActRight
	ActLineHome
	ActLineEnd
	ActSubmit

	// ファイル操作（filer §7・§8）。
	ActYank       // 対象を覚える
	ActPasteCopy  // 覚えた項目をコピーする
	ActPasteMove  // 覚えた項目を移動する
	ActDecide     // カーソル行の衝突に Action.Decision を設定する
	ActDecideAll  // すべての衝突に Action.Decision を設定する（使えるものだけ）
	ActNewerOnly  // 新しいときだけ上書き
	ActToggle     // 展開・折りたたみ（衝突の内側、結果の詳細）
	ActUnsetOnly  // 未選択の衝突だけを表示する
	ActEnglish    // 結果の英語の詳細
	ActLastResult // 直前の操作の結果をもう一度出す（L）
	ActForceQuit  // 中止しても応答がないときの終了（Q。filer §8.4）
	ActTrash      // ごみ箱へ（d）
	ActPurge      // 完全削除（D。確認画面・結果の画面では、ごみ箱に入らない項目の完全削除の確認へ）
	ActRename     // 名前の変更（r）
	ActNewDir     // 新しいフォルダ（n）
)

// Action は、利用者の操作。
type Action struct {
	Kind     ActionKind
	Text     string         // ActInsert
	Decision fsops.Decision // ActDecide・ActDecideAll
}

// Do は、利用者の操作を行う。キー入力のたびにメッセージ行を消す（filer §5.1）。
func (a *App) Do(act Action) []Cmd {
	return append(a.do(act), a.follow()...)
}

func (a *App) do(act Action) []Cmd {
	switch {
	case a.op != nil && a.op.planning:
		// 計画を作っている間に届いたキーは捨てる（filer U2）。Esc だけは中止にする。
		if act.Kind == ActCancel {
			a.discard()
			a.setMessage(msg.Kind(fsops.KindCanceled), false)
		}
		return nil
	case a.op != nil:
		a.message, a.messageErr = "", false // キー入力のたびにメッセージ行を消す（ファイル操作の画面でも）
		switch a.op.screen {
		case ScreenConfirm:
			return a.doConfirm(act)
		case ScreenConflicts:
			return a.doConflicts(act)
		case ScreenProgress:
			return a.doProgress(act) // 実行中はほかの操作を受け付けない（filer §7）
		case ScreenDelete:
			return a.doDelete(act)
		}
		return nil
	case a.result != nil && a.result.open:
		a.message, a.messageErr = "", false
		return a.doResult(act)
	}
	if a.dialog.kind != DialogNone {
		return a.doDialog(act)
	}
	a.message, a.messageErr = "", false
	p := a.panes[a.active]
	if act.Kind == ActCancel {
		return a.cancel()
	}
	// 読み込み中のペインでは、そのペインの操作を受け付けない（読み込みが終わると一覧が変わるため）。
	if p.load != nil && paneLocal(act.Kind) {
		return nil
	}
	switch act.Kind {
	case ActUp:
		p.move(-1)
	case ActDown:
		p.move(1)
	case ActPageUp:
		p.move(-max(p.rows, 1))
	case ActPageDown:
		p.move(max(p.rows, 1))
	case ActHome:
		p.move(-len(p.visible))
	case ActEnd:
		p.move(len(p.visible))
	case ActEnter:
		return a.enter()
	case ActEnterDir:
		return a.enterDir()
	case ActParent:
		return a.parent(a.active)
	case ActNextPane:
		a.active = (a.active + 1) % len(a.panes)
	case ActMark:
		if it, ok := p.current(); ok && !it.Parent {
			p.toggleMark(it.Name)
		}
		p.move(1)
	case ActMarkAll:
		p.markAll()
	case ActToggleHidden:
		a.showHidden = !a.showHidden
		for _, q := range a.panes {
			q.filter(a.showHidden)
		}
	case ActReload:
		// 最初の読み込みに失敗した・中止したペイン（一覧がない）は、起動時と同じく読み込み直す。
		var cmds []Cmd
		for i, q := range a.panes {
			kind := loadReload
			if !q.loaded {
				kind = loadInitial
			}
			cmds = append(cmds, a.load(i, q.dir, kind, "")...)
		}
		return cmds
	case ActGoPath:
		a.dialog = dialog{kind: DialogPath, edit: lineedit.New(p.dir, len(p.dir))}
	case ActSyncOther:
		if len(a.panes) > 1 && p.loaded {
			return a.load((a.active+1)%len(a.panes), p.dir, loadGo, "")
		}
	case ActHelp:
		a.dialog = dialog{kind: DialogHelp}
	case ActQuit:
		a.quit = true
	case ActYank:
		a.yank()
	case ActPasteCopy:
		return a.paste(fsops.OpCopy)
	case ActPasteMove:
		return a.paste(fsops.OpMove)
	case ActLastResult:
		if a.result != nil {
			a.result.open, a.result.frame = true, a.frames
		}
	case ActTrash:
		return a.trash()
	case ActPurge:
		return a.purge()
	case ActRename:
		a.rename()
	case ActNewDir:
		a.newDir()
	}
	return nil
}

// paneLocal は、操作中のペインの一覧を使う操作かを返す。
func paneLocal(k ActionKind) bool {
	switch k {
	case ActUp, ActDown, ActPageUp, ActPageDown, ActHome, ActEnd, ActEnter, ActEnterDir, ActParent, ActMark, ActMarkAll, ActGoPath, ActSyncOther,
		ActYank, ActPasteCopy, ActPasteMove, ActTrash, ActPurge, ActRename, ActNewDir:
		return true
	}
	return false
}

// cancel は、読み込みと、開く前の確認を中止する（filer U5。待つのをやめて結果を捨てる）。
func (a *App) cancel() []Cmd {
	opening := a.opening != 0
	a.opening = 0
	loading := false
	for _, p := range a.panes {
		if p.load != nil {
			p.load = nil
			loading = true
		}
	}
	switch {
	case loading:
		a.setMessage(msg.LoadCanceled, false)
	case opening:
		a.setMessage(msg.Kind(fsops.KindCanceled), false)
	}
	return nil
}

// doDialog は、ダイアログを開いているときの操作を行う。
func (a *App) doDialog(act Action) []Cmd {
	d := &a.dialog
	switch d.kind {
	case DialogHelp:
		if act.Kind != ActInsert { // 貼り付けでは閉じない
			a.dialog = dialog{}
		}
	case DialogExec:
		if a.frames <= d.frame {
			return nil // 確認を描く前に届いたキー（filer U2）
		}
		switch act.Kind {
		case ActYes:
			path, name := d.path, d.name
			a.dialog = dialog{}
			return a.openCmd(path, name)
		case ActNo, ActCancel:
			a.dialog = dialog{}
		}
	case DialogRename, DialogNewDir:
		return a.doName(act)
	case DialogPath:
		if edit(d.edit, act) {
			return nil
		}
		switch act.Kind {
		case ActCancel:
			a.dialog = dialog{}
		case ActSubmit:
			text := d.edit.Text()
			a.dialog = dialog{}
			a.message, a.messageErr = "", false
			if strings.TrimSpace(text) == "" {
				return nil
			}
			return a.load(a.active, Resolve(a.panes[a.active].dir, text), loadGo, "")
		}
	}
	return nil
}

// edit は、入力欄 e の編集の操作（文字を入れる、消す、カーソルを動かす）を行う。編集の操作でなければ false。
func edit(e *lineedit.Editor, act Action) bool {
	switch act.Kind {
	case ActInsert:
		e.Insert(act.Text)
	case ActBackspace:
		e.DeleteBackward()
	case ActDelete:
		e.DeleteForward()
	case ActLeft:
		e.Left()
	case ActRight:
		e.Right()
	case ActLineHome:
		e.Home()
	case ActLineEnd:
		e.End()
	default:
		return false
	}
	return true
}

// Resolve は、入力されたパス（g の入力欄、起動の引数）を絶対パスにする。相対パスは dir から数える。
// Windows の D: はドライブのルートに、ドライブ名のない \foo は dir のドライブのルートからにする。
// 入力されたパスは利用者が打った文字列で、表示用に加工したものではない（filer U4）。
func Resolve(dir, input string) string {
	switch {
	case filepath.IsAbs(input):
		return filepath.Clean(input)
	case filepath.VolumeName(input) != "":
		vol := filepath.VolumeName(input)
		return filepath.Clean(vol + string(filepath.Separator) + input[len(vol):])
	case input != "" && os.IsPathSeparator(input[0]): // Windows のみ（Unix では IsAbs）
		return filepath.Clean(filepath.VolumeName(dir) + input)
	}
	return filepath.Join(dir, input)
}

func (a *App) setMessage(text string, isErr bool) { a.message, a.messageErr = text, isErr }

func (a *App) logErr(err error) {
	if err != nil && a.cfg.Log != nil {
		a.cfg.Log(err)
	}
}

// ---- フォルダの読み込み ----

type loadKind int

const (
	loadInitial loadKind = iota + 1 // 起動時。開けなければ、開ける祖先を表示する
	loadReload                      // 再読み込み。フォルダが消えていれば、存在する祖先を表示する（filer §6）
	loadGo                          // ほかのフォルダへ（入る、親へ、パスの入力）。開けなければ留まる
	loadLink                        // リンクに入る。リンク先がフォルダでなければ、開く処理に移る
	loadLinkDir                     // リンクに入る（l・→）。リンク先がフォルダでなければ何もしない
)

type loading struct {
	gen   int
	dir   string
	kind  loadKind
	focus string // 読み込んだ後にカーソルを置く名前（親へ移ったとき、来たフォルダ）
	slow  bool   // 読み込み中の表示を出す
}

// loaded は、読み込みの結果。
type loaded struct {
	pane, gen int
	dir       string // 読み込んだフォルダ（祖先を表示するときは、その祖先）
	items     []listing.Item
	err       error // 求めたフォルダを開けなかった理由
	notDir    bool  // loadLink で、リンク先がフォルダでなかった
}

// loadSlow は、読み込みが slowLoad を超えたこと。
type loadSlow struct{ pane, gen int }

// load は、ペイン i にフォルダ dir を読み込む処理を返す。
func (a *App) load(i int, dir string, kind loadKind, focus string) []Cmd {
	a.gen++
	gen := a.gen
	a.panes[i].load = &loading{gen: gen, dir: dir, kind: kind, focus: focus}
	readDir, dotHidden := a.cfg.ReadDir, a.cfg.DotFilesHidden
	run := func() any {
		entries, err := readDir(dir)
		if err == nil {
			return loaded{pane: i, gen: gen, dir: dir, items: listing.Build(dir, entries, dotHidden)}
		}
		res := loaded{pane: i, gen: gen, dir: dir, err: err}
		kindOf := fsops.KindUnknown
		if oe, ok := errors.AsType[*fsops.OpError](err); ok {
			kindOf = oe.Kind
		}
		switch {
		case (kind == loadLink || kind == loadLinkDir) && kindOf == fsops.KindNotFound:
			res.notDir = true
		case kind == loadInitial || kind == loadReload && kindOf == fsops.KindNotFound:
			// 開ける祖先を探す（filer §6）。
			for d := filepath.Dir(dir); ; d = filepath.Dir(d) {
				if entries, err := readDir(d); err == nil {
					res.dir, res.items = d, listing.Build(d, entries, dotHidden)
					break
				}
				if filepath.Dir(d) == d {
					break
				}
			}
		}
		return res
	}
	return []Cmd{{Run: run}, {Delay: slowLoad, Run: func() any { return loadSlow{pane: i, gen: gen} }}}
}

// Update は、Cmd の結果を反映する。
func (a *App) Update(m any) []Cmd {
	return append(a.update(m), a.follow()...)
}

func (a *App) update(m any) []Cmd {
	switch m := m.(type) {
	case loaded:
		return a.loaded(m)
	case loadSlow:
		if p := a.panes[m.pane]; p.load != nil && p.load.gen == m.gen {
			p.load.slow = true
		}
	case linkRead:
		// 読み取りを始めた後に一覧を置き換えていれば（再読み込みを含む）、結果を捨てる。
		if p := a.panes[m.pane]; p.listGen == m.listGen {
			delete(p.pending, m.name)
			p.targets[m.name] = m.target
		}
	case checked:
		return a.checked(m)
	case parentRead:
		a.parentRead(m)
	case previewTick:
		return a.previewTick(m)
	case previewRead:
		if m.gen == a.previewGen {
			a.preview = m.preview
		}
	case planned:
		a.planned(m)
	case planSlow:
		if a.op != nil && a.op.gen == m.gen && a.op.planning {
			a.op.slow = true
		}
	case executed:
		return a.executed(m)
	case opTick:
		return a.opTick(m)
	case named:
		return a.named(m)
	case opened:
		if m.err != nil {
			a.logErr(m.err)
			a.setMessage(msg.OpenFailed(m.name), true)
		} else {
			a.setMessage(msg.Opened(m.name), false)
		}
	}
	return nil
}

func (a *App) loaded(m loaded) []Cmd {
	p := a.panes[m.pane]
	if p.load == nil || p.load.gen != m.gen {
		return nil // 中止した、または新しい読み込みに置き換えた
	}
	ld := p.load
	p.load = nil
	if m.notDir {
		if ld.kind == loadLinkDir {
			return nil // l・→ はフォルダに入るだけ
		}
		// リンク先がフォルダでない。ファイルとして開く（filer §7）。
		return a.startOpen(m.dir, filepath.Base(m.dir))
	}
	if m.err != nil {
		a.logErr(m.err)
		if m.items == nil {
			a.setMessage(msg.CannotOpenDir(msg.Error(m.err)), true)
			return nil
		}
		a.setMessage(msg.ShowingAncestor(msg.Error(m.err)), true)
	}
	keep := ld.kind == loadReload && m.dir == p.dir && p.loaded
	p.set(m.dir, m.items, a.showHidden, keep, ld.focus)
	if m.pane == a.active {
		a.previewStale = true // 読み直した一覧で、プレビューも読み直す
	}
	return a.linkTarget(m.pane)
}

// enter は、カーソル行の項目に入る・開く（filer §7）。
func (a *App) enter() []Cmd {
	p := a.panes[a.active]
	it, ok := p.current()
	if !ok {
		return nil
	}
	path := filepath.Join(p.dir, it.Name) // 列挙で得た名前から作る（U4）
	switch {
	case it.Parent:
		return a.parent(a.active)
	case it.Err != nil:
		a.setMessage(msg.Error(it.Err), true)
		return nil
	case it.IsDir():
		return a.load(a.active, path, loadGo, "")
	case it.Info.Type == fsops.TypeSymlink:
		return a.load(a.active, path, loadLink, "")
	case it.Info.Type == fsops.TypeSpecial:
		a.setMessage(msg.Kind(fsops.KindUnsupportedType), true)
		return nil
	}
	return a.startOpen(path, it.Name)
}

// enterDir は、カーソル行がフォルダ（リンク・ジャンクションのフォルダ、.. を含む）なら入る。ファイルでは何もしない（l・→）。
func (a *App) enterDir() []Cmd {
	p := a.panes[a.active]
	it, ok := p.current()
	if !ok || it.Err != nil {
		return nil
	}
	path := filepath.Join(p.dir, it.Name) // 列挙で得た名前から作る（U4）
	switch {
	case it.Parent:
		return a.parent(a.active)
	case it.IsDir():
		return a.load(a.active, path, loadGo, "")
	case it.Info.Type == fsops.TypeSymlink:
		return a.load(a.active, path, loadLinkDir, "")
	}
	return nil
}

// parent は、ペイン i を親のフォルダにし、カーソルを来たフォルダに置く。
func (a *App) parent(i int) []Cmd {
	p := a.panes[i]
	if !p.loaded || listing.IsRoot(p.dir) {
		return nil
	}
	return a.load(i, filepath.Dir(p.dir), loadGo, filepath.Base(p.dir))
}

// ---- 関連付けで開く ----

// checked は、開く前の確認（開ける名前か、実行ファイルか）の結果。
type checked struct {
	gen        int
	path, name string
	canOpen    bool
	exec       bool
}

// opened は、関連付けで開いた結果。
type opened struct {
	name string
	err  error
}

// startOpen は、開く前の確認を作業用の goroutine で行う（実行ファイルの判定はファイルシステムを調べることがある。filer U5）。
func (a *App) startOpen(path, name string) []Cmd {
	a.gen++
	gen := a.gen
	a.opening = gen
	canOpen, isExec := a.cfg.CanOpen, a.cfg.IsExecutable
	return []Cmd{{Run: func() any {
		return checked{gen: gen, path: path, name: name, canOpen: canOpen(path), exec: isExec(path, false)}
	}}}
}

func (a *App) checked(m checked) []Cmd {
	if a.opening != m.gen {
		return nil
	}
	a.opening = 0
	switch {
	case !m.canOpen:
		a.setMessage(msg.CannotOpenName, true)
		return nil
	case m.exec:
		a.dialog = dialog{kind: DialogExec, path: m.path, name: m.name, frame: a.frames}
		return nil
	}
	return a.openCmd(m.path, m.name)
}

func (a *App) openCmd(path, name string) []Cmd {
	open := a.cfg.Open
	return []Cmd{{Run: func() any { return opened{name: name, err: open(path)} }}}
}

// ---- リンク先 ----

// linkRead は、リンク先の読み取りの結果（状態行に出す）。
type linkRead struct {
	pane, listGen int
	name, target  string
}

// linkTarget は、ペイン i のカーソル行がリンク・ジャンクションで、リンク先をまだ読んでいなければ、読む処理を返す。
func (a *App) linkTarget(i int) []Cmd {
	p := a.panes[i]
	it, ok := p.current()
	if !ok || p.load != nil || it.Err != nil || it.Info.Type != fsops.TypeSymlink && it.Info.Type != fsops.TypeJunction {
		return nil
	}
	if _, ok := p.targets[it.Name]; ok {
		return nil
	}
	if _, ok := p.pending[it.Name]; ok {
		return nil
	}
	p.pending[it.Name] = struct{}{}
	path, name, gen, readlink := filepath.Join(p.dir, it.Name), it.Name, p.listGen, a.cfg.Readlink
	return []Cmd{{Run: func() any {
		target, err := readlink(path)
		if err != nil {
			target = "(" + msg.Error(err) + ")"
		}
		return linkRead{pane: i, listGen: gen, name: name, target: target}
	}}}
}

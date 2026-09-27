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

// App は、画面の状態。
type App struct {
	cfg        Config
	panes      []*Pane
	active     int // 操作中のペインの ID
	showHidden bool
	message    string // 知らせ（メッセージ行。filer §4 の置き方「知らせ」）
	messageErr bool
	ws         mounted    // 作業場（フォーカスの道筋の根。重ねる部品がないとき）
	modals     []*mounted // 作業場の上に重ねた部品（下から順）
	inner      focusKey   // 道筋の一番内側（変わったら門を掛け直す）
	gen        int        // ID と世代の払い出し（ペイン、読み込み・開く処理・ファイル操作・名前の変更）。古い結果を捨てる
	opening    int        // 関連付けで開く前の確認をしている世代（0 ならしていない）
	frames     int        // 描いた回数（Drawn）
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
	a.ws = mounted{c: workspaceComp{}, id: a.newID()}
	var cmds []Cmd
	for _, dir := range cfg.Dirs {
		p := &Pane{id: a.newID(), dir: dir, marks: map[string]struct{}{}, targets: map[string]string{}}
		p.node = mounted{c: paneComp{p: p}, id: p.id}
		a.panes = append(a.panes, p)
	}
	if len(a.panes) > 0 {
		a.active = a.panes[0].id
	}
	for _, p := range a.panes {
		cmds = append(cmds, a.load(p, p.dir, loadInitial, "")...)
	}
	a.settleFocus()
	return a, cmds
}

// ---- tui が読む状態 ----

// Panes は、ペインの並びを返す。
func (a *App) Panes() []*Pane { return a.panes }

// Active は、操作中のペインの番号（Panes の添字）を返す。
func (a *App) Active() int { return a.indexOf(a.active) }

// newID は、ID と世代を払い出す（ペイン、読み込み・開く処理・ファイル操作・名前の変更で 1 つの数を使う）。
func (a *App) newID() int {
	a.gen++
	return a.gen
}

// cur は、操作中のペインを返す。
func (a *App) cur() *Pane { return a.panes[a.Active()] }

// indexOf は、ID id のペインの添字を返す。なければ -1。
func (a *App) indexOf(id int) int {
	for i, p := range a.panes {
		if p.id == id {
			return i
		}
	}
	return -1
}

// paneOf は、ID id のペインを返す。なければ nil（作業用の goroutine の結果が届く前に、ペインがなくなった）。
func (a *App) paneOf(id int) *Pane {
	if i := a.indexOf(id); i >= 0 {
		return a.panes[i]
	}
	return nil
}

// ShowHidden は、隠しファイルを表示しているかを返す。
func (a *App) ShowHidden() bool { return a.showHidden }

// Message は、メッセージ行の文言と、それがエラーかを返す。
func (a *App) Message() (text string, isErr bool) { return a.message, a.messageErr }

// Dialog は、開いているダイアログの種類を返す（一番上の重ねる部品から求める）。
func (a *App) Dialog() DialogKind {
	switch a.topRole() {
	case RoleHelp:
		return DialogHelp
	case RolePath:
		return DialogPath
	case RoleExec:
		return DialogExec
	case RoleRename:
		return DialogRename
	case RoleNewDir:
		return DialogNewDir
	}
	return DialogNone
}

// PathEditor は、パスの入力欄を返す（DialogPath のとき）。
func (a *App) PathEditor() *lineedit.Editor {
	if v, ok := ModalView[PathView](a); ok {
		return v.Edit
	}
	return nil
}

// NameDialog は、名前の変更・新しいフォルダの画面の内容を返す（DialogRename・DialogNewDir のとき）。
func (a *App) NameDialog() NameView {
	v, _ := ModalView[NameView](a)
	return v
}

// ExecName は、実行の確認で表示する名前を返す（DialogExec のとき）。
func (a *App) ExecName() string {
	v, _ := ModalView[ExecView](a)
	return v.Name
}

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
	Role     Role           // 届ける部品の役割（tui の keymap がキーを解決した役割）。RoleNone なら、道筋の内側から探す
}

// Do は、利用者の操作を行う。キー入力のたびにメッセージ行を消す（filer §5.1）。
func (a *App) Do(act Action) []Cmd {
	a.KeyPressed()
	cmds := append(a.dispatch(act), a.follow()...)
	a.settleFocus()
	return cmds
}

// ClearMessage は、メッセージ行を消す。操作にならないキー入力（割り当てのないキー、表示形式の切り替えなど）のときに tui が呼ぶ（filer §5.1）。
func (a *App) ClearMessage() { a.message, a.messageErr = "", false }

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

// loaded は、読み込みの結果。pane はペインの ID。
type loaded struct {
	pane, gen int
	dir       string // 読み込んだフォルダ（祖先を表示するときは、その祖先）
	items     []listing.Item
	err       error // 求めたフォルダを開けなかった理由
	notDir    bool  // loadLink で、リンク先がフォルダでなかった
}

// loadSlow は、読み込みが slowLoad を超えたこと。pane はペインの ID。
type loadSlow struct{ pane, gen int }

// load は、ペイン p にフォルダ dir を読み込む処理を返す。
func (a *App) load(p *Pane, dir string, kind loadKind, focus string) []Cmd {
	gen, i := a.newID(), p.id
	p.load = &loading{gen: gen, dir: dir, kind: kind, focus: focus}
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
	cmds := append(a.update(m), a.follow()...)
	a.settleFocus()
	return cmds
}

func (a *App) update(m any) []Cmd {
	switch m := m.(type) {
	case loaded:
		return a.loaded(m)
	case loadSlow:
		if p := a.paneOf(m.pane); p != nil && p.load != nil && p.load.gen == m.gen {
			p.load.slow = true
		}
	case linkRead:
		// 読み取りを始めた後に一覧を置き換えていれば（再読み込みを含む）、結果を捨てる。
		if p := a.paneOf(m.pane); p != nil && p.listGen == m.listGen {
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
	p := a.paneOf(m.pane)
	if p == nil || p.load == nil || p.load.gen != m.gen {
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
	return a.linkTarget(p)
}

// enter は、カーソル行の項目に入る・開く（filer §7）。
func (a *App) enter() []Cmd {
	p := a.cur()
	it, ok := p.current()
	if !ok {
		return nil
	}
	path := p.pathOf(it)
	switch {
	case it.Parent:
		return a.parent(p)
	case it.Err != nil:
		a.setMessage(msg.Error(it.Err), true)
		return nil
	case it.IsDir():
		return a.load(p, path, loadGo, "")
	case it.Info.Type == fsops.TypeSymlink:
		return a.load(p, path, loadLink, "")
	case it.Info.Type == fsops.TypeSpecial:
		a.setMessage(msg.Kind(fsops.KindUnsupportedType), true)
		return nil
	}
	return a.startOpen(path, it.Name)
}

// enterDir は、カーソル行がフォルダ（リンク・ジャンクションのフォルダ、.. を含む）なら入る。ファイルでは何もしない（l・→）。
func (a *App) enterDir() []Cmd {
	p := a.cur()
	it, ok := p.current()
	if !ok || it.Err != nil {
		return nil
	}
	path := p.pathOf(it)
	switch {
	case it.Parent:
		return a.parent(p)
	case it.IsDir():
		return a.load(p, path, loadGo, "")
	case it.Info.Type == fsops.TypeSymlink:
		return a.load(p, path, loadLinkDir, "")
	}
	return nil
}

// parent は、ペイン p を親のフォルダにし、カーソルを来たフォルダに置く。
func (a *App) parent(p *Pane) []Cmd {
	if !p.loaded || listing.IsRoot(p.dir) {
		return nil
	}
	return a.load(p, filepath.Dir(p.dir), loadGo, filepath.Base(p.dir))
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
	gen := a.newID()
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
		// 閲覧の画面でなければ（ヘルプ・入力欄・結果の画面などを開いていれば）、確認を出さずにやめる（pushAsync が重ねない）。
		// 出すと、ほかの画面の下に隠れたまま（またはそれを置き換えて）描いた後の扱いになり、見ていない確認を確定できる（filer §7。U2）。
		a.pushAsync(&execComp{path: m.path, name: m.name})
		return nil
	}
	return a.openCmd(m.path, m.name)
}

func (a *App) openCmd(path, name string) []Cmd {
	open := a.cfg.Open
	return []Cmd{{Run: func() any { return opened{name: name, err: open(path)} }}}
}

// ---- リンク先 ----

// linkRead は、リンク先の読み取りの結果（状態行に出す）。pane はペインの ID。
type linkRead struct {
	pane, listGen int
	name, target  string
}

// linkTarget は、ペイン p のカーソル行がリンク・ジャンクションで、リンク先をまだ読んでいなければ、読む処理を返す。
func (a *App) linkTarget(p *Pane) []Cmd {
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
	path, name, gen, readlink, i := p.pathOf(it), it.Name, p.listGen, a.cfg.Readlink, p.id
	return []Cmd{{Run: func() any {
		target, err := readlink(path)
		if err != nil {
			target = "(" + msg.Error(err) + ")"
		}
		return linkRead{pane: i, listGen: gen, name: name, target: target}
	}}}
}

// ---- ダイアログの部品 ----

// HelpView は、ヘルプの内容。
type HelpView struct{}

func (HelpView) Role() Role { return RoleHelp }

// helpComp は、ヘルプ。文字の入力（貼り付け）のほかのどの操作でも閉じる。
type helpComp struct{ base }

func (helpComp) role() Role     { return RoleHelp }
func (helpComp) view(*App) View { return HelpView{} }
func (helpComp) commands() commandTable {
	t := commandTable{}
	for k := ActUp; k <= ActNewDir; k++ {
		if k != ActInsert { // 貼り付けでは閉じない
			t[k] = command{GateFree, func(a *App, _ Action) []Cmd { a.pop(); return nil }}
		}
	}
	return t
}

// PathView は、パスの入力（g）の内容。
type PathView struct {
	Edit *lineedit.Editor
}

func (PathView) Role() Role { return RolePath }

// pathComp は、パスの入力（g）。入力したパスは利用者が打った文字列で、表示用に加工したものではない（filer U4）。
type pathComp struct {
	base
	edit *lineedit.Editor
}

func (*pathComp) role() Role       { return RolePath }
func (c *pathComp) view(*App) View { return PathView{Edit: c.edit} }
func (c *pathComp) commands() commandTable {
	return editCommands(func() *lineedit.Editor { return c.edit }, nil, nil).with(commandTable{
		ActCancel: {GateFree, func(a *App, _ Action) []Cmd { a.pop(); return nil }},
		ActSubmit: {GateFree, func(a *App, _ Action) []Cmd {
			text := c.edit.Text()
			a.pop()
			if strings.TrimSpace(text) == "" {
				return nil
			}
			p := a.cur()
			return a.load(p, Resolve(p.dir, text), loadGo, "")
		}},
	})
}

// ExecView は、実行ファイルを開く前の確認の内容。
type ExecView struct {
	Name string // 表示する名前
}

func (ExecView) Role() Role { return RoleExec }

// execComp は、実行ファイルを開く前の確認（filer §7）。作業用の goroutine の結果で出すので、どの操作も描いた後だけ行う（Esc も）。
type execComp struct {
	base
	path, name string // 開くパス（列挙で得た名前から作ったもの。U4）、表示する名前
}

func (*execComp) role() Role       { return RoleExec }
func (c *execComp) view(*App) View { return ExecView{Name: c.name} }
func (c *execComp) commands() commandTable {
	closeIt := command{GateAfterDraw, func(a *App, _ Action) []Cmd { a.pop(); return nil }}
	return commandTable{
		ActYes: {GateAfterDraw, func(a *App, _ Action) []Cmd {
			a.pop()
			return a.openCmd(c.path, c.name)
		}},
		ActNo:     closeIt,
		ActCancel: closeIt,
	}
}

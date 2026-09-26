package app

import (
	"errors"
	"path/filepath"
	"time"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/listing"
	"github.com/zredjet/tana/internal/textfmt"
)

// 表示形式が求める、操作中のペインの親フォルダの一覧とプレビュー（filer §5.3・§6）。
// app は表示形式を知らない。tui が SetNeeds で求めたものだけを読む（2 ペインでは何も読まない）。

const (
	previewDelay = 100 * time.Millisecond // カーソルがこの時間止まってからプレビューを読む（filer §6）
	previewBytes = 64 << 10               // プレビューで読むファイルの先頭の大きさ
	previewLines = 200                    // プレビューに出すテキストの行数の上限
)

// Needs は、表示形式が求めるもの。
type Needs struct {
	Parent  bool // 操作中のペインの親フォルダの一覧
	Preview bool // 操作中のペインのカーソル行のプレビュー
}

// SetNeeds は、表示形式が求めるものを設定し、足りないものを読む処理を返す。
func (a *App) SetNeeds(n Needs) []Cmd {
	a.needs = n
	if !n.Preview {
		a.previewGen++ // 読んでいる途中のものを捨てる
		a.preview = Preview{}
	}
	return a.follow()
}

// follow は、操作や結果の反映の後で、操作中のペインについて足りないもの（リンク先、親フォルダの一覧、プレビュー）を読む処理を返す。
// どれも、読んだもの・読んでいる途中のものがあれば何もしない。
func (a *App) follow() []Cmd {
	cmds := a.linkTarget(a.active)
	cmds = append(cmds, a.scheduleParent()...)
	return append(cmds, a.schedulePreview()...)
}

// ---- 親フォルダの一覧 ----

type parentList struct {
	forDir  string // この一覧を読んだときのペインのフォルダ
	listGen int    // そのときのペインの一覧の世代（再読み込みで読み直す）
	items   []listing.Item
	err     error // 読めなかった理由
	done    bool
}

// parentRead は、親フォルダの一覧の読み取りの結果。
type parentRead struct {
	pane, listGen int
	forDir        string
	items         []listing.Item
	err           error
}

// Parent は、親フォルダの一覧（.. を除く）と、その中の今のフォルダの名前を返す。読んでいない・ルートなら ok が偽。
// 読めなかったときは、その理由を err に返す（U3）。
func (p *Pane) Parent() (items []listing.Item, current string, err error, ok bool) {
	if p.parent == nil || !p.parent.done || p.parent.forDir != p.dir {
		return nil, "", nil, false
	}
	return p.parent.items, filepath.Base(p.dir), p.parent.err, true
}

func (a *App) scheduleParent() []Cmd {
	i := a.active
	p := a.panes[i]
	if !a.needs.Parent || !p.loaded || p.load != nil || listing.IsRoot(p.dir) {
		return nil
	}
	if p.parent != nil && p.parent.forDir == p.dir && p.parent.listGen == p.listGen {
		return nil
	}
	next := &parentList{forDir: p.dir, listGen: p.listGen}
	if p.parent != nil && p.parent.forDir == p.dir {
		// 同じフォルダの再読み込み。読み直す間は、前の一覧を出したままにする（ちらつかないように）。
		next.items, next.err, next.done = p.parent.items, p.parent.err, p.parent.done
	}
	p.parent = next
	dir, gen, readDir, dotHidden := p.dir, p.listGen, a.cfg.ReadDir, a.cfg.DotFilesHidden
	return []Cmd{{Run: func() any {
		parent := filepath.Dir(dir)
		entries, err := readDir(parent)
		var items []listing.Item
		if err == nil {
			items = withoutParent(listing.Build(parent, entries, dotHidden))
		}
		return parentRead{pane: i, listGen: gen, forDir: dir, items: items, err: err}
	}}}
}

func (a *App) parentRead(m parentRead) {
	p := a.panes[m.pane]
	if p.parent != nil && p.parent.forDir == m.forDir && p.parent.listGen == m.listGen {
		p.parent.items, p.parent.err, p.parent.done = m.items, m.err, true
	}
}

// withoutParent は、先頭の .. を除く。
func withoutParent(items []listing.Item) []listing.Item {
	if len(items) > 0 && items[0].Parent {
		return items[1:]
	}
	return items
}

// ---- プレビュー ----

// PreviewKind はプレビューの種類。
type PreviewKind int

const (
	PreviewNone     PreviewKind = iota // まだ読んでいない（読んでいる途中を含む）
	PreviewDir                         // フォルダの中身（Items）
	PreviewText                        // テキストの先頭の行（Lines、Encoding）
	PreviewBinary                      // テキストでないファイル（Size）
	PreviewNotLocal                    // 中身が手元にないファイル（読むと取得が始まるので読まない。Size）
	PreviewSpecial                     // 特殊なファイル（読まない）
	PreviewError                       // 読めなかった（Err）
)

// Preview は、カーソル行の項目のプレビュー（filer §6）。
type Preview struct {
	Path     string // 対象のパス（.. なら親フォルダ）
	Kind     PreviewKind
	Items    []listing.Item // PreviewDir（.. を除く）
	Lines    []string       // PreviewText（タブは広げてある。制御文字などは描くときに置き換える）
	Encoding string         // PreviewText（UTF-8・UTF-16・Shift_JIS）
	Size     int64          // PreviewText・PreviewBinary・PreviewNotLocal
	Err      error          // PreviewError
}

type previewTick struct{ gen int }

type previewRead struct {
	gen     int
	preview Preview
}

// Preview は、操作中のペインのカーソル行のプレビューを返す。読んでいなければ Kind が PreviewNone。
func (a *App) Preview() Preview {
	path, _, ok := a.previewTarget()
	if !ok || a.preview.Path != path {
		return Preview{Path: path}
	}
	return a.preview
}

// previewTarget は、プレビューする項目とパスを返す。一覧を読み込み中なら ok が偽。
func (a *App) previewTarget() (path string, it listing.Item, ok bool) {
	p := a.panes[a.active]
	if !p.loaded || p.load != nil {
		return "", listing.Item{}, false
	}
	if it, ok = p.current(); !ok {
		return "", listing.Item{}, false
	}
	if it.Parent {
		return filepath.Dir(p.dir), it, true
	}
	return filepath.Join(p.dir, it.Name), it, true // 列挙で得た名前から作る（U4）
}

// schedulePreview は、カーソル行のプレビューを、カーソルが previewDelay 止まってから読む処理を返す。
func (a *App) schedulePreview() []Cmd {
	if !a.needs.Preview {
		return nil
	}
	path, it, ok := a.previewTarget()
	if !ok || a.preview.Path == path && !a.previewStale {
		return nil
	}
	a.previewGen++
	if a.preview.Path != path {
		a.preview = Preview{Path: path}
	} // 同じ項目を読み直す（再読み込みの後）ときは、読み終わるまで前のプレビューを出したままにする
	a.previewStale = false
	switch {
	case it.Err != nil:
		a.preview = Preview{Path: path, Kind: PreviewError, Err: it.Err}
		return nil
	case it.Info.Type == fsops.TypeSpecial:
		a.preview = Preview{Path: path, Kind: PreviewSpecial}
		return nil
	}
	gen := a.previewGen
	return []Cmd{{Delay: previewDelay, Run: func() any { return previewTick{gen: gen} }}}
}

// previewTick は、カーソルが止まっていれば、プレビューを作業用の goroutine で読む処理を返す。
func (a *App) previewTick(m previewTick) []Cmd {
	if m.gen != a.previewGen {
		return nil // カーソルが動いた
	}
	path, it, ok := a.previewTarget()
	if !ok || a.preview.Path != path {
		return nil
	}
	gen, cfg := a.previewGen, a.cfg
	return []Cmd{{Run: func() any { return previewRead{gen: gen, preview: readPreview(cfg, path, it)} }}}
}

// readPreview は、path（項目 it）のプレビューを読む。作業用の goroutine で呼ぶ。
func readPreview(cfg Config, path string, it listing.Item) Preview {
	pv := Preview{Path: path}
	if it.IsDir() || it.Info.Type == fsops.TypeSymlink {
		entries, err := cfg.ReadDir(path) // リンク・ジャンクションは辿る
		if err == nil {
			pv.Kind, pv.Items = PreviewDir, withoutParent(listing.Build(path, entries, cfg.DotFilesHidden))
			return pv
		}
		if oe, ok := errors.AsType[*fsops.OpError](err); it.IsDir() || !ok || oe.Kind != fsops.KindNotFound {
			pv.Kind, pv.Err = PreviewError, err
			return pv
		}
		// リンク先がフォルダでない。ファイルとして読む。
	}
	h, err := cfg.ReadHead(path, previewBytes)
	switch {
	case err != nil:
		pv.Kind, pv.Err = PreviewError, err
	case h.NotLocal:
		pv.Kind, pv.Size = PreviewNotLocal, h.Size
	default:
		pv.Size = h.Size
		if text, enc, ok := textfmt.DecodeText(h.Data, int64(len(h.Data)) < h.Size); ok {
			pv.Kind, pv.Lines, pv.Encoding = PreviewText, textfmt.PreviewLines(text, previewLines), enc
		} else {
			pv.Kind = PreviewBinary
		}
	}
	return pv
}

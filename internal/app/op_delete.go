package app

import (
	"path/filepath"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

// ごみ箱（d）と完全削除（D）の入口と、完全削除の確認（filer §8.6）。
// 完全削除（OpDelete）は、この確認の画面で、画面を描いた後に y を押した場合だけ実行する（U2）。
// ごみ箱に入らない項目を、UI が自分から完全削除に切り替えることはしない（fsops I5 の UI 側）。

// trash は、ペイン p の対象をごみ箱へ入れる計画を作り始める（filer §8.2）。
func (a *App) trash(p *Pane) []Cmd {
	t := p.selected()
	if len(t) == 0 {
		a.setMessage(msg.NoTarget, false)
		return nil
	}
	return a.begin(fsops.Request{Op: fsops.OpTrash, Sources: t}, p.dir, false)
}

// purge は、ペイン p の対象を完全に削除する計画を作り始める。計画ができたら、完全削除の確認を出す（filer §8.6）。
func (a *App) purge(p *Pane) []Cmd {
	t := p.selected()
	if len(t) == 0 {
		a.setMessage(msg.NoTarget, false)
		return nil
	}
	return a.begin(fsops.Request{Op: fsops.OpDelete, Sources: t}, p.dir, false)
}

// DeleteItem は、完全削除の確認に出す項目（名前と、ファイルかフォルダか、ファイルのサイズ。filer §8.6）。
type DeleteItem struct {
	Name string
	Info fsops.EntryInfo
}

func (DeleteView) Role() Role { return RoleDelete }

// DeleteView は、完全削除の確認の内容（filer §8.6）。
type DeleteView struct {
	FromTrash       bool   // ごみ箱に入らなかった項目から進んだ
	Dir             string // 項目のあるフォルダ
	Count, Runnable int
	Items           []DeleteItem // 実行する項目（計画の順）
	NotRunnable     []ItemNote
	Files           int
	Bytes           int64
	Warnings        []string
}

// deleteView は、流れ f の完全削除の確認の内容。
func deleteView(f *flow) DeleteView {
	op := f
	pl := op.plan
	v := DeleteView{FromTrash: op.fromTrash, Dir: op.from, Files: pl.TotalFiles(), Bytes: pl.TotalBytes(), Warnings: op.warnings}
	for _, it := range pl.Items() {
		v.Count++
		if it.Err != nil {
			v.NotRunnable = append(v.NotRunnable, ItemNote{Name: filepath.Base(it.Src), Reason: msg.Error(it.Err)})
			continue
		}
		v.Runnable++
		v.Items = append(v.Items, DeleteItem{Name: filepath.Base(it.Src), Info: it.Info})
	}
	return v
}

// deleteComp は、完全削除の確認（filer §8.6）。確定は、画面を描いた後の y だけ。Enter・n・Esc はやめる（うっかり Enter で消さない。U2）。
// 描く前に届いたキーは、Esc（やめる）のほかは何もしない。
type deleteComp struct {
	base
	f *flow
}

func (*deleteComp) role() Role       { return RoleDelete }
func (c *deleteComp) view(*App) View { return deleteView(c.f) }
func (c *deleteComp) commands() commandTable {
	stop := func(a *App, _ Action) []Cmd { a.discard(c.f); return nil }
	return commandTable{
		ActCancel: {GateFree, stop},
		ActYes: {GateAfterDraw, func(a *App, _ Action) []Cmd {
			if deleteView(c.f).Runnable > 0 {
				return a.execute(c.f)
			}
			return nil
		}},
		ActNo:     {GateAfterDraw, stop},
		ActSubmit: {GateAfterDraw, stop},
	}
}

// trashUnavailable は、ごみ箱が使えないと確かめられた理由かを返す。完全削除を勧めるのはこの理由のときだけ
// （確かめられなかった理由では、ごみ箱が使えるかもしれない項目を、取り消せない操作に誘導しない。fsops §12.1）。
func trashUnavailable(err *fsops.OpError) bool {
	return err != nil && err.Kind == fsops.KindTrashUnavailable
}

// untrashable は、ごみ箱に入らなかった項目（Outcome が失敗で、Kind が KindTrashUnavailable のもの）のパスを返す（filer §8.5）。
// Kind だけで選ばない。「ごみ箱に入ったか確かめられない」は Kind が同じでも元の場所から消えているので、完全削除を勧めない。
func untrashable(res *fsops.Result) []string {
	var out []string
	for _, it := range res.Items {
		if it.Outcome == fsops.OutcomeFailed && trashUnavailable(it.Err) {
			out = append(out, it.Src)
		}
	}
	return out
}

// untrashableItems は、計画の時点でごみ箱に入らないと分かった項目のパスを返す（filer §8.2）。
func untrashableItems(items []fsops.Item) []string {
	var out []string
	for _, it := range items {
		if trashUnavailable(it.Err) {
			out = append(out, it.Src)
		}
	}
	return out
}

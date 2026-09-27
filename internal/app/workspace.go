package app

import (
	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/lineedit"
)

// 作業場（filer §4 の「UI の骨格」）: 重ねる部品がないときのフォーカスの道筋は [操作中のペイン, 作業場]。
// ペインの操作（一覧を使う操作）はペインの部品が、それ以外の閲覧の操作は作業場の部品が受ける。
// どれも描く前でも行う（閲覧の画面は、いつも出ている）。

// workspaceComp は、作業場。フォーカスのある子は、操作中のペイン。
type workspaceComp struct{ base }

func (workspaceComp) role() Role     { return RoleWorkspace }
func (workspaceComp) view(*App) View { return nil }
func (workspaceComp) focused(a *App) *mounted {
	if len(a.panes) == 0 {
		return nil
	}
	return &a.cur().node
}

func (workspaceComp) commands() commandTable {
	free := func(f func(a *App) []Cmd) command {
		return command{GateFree, func(a *App, _ Action) []Cmd { return f(a) }}
	}
	return commandTable{
		ActCancel: free((*App).cancel),
		ActNextPane: free(func(a *App) []Cmd {
			a.active = a.panes[(a.Active()+1)%len(a.panes)].id
			return nil
		}),
		ActToggleHidden: free(func(a *App) []Cmd {
			a.showHidden = !a.showHidden
			for _, q := range a.panes {
				q.filter(a.showHidden)
			}
			return nil
		}),
		ActReload: free(func(a *App) []Cmd {
			// 最初の読み込みに失敗した・中止したペイン（一覧がない）は、起動時と同じく読み込み直す。
			// 読み込み中のペインは読み直さない（今の移動を知らせなしに取り消さない。filer §6）。
			var cmds []Cmd
			for _, q := range a.panes {
				if q.load != nil {
					continue
				}
				kind := loadReload
				if !q.loaded {
					kind = loadInitial
				}
				cmds = append(cmds, a.load(q, q.dir, kind, "")...)
			}
			return cmds
		}),
		ActHelp: free(func(a *App) []Cmd { a.push(helpComp{}, 0); return nil }),
		ActQuit: free(func(a *App) []Cmd { a.quit = true; return nil }),
		ActLastResult: free(func(a *App) []Cmd {
			if a.last != nil {
				a.push(&resultComp{r: a.last}, 0)
			}
			return nil
		}),
	}
}

// paneComp は、操作中のペイン。読み込み中は、一覧を使う操作を受け付けない（読み込みが終わると一覧が変わるため）。
type paneComp struct {
	base
	p *Pane
}

func (paneComp) role() Role     { return RolePane }
func (paneComp) view(*App) View { return nil }

func (pc paneComp) commands() commandTable {
	t := commandTable{}
	for _, k := range []ActionKind{ActUp, ActDown, ActPageUp, ActPageDown, ActHome, ActEnd, ActEnter, ActEnterDir, ActParent, ActMark, ActMarkAll,
		ActGoPath, ActSyncOther, ActYank, ActPasteCopy, ActPasteMove, ActTrash, ActPurge, ActRename, ActNewDir} {
		t[k] = command{GateFree, func(a *App, act Action) []Cmd {
			if pc.p.load != nil {
				return nil
			}
			return a.paneAction(pc.p, act)
		}}
	}
	return t
}

// paneAction は、ペイン p の一覧を使う操作を行う。
func (a *App) paneAction(p *Pane, act Action) []Cmd {
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
		return a.parent(p)
	case ActMark:
		if it, ok := p.current(); ok && !it.Parent {
			p.toggleMark(it.Name)
		}
		p.move(1)
	case ActMarkAll:
		p.markAll()
	case ActGoPath:
		a.push(&pathComp{edit: lineedit.New(p.dir, len(p.dir))}, 0)
	case ActSyncOther:
		if len(a.panes) > 1 && p.loaded {
			return a.load(a.panes[(a.Active()+1)%len(a.panes)], p.dir, loadGo, "")
		}
	case ActYank:
		a.yank()
	case ActPasteCopy:
		return a.paste(fsops.OpCopy)
	case ActPasteMove:
		return a.paste(fsops.OpMove)
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

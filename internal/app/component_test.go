package app

import (
	"fmt"
	"slices"
	"testing"
)

// UI の骨格（filer §4）の性質のテスト。部品・役割を足すと、自動で対象になる（役割の数が合わなければ失敗する）。

// commitKinds は、確定の操作。重ねる部品では、入力欄のほかは描いた後だけ行う（U2）。
var commitKinds = []ActionKind{ActSubmit, ActYes, ActPurge, ActDecide, ActDecideAll, ActNewerOnly}

// TestGateDeclared は、すべての役割の操作表で、門を宣言していること（0 がないこと）と、
// 重ねる部品の確定の操作が AfterDraw であることを確かめる。
func TestGateDeclared(t *testing.T) {
	t.Parallel()
	for _, r := range AllRoles() {
		table := tableOf(r)
		if table == nil {
			t.Errorf("role %d has no command table", r)
			continue
		}
		for k, c := range table {
			if c.gate != GateFree && c.gate != GateAfterDraw {
				t.Errorf("role %d action %d: gate %d is not declared", r, k, c.gate)
			}
			switch r {
			case RoleWorkspace, RolePane: // 閲覧の画面（いつも出ている）
				continue
			case RolePath, RoleRename, RoleNewDir: // 入力欄（打った文字を失わない。Enter は打った名前・パスで行う）
				continue
			case RoleHelp: // どの操作でも閉じるだけ（確定しない）
				continue
			}
			if slices.Contains(commitKinds, k) && c.gate != GateAfterDraw {
				t.Errorf("role %d: the confirming action %d must be AfterDraw (U2)", r, k)
			}
		}
	}
}

// TestPushAsync は、作業用の goroutine の結果で重ねる口が、操作がすべて AfterDraw の部品を、作業場の上にだけ重ねることを確かめる（filer §4）。
func TestPushAsync(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil, tree(t))
	if h.a.pushAsync(helpComp{}) {
		t.Error("pushAsync accepted a component with Free actions")
	}
	if !h.a.pushAsync(&execComp{name: "x"}) {
		t.Fatal("pushAsync refused the exec confirmation over the workspace")
	}
	if h.a.pushAsync(&execComp{name: "y"}) {
		t.Error("pushAsync stacked over another component (it would be hidden or replace it)")
	}
}

// focusSig は、道筋と、重ねた部品の内容。
func focusSig(a *App) string { return fmt.Sprintf("%v %+v", a.FocusPath(), a.Modals()) }

// TestGateProperty は、すべての役割で、画面を描く前に届いた AfterDraw の操作が何も変えないことと、
// 道筋・重ねた部品の内容をどの状態でも求められることを確かめる（U2）。
func TestGateProperty(t *testing.T) {
	t.Parallel()
	covered := map[Role]bool{RoleWorkspace: true, RolePane: true} // 閲覧の画面は AfterDraw の操作を持たない（TestGateDeclared）
	for _, st := range gateStates {
		h := st.build(t)
		m := h.a.path()[0]
		covered[m.c.role()] = true
		for k, c := range m.c.commands() {
			if c.gate != GateAfterDraw {
				continue
			}
			h := st.build(t)
			before := focusSig(h.a)
			if cmds := h.a.Do(gateAction(k)); len(cmds) != 0 || focusSig(h.a) != before {
				t.Errorf("%s: action %d before the draw changed the screen or returned %d commands", st.name, k, len(cmds))
			}
		}
	}
	for _, r := range AllRoles() {
		if !covered[r] {
			t.Errorf("role %d has no state in gateStates (add one so that its gate is tested)", r)
		}
	}
}

// TestFocusPath は、重ねた部品が作業場への操作を止めること、役割を指定した操作がその部品にだけ届くこと、
// 部品を下ろして下の部品が現れたときに門を掛け直すことを確かめる（filer §4）。
func TestFocusPath(t *testing.T) {
	t.Parallel()
	root := tree(t)
	h := newHarness(t, nil, root)
	if got := h.a.FocusPath(); !slices.Equal(got, []Role{RolePane, RoleWorkspace}) {
		t.Fatalf("browse path %v", got)
	}
	cursor := h.pane(0).Cursor()
	h.act(Action{Kind: ActDown, Role: RoleWorkspace}) // 作業場は移動を受け付けない（ペインへは渡さない）
	if h.pane(0).Cursor() != cursor {
		t.Error("an action for the workspace reached the pane")
	}
	h.act(Action{Kind: ActDown, Role: RolePane})
	if h.pane(0).Cursor() != cursor+1 {
		t.Error("an action for the pane did not reach it")
	}

	h.a.pushAsync(&execComp{name: "x"})
	h.a.settleFocus()
	if got := h.a.FocusPath(); !slices.Equal(got, []Role{RoleExec}) {
		t.Fatalf("path with the exec confirmation %v", got)
	}
	h.a.Drawn()
	h.do(ActQuit) // 作業場の操作は、重ねた部品の下へ渡らない
	if h.a.Quit() {
		t.Error("q reached the workspace under the exec confirmation")
	}

	// 下ろして現れた部品は、描き直すまで確定しない。
	h.a.push(&execComp{name: "y"}, 0)
	h.a.settleFocus()
	h.a.Drawn()
	h.do(ActNo) // 上の確認を閉じる（下の確認が現れる）
	if v, _ := ModalView[ExecView](h.a); v.Name != "x" {
		t.Fatalf("revealed %q", v.Name)
	}
	h.do(ActYes)
	if len(h.opened) != 0 || len(h.a.Modals()) != 1 {
		t.Errorf("the revealed confirmation was accepted before it was drawn again: opened %q", h.opened)
	}
	h.a.Drawn()
	h.do(ActNo)
	if len(h.a.Modals()) != 0 {
		t.Error("n after the draw did not close the revealed confirmation")
	}
}

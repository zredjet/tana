package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
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
		h := st.build(t, tree(t))
		m := h.a.path()[0]
		covered[m.c.role()] = true
		for k, c := range m.c.commands() {
			if c.gate != GateAfterDraw {
				continue
			}
			h := st.build(t, tree(t))
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

// TestStaleFlowResults は、終わった・中止した流れ宛ての結果が、何も変えないことを確かめる（持ち主の ID で届け先を決める。filer §4）。
func TestStaleFlowResults(t *testing.T) {
	t.Parallel()
	h := gateCopy(t, tree(t), false)
	h.confirm()
	h.confirm() // 実行して、結果の画面
	before := focusSig(h.a)
	for _, m := range []any{planned{flow: 12345}, planSlow{flow: 12345}, executed{flow: 12345}, opTick{flow: 12345}} {
		if cmds := h.a.Update(m); len(cmds) != 0 || focusSig(h.a) != before {
			t.Errorf("%T for a finished flow changed the screen or returned %d commands", m, len(cmds))
		}
	}
}

// TestOneFlowAtATime は、v0.1 ではファイル操作の流れが同時に 1 つだけであることを確かめる（filer §7）。
func TestOneFlowAtATime(t *testing.T) {
	t.Parallel()
	h := gateCopy(t, tree(t), true) // 計画を作っている途中
	if cmds := h.a.begin(h.a.flows[0].req, "", false); cmds != nil || len(h.a.flows) != 1 {
		t.Errorf("a second flow started: %d commands, %d flows", len(cmds), len(h.a.flows))
	}
}

// fakeRunner は、fsops の流れでない、止められる実行（進捗と中止の確認の部品が、流れの型に縛られていないことを確かめる）。
type fakeRunner struct {
	stopped bool
}

func (*fakeRunner) ownerID() int               { return 777 }
func (*fakeRunner) progress(*App) ProgressView { return ProgressView{DoneFiles: 3} }
func (r *fakeRunner) stop(*App)                { r.stopped = true }
func (r *fakeRunner) state() runState {
	if r.stopped {
		return runStopping
	}
	return runRunning
}

// TestRunnerComponents は、進捗と中止の確認の部品が、runner だけを見て動くことを確かめる（filer §4、§16 の X3）。
func TestRunnerComponents(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil, tree(t))
	r := &fakeRunner{}
	h.a.push(&progressComp{r: r}, r.ownerID())
	h.a.settleFocus()
	if v := h.a.Progress(); v.DoneFiles != 3 {
		t.Fatalf("progress %+v", v)
	}
	h.do(ActCancel)
	if !h.a.Progress().AskCancel || h.a.topRole() != RoleCancelAsk {
		t.Fatal("no cancel question over the runner's progress")
	}
	h.do(ActYes) // 描く前の y
	if r.stopped {
		t.Fatal("stopped by y before the question was drawn (U2)")
	}
	h.a.Drawn()
	h.do(ActYes)
	if !r.stopped || h.a.topRole() != RoleProgress {
		t.Errorf("stopped %v, top %v", r.stopped, h.a.topRole())
	}
	h.do(ActCancel) // 止めている間は、もう確認を出さない
	if h.a.topRole() != RoleProgress {
		t.Error("a second cancel question while stopping")
	}
}

// TestPlanningCancelStopsPlan は、計画を作っている間に中止したら、NewPlan に渡した ctx を止めることを確かめる（filer U5、§4 の「持ち主と ID」）。
// 止めないと、画面は閲覧に戻っても、fsops の走査が最後まで続く。
func TestPlanningCancelStopsPlan(t *testing.T) {
	t.Parallel()
	root := tree(t)
	var planErr error
	h := newHarness(t, func(c *Config) {
		c.NewPlan = func(ctx context.Context, req fsops.Request) (Plan, error) {
			planErr = ctx.Err() // 計画の処理が動き始めたときに、もう止められているか
			return nil, planErr
		}
	}, root, filepath.Join(root, "sub"))
	h.hold = true
	h.pasteInto("a.txt", 1, ActPasteCopy)
	h.do(ActCancel) // 計画を作っている間に中止する
	h.release()     // 計画の処理が動く
	if !errors.Is(planErr, context.Canceled) {
		t.Errorf("NewPlan's context after canceling the planning: %v, want canceled", planErr)
	}
	h.wantMessage(msg.Kind(fsops.KindCanceled))
}

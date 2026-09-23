package fsops

import (
	"testing"
)

// TestPlanAccessorsReturnCopies は、Plan の取得メソッドが返した値を変更しても計画が変わらないことを確かめる（SPEC §5）。
func TestPlanAccessorsReturnCopies(t *testing.T) {
	t.Parallel()
	p := &Plan{
		req:       Request{Op: OpCopy, Sources: []string{"/a"}, DestDir: "/d"},
		items:     []Item{{Src: "/a", Dst: "/d/a", Err: &OpError{Op: "plan", Path: "/a", Kind: KindNotFound}}},
		conflicts: []Conflict{{ID: 1, Src: "/a", Dst: "/d/a"}},
		warnings:  []*OpError{{Op: "plan", Kind: KindNoSpace}},
	}

	r := p.Request()
	r.Sources[0] = "/changed"
	items := p.Items()
	items[0].Src = "/changed"
	items[0].Err.Kind = KindExist
	cs := p.Conflicts()
	cs[0].Decision = DecisionOverwrite
	ws := p.Warnings()
	ws[0].Kind = KindExist

	if got := p.req.Sources[0]; got != "/a" {
		t.Errorf("Request().Sources changed the plan: %q", got)
	}
	if got := p.items[0].Src; got != "/a" {
		t.Errorf("Items() changed the plan: Src = %q", got)
	}
	if got := p.items[0].Err.Kind; got != KindNotFound {
		t.Errorf("Items() changed the plan: Err.Kind = %v", got)
	}
	if got := p.conflicts[0].Decision; got != DecisionUnset {
		t.Errorf("Conflicts() changed the plan: Decision = %v", got)
	}
	if got := p.warnings[0].Kind; got != KindNoSpace {
		t.Errorf("Warnings() changed the plan: Kind = %v", got)
	}
}

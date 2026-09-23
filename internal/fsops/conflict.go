package fsops

// decisionAllowed は、衝突 c に決定 d が許されるかを返す（§9.1 の表）。
// TypeFile 同士以外の組み合わせ（リンク同士、特殊なファイルを含むもの）は「種類が違う」の行に従う。
func decisionAllowed(c Conflict, d Decision) bool {
	switch d {
	case DecisionUnset, DecisionSkip, DecisionAutoRename:
		return true
	case DecisionOverwrite:
		return !c.Self && c.SrcInfo.Type == TypeFile && c.DstInfo.Type == TypeFile
	case DecisionMerge:
		return !c.Self && c.SrcInfo.Type == TypeDir && c.DstInfo.Type == TypeDir
	}
	return false
}

// Decide は衝突の決定を設定する。
// SPEC §9.1 で許されない決定、存在しない ID、Execute の開始後の呼び出しは error（KindInvalidRequest）を返す。
func (p *Plan) Decide(id ConflictID, d Decision) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started || id < 1 || int(id) > len(p.conflicts) || !decisionAllowed(p.conflicts[id-1], d) {
		return &OpError{Op: "decide", Kind: KindInvalidRequest}
	}
	p.conflicts[id-1].Decision = d
	return nil
}

package fsops

// spaceInfo は、§6.4 の空き容量と大きさの上限の警告を、Warnings を呼んだ時点の衝突の決定で計算し直すための、計画時の記録。
type spaceInfo struct {
	dest     string
	measured bool   // 空き容量を測れた
	free     uint64 // 計画時に測った空き容量
	base     int64  // 衝突に関わらず書くバイト数
	// owned は、衝突ごとの、その衝突の決定で書くかが決まるバイト数（ファイルの衝突ではコピー元の大きさ、
	// 衝突のあるフォルダでは、中の、自分の衝突を持たないファイルの大きさの合計）。
	owned    map[ConflictID]int64
	tooLarge []ownedPath // コピー先の上限を超えるファイル（§10.6）
}

// ownedPath は、パスと、それを書くかを決める衝突（なければ 0）。
type ownedPath struct {
	path  string
	owner ConflictID
}

// add は、owner の衝突の決定で書くかが決まる n バイトを記録する（owner が 0 なら必ず書く）。
func (s *spaceInfo) add(owner ConflictID, n int64) {
	if owner == 0 {
		s.base += n
		return
	}
	s.owned[owner] += n
}

// renamed は、衝突 id か、その祖先の衝突が自動リネーム（中身をすべて別名で書く）かを返す。p.mu を持って呼ぶ。
func (p *Plan) renamed(id ConflictID) bool {
	for ; id != 0; id = p.conflicts[id-1].Parent {
		if p.conflicts[id-1].Decision == DecisionAutoRename {
			return true
		}
	}
	return false
}

// writes は、衝突 id の決定で、その対象（ファイル、またはフォルダの中の自分の衝突を持たないもの）を書くかと、
// 上書き（上書き先の大きさが空く）かを返す（§6.4）。p.mu を持って呼ぶ。
func (p *Plan) writes(id ConflictID) (write, overwrite bool) {
	c := p.conflicts[id-1]
	if c.Parent != 0 {
		if w, _ := p.writes(c.Parent); !w {
			return false, false // 親のフォルダを書かない
		}
		if p.renamed(c.Parent) {
			return true, false // 親のフォルダを別名で丸ごと書く（中の衝突は関係ない）
		}
	}
	switch c.Decision {
	case DecisionOverwrite:
		return true, c.DstInfo.Type == TypeFile
	case DecisionAutoRename, DecisionMerge:
		return true, false
	}
	return false, false // Skip・未設定
}

// spaceNeed は、今の決定で書き込むバイト数を返す（§6.4）。
func (p *Plan) spaceNeed() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.spaceNeedLocked()
}

func (p *Plan) spaceNeedLocked() int64 {
	s := p.space
	if s == nil {
		return 0
	}
	need := s.base
	for id, n := range s.owned {
		if w, over := p.writes(id); w {
			need += n
			if over {
				need -= p.conflicts[id-1].DstInfo.Size
			}
		}
	}
	return max(need, 0)
}

// spaceWarnings は、今の決定での §6.4 の警告（KindNoSpace、KindFileTooLarge）を返す。p.mu を持って呼ぶ。
func (p *Plan) spaceWarnings() []*OpError {
	s := p.space
	if s == nil {
		return nil
	}
	var ws []*OpError
	if need := p.spaceNeedLocked(); s.measured && need > 0 && uint64(need) > s.free {
		ws = append(ws, planError(s.dest, KindNoSpace, nil))
	}
	for _, f := range s.tooLarge {
		if w, _ := p.writesOwner(f.owner); w {
			ws = append(ws, planError(f.path, KindFileTooLarge, nil))
		}
	}
	return ws
}

// writesOwner は writes の、衝突のないもの（owner が 0）も扱う形。
func (p *Plan) writesOwner(owner ConflictID) (bool, bool) {
	if owner == 0 {
		return true, false
	}
	return p.writes(owner)
}

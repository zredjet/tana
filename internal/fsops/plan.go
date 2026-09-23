package fsops

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
)

// NewPlan は計画を作る（§6）。ファイルシステムは一切変更しない。
// error を返すのはリクエスト全体が不正な場合（§6.1）と、ctx がキャンセルされた場合（KindCanceled）だけ。
// 項目ごとの問題は Item.Err に入れ、フォルダ内の走査エラーなどは Warnings に入れる。
func NewPlan(ctx context.Context, req Request) (*Plan, error) {
	pl := &planner{ctx: ctx, plan: &Plan{}}
	if err := pl.run(req); err != nil {
		return nil, err
	}
	return pl.plan, nil
}

// planner は NewPlan の作業状態。
type planner struct {
	ctx    context.Context
	plan   *Plan
	destID idStat // OpCopy / OpMove の DestDir（リンクを辿った先）
}

// errCanceled は、ctx がキャンセルされていれば KindCanceled の *OpError を返す。
func (pl *planner) errCanceled() error {
	if err := pl.ctx.Err(); err != nil {
		return &OpError{Op: "plan", Kind: KindCanceled, Err: err}
	}
	return nil
}

func (pl *planner) run(req Request) error {
	if err := pl.errCanceled(); err != nil {
		return err
	}
	r, err := pl.checkRequest(req)
	if err != nil {
		return err
	}
	pl.plan.req = r
	var writeBytes int64 // §6.4 の空き容量と比べるバイト数
	for i, src := range r.Sources {
		if err := pl.errCanceled(); err != nil {
			return err
		}
		it := pl.item(i, src)
		pl.plan.items = append(pl.plan.items, it)
		if it.Err != nil {
			continue
		}
		files, bytes, err := pl.count(i, it)
		if err != nil {
			return err
		}
		pl.plan.totalFiles += files
		pl.plan.totalBytes += bytes
		if it.Method == MethodCopy || it.Method == MethodCopyThenRemove {
			writeBytes += bytes
		}
	}
	if writeBytes > 0 {
		pl.checkFreeSpace(r.DestDir, writeBytes)
	}
	return nil
}

// checkRequest はリクエスト全体の検査（§6.1）を行い、パスを Clean したリクエストを返す。
func (pl *planner) checkRequest(req Request) (Request, error) {
	invalid := func(p string) error { return &OpError{Op: "plan", Path: p, Kind: KindInvalidRequest} }
	if req.Op < OpCopy || req.Op > OpDelete || len(req.Sources) == 0 {
		return Request{}, invalid("")
	}
	r := Request{Op: req.Op, Sources: make([]string, len(req.Sources))}
	for i, s := range req.Sources {
		c, err := checkPath(s)
		if err != nil || isVolumeRoot(c) {
			return Request{}, invalid(s)
		}
		r.Sources[i] = c
	}
	switch req.Op {
	case OpCopy, OpMove:
		d, err := checkPath(req.DestDir)
		if err != nil {
			return Request{}, invalid(req.DestDir)
		}
		st, err := fileIDFollow(d)
		if err != nil {
			if classify(err, classifyOpts{}) == KindNotFound {
				return Request{}, &OpError{Op: "plan", Path: d, Kind: KindNotFound, Err: err}
			}
			return Request{}, &OpError{Op: "plan", Path: d, Kind: KindInvalidRequest, Err: err}
		}
		if !st.isDir {
			return Request{}, invalid(d)
		}
		r.DestDir = d
		pl.destID = st
	default:
		if req.DestDir != "" {
			return Request{}, invalid(req.DestDir)
		}
	}
	if p, err := pl.checkNesting(r.Sources); err != nil {
		return Request{}, err
	} else if p != "" {
		return Request{}, invalid(p)
	}
	return r, nil
}

// checkNesting は、同じパスの重複と、一方が他方の内側にある Sources を探す（§6.1）。見つかればそのパスを返す。
// 各 Source の親を字句的に辿り、各段をほかの Source と fileID で比べる。Source 自体はリンクを辿らず、
// それより上の段はリンクを辿る（途中のフォルダは、パスを開くときに OS が辿るため）。
// 調べられない Source（存在しないなど）は比べない（その問題は §6.2 で Item.Err になる）。
func (pl *planner) checkNesting(srcs []string) (string, error) {
	owner := map[fileID]int{}
	for i, s := range srcs {
		st, err := fileIDOf(s)
		if err != nil {
			continue
		}
		if _, dup := owner[st.id]; dup {
			return s, nil
		}
		owner[st.id] = i
	}
	for i, s := range srcs {
		for p := filepath.Dir(s); ; p = filepath.Dir(p) {
			if err := pl.errCanceled(); err != nil {
				return "", err
			}
			if st, err := fileIDFollow(p); err == nil {
				if j, ok := owner[st.id]; ok && j != i {
					return s, nil
				}
			}
			if filepath.Dir(p) == p {
				break
			}
		}
	}
	return "", nil
}

// planError は項目ごとの問題（§6.2）の *OpError を作る。
func planError(path string, kind Kind, err error) *OpError {
	return &OpError{Op: "plan", Path: path, Kind: kind, Err: err}
}

// item は項目ごとの判定（§6.2）を行う。
func (pl *planner) item(i int, src string) Item {
	r := pl.plan.req
	it := Item{Src: src}
	switch r.Op {
	case OpCopy:
		it.Method = MethodCopy
	case OpMove:
		it.Method = MethodRename
	case OpTrash:
		it.Method = MethodTrash
	case OpDelete:
		it.Method = MethodRemove
	}
	if r.Op == OpCopy || r.Op == OpMove {
		it.Dst = filepath.Join(r.DestDir, filepath.Base(src)) // 名前はバイト単位でそのまま（I6）
	}
	info, err := lstatEntry(src)
	if err != nil {
		it.Err = planError(src, classify(err, classifyOpts{}), err)
		return it
	}
	it.Info = info
	switch r.Op {
	case OpCopy, OpMove:
		if r.Op == OpMove {
			if parent, err := fileIDFollow(filepath.Dir(src)); err == nil && parent.id == pl.destID.id {
				it.Err = planError(src, KindSameFile, nil)
				return it
			}
		}
		if info.Type == TypeDir {
			inside, err := destInside(src, r.DestDir)
			if err != nil {
				it.Err = planError(src, classify(err, classifyOpts{}), err)
				return it
			}
			if inside {
				it.Err = planError(src, KindDestInsideSource, nil)
				return it
			}
		}
		if r.Op == OpMove {
			st, err := fileIDOf(src)
			if err != nil {
				it.Err = planError(src, classify(err, classifyOpts{}), err)
				return it
			}
			if st.id.method != pl.destID.id.method || st.id.vol != pl.destID.id.vol {
				it.Method = MethodCopyThenRemove
			}
		}
	case OpTrash:
		if err := trashPrecheck(pl.ctx, src, info); err != nil {
			it.Err = err
		}
	}
	return it
}

// count は項目の TotalFiles・TotalBytes への寄与を求め、衝突を検出する（§6.3）。
func (pl *planner) count(i int, it Item) (files int, bytes int64, err error) {
	if it.Method == MethodTrash {
		return 1, 0, nil
	}
	w := &walker{pl: pl, item: i, countBytes: it.Method != MethodRename}
	if it.Dst != "" {
		dirConflict, cid, err := w.conflict(it.Src, it.Dst, it.Info, 0)
		if err != nil {
			return 0, 0, err
		}
		switch {
		case dirConflict:
			err = w.walk(it.Src, it.Dst, cid)
		case it.Method == MethodRename:
			return 1, 0, nil // 衝突がなければ走査せず、1 項目として数える
		case it.Info.Type == TypeDir:
			err = w.walk(it.Src, "", 0)
		default:
			w.add(it.Info)
		}
		return w.files, w.bytes, err
	}
	if it.Info.Type == TypeDir {
		err = w.walk(it.Src, "", 0)
	} else {
		w.add(it.Info)
	}
	return w.files, w.bytes, err
}

// walker は、1 項目の中身を数え、衝突を検出する走査（§6.3、§13.1）。
type walker struct {
	pl         *planner
	item       int
	countBytes bool
	files      int
	bytes      int64
}

func (w *walker) add(info EntryInfo) {
	if info.Type == TypeDir {
		return
	}
	w.files++
	if w.countBytes && info.Type == TypeFile {
		w.bytes += info.Size
	}
}

func (w *walker) warn(path string, err error) {
	w.pl.plan.warnings = append(w.pl.plan.warnings, planError(path, classify(err, classifyOpts{}), withUserPaths(err, path, "")))
}

// conflict は、src から dst への衝突を調べ、あれば記録する（§6.3）。
// dirConflict は、フォルダ同士の衝突（中を走査して内側の衝突を探す必要がある）かを返す。
func (w *walker) conflict(src, dst string, srcInfo EntryInfo, parent ConflictID) (dirConflict bool, id ConflictID, err error) {
	dstInfo, err := lstatEntry(dst)
	if errors.Is(err, fs.ErrNotExist) || classify(err, classifyOpts{}) == KindNotFound {
		return false, 0, nil
	}
	if err != nil {
		w.warn(dst, err)
		return false, 0, nil
	}
	c := Conflict{Parent: parent, Item: w.item, Src: src, Dst: dst, SrcInfo: srcInfo, DstInfo: dstInfo}
	var dstID fileID
	if st, err := fileIDOf(dst); err == nil {
		dstID = st.id
		if parent == 0 {
			if s, err := fileIDOf(src); err == nil && s.id == st.id {
				c.Self = true
			}
		}
	} else {
		// 記録できなければゼロのままにする。実行時の照合（§7.3）で一致しないので、上書き・マージされない。
		w.warn(dst, err)
	}
	p := w.pl.plan
	c.ID = ConflictID(len(p.conflicts) + 1)
	p.conflicts = append(p.conflicts, c)
	p.conflictDst = append(p.conflictDst, dstID)
	return !c.Self && srcInfo.Type == TypeDir && dstInfo.Type == TypeDir, c.ID, nil
}

// walk は、フォルダ src の中身を数える。dst が空でなければ、dst の同名のエントリとの衝突も調べる（parent はその親の衝突）。
// リンク・ジャンクション・特殊なファイルには入り込まない（§13.1、I4）。
func (w *walker) walk(src, dst string, parent ConflictID) error {
	entries, err := readDir(src)
	if err != nil {
		w.warn(src, err)
		return nil
	}
	for _, e := range entries {
		if err := w.pl.errCanceled(); err != nil {
			return err
		}
		w.add(e.info)
		childSrc := filepath.Join(src, e.name)
		childDst := ""
		var cid ConflictID
		if dst != "" {
			d := filepath.Join(dst, e.name)
			dirConflict, id, err := w.conflict(childSrc, d, e.info, parent)
			if err != nil {
				return err
			}
			if dirConflict {
				childDst, cid = d, id
			}
		}
		if e.info.Type == TypeDir {
			if err := w.walk(childSrc, childDst, cid); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkFreeSpace は、書き込むバイト数とコピー先ボリュームの空き容量を比べ、足りなければ Warnings に KindNoSpace を加える（§6.4）。
func (pl *planner) checkFreeSpace(dest string, need int64) {
	free, err := freeSpace(dest)
	if err != nil {
		pl.plan.warnings = append(pl.plan.warnings, planError(dest, classify(err, classifyOpts{}), withUserPaths(err, dest, "")))
		return
	}
	if uint64(need) > free {
		pl.plan.warnings = append(pl.plan.warnings, planError(dest, KindNoSpace, nil))
	}
}

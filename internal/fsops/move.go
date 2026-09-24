package fsops

import (
	"path/filepath"
	"slices"
	"strings"
)

// copyThenRemove は、ボリュームをまたぐ移動（MethodCopyThenRemove）のトップレベルの 1 項目を処理する（§11.2）。
// コピー（必ず同期する）が失敗・キャンセル・決定によらない Skip なしで完了した場合だけ、記録したエントリだけを移動元から削除する（I2）。
func (ex *executor) copyThenRemove(i int, it Item) ItemResult {
	ex.progress.start(StageCopy, it.Src)
	res, rec := ex.copyTop(i, it, true)
	if res.Outcome != OutcomeDone || rec == nil {
		return res // 移動元には一切手を付けない（§11.2 の手順 2）。移動先に途中までコピーされたものはそのまま報告する
	}
	ex.opt.hooks.removeSource(it.Src)
	ex.progress.start(StageRemoveSource, it.Src)
	out := removeRecorded(ex.locks, it.Src, *rec, func(EntryInfo) { ex.progress.report(false) })
	res.Details = append(res.Details, out.details...)
	sortDetails(res.Details) // コピーの結果と移動元の削除の結果を合わせて名前順にする（§5）
	switch {
	case out.canceled:
		res.Outcome, res.Err = OutcomeCopiedSourceKept, &OpError{Op: "move", Path: it.Src, Kind: KindCanceled, Err: ex.ctx.Err()}
	case out.firstErr != nil:
		res.Outcome, res.Err = OutcomeCopiedSourceKept, out.firstErr // §11.2 の手順 4
	}
	return res
}

// sortDetails は、Details をパスの要素ごとの名前順（フォルダの中を名前順に深さ優先でたどる順）に並べる（§5）。
// 同じ項目の中のパス同士を並べるだけで、同一性の判定には使わない。
func sortDetails(ds []EntryResult) {
	slices.SortStableFunc(ds, func(a, b EntryResult) int {
		return slices.Compare(strings.Split(a.Src, string(filepath.Separator)), strings.Split(b.Src, string(filepath.Separator)))
	})
}

// mover は、同一ボリュームの移動（MethodRename。§11.1）のトップレベルの 1 項目の作業状態。
type mover struct {
	ex       *executor
	details  []EntryResult
	firstErr *OpError
	canceled bool
	movedAny bool // 何かを移動した
}

// entry は、マージ移動の中のエントリの結果を記録する。Done 以外だけを Details に入れる（§7.4）。
func (mv *mover) entry(src, dst string, out Outcome, err *OpError) {
	if out == OutcomeDone || mv.canceled && err != nil && err.Kind == KindCanceled {
		return
	}
	mv.details = append(mv.details, EntryResult{Src: src, Dst: dst, Outcome: out, Err: err})
	if err != nil && mv.firstErr == nil {
		mv.firstErr = err
	}
}

func (mv *mover) checkCanceled() bool {
	if mv.ex.ctx.Err() != nil {
		mv.canceled = true
	}
	return mv.canceled
}

// moved は、エントリ 1 件を移動したことを記録する。
func (mv *mover) moved(src string) {
	mv.movedAny = true
	mv.ex.progress.fileDone(src)
}

// moveItem は、同一ボリュームの移動（MethodRename）のトップレベルの 1 項目を処理する（§11.1）。i は Items() の添字。
// トップレベルの項目のリネームがボリューム違いのエラーで失敗したら、§11.2 の方式でやり直す。
func (ex *executor) moveItem(i int, it Item) ItemResult {
	res := ItemResult{Src: it.Src, Dst: it.Dst}
	pc := ex.conflictIdx.top[i]
	if pc != nil && (pc.c.Decision == DecisionUnset || pc.c.Decision == DecisionSkip) {
		res.Outcome = OutcomeSkipped // 衝突の決定による Skip（I1）
		return res
	}
	e, err := statTop(it.Src)
	if err != nil {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "move", Path: it.Src, Kind: classify(err, classifyOpts{}), Err: err}
		return res
	}
	if e.info.Type != it.Info.Type || pc != nil && e.info.Type != pc.c.SrcInfo.Type {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "move", Path: it.Src, Kind: KindSourceChanged}
		return res
	}
	e.name = filepath.Base(it.Src)
	// 移動先のフォルダ（DestDir）を、計画時と同じもの（fileID）であることを確かめて開き、項目の処理が終わるまで持つ（§13.1）。
	dd, err := openDestRoot(ex.plan.req.DestDir, ex.plan.destID)
	if err != nil {
		res.Outcome, res.Err = OutcomeFailed, destErr(&OpError{Op: "move", Path: it.Src, Dest: it.Dst, Kind: KindOf(err), Err: err})
		return res
	}
	defer dd.close()
	mv := &mover{ex: ex}
	var out Outcome
	var oe *OpError
	if pc != nil && pc.c.Decision == DecisionMerge {
		gone, terr := checkTarget(it.Src, it.Dst, pc, dd)
		switch {
		case terr != nil && terr.Kind == KindExist:
			res.Outcome, res.Err = OutcomeSkipped, terr
			return res
		case terr != nil:
			res.Outcome, res.Err = OutcomeFailed, terr
			return res
		case !gone:
			out, oe, _ = mv.merge(nil, it.Src, it.Dst, e, pc, dd)
		default:
			res.Dst, out, oe = mv.rename(nil, it.Src, it.Dst, e, nil, dd) // マージ先が消えていれば、衝突なしとして移動する
		}
	} else {
		res.Dst, out, oe = mv.rename(nil, it.Src, it.Dst, e, pc, dd)
	}
	if oe != nil && oe.Kind == KindCrossDevice {
		return ex.copyThenRemove(i, it) // §11.1: トップレベルの項目は §11.2 の方式でやり直す
	}
	res.Details = mv.details
	switch {
	case mv.canceled && mv.movedAny:
		res.Outcome, res.Err = OutcomePartial, &OpError{Op: "move", Path: it.Src, Kind: KindCanceled, Err: ex.ctx.Err()}
	case mv.canceled:
		res.Outcome, res.Err = OutcomeSkipped, &OpError{Op: "move", Path: it.Src, Kind: KindCanceled, Err: ex.ctx.Err()}
	case out != OutcomeDone:
		res.Outcome, res.Err = out, oe
	case mv.firstErr != nil:
		res.Outcome, res.Err = OutcomePartial, mv.firstErr
	default:
		res.Outcome = OutcomeDone
	}
	return res
}

// rename は、エントリ e（src）を dst へリネームで移動する（§11.1）。pc は計画時に検出した衝突（なければ nil。マージ以外）。
// parent が nil ならパスで、そうでなければ開いたフォルダ parent からの相対で移動する（§13.1）。
// 移動先は、確かめて開いた移動先のフォルダ dd の中の名前として扱う（総点検の穴 4）。
// 衝突なしは排他リネーム、上書きは §9.3 の事前確認の後に置換リネーム、自動リネームは §9.2 の候補への排他リネーム。
// 実際の移動先のパスと結果を返す。ボリューム違いのエラーは KindCrossDevice のまま返す（呼び出し側が扱う）。
func (mv *mover) rename(parent *secDir, src, dst string, e dirEntry, pc *planned, dd *secDir) (string, Outcome, *OpError) {
	do := func(d string, replace bool) error {
		if err := mv.ex.opt.hooks.moveRename(src, d); err != nil {
			return err
		}
		from := e.name
		if parent == nil {
			s, err := sysPath(src)
			if err != nil {
				return err
			}
			from = s
		}
		if err := mv.ex.opt.hooks.lockFaultErr("rename", d); err != nil {
			return err
		}
		return withUserPaths(renameBetween(parent, from, dd, filepath.Base(d), replace), src, d)
	}
	// 使用中で失敗したら、上書きの確認からやり直す（§17.1）。
	locks := mv.ex.locks
	var final string
	var out Outcome
	var oe *OpError
	switch {
	case pc != nil && pc.c.Decision == DecisionOverwrite && pc.dstID == e.id && !e.id.synthetic():
		// 上書き先が移動元と同じファイル（ハードリンク）。rename は何もせずに成功するので、Done と報告しないよう失敗にする（§7.3）。
		final, out, oe = dst, OutcomeFailed, &OpError{Op: "move", Path: src, Dest: dst, Kind: KindSameFile}
	case pc != nil && pc.c.Decision == DecisionOverwrite:
		final = dst
		out, oe = locks.result(dst, func() (Outcome, *OpError) { return overwriteOnce(src, dst, pc, dd, do) })
	case pc != nil && pc.c.Decision == DecisionAutoRename:
		final, out, oe = autoRename(src, dst, e.info.Type == TypeDir, false, func(cand string) error {
			return locks.retry(cand, false, func() error { return do(cand, false) }, lockedErr)
		})
	default:
		final = dst
		out, oe = locks.result(dst, func() (Outcome, *OpError) { return createResult(src, dst, do(dst, false), false) })
	}
	if oe != nil && oe.Kind == KindCanceled && mv.checkCanceled() {
		return final, OutcomeSkipped, &OpError{Op: "move", Path: src, Dest: final, Kind: KindCanceled, Err: mv.ex.ctx.Err()}
	}
	if oe != nil {
		oe.Op = "move"
		return final, out, oe
	}
	mv.moved(src)
	return final, OutcomeDone, nil
}

// merge は、フォルダ e（src）を既存のフォルダ dst へマージ移動する（§11.1）。中身を 1 件ずつ移動し、内側の衝突はそれぞれの決定に従う。
// src には §13.1 の方法で入り（リンクに置き換えられていれば入らない。I4）、中身は開いたフォルダからの相対で移動する。
// マージ先の dst も、移動先のフォルダ ddParent の中で §13.1 の方法で開いて計画時の fileID と照合し、中身はそのハンドルの中へ移動する
// （照合した後にリンクへ置き換えられても、リンクの先へ移動しない。総点検の穴 4）。
// 最後に移動元のフォルダが空なら §13.2 の方法で削除する。中身の結果は Details に記録し、フォルダ自体の結果と、
// 移動元のフォルダを削除できたか（removed）を返す。
func (mv *mover) merge(parent *secDir, src, dst string, e dirEntry, pc *planned, ddParent *secDir) (out Outcome, oe *OpError, removed bool) {
	mv.ex.opt.hooks.openDest(dst)
	dd, derr := openSecDir(ddParent, dst, filepath.Base(dst), pc.dstID)
	if derr != nil {
		doe, ok := derr.(*OpError)
		if !ok {
			doe = &OpError{Kind: classify(derr, classifyOpts{}), Err: derr}
		}
		doe.Op, doe.Path, doe.Dest = "move", src, dst
		if doe.Kind == KindSourceChanged {
			doe.Kind = KindExist // マージ先が照合の後に置き換えられた（計画後に現れた衝突。§7.3）
			return OutcomeSkipped, destErr(doe), false
		}
		return OutcomeFailed, destErr(doe), false
	}
	defer dd.close()
	mv.ex.opt.hooks.enterDir(src)
	d, err := openSecDir(parent, src, e.name, e.id)
	if err != nil {
		oe, ok := err.(*OpError)
		if !ok {
			oe = &OpError{Op: "move", Path: src, Kind: classify(err, classifyOpts{}), Err: err}
		}
		oe.Op, oe.Dest = "move", dst
		return OutcomeFailed, oe, false
	}
	entries, err := d.list()
	if err != nil {
		d.close()
		return OutcomeFailed, &OpError{Op: "move", Path: src, Dest: dst, Kind: classify(err, classifyOpts{}), Err: err}, false
	}
	inner := mv.ex.conflictIdx.inner[pc.c.ID]
	left := 0 // 移動元に残したエントリの数（衝突の決定による Skip、失敗）
	for _, ce := range entries {
		if mv.checkCanceled() {
			d.close()
			return OutcomeDone, nil, false
		}
		childSrc, childDst := filepath.Join(src, ce.name), filepath.Join(dst, ce.name)
		final, out, oe, gone := mv.mergeEntry(d, childSrc, childDst, ce, inner[ce.name], dd)
		mv.entry(childSrc, final, out, oe)
		if out != OutcomeDone || !gone {
			left++ // 移動元に残した（中にスキップしたものが残って、フォルダ自体を消さなかった場合を含む）
		}
		if mv.canceled {
			d.close()
			return OutcomeDone, nil, false
		}
	}
	d.close() // フォルダ自体を削除する直前に閉じる（§13.1）
	mv.ex.opt.hooks.remove(src)
	remove := func() error { return removeTop(src, e, false) }
	restat := func() (dirEntry, error) { return statTop(src) }
	if parent != nil {
		remove = func() error { return parent.remove(e, false) }
		restat = func() (dirEntry, error) { return parent.stat(e.name) }
	}
	// 削除と同じ方法で分類する（置き換えられていれば KindSourceChanged）。使用中の間はやり直す（§17.1）。
	_, oe = removeWithRetry(mv.ex.locks, src, e, remove, restat)
	if oe == nil {
		return OutcomeDone, nil, true
	}
	if oe.Kind == KindCanceled && mv.checkCanceled() {
		return OutcomeDone, nil, false // キャンセルで打ち切った。移動元のフォルダは残る
	}
	if left == 0 || oe.Kind == KindSourceChanged {
		// 残したものがないのに空でない（移動中に追加された）、削除できなかった、または置き換えられていた。移動元のフォルダは残る。
		oe.Op = "move"
		mv.entry(src, dst, OutcomeFailed, oe)
	}
	return OutcomeDone, nil, false
}

// mergeEntry は、マージ移動の中のエントリ 1 件を処理する。実際の移動先のパスと結果、移動元から消えたか（gone）を返す。
func (mv *mover) mergeEntry(d *secDir, src, dst string, e dirEntry, pc *planned, dd *secDir) (final string, out Outcome, oe *OpError, gone bool) {
	if pc != nil && (pc.c.Decision == DecisionUnset || pc.c.Decision == DecisionSkip) {
		return dst, OutcomeSkipped, nil, false // 衝突の決定による Skip。移動元に残る
	}
	if pc != nil && e.info.Type != pc.c.SrcInfo.Type {
		return dst, OutcomeFailed, &OpError{Op: "move", Path: src, Dest: dst, Kind: KindSourceChanged}, false
	}
	if pc != nil && pc.c.Decision == DecisionMerge {
		targetGone, terr := checkTarget(src, dst, pc, dd)
		switch {
		case terr != nil && terr.Kind == KindExist:
			return dst, OutcomeSkipped, terr, false
		case terr != nil:
			return dst, OutcomeFailed, terr, false
		case !targetGone:
			out, oe, removed := mv.merge(d, src, dst, e, pc, dd)
			return dst, out, oe, removed
		}
		pc = nil // マージ先が消えていれば、衝突なしとして移動する
	}
	final, out, oe = mv.rename(d, src, dst, e, pc, dd)
	if oe != nil && oe.Kind == KindCrossDevice {
		// §11.1: マージの途中で内側のエントリがボリューム違いになった場合は、そのエントリを失敗とする。
		// KindCrossDevice は結果に出さない（§17）ので KindUnknown にする。
		oe.Kind = KindUnknown
	}
	return final, out, oe, oe == nil
}

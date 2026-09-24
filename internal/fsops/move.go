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
	out := removeRecorded(ex.ctx, it.Src, *rec, ex.opt.hooks, func(EntryInfo) { ex.progress.report(false) })
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
	mv := &mover{ex: ex}
	var out Outcome
	var oe *OpError
	if pc != nil && pc.c.Decision == DecisionMerge {
		gone, terr := checkTarget(it.Src, it.Dst, pc)
		switch {
		case terr != nil && terr.Kind == KindExist:
			res.Outcome, res.Err = OutcomeSkipped, terr
			return res
		case terr != nil:
			res.Outcome, res.Err = OutcomeFailed, terr
			return res
		case !gone:
			out, oe, _ = mv.merge(nil, it.Src, it.Dst, e, pc)
		default:
			res.Dst, out, oe = mv.rename(nil, it.Src, it.Dst, e, nil) // マージ先が消えていれば、衝突なしとして移動する
		}
	} else {
		res.Dst, out, oe = mv.rename(nil, it.Src, it.Dst, e, pc)
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
// 衝突なしは排他リネーム、上書きは §9.3 の事前確認の後に置換リネーム、自動リネームは §9.2 の候補への排他リネーム。
// 実際の移動先のパスと結果を返す。ボリューム違いのエラーは KindCrossDevice のまま返す（呼び出し側が扱う）。
func (mv *mover) rename(parent *secDir, src, dst string, e dirEntry, pc *planned) (string, Outcome, *OpError) {
	do := func(d string, replace bool) error {
		if err := mv.ex.opt.hooks.moveRename(src, d); err != nil {
			return err
		}
		ds, err := sysPath(d)
		if err != nil {
			return err
		}
		if parent != nil {
			return withUserPaths(parent.renameOut(e.name, ds, replace), src, d)
		}
		if replace {
			return renameReplace(src, d)
		}
		return renameExclusive(src, d)
	}
	var final string
	var out Outcome
	var oe *OpError
	switch {
	case pc != nil && pc.c.Decision == DecisionOverwrite:
		final = dst
		gone, o, terr := checkOverwrite(src, dst, pc)
		switch {
		case terr != nil:
			out, oe = o, terr
		case gone:
			out, oe = createResult(src, dst, do(dst, false), false)
		default:
			out, oe = replaceResult(src, dst, do(dst, true))
		}
	case pc != nil && pc.c.Decision == DecisionAutoRename:
		final, out, oe = autoRename(src, dst, e.info.Type == TypeDir, false, func(cand string) error { return do(cand, false) })
	default:
		final = dst
		out, oe = createResult(src, dst, do(dst, false), false)
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
// 最後に移動元のフォルダが空なら §13.2 の方法で削除する。中身の結果は Details に記録し、フォルダ自体の結果と、
// 移動元のフォルダを削除できたか（removed）を返す。
func (mv *mover) merge(parent *secDir, src, dst string, e dirEntry, pc *planned) (out Outcome, oe *OpError, removed bool) {
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
		final, out, oe, gone := mv.mergeEntry(d, childSrc, childDst, ce, inner[ce.name])
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
	var rerr error
	if parent != nil {
		rerr = parent.remove(e, false)
	} else {
		rerr = removeTop(src, e, false)
	}
	if rerr != nil && left == 0 {
		// 残したものがないのに空でない（移動中に追加された）、または削除できなかった。移動元のフォルダは残る。
		ss, _ := sysPath(src)
		mv.entry(src, dst, OutcomeFailed, &OpError{Op: "move", Path: src, Kind: classify(rerr, classifyOpts{readOnly: readOnlySys(ss)}), Err: withUserPaths(rerr, src, "")})
	}
	return OutcomeDone, nil, rerr == nil
}

// mergeEntry は、マージ移動の中のエントリ 1 件を処理する。実際の移動先のパスと結果、移動元から消えたか（gone）を返す。
func (mv *mover) mergeEntry(d *secDir, src, dst string, e dirEntry, pc *planned) (final string, out Outcome, oe *OpError, gone bool) {
	if pc != nil && (pc.c.Decision == DecisionUnset || pc.c.Decision == DecisionSkip) {
		return dst, OutcomeSkipped, nil, false // 衝突の決定による Skip。移動元に残る
	}
	if pc != nil && e.info.Type != pc.c.SrcInfo.Type {
		return dst, OutcomeFailed, &OpError{Op: "move", Path: src, Dest: dst, Kind: KindSourceChanged}, false
	}
	if pc != nil && pc.c.Decision == DecisionMerge {
		targetGone, terr := checkTarget(src, dst, pc)
		switch {
		case terr != nil && terr.Kind == KindExist:
			return dst, OutcomeSkipped, terr, false
		case terr != nil:
			return dst, OutcomeFailed, terr, false
		case !targetGone:
			out, oe, removed := mv.merge(d, src, dst, e, pc)
			return dst, out, oe, removed
		}
		pc = nil // マージ先が消えていれば、衝突なしとして移動する
	}
	final, out, oe = mv.rename(d, src, dst, e, pc)
	if oe != nil && oe.Kind == KindCrossDevice {
		// §11.1: マージの途中で内側のエントリがボリューム違いになった場合は、そのエントリを失敗とする。
		// KindCrossDevice は結果に出さない（§17）ので KindUnknown にする。
		oe.Kind = KindUnknown
	}
	return final, out, oe, oe == nil
}

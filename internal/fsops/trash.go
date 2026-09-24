package fsops

import "context"

// trashPrecheck は、ごみ箱が使えるかの事前確認（§12.1）。ファイルシステムを変更しない。
// 使えなければ KindTrashUnavailable、確認の途中で ctx がキャンセルされたら KindCanceled の *OpError を返す。
// 計画時（NewPlan）と実行時（Execute）の両方で使う。
func trashPrecheck(ctx context.Context, src string, info EntryInfo) *OpError {
	ok, err := trashAvailable(ctx, src, info)
	if err != nil && ctx.Err() != nil {
		return &OpError{Op: "trash", Path: src, Kind: KindCanceled, Err: ctx.Err()}
	}
	if !ok {
		return &OpError{Op: "trash", Path: src, Kind: KindTrashUnavailable, Err: err}
	}
	return nil
}

// trashItem は、ごみ箱（MethodTrash）のトップレベルの 1 項目を処理する（§12）。
// 実行時にも事前確認をもう一度行い（§12.1）、使えなければ何もせず KindTrashUnavailable にする（I5）。
func (ex *executor) trashItem(it Item) ItemResult {
	res := ItemResult{Src: it.Src}
	e, err := statTop(it.Src)
	if err != nil {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "trash", Path: it.Src, Kind: classify(err, classifyOpts{}), Err: err}
		return res
	}
	if e.info.Type != it.Info.Type {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "trash", Path: it.Src, Kind: KindSourceChanged}
		return res
	}
	if ex.opt.hooks.trashPrecheck() {
		// 計画の後に大きくなった項目などを見逃さないよう、今の大きさで確かめる。
		if oe := trashPrecheck(ex.ctx, it.Src, e.info); oe != nil {
			res.Outcome, res.Err = OutcomeFailed, oe
			if oe.Kind == KindCanceled {
				res.Outcome = OutcomeSkipped
			}
			return res
		}
	}
	if err := ex.ctx.Err(); err != nil {
		res.Outcome, res.Err = OutcomeSkipped, &OpError{Op: "trash", Path: it.Src, Kind: KindCanceled, Err: err}
		return res
	}
	trashed, err := trashSys(it.Src, e.info)
	if err != nil {
		oe, ok := err.(*OpError)
		if !ok {
			oe = &OpError{Op: "trash", Path: it.Src, Kind: classify(err, classifyOpts{}), Err: err}
		}
		res.Outcome, res.Err = OutcomeFailed, oe
		return res
	}
	// 成功を返しても元の場所に残っていれば、ごみ箱に入ったとはいえないので失敗にする。
	if _, err := statTop(it.Src); err == nil || classify(err, classifyOpts{}) != KindNotFound {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "trash", Path: it.Src, Kind: KindUnknown, Err: err}
		return res
	}
	res.Outcome, res.TrashedPath = OutcomeDone, trashed
	ex.progress.done(it.Src, e.info)
	return res
}

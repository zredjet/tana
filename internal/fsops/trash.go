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
	trashed, err := ex.opt.hooks.trash(it.Src, e.info)
	res.Outcome, res.Err = trashOutcome(it.Src, e, trashed, err)
	if res.Outcome == OutcomeDone {
		res.TrashedPath = trashed
	}
	if res.Outcome != OutcomeFailed {
		// 元の場所から消えた。ごみ箱の項目は、計画で 1 件・0 バイトと数える（§6.3）ので、大きさは加えない。
		ex.progress.done(it.Src, EntryInfo{Type: e.info.Type})
	}
	return res
}

// trashOutcome は、ごみ箱へ移す呼び出しの後の状態から、項目 e（src）の結果を決める（§12.1）。
// 呼び出しが返した成否（trashed、err）ではなく、元の場所に元の項目があるか、ごみ箱の中の項目があるかで決める（利用者に誤った状態を伝えないため）。
func trashOutcome(src string, e dirEntry, trashed string, err error) (Outcome, *OpError) {
	callErr := func() *OpError {
		if err == nil {
			return &OpError{Op: "trash", Path: src, Kind: KindUnknown}
		}
		if oe, ok := err.(*OpError); ok {
			return oe
		}
		return &OpError{Op: "trash", Path: src, Kind: classify(err, classifyOpts{}), Err: err}
	}
	now, serr := statTop(src)
	switch {
	case serr == nil && now.id == e.id:
		return OutcomeFailed, callErr() // 1. 元の場所に残っている
	case serr != nil && classify(serr, classifyOpts{}) != KindNotFound:
		// 4. 元の場所を調べられない。状態がわからない
		return OutcomeFailed, &OpError{Op: "trash", Path: src, Kind: classify(serr, classifyOpts{}), Err: serr}
	}
	// 元の項目は元の場所にない（消えた、または別のものに置き換わった）。
	if trashed != "" {
		if _, err := lstatEntry(trashed); err == nil {
			return OutcomeDone, nil // 2. ごみ箱の中にある（呼び出しが失敗を返していても）
		}
	}
	return OutcomeTrashUnconfirmed, callErr() // 3. ごみ箱に入ったことを確かめられない（完全に削除された可能性がある。V18）
}

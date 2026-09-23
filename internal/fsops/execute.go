package fsops

import (
	"context"
	"errors"
)

// Execute は計画を実行する（§7）。開始時に決定を固定する。
// error を返すのは、何も実行しなかった場合（§7.1）だけ: Plan が nil・ゼロ値、2 回目の実行（並行を含む）、許されない決定がある場合。
func (p *Plan) Execute(ctx context.Context, opt ExecOptions) (*Result, error) {
	invalid := &OpError{Op: "execute", Kind: KindInvalidRequest}
	if p == nil {
		return nil, invalid
	}
	p.mu.Lock()
	if len(p.items) == 0 || p.started {
		p.mu.Unlock()
		return nil, invalid
	}
	for _, c := range p.conflicts {
		if !decisionAllowed(c, c.Decision) {
			p.mu.Unlock()
			return nil, invalid
		}
	}
	p.started = true
	conflicts := append([]Conflict(nil), p.conflicts...) // 決定を固定する
	p.mu.Unlock()

	ex := &executor{
		ctx:         ctx,
		plan:        p,
		opt:         opt,
		conflicts:   conflicts,
		conflictIdx: newConflictIndex(conflicts, p.conflictDst),
		progress:    &progressReporter{fn: opt.Progress, cur: Progress{TotalFiles: p.totalFiles, TotalBytes: p.totalBytes}},
	}
	return ex.run(), nil
}

// executor は Execute の作業状態。
type executor struct {
	ctx         context.Context
	plan        *Plan
	opt         ExecOptions
	conflicts   []Conflict // Execute の開始時に固定した決定
	conflictIdx conflictIndex
	progress    *progressReporter
	buf         []byte // コピーのバッファ（§10.1）。項目は 1 件ずつ処理するので、Execute の間使い回す
}

// copyBuf はコピーのバッファを返す。最初に使うときに確保する。
func (ex *executor) copyBuf() []byte {
	if ex.buf == nil {
		ex.buf = make([]byte, copyBufSize)
	}
	return ex.buf
}

// run は、トップレベルの項目を計画の順に 1 件ずつ処理する（§7.2）。
func (ex *executor) run() *Result {
	items := ex.plan.items
	res := &Result{Items: make([]ItemResult, len(items))}
	canceled, noSpace := false, false
	for i, it := range items {
		if canceled || ex.ctx.Err() != nil {
			canceled = true
			res.Items[i] = skipped(it, KindCanceled, ex.ctx.Err())
			continue
		}
		var r ItemResult
		switch {
		case it.Err != nil:
			r = ItemResult{Src: it.Src, Dst: it.Dst, Outcome: OutcomeFailed, Err: it.Err.clone()}
		case noSpace && (it.Method == MethodCopy || it.Method == MethodCopyThenRemove):
			r = skipped(it, KindNoSpace, nil)
		default:
			r = ex.do(i, it)
		}
		if r.Err != nil && r.Err.Kind == KindNoSpace && r.Outcome != OutcomeSkipped {
			noSpace = true // 以後の書き込みを伴う項目は Skipped（§7.2）
		}
		if r.Err != nil && r.Err.Kind == KindCanceled {
			canceled = true // 処理中の項目でキャンセルを検出した
		}
		res.Items[i] = r
		ex.progress.report(true) // 項目の区切りでは必ず報告する（§16）
	}
	ex.progress.report(true) // 終了時にも必ず報告する
	res.Status = status(res.Items, canceled)
	return res
}

// do は 1 項目を処理する。i は Items() の添字。
func (ex *executor) do(i int, it Item) ItemResult {
	switch it.Method {
	case MethodCopy:
		ex.progress.start(StageCopy, it.Src)
		return ex.copyItem(i, it)
	case MethodRemove:
		ex.progress.start(StageDelete, it.Src)
		return deleteItem(ex.ctx, ex.opt.hooks, it, ex.progress.done)
	}
	// 移動・ごみ箱はフェーズ9〜10で作る。
	return ItemResult{Src: it.Src, Dst: it.Dst, Outcome: OutcomeFailed,
		Err: &OpError{Op: "execute", Path: it.Src, Kind: KindUnknown, Err: errors.ErrUnsupported}}
}

// skipped は、着手せずに打ち切った項目の結果（§7.4）。
func skipped(it Item, kind Kind, err error) ItemResult {
	return ItemResult{Src: it.Src, Dst: it.Dst, Outcome: OutcomeSkipped, Err: &OpError{Op: "execute", Path: it.Src, Kind: kind, Err: err}}
}

// status は Result.Status を決める（§7.4）。
// キャンセルを検出したら StatusCanceled。それ以外で、Failed・Partial・CopiedSourceKept、または Err 付きの Skipped が
// 1 件でもあれば StatusCompletedWithErrors。それ以外は StatusCompleted。
func status(items []ItemResult, canceled bool) Status {
	if canceled {
		return StatusCanceled
	}
	for _, it := range items {
		switch it.Outcome {
		case OutcomeFailed, OutcomePartial, OutcomeCopiedSourceKept:
			return StatusCompletedWithErrors
		case OutcomeSkipped:
			if it.Err != nil {
				return StatusCompletedWithErrors
			}
		}
	}
	return StatusCompleted
}

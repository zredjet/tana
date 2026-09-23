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

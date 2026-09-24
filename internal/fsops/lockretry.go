package fsops

import (
	"context"
	"errors"
	"time"
)

// 使用中の一時的な失敗のやり直しの間隔と上限（§17.1）。
const (
	lockRetryFirst       = 10 * time.Millisecond
	lockRetryMaxInterval = 200 * time.Millisecond
	lockRetryPerOp       = time.Second      // 1 操作の待ちの合計の上限
	lockRetryPerExecute  = 10 * time.Second // 1 回の Execute（Rename は 1 回の呼び出し）の待ちの合計の上限
)

// errInjectedLock は、テストのフック（lockFault）が注入した使用中の失敗の印。
var errInjectedLock = errors.New("fsops: injected lock (test hook)")

// lockRetrier は、使用中の一時的な失敗のやり直し（§17.1）の状態。1 回の Execute（または Rename の 1 回の呼び出し）の間だけ使う。
type lockRetrier struct {
	ctx    context.Context
	hooks  *testHooks
	waited time.Duration // これまでの待ちの合計
}

func newLockRetrier(ctx context.Context, h *testHooks) *lockRetrier {
	return &lockRetrier{ctx: ctx, hooks: h}
}

// lockedErr は、err が §17.1 のやり直しの対象（使用中の失敗）かを返す。注入した失敗は、どの OS でも対象にする。
func lockedErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, errInjectedLock) || lockRetrySys && classify(err, classifyOpts{}) == KindLocked
}

// lockedOpError は、分類済みの oe が §17.1 のやり直しの対象かを返す（§9.3 の使用中の判定を含む）。
func lockedOpError(err error) bool {
	oe, ok := err.(*OpError)
	return ok && oe != nil && oe.Kind == KindLocked && (lockRetrySys || errors.Is(oe, errInjectedLock))
}

// retry は attempt を呼び、locked が真の失敗なら、間隔を空けて attempt をやり直す（§17.1）。
// attempt は「確かめてから操作するまで」の一連で、確認も毎回やり直す。path は待ちのフックに渡すパス。
// 上限に達したら最後の失敗を返す。待っている間にキャンセルされたら KindCanceled の *OpError を返す。
// ignoreCancel が真なら、キャンセルされていても上限まで待ってやり直す（fsops の一時ファイルの削除。I3）。
func (lr *lockRetrier) retry(path string, ignoreCancel bool, attempt func() error, locked func(error) bool) error {
	interval := lockRetryFirst
	var waited time.Duration
	for {
		err := attempt()
		if !locked(err) {
			return err
		}
		d := min(interval, lockRetryPerOp-waited, lockRetryPerExecute-lr.waited)
		if d <= 0 {
			return err
		}
		if cerr := lr.wait(path, d, ignoreCancel); cerr != nil {
			return &OpError{Op: "wait", Path: path, Kind: KindCanceled, Err: cerr}
		}
		waited += d
		lr.waited += d
		interval = min(interval*2, lockRetryMaxInterval)
	}
}

// wait は d だけ待つ。ignoreCancel が偽なら、キャンセルされたらすぐに ctx.Err() を返す。
func (lr *lockRetrier) wait(path string, d time.Duration, ignoreCancel bool) error {
	if lr.hooks.waitLock(path, d) {
		if ignoreCancel {
			return nil
		}
		return lr.ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	if ignoreCancel {
		<-t.C
		return nil
	}
	select {
	case <-t.C:
		return nil
	case <-lr.ctx.Done():
		return lr.ctx.Err()
	}
}

// result は、結果を分類して返す操作 attempt を、KindLocked の間やり直す（§17.1）。
// 待っている間にキャンセルされたら OutcomeSkipped と KindCanceled を返す。
func (lr *lockRetrier) result(path string, attempt func() (Outcome, *OpError)) (Outcome, *OpError) {
	var out Outcome
	err := lr.retry(path, false, func() error {
		o, oe := attempt()
		out = o
		if oe == nil {
			return nil
		}
		return oe
	}, lockedOpError)
	if err == nil {
		return out, nil
	}
	oe := err.(*OpError)
	if oe.Kind == KindCanceled {
		return OutcomeSkipped, oe
	}
	return out, oe
}

// op は、分類済みのエラーを返す操作 attempt を、KindLocked の間やり直す（§17.1）。
// 待っている間にキャンセルされたら KindCanceled を返す。
func (lr *lockRetrier) op(path string, attempt func() *OpError) *OpError {
	err := lr.retry(path, false, func() error {
		if oe := attempt(); oe != nil {
			return oe
		}
		return nil
	}, lockedOpError)
	if err == nil {
		return nil
	}
	return err.(*OpError)
}

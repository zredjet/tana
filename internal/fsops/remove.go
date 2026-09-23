package fsops

import (
	"context"
	"path/filepath"
)

// remover は、完全削除（§13.2）と記録した項目だけの削除（§13.3）の作業状態。
type remover struct {
	ctx      context.Context
	hooks    *testHooks
	recorded bool // §13.3（移動元の削除）なら真
	// onRemoved は、フォルダ以外のエントリを削除するたびに呼ばれる（進捗用。nil 可）。
	onRemoved func(path string, info EntryInfo)

	details    []EntryResult
	firstErr   *OpError
	canceled   bool
	removedAny bool
}

// fail は、削除しなかったエントリを Details に記録する。
// 完全削除では OutcomeFailed、移動元の削除では OutcomeCopiedSourceKept（移動元に残したもの）とする。
func (r *remover) fail(path string, err *OpError) {
	outcome := OutcomeFailed
	if r.recorded {
		outcome = OutcomeCopiedSourceKept
	}
	r.details = append(r.details, EntryResult{Src: path, Outcome: outcome, Err: err})
	if r.firstErr == nil {
		r.firstErr = err
	}
}

// checkCanceled は、ctx がキャンセルされていれば canceled にして真を返す（エントリごとに確かめる。§16）。
func (r *remover) checkCanceled() bool {
	if r.ctx.Err() != nil {
		r.canceled = true
	}
	return r.canceled
}

// removeErr は、削除の失敗を分類する（§13.2、§17）。種類に合わない方法での削除の失敗なら調べ直し、
// 種類が変わっていれば KindSourceChanged にする。restat は調べ直す関数、sysPath は読み取り専用の判定に使うパス。
func removeErr(path string, err error, e dirEntry, restat func() (dirEntry, error), sys string) *OpError {
	if isMismatchRemoveErr(err) {
		if now, serr := restat(); serr == nil && now.dirAttr != e.dirAttr {
			return &OpError{Op: "remove", Path: path, Kind: KindSourceChanged, Err: err}
		}
	}
	return &OpError{Op: "remove", Path: path, Kind: classify(err, classifyOpts{readOnly: readOnlySys(sys)}), Err: withUserPaths(err, path, "")}
}

// removeIn は、確かめて開いたフォルダ d の中のエントリ e を削除する。削除できたら真。
func (r *remover) removeIn(d *secDir, e dirEntry) bool {
	path := filepath.Join(d.path, e.name)
	r.hooks.remove(path)
	if r.checkCanceled() {
		return false
	}
	if err := d.remove(e, r.recorded); err != nil {
		sys, _ := sysPath(path)
		r.fail(path, removeErr(path, err, e, func() (dirEntry, error) { return d.stat(e.name) }, sys))
		return false
	}
	r.removed(path, e.info)
	return true
}

// removeTopEntry は、トップレベルのエントリ path を削除する。削除できたら真。
func (r *remover) removeTopEntry(path string, e dirEntry) bool {
	r.hooks.remove(path)
	if r.checkCanceled() {
		return false
	}
	if err := removeTop(path, e, r.recorded); err != nil {
		sys, _ := sysPath(path)
		r.fail(path, removeErr(path, err, e, func() (dirEntry, error) { return statTop(path) }, sys))
		return false
	}
	r.removed(path, e.info)
	return true
}

func (r *remover) removed(path string, info EntryInfo) {
	r.removedAny = true
	if info.Type != TypeDir && r.onRemoved != nil {
		r.onRemoved(path, info)
	}
}

// enter は、フォルダ path に §13.1 の方法で入る。確かめられなければ記録して nil を返す（I4）。
func (r *remover) enter(parent *secDir, path, name string, want fileID) *secDir {
	r.hooks.enterDir(path)
	d, err := openSecDir(parent, path, name, want)
	if err != nil {
		oe, ok := err.(*OpError)
		if !ok {
			oe = &OpError{Op: "open", Path: path, Kind: classify(err, classifyOpts{}), Err: err}
		}
		r.fail(path, oe)
		return nil
	}
	return d
}

// deleteContents は、開いたフォルダ d の中身を後順で削除する（§13.2）。すべて削除できたら真。
func (r *remover) deleteContents(d *secDir) bool {
	entries, err := d.list()
	if err != nil {
		r.fail(d.path, &OpError{Op: "remove", Path: d.path, Kind: classify(err, classifyOpts{}), Err: err})
		return false
	}
	all := true
	for _, e := range entries {
		if r.checkCanceled() {
			return false
		}
		if e.info.Type == TypeDir {
			cd := r.enter(d, filepath.Join(d.path, e.name), e.name, e.id)
			if cd == nil {
				all = false
				continue
			}
			ok := r.deleteContents(cd)
			cd.close() // フォルダ自体を削除する直前に閉じる（§13.1）
			if r.canceled {
				return false
			}
			if !ok {
				all = false // 中身の失敗は報告済み。フォルダは空でないので削除しない
				continue
			}
		}
		if !r.removeIn(d, e) {
			if r.canceled {
				return false
			}
			all = false
		}
	}
	return all
}

// deleteItem は完全削除（§13.2）の 1 項目を処理する。
func deleteItem(ctx context.Context, h *testHooks, it Item, onRemoved func(string, EntryInfo)) ItemResult {
	res := ItemResult{Src: it.Src}
	r := &remover{ctx: ctx, hooks: h, onRemoved: onRemoved}
	e, err := statTop(it.Src)
	if err != nil {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "remove", Path: it.Src, Kind: classify(err, classifyOpts{}), Err: err}
		return res
	}
	if e.info.Type != it.Info.Type {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "remove", Path: it.Src, Kind: KindSourceChanged}
		return res
	}
	if e.info.Type == TypeDir {
		if d := r.enter(nil, it.Src, filepath.Base(it.Src), e.id); d != nil {
			ok := r.deleteContents(d)
			d.close()
			if ok {
				r.removeTopEntry(it.Src, e)
			}
		}
	} else {
		r.removeTopEntry(it.Src, e)
	}
	res.Details = r.details
	switch {
	case r.canceled && r.removedAny:
		res.Outcome, res.Err = OutcomePartial, &OpError{Op: "remove", Path: it.Src, Kind: KindCanceled, Err: ctx.Err()}
	case r.canceled:
		res.Outcome, res.Err = OutcomeSkipped, &OpError{Op: "remove", Path: it.Src, Kind: KindCanceled, Err: ctx.Err()}
	case r.firstErr == nil:
		res.Outcome = OutcomeDone
	case r.removedAny:
		res.Outcome, res.Err = OutcomePartial, r.firstErr
	default:
		// 何も削除できなかった。トップレベルの項目自体の失敗なら Details に重ねて入れない。
		res.Outcome, res.Err = OutcomeFailed, r.firstErr
		if len(r.details) == 1 && r.details[0].Src == it.Src {
			res.Details = nil
		}
	}
	return res
}

// recordEntry は、§11.2 でコピーしたと記録したエントリ（§13.3 の照合に使う）。
type recordEntry struct {
	name     string        // 親フォルダの中の名前
	info     EntryInfo     // 記録した時点の種類・サイズ・更新日時
	id       fileID        // 記録した時点の fileID
	children []recordEntry // フォルダのとき、コピーした中身（名前順）
}

// matches は、今のエントリ now が記録 rec と一致するかを返す（§13.3）。
// ファイルとリンクでは fileID・種類・サイズ・更新日時、フォルダでは fileID と種類だけを比べる（フォルダの更新日時は中身を消すと変わるため）。
func (rec recordEntry) matches(now dirEntry) bool {
	if now.id != rec.id || now.info.Type != rec.info.Type {
		return false
	}
	return rec.info.Type == TypeDir || now.info.Size == rec.info.Size && now.info.ModTime.Equal(rec.info.ModTime)
}

// removeOutcome は removeRecorded の結果。
type removeOutcome struct {
	details    []EntryResult // 削除しなかったエントリ（OutcomeCopiedSourceKept）
	canceled   bool
	firstErr   *OpError
	removedTop bool // トップレベルの項目まで削除できた
}

// removeRecorded は、path にある項目のうち、記録 rec と照合して一致するエントリだけを削除する（§13.3、I2）。
// 一致しないもの（コピー後に変更・置き換えられたもの）は削除せず Details に KindSourceChanged で報告する。
// フォルダは中身の後に削除し、空でなければ（コピー中に追加されたファイルなど）残す。
// Windows では、照合で一致したエントリの読み取り専用属性を外してから削除する（macOS のロックは外さない）。
// onRemoved は、フォルダ以外のエントリを削除するたびに呼ばれる（進捗用。nil 可）。
func removeRecorded(ctx context.Context, path string, rec recordEntry, h *testHooks, onRemoved func(EntryInfo)) removeOutcome {
	r := &remover{ctx: ctx, hooks: h, recorded: true}
	if onRemoved != nil {
		r.onRemoved = func(_ string, info EntryInfo) { onRemoved(info) }
	}
	out := removeOutcome{}
	now, err := statTop(path)
	switch {
	case err != nil && classify(err, classifyOpts{}) == KindNotFound:
		// 計画後に消えたものは何もしない
	case err != nil:
		r.fail(path, &OpError{Op: "remove", Path: path, Kind: classify(err, classifyOpts{}), Err: err})
	case !rec.matches(now):
		r.fail(path, &OpError{Op: "remove", Path: path, Kind: KindSourceChanged})
	case rec.info.Type == TypeDir:
		if d := r.enter(nil, path, filepath.Base(path), rec.id); d != nil {
			ok := r.removeRecordedContents(d, rec.children)
			d.close()
			if ok && !r.canceled {
				out.removedTop = r.removeTopEntry(path, now) // 空でなければ KindNotEmpty で残る
			}
		}
	default:
		out.removedTop = r.removeTopEntry(path, now)
	}
	out.details, out.canceled, out.firstErr = r.details, r.canceled, r.firstErr
	return out
}

// removeRecordedContents は、開いたフォルダ d の中の、記録 recs のエントリだけを削除する。記録したものをすべて削除できたら真。
func (r *remover) removeRecordedContents(d *secDir, recs []recordEntry) bool {
	all := true
	for _, rec := range recs {
		if r.checkCanceled() {
			return false
		}
		path := filepath.Join(d.path, rec.name)
		now, err := d.stat(rec.name)
		if err != nil {
			if classify(err, classifyOpts{}) == KindNotFound {
				continue // 記録の後に消えたものは何もしない
			}
			r.fail(path, &OpError{Op: "remove", Path: path, Kind: classify(err, classifyOpts{}), Err: err})
			all = false
			continue
		}
		now.name = rec.name
		if !rec.matches(now) {
			r.fail(path, &OpError{Op: "remove", Path: path, Kind: KindSourceChanged})
			all = false
			continue
		}
		if rec.info.Type == TypeDir {
			cd := r.enter(d, path, rec.name, rec.id)
			if cd == nil {
				all = false
				continue
			}
			ok := r.removeRecordedContents(cd, rec.children)
			cd.close()
			if r.canceled {
				return false
			}
			if !ok {
				all = false
				continue
			}
		}
		if !r.removeIn(d, now) {
			if r.canceled {
				return false
			}
			all = false
		}
	}
	return all
}

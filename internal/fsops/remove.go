package fsops

import (
	"context"
	"path/filepath"
	"slices"
)

// remover は、完全削除（§13.2）と記録した項目だけの削除（§13.3）の作業状態。
type remover struct {
	ctx      context.Context
	hooks    *testHooks
	locks    *lockRetrier // 使用中の一時的な失敗のやり直し（§17.1）
	recorded bool         // §13.3（移動元の削除）なら真
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

// removeErr は、削除の失敗を分類する（§13.2、§17）。失敗したら調べ直し、
// エントリがもうなければ vanished を真にする（列挙・確認の後に消えた。消すものがないので失敗にしない）。
// 種類に合わない方法での削除の失敗で、種類が変わっていれば KindSourceChanged にする。
// restat は調べ直す関数、sys は読み取り専用の判定に使うパス。
func removeErr(path string, err error, e dirEntry, restat func() (dirEntry, error), sys string) (oe *OpError, vanished bool) {
	now, serr := restat()
	if serr != nil && classify(serr, classifyOpts{}) == KindNotFound {
		return nil, true
	}
	if isMismatchRemoveErr(err) && serr == nil && now.dirAttr != e.dirAttr {
		return &OpError{Op: "remove", Path: path, Kind: KindSourceChanged, Err: err}, false
	}
	return &OpError{Op: "remove", Path: path, Kind: classify(err, classifyOpts{readOnly: readOnlySys(sys)}), Err: withUserPaths(err, path, "")}, false
}

// removeIn は、確かめて開いたフォルダ d の中のエントリ e を削除する。削除できたら真。
func (r *remover) removeIn(d *secDir, e dirEntry) bool {
	path := filepath.Join(d.path, e.name)
	r.hooks.remove(path)
	if r.checkCanceled() {
		return false
	}
	return r.removeOnce(path, e, func() error { return d.remove(e, r.recorded) }, func() (dirEntry, error) { return d.stat(e.name) })
}

// removeTopEntry は、トップレベルのエントリ path を削除する。削除できたら真。
func (r *remover) removeTopEntry(path string, e dirEntry) bool {
	r.hooks.remove(path)
	if r.checkCanceled() {
		return false
	}
	return r.removeOnce(path, e, func() error { return removeTop(path, e, r.recorded) }, func() (dirEntry, error) { return statTop(path) })
}

// removeOnce は、エントリ e（path）を remove で削除する。削除できた（または既になかった）なら真。
// 使用中で失敗したら、§13.2 の確認（Windows では開いたハンドルの fileID とリパースの確認）を含めてやり直す（§17.1）。
func (r *remover) removeOnce(path string, e dirEntry, remove func() error, restat func() (dirEntry, error)) bool {
	gone, oe := removeWithRetry(r.locks, path, e, remove, restat)
	switch {
	case oe != nil && oe.Kind == KindCanceled && r.checkCanceled():
		return false
	case oe != nil:
		r.fail(path, oe)
		return false
	case !gone:
		r.removed(path, e.info)
	}
	return true
}

// removeWithRetry は、エントリ e（path）を remove で削除し、失敗を removeErr で分類する。使用中の間はやり直す（§17.1）。
// 削除の前にエントリが消えていた（列挙・確認の後に消えた）なら vanished が真。待っている間にキャンセルされたら KindCanceled。
func removeWithRetry(lr *lockRetrier, path string, e dirEntry, remove func() error, restat func() (dirEntry, error)) (vanished bool, oe *OpError) {
	oe = lr.op(path, func() *OpError {
		err := lr.hooks.lockFaultErr("remove", path)
		if err == nil {
			err = remove()
		}
		if err == nil {
			return nil
		}
		sys, _ := sysPath(path)
		var oe *OpError
		oe, vanished = removeErr(path, err, e, restat, sys)
		return oe
	})
	return vanished, oe
}

func (r *remover) removed(path string, info EntryInfo) {
	r.removedAny = true
	if info.Type != TypeDir && r.onRemoved != nil {
		r.onRemoved(path, info)
	}
}

// enter は、フォルダ path に §13.1 の方法で入る。確かめられなければ記録して nil を返す（I4）。
// 列挙の後に消えていた場合は、記録せずに nil と gone=true を返す（消すものがない）。
func (r *remover) enter(parent *secDir, path, name string, want fileID) (d *secDir, gone bool) {
	r.hooks.enterDir(path)
	d, err := openSecDir(parent, path, name, want)
	if err != nil {
		if KindOf(err) == KindNotFound {
			if _, serr := statTop(path); serr != nil && classify(serr, classifyOpts{}) == KindNotFound {
				return nil, true // 列挙の後に消えた。消すものがない
			}
		}
		oe, ok := err.(*OpError)
		if !ok {
			oe = &OpError{Op: "open", Path: path, Kind: classify(err, classifyOpts{}), Err: err}
		}
		r.fail(path, oe)
		return nil, false
	}
	return d, false
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
		if e.info.Type == TypeDir && onOtherVolume(e.id, d.id) {
			// マウントポイント。中は別のボリュームのもので、削除を指示されたフォルダの一部ではないので入らない（§13.1）。
			path := filepath.Join(d.path, e.name)
			r.fail(path, &OpError{Op: "remove", Path: path, Kind: KindMountPoint})
			all = false
			continue
		}
		if e.info.Type == TypeDir {
			cd, gone := r.enter(d, filepath.Join(d.path, e.name), e.name, e.id)
			if gone {
				continue
			}
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
func deleteItem(lr *lockRetrier, it Item, onRemoved func(string, EntryInfo)) ItemResult {
	ctx := lr.ctx
	res := ItemResult{Src: it.Src}
	r := &remover{ctx: ctx, hooks: lr.hooks, locks: lr, onRemoved: onRemoved}
	e, err := statTop(it.Src)
	if err != nil {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "remove", Path: it.Src, Kind: classify(err, classifyOpts{}), Err: err}
		return res
	}
	if e.info.Type != it.Info.Type {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "remove", Path: it.Src, Kind: KindSourceChanged}
		return res
	}
	if mountPoint(it.Src, e) {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "remove", Path: it.Src, Kind: KindMountPoint} // 計画の後にマウントされた（§13.1）
		return res
	}
	// 確かめた後にトップレベルの項目が消えていた（別の場所へ移されたなど）ら、何も削除していないので失敗にする（§7.3）。
	gone := func() { r.fail(it.Src, &OpError{Op: "remove", Path: it.Src, Kind: KindNotFound}) }
	if e.info.Type == TypeDir {
		d, vanished := r.enter(nil, it.Src, filepath.Base(it.Src), e.id)
		switch {
		case vanished:
			gone()
		case d != nil:
			ok := r.deleteContents(d)
			d.close()
			if ok {
				r.removeTopEntry(it.Src, e)
			}
		}
	} else if r.removeTopEntry(it.Src, e) && !r.removedAny {
		gone() // 削除する前に消えていた
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
	// skipped は、フォルダのとき、衝突の決定による Skip でコピーしなかった中身の名前（§11.2 の手順 2）。
	// 移動元に残るので、フォルダがそれだけを残して空にならなくてもエラーにしない（§7.4）。
	skipped []string
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
	keptBySkip bool // トップレベルのフォルダを、衝突の決定による Skip で残したものだけのために残した（エラーではない）
}

// onlyNames は、開いたフォルダ d に残っているのが keep の名前だけか（1 件以上）を返す。
// 衝突の決定による Skip で移動元に残したものだけでフォルダが空にならない場合を、エラーと区別するために使う（§7.4、§11.2）。
// 名前は、同じフォルダの列挙で得たもの同士を比べる。
func onlyNames(d *secDir, keep []string) bool {
	if len(keep) == 0 {
		return false
	}
	entries, err := d.list()
	if err != nil || len(entries) == 0 {
		return false
	}
	for _, e := range entries {
		if !slices.Contains(keep, e.name) {
			return false
		}
	}
	return true
}

// removeRecorded は、path にある項目のうち、記録 rec と照合して一致するエントリだけを削除する（§13.3、I2）。
// 一致しないもの（コピー後に変更・置き換えられたもの）は削除せず Details に KindSourceChanged で報告する。
// フォルダは中身の後に削除し、空でなければ（コピー中に追加されたファイルなど）残す。
// Windows では、照合で一致したエントリの読み取り専用属性を外してから削除する（macOS のロックは外さない）。
// onRemoved は、フォルダ以外のエントリを削除するたびに呼ばれる（進捗用。nil 可）。
func removeRecorded(lr *lockRetrier, path string, rec recordEntry, onRemoved func(EntryInfo)) removeOutcome {
	r := &remover{ctx: lr.ctx, hooks: lr.hooks, locks: lr, recorded: true}
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
	case mountPoint(path, now):
		r.fail(path, &OpError{Op: "remove", Path: path, Kind: KindMountPoint}) // §13.1
	case rec.info.Type == TypeDir:
		if d, _ := r.enter(nil, path, filepath.Base(path), rec.id); d != nil {
			ok, kept := r.removeRecordedContents(d, rec.children, rec.skipped)
			if ok && !r.canceled && onlyNames(d, append(kept, rec.skipped...)) {
				out.keptBySkip = true
				ok = false
			}
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

// reportAdded は、開いたフォルダ d の中の、記録 recs にも衝突の決定による Skip（skipped）にもないエントリを、
// コピーの後に追加されたもの（移動先にはない）として Details に KindSourceChanged で報告する（§11.2 の手順 4、§13.3）。
// 報告したものがあれば真。列挙できなければ何もしない（フォルダの削除の失敗として報告される）。
func (r *remover) reportAdded(d *secDir, recs []recordEntry, skipped []string) bool {
	entries, err := d.list()
	if err != nil {
		return false
	}
	added := false
	for _, e := range entries {
		if slices.ContainsFunc(recs, func(rec recordEntry) bool { return rec.name == e.name }) || slices.Contains(skipped, e.name) {
			continue
		}
		path := filepath.Join(d.path, e.name)
		r.fail(path, &OpError{Op: "remove", Path: path, Kind: KindSourceChanged})
		added = true
	}
	return added
}

// removeRecordedContents は、開いたフォルダ d の中の、記録 recs のエントリだけを削除する。
// 記録したものをすべて削除できた（衝突の決定による Skip で残したものだけのために残したフォルダを除く）なら all が真。
// kept は、衝突の決定による Skip で残したものだけのために残したフォルダの名前。
// skipped は、このフォルダの中の、衝突の決定による Skip でコピーしなかったエントリの名前（記録にないが、報告しない）。
func (r *remover) removeRecordedContents(d *secDir, recs []recordEntry, skipped []string) (all bool, kept []string) {
	all = true
	defer func() {
		// 記録の後に追加されたエントリは消さずに残るので、1 件ずつ報告する（§11.2 の手順 4）。そのフォルダは空にならない。
		if !r.canceled && r.reportAdded(d, recs, skipped) {
			all = false
		}
	}()
	for _, rec := range recs {
		if r.checkCanceled() {
			return false, kept
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
		if rec.info.Type == TypeDir && onOtherVolume(now.id, d.id) {
			r.fail(path, &OpError{Op: "remove", Path: path, Kind: KindMountPoint}) // マウントポイントには入らない（§13.1）
			all = false
			continue
		}
		if rec.info.Type == TypeDir {
			cd, gone := r.enter(d, path, rec.name, rec.id)
			if gone {
				continue
			}
			if cd == nil {
				all = false
				continue
			}
			ok, childKept := r.removeRecordedContents(cd, rec.children, rec.skipped)
			if ok && !r.canceled && onlyNames(cd, append(childKept, rec.skipped...)) {
				cd.close()
				kept = append(kept, rec.name)
				continue
			}
			cd.close()
			if r.canceled {
				return false, kept
			}
			if !ok {
				all = false
				continue
			}
		}
		if !r.removeIn(d, now) {
			if r.canceled {
				return false, kept
			}
			all = false
		}
	}
	return all, kept
}

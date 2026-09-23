package fsops

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// copyBufSize は、ファイルのコピーのバッファの大きさ（§10.1）。バッファごとに ctx を確かめる（§16）。
const copyBufSize = 1 << 20

// autoRenameLimit は、自動リネームで試す候補の数の上限（§9.2）。
const autoRenameLimit = 9999

// planned は、計画時に検出した衝突と、その時点の上書き先・マージ先の fileID（§7.3 の照合に使う）。
type planned struct {
	c     Conflict
	dstID fileID
}

// conflictIndex は、Execute の開始時に固定した衝突を、処理中のエントリから引くための索引。
// パスの文字列ではなく、項目の添字と、親の衝突・名前で引く（自動リネームなどで実際のパスが計画と変わっても取り違えないため）。
type conflictIndex struct {
	top   map[int]*planned                   // トップレベルの項目の添字 → 衝突
	inner map[ConflictID]map[string]*planned // 親の衝突（マージするフォルダ）→ 中の名前 → 衝突
}

func newConflictIndex(conflicts []Conflict, dstIDs []fileID) conflictIndex {
	idx := conflictIndex{top: map[int]*planned{}, inner: map[ConflictID]map[string]*planned{}}
	for i, c := range conflicts {
		p := &planned{c: c, dstID: dstIDs[i]}
		if c.Parent == 0 {
			idx.top[c.Item] = p
			continue
		}
		m := idx.inner[c.Parent]
		if m == nil {
			m = map[string]*planned{}
			idx.inner[c.Parent] = m
		}
		m[filepath.Base(c.Dst)] = p
	}
	return idx
}

// copier は、トップレベルの 1 項目のコピー（§10）の作業状態。
type copier struct {
	ex       *executor
	details  []EntryResult
	firstErr *OpError
	warnings []*OpError
	canceled bool
	wrote    bool // コピー先に何かを残した（最終名にしたファイル、作ったフォルダ）
	// syncDirs は、SyncAlways のとき、最終名へのリネームやフォルダの作成を行ったフォルダ（§10.5）。
	// 同じフォルダを何度も同期しないためだけに使い、同一性の判定には使わない。
	syncDirs map[string]bool
	dirOrder []string
}

// entry は、フォルダの中のエントリの結果を記録する。Done 以外だけを Details に入れる（§7.4）。
func (cp *copier) entry(src, dst string, out Outcome, err *OpError) {
	if out == OutcomeDone || cp.canceled && err != nil && err.Kind == KindCanceled {
		return // キャンセルで打ち切ったエントリは Details に入れない（項目全体を KindCanceled で報告する）
	}
	cp.details = append(cp.details, EntryResult{Src: src, Dst: dst, Outcome: out, Err: err})
	if err != nil && cp.firstErr == nil {
		cp.firstErr = err
	}
}

// checkCanceled は、ctx がキャンセルされていれば canceled にして真を返す。
func (cp *copier) checkCanceled() bool {
	if cp.ex.ctx.Err() != nil {
		cp.canceled = true
	}
	return cp.canceled
}

func (cp *copier) canceledErr(path string) *OpError {
	return &OpError{Op: "copy", Path: path, Kind: KindCanceled, Err: cp.ex.ctx.Err()}
}

// changed は、フォルダ dir の中の名前を変えた（最終名へのリネーム、フォルダの作成）ことを記録する（§10.5 の同期用）。
func (cp *copier) changed(dir string) {
	cp.wrote = true
	if cp.ex.opt.Sync != SyncAlways {
		return
	}
	if cp.syncDirs == nil {
		cp.syncDirs = map[string]bool{}
	}
	if !cp.syncDirs[dir] {
		cp.syncDirs[dir] = true
		cp.dirOrder = append(cp.dirOrder, dir)
	}
}

// syncChanged は、名前を変えたフォルダを同期する（§10.5）。データは最終名で書き終えているので、失敗は Warnings にする。
func (cp *copier) syncChanged() {
	for _, dir := range cp.dirOrder {
		s, err := sysPath(dir)
		if err == nil {
			err = syncDirSys(s)
		}
		if err != nil {
			cp.warnings = append(cp.warnings, &OpError{Op: "sync", Path: dir, Kind: classify(err, classifyOpts{}), Err: withUserPaths(err, dir, "")})
		}
	}
}

// copyItem は、コピー（MethodCopy）のトップレベルの 1 項目を処理する（§10）。i は Items() の添字。
func (ex *executor) copyItem(i int, it Item) ItemResult {
	res := ItemResult{Src: it.Src, Dst: it.Dst}
	e, err := statTop(it.Src)
	if err != nil {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "copy", Path: it.Src, Kind: classify(err, classifyOpts{}), Err: err}
		return res
	}
	if e.info.Type != it.Info.Type {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "copy", Path: it.Src, Kind: KindSourceChanged}
		return res
	}
	cp := &copier{ex: ex}
	dst, out, oe := cp.copyEntry(it.Src, it.Dst, e, ex.conflictIdx.top[i])
	cp.syncChanged()
	res.Dst, res.Details, res.Warnings = dst, cp.details, cp.warnings
	switch {
	case cp.canceled && cp.wrote:
		res.Outcome, res.Err = OutcomePartial, cp.canceledErr(it.Src)
	case cp.canceled:
		res.Outcome, res.Err = OutcomeSkipped, cp.canceledErr(it.Src)
	case out != OutcomeDone:
		res.Outcome, res.Err = out, oe
	case cp.firstErr != nil:
		res.Outcome, res.Err = OutcomePartial, cp.firstErr
	default:
		res.Outcome = OutcomeDone
	}
	return res
}

// copyEntry は、エントリ e（src）を dst にコピーする。pc は計画時に検出した衝突（なければ nil）。
// 実際のコピー先のパスと、エントリ自体の結果を返す。フォルダの中のエントリの結果は Details に記録する。
func (cp *copier) copyEntry(src, dst string, e dirEntry, pc *planned) (string, Outcome, *OpError) {
	if pc != nil && e.info.Type != pc.c.SrcInfo.Type {
		// 決定は計画時の種類に対して行われたものなので、そのまま適用しない。
		return dst, OutcomeFailed, &OpError{Op: "copy", Path: src, Dest: dst, Kind: KindSourceChanged}
	}
	if pc != nil && (pc.c.Decision == DecisionUnset || pc.c.Decision == DecisionSkip) {
		return dst, OutcomeSkipped, nil // 衝突の決定による Skip（I1）
	}
	switch e.info.Type {
	case TypeFile:
		return cp.copyFile(src, dst, e, pc)
	case TypeDir:
		return cp.copyDir(src, dst, e, pc)
	}
	// リンクと特殊なファイル（§14.2）はフェーズ8で作る。
	return dst, OutcomeFailed, &OpError{Op: "copy", Path: src, Kind: KindUnknown, Err: errors.ErrUnsupported}
}

// checkTarget は、上書き先・マージ先 dst が計画時のものから変わっていないかを調べる（§7.3）。
// 上書きでは fileID・種類・サイズ・更新日時を、マージでは fileID と種類（TypeDir）を比べる。
// 消えていれば gone を真にする（衝突なしとして書く）。変わっていれば KindExist（計画後に現れた衝突。I1）を返す。
func checkTarget(src, dst string, pc *planned) (gone bool, oe *OpError) {
	now, err := statTop(dst)
	if err != nil {
		if classify(err, classifyOpts{}) == KindNotFound {
			return true, nil
		}
		return false, &OpError{Op: "copy", Path: src, Dest: dst, Kind: classify(err, classifyOpts{}), Err: err}
	}
	want := pc.c.DstInfo
	same := pc.dstID.method != idMethodNone && now.id == pc.dstID && now.info.Type == want.Type
	if same && want.Type != TypeDir {
		same = now.info.Size == want.Size && now.info.ModTime.Equal(want.ModTime)
	}
	if !same {
		return false, &OpError{Op: "copy", Path: src, Dest: dst, Kind: KindExist}
	}
	return false, nil
}

// copyFile はファイルをコピーする（§10.1）。
func (cp *copier) copyFile(src, dst string, e dirEntry, pc *planned) (string, Outcome, *OpError) {
	decision := DecisionUnset
	if pc != nil {
		decision = pc.c.Decision
	}
	if decision == DecisionOverwrite {
		// 書き込む前にも確かめる（読み取り専用・変わった上書き先に、無駄なコピーをしないため）。
		if _, out, oe := checkOverwrite(src, dst, pc); oe != nil {
			return dst, out, oe
		}
	}
	tmp, oe := cp.writeTemp(src, dst, e)
	if oe != nil {
		if oe.Kind == KindCanceled {
			return dst, OutcomeSkipped, oe
		}
		return dst, OutcomeFailed, oe
	}
	final, out, oe := cp.finalize(src, tmp, dst, decision, pc)
	if out != OutcomeDone {
		removeTemp(tmp)
		return final, out, oe
	}
	cp.changed(filepath.Dir(final))
	cp.ex.progress.fileDone(src)
	return final, OutcomeDone, nil
}

// checkOverwrite は、上書きの直前の確認（§7.3、§9.3）。上書きしてよければ oe が nil。
// 上書き先が消えていれば gone を真にする（衝突なしとして排他リネームで書く）。
// 上書き先が読み取り専用なら KindReadOnly（Unix の rename はファイル自身の権限を見ないため、両 OS で結果をそろえるために先に調べる）。
func checkOverwrite(src, dst string, pc *planned) (gone bool, out Outcome, oe *OpError) {
	gone, oe = checkTarget(src, dst, pc)
	switch {
	case oe != nil && oe.Kind == KindExist:
		return false, OutcomeSkipped, oe
	case oe != nil:
		return false, OutcomeFailed, oe
	case gone:
		return true, OutcomeDone, nil
	}
	d, err := sysPath(dst)
	if err != nil {
		return false, OutcomeFailed, &OpError{Op: "copy", Path: src, Dest: dst, Kind: KindInvalidRequest, Err: err}
	}
	if targetReadOnlySys(d) {
		return false, OutcomeFailed, &OpError{Op: "copy", Path: src, Dest: dst, Kind: KindReadOnly}
	}
	return false, OutcomeDone, nil
}

// finalize は、書き終えた一時ファイル tmp を最終名にする（§10.1 の手順 7）。
// 衝突なしは排他リネーム、上書きは置換リネーム、自動リネームは §9.2 の候補への排他リネーム。
// 失敗した場合、一時ファイルの削除は呼び出し側が行う。
func (cp *copier) finalize(src, tmp, dst string, decision Decision, pc *planned) (string, Outcome, *OpError) {
	if cp.checkCanceled() {
		return dst, OutcomeSkipped, cp.canceledErr(src)
	}
	switch decision {
	case DecisionOverwrite:
		cp.ex.opt.hooks.finalRename(dst)
		gone, out, oe := checkOverwrite(src, dst, pc)
		switch {
		case oe != nil:
			return dst, out, oe
		case gone:
			out, oe = createResult(src, dst, renameExclusive(tmp, dst))
		default:
			out, oe = replaceResult(src, dst, renameReplace(tmp, dst))
		}
		return dst, out, oe
	case DecisionAutoRename:
		return autoRename(src, dst, false, func(cand string) error {
			cp.ex.opt.hooks.finalRename(cand)
			return renameExclusive(tmp, cand)
		})
	}
	cp.ex.opt.hooks.finalRename(dst)
	out, oe := createResult(src, dst, renameExclusive(tmp, dst))
	return dst, out, oe
}

// createResult は、dst を排他的に作る操作（一時ファイルからの排他リネーム、フォルダの作成）の結果を分類する。
// 「存在する」は計画後に現れた衝突なので Skipped（KindExist。I1、§7.3）。
func createResult(src, dst string, err error) (Outcome, *OpError) {
	if err == nil {
		return OutcomeDone, nil
	}
	d, _ := sysPath(filepath.Dir(dst))
	k := classify(err, classifyOpts{readOnly: readOnlySys(d)})
	oe := &OpError{Op: "copy", Path: src, Dest: dst, Kind: k, Err: err}
	if k == KindExist {
		return OutcomeSkipped, oe
	}
	return OutcomeFailed, oe
}

// replaceResult は、置換リネーム（上書き）の結果を分類する。
// 上書き先が他のプロセスに使用されていて置き換えられない場合は KindLocked（§9.3）。
func replaceResult(src, dst string, err error) (Outcome, *OpError) {
	if err == nil {
		return OutcomeDone, nil
	}
	d, _ := sysPath(dst)
	k := classify(err, classifyOpts{readOnly: readOnlySys(d)})
	if k == KindPermission && inUseSys(d) {
		k = KindLocked
	}
	return OutcomeFailed, &OpError{Op: "copy", Path: src, Dest: dst, Kind: k, Err: err}
}

// autoRename は、dst の §9.2 の候補の名前を順に try に渡し、「存在する」で失敗したら次の番号を試す。
// 使えた名前のパスを返す。上限まで見つからなければ KindExist。
func autoRename(src, dst string, isDir bool, try func(cand string) error) (string, Outcome, *OpError) {
	dir, name := filepath.Dir(dst), filepath.Base(dst)
	for n := 2; n < 2+autoRenameLimit; n++ {
		cand := filepath.Join(dir, autoRenameName(name, isDir, n))
		out, oe := createResult(src, cand, try(cand))
		if oe == nil || oe.Kind != KindExist {
			return cand, out, oe
		}
	}
	return dst, OutcomeFailed, &OpError{Op: "copy", Path: src, Dest: dst, Kind: KindExist}
}

// autoRenameName は、name の n 番目の自動リネームの候補を返す（§9.2）。
// 拡張子は最後の "." 以降。先頭の "." だけの名前（.gitignore）、拡張子のない名前、フォルダは末尾に付ける。
// 元の名前の "(2)" などは解釈しない。名前を切り詰めない（長すぎれば作成が KindInvalidName で失敗する）。
func autoRenameName(name string, isDir bool, n int) string {
	suffix := " (" + strconv.Itoa(n) + ")"
	if i := strings.LastIndexByte(name, '.'); !isDir && i > 0 {
		return name[:i] + suffix + name[i:]
	}
	return name + suffix
}

// writeTemp は、src の内容をコピー先のフォルダの一時ファイルに書き込み、閉じる（§10.1 の手順 1〜5）。
// 書き終えた一時ファイルのパスを返す。失敗・キャンセルしたら一時ファイルを削除する（I3）。
func (cp *copier) writeTemp(src, dst string, e dirEntry) (string, *OpError) {
	// エラーのパスは、一時ファイルではなくコピー元とコピー先で返す（一時ファイルのパスは Err の中にだけ現れる）。
	fail := func(err error, o classifyOpts) *OpError {
		return &OpError{Op: "copy", Path: src, Dest: dst, Kind: classify(err, o), Err: err}
	}
	s, err := sysPath(src)
	if err != nil {
		return "", fail(err, classifyOpts{})
	}
	in, err := openSourceSys(s, e.id)
	if err != nil {
		if oe, ok := err.(*OpError); ok {
			return "", &OpError{Op: "copy", Path: src, Dest: dst, Kind: oe.Kind, Err: oe.Err}
		}
		return "", fail(withUserPaths(err, src, ""), classifyOpts{noFollow: true}) // ELOOP はリンクに置き換えられていたことを示す
	}
	defer in.Close()

	tmp, out, err := createTemp(filepath.Dir(dst))
	if err != nil {
		return "", fail(err, classifyOpts{})
	}
	done := false
	defer func() {
		if !done {
			out.Close()
			removeTemp(tmp)
		}
	}()

	cp.ex.progress.step(src)
	buf := make([]byte, copyBufSize)
	var written int64
	for {
		if cp.checkCanceled() {
			return "", cp.canceledErr(src)
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return "", fail(withUserPaths(err, tmp, ""), classifyOpts{})
			}
			written += int64(n)
			cp.ex.progress.addBytes(int64(n))
			if err := cp.ex.opt.hooks.write(dst, written); err != nil {
				return "", fail(err, classifyOpts{})
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", fail(withUserPaths(rerr, src, ""), classifyOpts{})
		}
	}
	if cp.ex.opt.Sync == SyncAlways {
		if err := out.Sync(); err != nil {
			return "", fail(withUserPaths(err, tmp, ""), classifyOpts{})
		}
	}
	done = true
	if err := out.Close(); err != nil {
		removeTemp(tmp)
		return "", fail(withUserPaths(err, tmp, ""), classifyOpts{})
	}
	return tmp, nil
}

// createTemp は、フォルダ dir に一時ファイル（§10.1 の手順 2）を O_CREATE|O_EXCL|O_WRONLY、0o600 で作る。
// 名前が既に使われていれば、別の乱数で作り直す。返すパスは \\?\ の付かない形。
func createTemp(dir string) (string, *os.File, error) {
	var lastErr error
	for range 16 {
		name, err := tempName()
		if err != nil {
			return "", nil, err
		}
		tmp := filepath.Join(dir, name)
		s, err := sysPath(tmp)
		if err != nil {
			return "", nil, err
		}
		f, err := os.OpenFile(s, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return tmp, f, nil
		}
		err = withUserPaths(err, tmp, "")
		if classify(err, classifyOpts{}) != KindExist {
			return "", nil, err
		}
		lastErr = err
	}
	return "", nil, lastErr
}

// removeTemp は、fsops が作った一時ファイルを削除する（§10.1 の手順 8）。
// 一時ファイルは fsops が作ったものなので、削除できなければ読み取り専用属性を外してから削除し直す（§13.2 の規則は適用しない）。
func removeTemp(tmp string) {
	s, err := sysPath(tmp)
	if err != nil {
		return
	}
	if os.Remove(s) != nil && clearReadOnlySys(s) {
		os.Remove(s)
	}
}

// copyDir はフォルダをコピーする（§10.2）。中身の結果は Details に記録する。
// 実際のコピー先のパスと、フォルダ自体の結果（作成・マージできなかった場合は Skipped・Failed）を返す。
func (cp *copier) copyDir(src, dst string, e dirEntry, pc *planned) (string, Outcome, *OpError) {
	mkdir := func(p string) error {
		s, err := sysPath(p)
		if err != nil {
			return err
		}
		return withUserPaths(os.Mkdir(s, 0o700), p, "")
	}
	var inner map[string]*planned // マージする場合だけ、内側の衝突を使う
	switch {
	case pc != nil && pc.c.Decision == DecisionMerge:
		gone, oe := checkTarget(src, dst, pc)
		switch {
		case oe != nil && oe.Kind == KindExist:
			return dst, OutcomeSkipped, oe
		case oe != nil:
			return dst, OutcomeFailed, oe
		case gone:
			if out, oe := createResult(src, dst, mkdir(dst)); oe != nil {
				return dst, out, oe
			}
			cp.changed(filepath.Dir(dst))
		default:
			inner = cp.ex.conflictIdx.inner[pc.c.ID]
		}
	case pc != nil && pc.c.Decision == DecisionAutoRename:
		final, out, oe := autoRename(src, dst, true, mkdir)
		if oe != nil {
			return final, out, oe
		}
		dst = final
		cp.changed(filepath.Dir(dst))
	default:
		if out, oe := createResult(src, dst, mkdir(dst)); oe != nil {
			return dst, out, oe
		}
		cp.changed(filepath.Dir(dst))
	}

	entries, err := readDir(src)
	if err != nil {
		cp.entry(src, dst, OutcomeFailed, &OpError{Op: "copy", Path: src, Dest: dst, Kind: classify(err, classifyOpts{}), Err: err})
		return dst, OutcomeDone, nil
	}
	for _, ce := range entries {
		if cp.checkCanceled() {
			return dst, OutcomeDone, nil
		}
		childSrc, childDst := filepath.Join(src, ce.name), filepath.Join(dst, ce.name)
		final, out, oe := cp.copyEntry(childSrc, childDst, ce, inner[ce.name])
		cp.entry(childSrc, final, out, oe)
		if cp.canceled {
			return dst, OutcomeDone, nil
		}
	}
	return dst, OutcomeDone, nil
}

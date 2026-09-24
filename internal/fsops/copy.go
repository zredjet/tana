package fsops

import (
	"bytes"
	"crypto/sha256"
	"hash"
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
	noSpace  *OpError // 書き込み中の容量不足（§10.3）。起きたら、この項目の残りを処理しない
	wrote    bool     // コピー先に何かを残した（最終名にしたファイル、作ったフォルダ）
	// move は、ボリュームをまたぐ移動（§11.2）のコピーか。真なら必ず同期し（§10.5）、コピーしたエントリを記録する。
	move bool
	// cur は、記録を加えるフォルダ（move のとき。§11.2 の手順 1）。トップレベルの項目の記録は root.children に入る。
	cur  *recordEntry
	root recordEntry
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
	if out == OutcomeFailed && err != nil && err.Kind == KindNoSpace && cp.noSpace == nil {
		cp.noSpace = err
	}
}

// stopped は、この項目の残りを処理しないか（キャンセル、容量不足）を返す。
func (cp *copier) stopped() bool { return cp.canceled || cp.noSpace != nil }

// warn は、データは無事だがメタデータを保持できなかったことを Warnings に記録する（§15）。
func (cp *copier) warn(src, dst string, err error) {
	cp.warnings = append(cp.warnings, &OpError{Op: "metadata", Path: src, Dest: dst, Kind: KindMetadata, Err: err})
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

// sync は、同期するか（§10.5。移動では必ず、コピーでは SyncAlways のときだけ）を返す。
func (cp *copier) sync() bool { return cp.move || cp.ex.opt.Sync == SyncAlways }

// record は、コピーしたエントリを記録する（移動のとき。§11.2 の手順 1）。
func (cp *copier) record(rec recordEntry) {
	if cp.move {
		cp.cur.children = append(cp.cur.children, rec)
	}
}

// changed は、フォルダ dir の中の名前を変えた（最終名へのリネーム、フォルダの作成）ことを記録する（§10.5 の同期用）。
func (cp *copier) changed(dir string) {
	cp.wrote = true
	if !cp.sync() {
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

// syncChanged は、名前を変えたフォルダを同期する（§10.5）。
// コピーでは、データは最終名で書き終えているので、失敗は Warnings にする。
// 移動では、同期できなければ移動元を消さない（I2）ので、失敗として記録する。
func (cp *copier) syncChanged() {
	for _, dir := range cp.dirOrder {
		err := cp.ex.opt.hooks.syncDir(dir)
		if err == nil {
			var s string
			if s, err = sysPath(dir); err == nil {
				err = syncDirSys(s)
			}
		}
		if err == nil {
			continue
		}
		oe := &OpError{Op: "sync", Path: dir, Kind: classify(err, classifyOpts{}), Err: withUserPaths(err, dir, "")}
		if cp.move {
			if cp.firstErr == nil {
				cp.firstErr = oe
			}
		} else {
			cp.warnings = append(cp.warnings, oe)
		}
	}
}

// copyItem は、コピー（MethodCopy）のトップレベルの 1 項目を処理する（§10）。i は Items() の添字。
func (ex *executor) copyItem(i int, it Item) ItemResult {
	res, _ := ex.copyTop(i, it, false)
	return res
}

// copyTop は、トップレベルの 1 項目をコピーする。move が真なら、ボリュームをまたぐ移動（§11.2）のコピーとして、
// 必ず同期し、コピーしたエントリを記録して返す（コピーしたものがなければ nil）。
func (ex *executor) copyTop(i int, it Item, move bool) (ItemResult, *recordEntry) {
	res := ItemResult{Src: it.Src, Dst: it.Dst}
	pc := ex.conflictIdx.top[i]
	if pc != nil && (pc.c.Decision == DecisionUnset || pc.c.Decision == DecisionSkip) {
		res.Outcome = OutcomeSkipped // 衝突の決定による Skip（I1）。コピー元が変わっていても何もしないので調べない
		return res, nil
	}
	e, err := statTop(it.Src)
	if err != nil {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "copy", Path: it.Src, Kind: classify(err, classifyOpts{}), Err: err}
		return res, nil
	}
	if e.info.Type != it.Info.Type {
		res.Outcome, res.Err = OutcomeFailed, &OpError{Op: "copy", Path: it.Src, Kind: KindSourceChanged}
		return res, nil
	}
	e.name = filepath.Base(it.Src)
	cp := &copier{ex: ex, move: move}
	cp.cur = &cp.root
	dst, out, oe := cp.copyEntry(it.Src, it.Dst, e, pc)
	cp.syncChanged()
	res.Dst, res.Details, res.Warnings = dst, cp.details, cp.warnings
	switch {
	case cp.canceled && cp.wrote:
		res.Outcome, res.Err = OutcomePartial, cp.canceledErr(it.Src)
	case cp.canceled:
		res.Outcome, res.Err = OutcomeSkipped, cp.canceledErr(it.Src)
	case out == OutcomeFailed && cp.wrote:
		res.Outcome, res.Err = OutcomePartial, oe // 作ったフォルダの中身を列挙できなかった（空のフォルダが残る）
	case out != OutcomeDone:
		res.Outcome, res.Err = out, oe
	case cp.noSpace != nil && cp.wrote:
		res.Outcome, res.Err = OutcomePartial, cp.noSpace // Err は KindNoSpace にする（§7.2 で残りを Skipped にするため）
	case cp.noSpace != nil:
		res.Outcome, res.Err = OutcomeFailed, cp.noSpace
	case cp.firstErr != nil:
		res.Outcome, res.Err = OutcomePartial, cp.firstErr
	default:
		res.Outcome = OutcomeDone
	}
	var rec *recordEntry
	if len(cp.root.children) == 1 {
		rec = &cp.root.children[0]
	}
	return res, rec
}

// copyEntry は、エントリ e（src）を dst にコピーする。pc は計画時に検出した衝突（なければ nil）。
// 実際のコピー先のパスと、エントリ自体の結果を返す。フォルダの中のエントリの結果は Details に記録する。
func (cp *copier) copyEntry(src, dst string, e dirEntry, pc *planned) (string, Outcome, *OpError) {
	if pc != nil && (pc.c.Decision == DecisionUnset || pc.c.Decision == DecisionSkip) {
		return dst, OutcomeSkipped, nil // 衝突の決定による Skip（I1）。コピー元が変わっていても何もしないので確かめない
	}
	if pc != nil && e.info.Type != pc.c.SrcInfo.Type {
		// 決定は計画時の種類に対して行われたものなので、そのまま適用しない。
		return dst, OutcomeFailed, &OpError{Op: "copy", Path: src, Dest: dst, Kind: KindSourceChanged}
	}
	switch e.info.Type {
	case TypeFile:
		return cp.copyFile(src, dst, e, pc)
	case TypeDir:
		return cp.copyDir(src, dst, e, pc)
	case TypeSymlink:
		if cp.ex.opt.Links == LinkSkip {
			return dst, OutcomeSkipped, &OpError{Op: "copy", Path: src, Dest: dst, Kind: KindLinkUnsupported}
		}
		return cp.copySymlink(src, dst, e, pc)
	case TypeJunction:
		// ジャンクションは複製しない（§14.2）。中にも入らない（I4）。
		return dst, OutcomeSkipped, &OpError{Op: "copy", Path: src, Dest: dst, Kind: KindLinkUnsupported}
	}
	return dst, OutcomeSkipped, &OpError{Op: "copy", Path: src, Dest: dst, Kind: KindUnsupportedType} // 特殊なファイル（§14.2）
}

// copySymlink はシンボリックリンクを複製する（§14.2）。リンク先の文字列をそのまま使い、リンクの先には入らない（I4）。
// 一時名を使わず、最終名（自動リネームでは候補名）に直接作る。リンクの作成は不可分で、名前が存在すれば失敗するため、I1・I3 を満たす。
// メタデータは設定しない（§15。os.Chtimes・os.Chmod はリンクを辿るため）。
func (cp *copier) copySymlink(src, dst string, e dirEntry, pc *planned) (string, Outcome, *OpError) {
	s, err := sysPath(src)
	if err != nil {
		return dst, OutcomeFailed, &OpError{Op: "copy", Path: src, Dest: dst, Kind: KindInvalidRequest, Err: err}
	}
	target, err := os.Readlink(s)
	if err != nil {
		return dst, OutcomeFailed, sourceErr(src, dst, e, withUserPaths(err, src, ""))
	}
	if now, err := statTop(src); err != nil || now.id != e.id || now.info.Type != TypeSymlink {
		return dst, OutcomeFailed, &OpError{Op: "copy", Path: src, Dest: dst, Kind: KindSourceChanged, Err: err}
	}
	create := func(p string) error {
		cp.ex.opt.hooks.finalRename(p)
		if err := cp.ex.opt.hooks.symlink(p); err != nil {
			return err
		}
		d, err := sysPath(p)
		if err != nil {
			return err
		}
		return withUserPaths(createSymlinkSys(target, d, e.dirAttr), target, p)
	}
	final, out, oe := dst, OutcomeDone, (*OpError)(nil)
	if pc != nil && pc.c.Decision == DecisionAutoRename {
		final, out, oe = autoRename(src, dst, false, true, create)
	} else {
		out, oe = createResult(src, dst, create(dst), true)
	}
	if oe == nil {
		cp.changed(filepath.Dir(final))
		cp.record(recordEntry{name: e.name, info: e.info, id: e.id})
		cp.ex.progress.fileDone(src)
	}
	return final, out, oe
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
	tmp, m, warnings, oe := cp.writeTemp(src, dst, e)
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
	cp.warnings = append(cp.warnings, warnings...)
	cp.changed(filepath.Dir(final))
	// 記録するのは、コピーして検証した時点のコピー元（§10.4 で開いた時点から変わっていないことを確かめたもの）。
	cp.record(recordEntry{name: e.name, info: EntryInfo{Type: TypeFile, Size: m.size, ModTime: m.mtime}, id: e.id})
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
			out, oe = createResult(src, dst, renameExclusive(tmp, dst), false)
		default:
			out, oe = replaceResult(src, dst, renameReplace(tmp, dst))
		}
		return dst, out, oe
	case DecisionAutoRename:
		return autoRename(src, dst, false, false, func(cand string) error {
			cp.ex.opt.hooks.finalRename(cand)
			return renameExclusive(tmp, cand)
		})
	}
	cp.ex.opt.hooks.finalRename(dst)
	out, oe := createResult(src, dst, renameExclusive(tmp, dst), false)
	return dst, out, oe
}

// createResult は、dst を排他的に作る操作（一時ファイルからの排他リネーム、フォルダ・シンボリックリンクの作成）の結果を分類する。
// 「存在する」は計画後に現れた衝突なので Skipped（KindExist。I1、§7.3）。symlink は、シンボリックリンクの作成のエラーか（§17）。
func createResult(src, dst string, err error, symlink bool) (Outcome, *OpError) {
	if err == nil {
		return OutcomeDone, nil
	}
	d, _ := sysPath(filepath.Dir(dst))
	k := classify(err, classifyOpts{readOnly: readOnlySys(d), symlinkCreate: symlink})
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
// 使えた名前のパスを返す。上限まで見つからなければ KindExist。symlink は createResult と同じ。
func autoRename(src, dst string, isDir, symlink bool, try func(cand string) error) (string, Outcome, *OpError) {
	dir, name := filepath.Dir(dst), filepath.Base(dst)
	for n := 2; n < 2+autoRenameLimit; n++ {
		cand := filepath.Join(dir, autoRenameName(name, isDir, n))
		out, oe := createResult(src, cand, try(cand), symlink)
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

// writeTemp は、src の内容をコピー先のフォルダの一時ファイルに書き込む（§10.1 の手順 1〜6）。
// 書き込み・同期（§10.5）・検証（§10.4）・メタデータの設定（§15）を済ませた一時ファイルのパスと、メタデータの警告を返す。
// 失敗・キャンセルしたら一時ファイルを削除する（I3）。
func (cp *copier) writeTemp(src, dst string, e dirEntry) (string, srcMeta, []*OpError, *OpError) {
	// エラーのパスは、一時ファイルではなくコピー元とコピー先で返す（一時ファイルのパスは Err の中にだけ現れる）。
	fail := func(err error) *OpError {
		return &OpError{Op: "copy", Path: src, Dest: dst, Kind: classify(err, classifyOpts{}), Err: err}
	}
	s, err := sysPath(src)
	if err != nil {
		return "", srcMeta{}, nil, fail(err)
	}
	in, m, err := openSourceSys(s, e.id)
	if err != nil {
		if oe, ok := err.(*OpError); ok {
			return "", srcMeta{}, nil, &OpError{Op: "copy", Path: src, Dest: dst, Kind: oe.Kind, Err: oe.Err}
		}
		return "", srcMeta{}, nil, sourceErr(src, dst, e, withUserPaths(err, src, ""))
	}
	defer in.Close()
	var warnings []*OpError
	if m.extra, err = readExtra(in, s); err != nil {
		warnings = append(warnings, &OpError{Op: "metadata", Path: src, Dest: dst, Kind: KindMetadata, Err: withUserPaths(err, src, "")})
	}

	tmp, out, err := createTemp(filepath.Dir(dst))
	if err != nil {
		return "", srcMeta{}, nil, fail(err)
	}
	closed, keep := false, false
	defer func() {
		if !closed {
			out.Close()
		}
		if !keep {
			removeTemp(tmp)
		}
	}()
	var sum hash.Hash
	if cp.ex.opt.Verify == VerifyHash {
		sum = sha256.New()
	}
	cp.ex.progress.step(src)
	buf := cp.ex.copyBuf()
	var written int64
	for {
		if cp.checkCanceled() {
			return "", srcMeta{}, nil, cp.canceledErr(src)
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				return "", srcMeta{}, nil, fail(withUserPaths(err, tmp, ""))
			}
			if sum != nil {
				sum.Write(buf[:n])
			}
			written += int64(n)
			cp.ex.progress.addBytes(int64(n))
			if err := cp.ex.opt.hooks.write(dst, written); err != nil {
				return "", srcMeta{}, nil, fail(err)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", srcMeta{}, nil, fail(withUserPaths(rerr, src, ""))
		}
	}
	if cp.sync() {
		if err := out.Sync(); err != nil {
			return "", srcMeta{}, nil, fail(withUserPaths(err, tmp, ""))
		}
	}
	fi, err := out.Stat()
	if err != nil {
		return "", srcMeta{}, nil, fail(withUserPaths(err, tmp, ""))
	}
	// 一時ファイルの fileID は、書き込んだ後に記録する。macOS の exFAT・FAT32 では、空のファイルに最初のデータ領域を
	// 割り当てると fileID が変わる（2026-09-24 に手元の hdiutil のイメージで確認）。
	tmpID, err := fileIDOfFile(out)
	if err != nil {
		return "", srcMeta{}, nil, fail(withUserPaths(err, tmp, ""))
	}
	closed = true
	if err := out.Close(); err != nil {
		return "", srcMeta{}, nil, fail(withUserPaths(err, tmp, ""))
	}
	cp.ex.opt.hooks.verify(tmp)
	if oe := cp.verify(src, dst, tmp, e, m, written, fi.Size(), tmpID, sum); oe != nil {
		return "", srcMeta{}, nil, oe
	}
	if ts, err := sysPath(tmp); err == nil {
		if err := setMetaSys(ts, tmpID, m, false); err != nil {
			warnings = append(warnings, &OpError{Op: "metadata", Path: src, Dest: dst, Kind: KindMetadata, Err: withUserPathsAll(err, dst)})
		}
	}
	keep = true
	return tmp, m, warnings, nil
}

// verify は、書き終えて閉じた一時ファイル tmp を検証する（§10.4）。
// VerifySize: 書き込んだバイト数、一時ファイルの大きさ、開いた時点のコピー元の大きさが一致し、
// コピー元を調べ直して fileID・大きさ・更新日時が開いた時点から変わっていないこと。コピー元が変わっていれば KindSourceChanged。
// VerifyHash: さらに、読み込み時の SHA-256（sum）と、一時ファイルを読み直した SHA-256 が一致すること。
// 一致しなければ（書き込んだ内容が一時ファイルに残っていない）KindUnknown。
func (cp *copier) verify(src, dst, tmp string, e dirEntry, m srcMeta, written, tmpSize int64, tmpID fileID, sum hash.Hash) *OpError {
	if written != m.size {
		return &OpError{Op: "verify", Path: src, Dest: dst, Kind: KindSourceChanged}
	}
	if tmpSize != written {
		return &OpError{Op: "verify", Path: src, Dest: dst, Kind: KindUnknown}
	}
	now, err := statTop(src)
	if err != nil || now.id != e.id || now.info.Type != TypeFile || now.info.Size != m.size || !now.info.ModTime.Equal(m.mtime) {
		return &OpError{Op: "verify", Path: src, Dest: dst, Kind: KindSourceChanged, Err: err}
	}
	if sum == nil {
		return nil
	}
	cp.ex.progress.start(StageVerify, src)
	defer cp.ex.progress.setStage(StageCopy)
	ts, err := sysPath(tmp)
	if err != nil {
		return &OpError{Op: "verify", Path: src, Dest: dst, Kind: KindInvalidRequest, Err: err}
	}
	f, _, err := openSourceSys(ts, tmpID)
	if err != nil {
		return &OpError{Op: "verify", Path: src, Dest: dst, Kind: classify(err, classifyOpts{}), Err: withUserPaths(err, tmp, "")}
	}
	defer f.Close()
	h := sha256.New()
	buf := cp.ex.copyBuf()
	for {
		if cp.checkCanceled() {
			return cp.canceledErr(src)
		}
		n, rerr := f.Read(buf)
		h.Write(buf[:n])
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return &OpError{Op: "verify", Path: src, Dest: dst, Kind: classify(rerr, classifyOpts{}), Err: withUserPaths(rerr, tmp, "")}
		}
	}
	if !bytes.Equal(h.Sum(nil), sum.Sum(nil)) {
		return &OpError{Op: "verify", Path: src, Dest: dst, Kind: KindUnknown}
	}
	return nil
}

// withUserPathsAll は、errors.Join でまとめたエラーのそれぞれに含まれるパスを、呼び出し側に返す形の p にする（§8.2）。
func withUserPathsAll(err error, p string) error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		for _, e := range j.Unwrap() {
			withUserPathsAll(e, p)
		}
		return err
	}
	if oe, ok := err.(*OpError); ok {
		oe.Path = p
		return err
	}
	return withUserPaths(err, p, "")
}

// sourceErr は、コピー元 src（走査時のエントリ e）を開けなかったエラーを分類する。
// リンクに当たった（O_NOFOLLOW の ELOOP）場合と、調べ直して fileID か種類が走査時と違う場合は KindSourceChanged にする。
func sourceErr(src, dst string, e dirEntry, err error) *OpError {
	k := classify(err, classifyOpts{noFollow: true})
	if k != KindSourceChanged {
		if now, serr := statTop(src); serr == nil && (now.id != e.id || now.info.Type != e.info.Type) {
			k = KindSourceChanged
		}
	}
	return &OpError{Op: "copy", Path: src, Dest: dst, Kind: k, Err: err}
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
// 実際のコピー先のパスと、フォルダ自体の結果（作成・マージできなかった場合、中身を列挙できなかった場合は Skipped・Failed）を返す。
// リンクに置き換えられたフォルダには入らない（列挙はリンクを辿らない。§18.4 の I4）。
func (cp *copier) copyDir(src, dst string, e dirEntry, pc *planned) (string, Outcome, *OpError) {
	mkdir := func(p string) error {
		s, err := sysPath(p)
		if err != nil {
			return err
		}
		return withUserPaths(os.Mkdir(s, 0o700), p, "")
	}
	var inner map[string]*planned // マージする場合だけ、内側の衝突を使う
	created := true               // マージで既存のフォルダを使う場合は偽（そのフォルダのメタデータは変えない。§15）
	switch {
	case pc != nil && pc.c.Decision == DecisionMerge:
		gone, oe := checkTarget(src, dst, pc)
		switch {
		case oe != nil && oe.Kind == KindExist:
			return dst, OutcomeSkipped, oe
		case oe != nil:
			return dst, OutcomeFailed, oe
		case gone:
			if out, oe := createResult(src, dst, mkdir(dst), false); oe != nil {
				return dst, out, oe
			}
			cp.changed(filepath.Dir(dst))
		default:
			inner = cp.ex.conflictIdx.inner[pc.c.ID]
			created = false
		}
	case pc != nil && pc.c.Decision == DecisionAutoRename:
		final, out, oe := autoRename(src, dst, true, false, mkdir)
		if oe != nil {
			return final, out, oe
		}
		dst = final
		cp.changed(filepath.Dir(dst))
	default:
		if out, oe := createResult(src, dst, mkdir(dst), false); oe != nil {
			return dst, out, oe
		}
		cp.changed(filepath.Dir(dst))
	}

	// 作ったフォルダの fileID を記録し、メタデータを設定するときに同じフォルダであることを確かめる。
	var dstID fileID
	var meta srcMeta
	var metaErr error
	if created {
		if st, err := fileIDOf(dst); err == nil {
			dstID = st.id
		} else {
			metaErr = err
		}
		if ss, err := sysPath(src); err != nil {
			metaErr = err
		} else if meta, err = dirMetaSys(ss); err != nil {
			metaErr = withUserPathsAll(err, src)
		}
	}

	cp.ex.opt.hooks.enterDir(src)
	entries, err := readDir(src)
	if err != nil {
		return dst, OutcomeFailed, sourceErr(src, dst, e, err)
	}
	rec := &recordEntry{name: e.name, info: EntryInfo{Type: TypeDir}, id: e.id}
	parent := cp.cur
	cp.cur = rec
	defer func() {
		cp.cur = parent
		cp.record(*rec) // マージで既存のフォルダを使った場合も、移動元のフォルダは記録する（中身を消した後に消す）
	}()
	for _, ce := range entries {
		if cp.checkCanceled() || cp.stopped() {
			return dst, OutcomeDone, nil
		}
		childSrc, childDst := filepath.Join(src, ce.name), filepath.Join(dst, ce.name)
		final, out, oe := cp.copyEntry(childSrc, childDst, ce, inner[ce.name])
		cp.entry(childSrc, final, out, oe)
		if cp.move && out == OutcomeSkipped && oe == nil {
			rec.skipped = append(rec.skipped, ce.name) // 衝突の決定による Skip。移動元に残す（§11.2 の手順 2）
		}
		if cp.stopped() {
			return dst, OutcomeDone, nil // 途中までのフォルダにはメタデータ（読み取り専用など）を設定しない
		}
	}
	// フォルダのメタデータは、中身をすべて処理した後に設定する（§10.2、§15。先に 0o555 などにすると中身を作れないため）。
	if created {
		if metaErr == nil {
			if ds, err := sysPath(dst); err != nil {
				metaErr = err
			} else if err := setMetaSys(ds, dstID, meta, true); err != nil {
				metaErr = withUserPathsAll(err, dst)
			}
		}
		if metaErr != nil {
			cp.warn(src, dst, metaErr)
		}
	}
	return dst, OutcomeDone, nil
}

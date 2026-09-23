package fsops

import (
	"strconv"
	"time"
)

// Stage は進捗で報告する処理の段階。
type Stage int

const (
	StageCopy Stage = iota + 1
	StageMove       // 同一ボリュームの移動
	StageVerify
	StageRemoveSource
	StageTrash
	StageDelete
)

func (s Stage) String() string {
	switch s {
	case StageCopy:
		return "StageCopy"
	case StageMove:
		return "StageMove"
	case StageVerify:
		return "StageVerify"
	case StageRemoveSource:
		return "StageRemoveSource"
	case StageTrash:
		return "StageTrash"
	case StageDelete:
		return "StageDelete"
	}
	return "Stage(" + strconv.Itoa(int(s)) + ")"
}

// Progress は ExecOptions.Progress に渡す進捗（SPEC §16）。
type Progress struct {
	Stage      Stage
	Current    string // 処理中のパス
	DoneFiles  int
	TotalFiles int
	DoneBytes  int64
	TotalBytes int64
}

// progressInterval は、進捗の呼び出しを間引く間隔（§16）。
const progressInterval = 100 * time.Millisecond

// progressReporter は、ExecOptions.Progress を間引いて呼ぶ（§16）。Execute を実行している goroutine からだけ使う。
type progressReporter struct {
	fn   func(Progress)
	cur  Progress
	last time.Time
}

// report は進捗を報告する。force が偽なら、前回から progressInterval 経っていないときは呼ばない。
func (r *progressReporter) report(force bool) {
	if r.fn == nil {
		return
	}
	now := time.Now()
	if !force && now.Sub(r.last) < progressInterval {
		return
	}
	r.last = now
	r.fn(r.cur)
}

// start は、項目の区切りで段階と処理中のパスを設定して、必ず報告する。
func (r *progressReporter) start(stage Stage, current string) {
	r.cur.Stage, r.cur.Current = stage, current
	r.report(true)
}

// done は、フォルダ以外のエントリ 1 件の完了を加える。
func (r *progressReporter) done(path string, info EntryInfo) {
	r.cur.Current = path
	r.cur.DoneFiles++
	if info.Type == TypeFile {
		r.cur.DoneBytes += info.Size
	}
	r.report(false)
}

// step は、処理中のパスを設定する（間引いて報告する）。
func (r *progressReporter) step(path string) {
	r.cur.Current = path
	r.report(false)
}

// addBytes は、コピーで書き込んだバイト数を加える（バッファごと。§16）。
// 進捗が逆戻りしないよう、失敗・キャンセルしたファイルの書き込み済みの分も戻さない（処理したバイト数として数える）。
func (r *progressReporter) addBytes(n int64) {
	r.cur.DoneBytes += n
	r.report(false)
}

// fileDone は、コピーしたファイル 1 件の完了を加える。バイト数は addBytes で加え済み。
func (r *progressReporter) fileDone(path string) {
	r.cur.Current = path
	r.cur.DoneFiles++
	r.report(false)
}

// setStage は、報告せずに段階を変える（検証の後にコピーの段階に戻すときに使う）。
func (r *progressReporter) setStage(s Stage) { r.cur.Stage = s }

package fsops

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"
)

// ---- リクエストと計画 ----

// OpKind は操作の種類。
type OpKind int

const (
	OpCopy OpKind = iota + 1
	OpMove
	OpTrash
	OpDelete // 完全削除。UI 側で明示的な確認を経た場合だけ使う
)

func (k OpKind) String() string {
	switch k {
	case OpCopy:
		return "OpCopy"
	case OpMove:
		return "OpMove"
	case OpTrash:
		return "OpTrash"
	case OpDelete:
		return "OpDelete"
	}
	return "OpKind(" + strconv.Itoa(int(k)) + ")"
}

// Request は操作の依頼。
type Request struct {
	Op      OpKind
	Sources []string // 絶対パス。1 件以上
	DestDir string   // OpCopy / OpMove のみ。存在するフォルダの絶対パス。OpTrash / OpDelete では空
}

// Plan は NewPlan が作る計画。フィールドはすべて非公開で、NewPlan 以外では作れない。
// 呼び出し側が変更できるのは、Decide による衝突の決定だけ。
//
// NewPlan が作る Plan は items が 1 件以上なので、items が空の Plan はゼロ値（NewPlan 以外で作られたもの）とみなす。
type Plan struct {
	req       Request
	items     []Item
	conflicts []Conflict
	// conflictDst は、conflicts と同じ添字で、計画時に記録した上書き先・マージ先の fileID（§6.3、§7.3）。
	conflictDst []fileID
	totalFiles  int
	totalBytes  int64
	warnings    []*OpError

	mu      sync.Mutex // conflicts の Decision と started を守る
	started bool       // Execute が開始された
}

// Request は計画のもとになったリクエストを返す。
func (p *Plan) Request() Request {
	r := p.req
	r.Sources = append([]string(nil), p.req.Sources...)
	return r
}

// Items はトップレベルの項目（Sources 1 件ごと）のコピーを返す。
func (p *Plan) Items() []Item {
	items := make([]Item, len(p.items))
	for i, it := range p.items {
		it.Err = it.Err.clone()
		items[i] = it
	}
	return items
}

// Conflicts は衝突のコピーを返す。
func (p *Plan) Conflicts() []Conflict {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]Conflict(nil), p.conflicts...)
}

// TotalFiles は処理するファイルの数を返す（フォルダ以外のエントリの数。§6.3）。
func (p *Plan) TotalFiles() int { return p.totalFiles }

// TotalBytes は処理する通常のファイルの大きさの合計を返す（§6.3。同一ボリュームの移動とごみ箱では 0）。
func (p *Plan) TotalBytes() int64 { return p.totalBytes }

// Warnings は実行を妨げない問題（空き容量不足の見込み、フォルダ内の走査エラーなど）のコピーを返す。
func (p *Plan) Warnings() []*OpError {
	ws := make([]*OpError, len(p.warnings))
	for i, w := range p.warnings {
		ws[i] = w.clone()
	}
	return ws
}

// Item は計画のトップレベルの項目。
type Item struct {
	Src, Dst string // Dst は OpTrash / OpDelete では空
	Info     EntryInfo
	Method   Method
	// Err は計画の時点で実行できないと分かった理由（KindNotFound、KindDestInsideSource、
	// KindSameFile、KindTrashUnavailable など）。Execute はこの項目を処理せず OutcomeFailed にする。
	Err *OpError
}

// Method は項目の処理方式。
type Method int

const (
	MethodRename         Method = iota + 1 // 同一ボリュームの移動
	MethodCopy                             // コピー
	MethodCopyThenRemove                   // ボリュームをまたぐ移動
	MethodTrash
	MethodRemove
)

func (m Method) String() string {
	switch m {
	case MethodRename:
		return "MethodRename"
	case MethodCopy:
		return "MethodCopy"
	case MethodCopyThenRemove:
		return "MethodCopyThenRemove"
	case MethodTrash:
		return "MethodTrash"
	case MethodRemove:
		return "MethodRemove"
	}
	return "Method(" + strconv.Itoa(int(m)) + ")"
}

// EntryType はエントリの種類（SPEC §14.1）。
type EntryType int

const (
	TypeFile EntryType = iota + 1
	TypeDir
	TypeSymlink
	TypeJunction // Windows のマウントポイント
	TypeSpecial  // 上記以外（未知のリパースポイント、FIFO、デバイスなど）SPEC §14
)

func (t EntryType) String() string {
	switch t {
	case TypeFile:
		return "TypeFile"
	case TypeDir:
		return "TypeDir"
	case TypeSymlink:
		return "TypeSymlink"
	case TypeJunction:
		return "TypeJunction"
	case TypeSpecial:
		return "TypeSpecial"
	}
	return "EntryType(" + strconv.Itoa(int(t)) + ")"
}

// EntryInfo はエントリの情報。
type EntryInfo struct {
	Type    EntryType
	Size    int64 // TypeFile のときだけ意味を持つ
	ModTime time.Time
}

// ---- 衝突 ----

// ConflictID は衝突の識別子。1 から始まる。
type ConflictID int

// Conflict は計画時に検出した衝突。
type Conflict struct {
	ID               ConflictID
	Parent           ConflictID // フォルダ同士の衝突の内側で見つかった場合、その親。0 ならトップレベル
	Item             int        // 対応する Items() の添字
	Src, Dst         string
	SrcInfo, DstInfo EntryInfo
	Self             bool     // コピー先がコピー元そのもの（同じフォルダへのコピー）
	Decision         Decision // 読み取り専用。変更は Plan.Decide で行う
}

// Decision は衝突の決定。ゼロ値は Skip と同じ扱い（I1）。
type Decision int

const (
	DecisionUnset Decision = iota // 未設定。Skip と同じ扱い（I1）
	DecisionSkip
	DecisionOverwrite  // ファイル同士のみ
	DecisionAutoRename // "name (2).ext"
	DecisionMerge      // フォルダ同士のみ
)

func (d Decision) String() string {
	switch d {
	case DecisionUnset:
		return "DecisionUnset"
	case DecisionSkip:
		return "DecisionSkip"
	case DecisionOverwrite:
		return "DecisionOverwrite"
	case DecisionAutoRename:
		return "DecisionAutoRename"
	case DecisionMerge:
		return "DecisionMerge"
	}
	return "Decision(" + strconv.Itoa(int(d)) + ")"
}

// ---- 実行 ----

// ExecOptions は Execute の設定。ゼロ値が既定の設定になる。
type ExecOptions struct {
	Links    LinkPolicy     // ゼロ値 = LinkKeep
	Verify   VerifyMode     // ゼロ値 = VerifySize
	Sync     SyncMode       // ゼロ値 = SyncMoveOnly
	Progress func(Progress) // SPEC §16。すぐに戻ること。nil 可
	hooks    *testHooks     // 障害の注入用。パッケージ内のテストからだけ設定できる。nil なら何もしない
}

// LinkPolicy はコピー時のリンクの扱い。
type LinkPolicy int

const (
	LinkKeep LinkPolicy = iota // リンクをリンクとして複製する
	LinkSkip                   // リンクは複製せず Skipped として報告する
)

func (l LinkPolicy) String() string {
	switch l {
	case LinkKeep:
		return "LinkKeep"
	case LinkSkip:
		return "LinkSkip"
	}
	return "LinkPolicy(" + strconv.Itoa(int(l)) + ")"
}

// VerifyMode はコピー後の検証の方法（SPEC §10.4）。
type VerifyMode int

const (
	VerifySize VerifyMode = iota // サイズとコピー元の不変を確認
	VerifyHash                   // さらに SHA-256 で内容を比較
)

func (v VerifyMode) String() string {
	switch v {
	case VerifySize:
		return "VerifySize"
	case VerifyHash:
		return "VerifyHash"
	}
	return "VerifyMode(" + strconv.Itoa(int(v)) + ")"
}

// SyncMode は同期（fsync）の方針（SPEC §10.5）。
type SyncMode int

const (
	SyncMoveOnly SyncMode = iota // 移動のときだけ fsync する
	SyncAlways
)

func (s SyncMode) String() string {
	switch s {
	case SyncMoveOnly:
		return "SyncMoveOnly"
	case SyncAlways:
		return "SyncAlways"
	}
	return "SyncMode(" + strconv.Itoa(int(s)) + ")"
}

// Execute は計画を実行する。開始時に決定を固定する。
// error を返すのは、何も実行しなかった場合だけ（SPEC §7.1）。
func (p *Plan) Execute(ctx context.Context, opt ExecOptions) (*Result, error) {
	return nil, &OpError{Op: "execute", Kind: KindUnknown, Err: errors.ErrUnsupported}
}

// Result は実行結果。
type Result struct {
	Status Status
	Items  []ItemResult // Items() と同じ順・同じ件数
}

// Status は実行全体の結果。
type Status int

const (
	StatusCompleted Status = iota + 1
	StatusCompletedWithErrors
	StatusCanceled
)

func (s Status) String() string {
	switch s {
	case StatusCompleted:
		return "StatusCompleted"
	case StatusCompletedWithErrors:
		return "StatusCompletedWithErrors"
	case StatusCanceled:
		return "StatusCanceled"
	}
	return "Status(" + strconv.Itoa(int(s)) + ")"
}

// ItemResult はトップレベルの項目ごとの結果。
type ItemResult struct {
	Src, Dst    string // Dst は自動リネーム後の実際のパス
	Outcome     Outcome
	Err         *OpError      // SPEC §7.4 の表に従う。衝突の決定による Skip では nil
	Warnings    []*OpError    // メタデータを保持できなかった等（データ自体は無事）
	TrashedPath string        // ごみ箱に入った後のパス（取得できた場合）
	Details     []EntryResult // フォルダ内で Done 以外になったエントリ（名前順）
}

// EntryResult はフォルダ内のエントリの結果。
type EntryResult struct {
	Src, Dst string
	Outcome  Outcome  // OutcomeSkipped / OutcomeFailed / OutcomeCopiedSourceKept（移動元に残したもの）
	Err      *OpError // 衝突の決定による Skip では nil
}

// Outcome は項目・エントリの結果。
type Outcome int

const (
	OutcomeDone Outcome = iota + 1
	OutcomeSkipped
	OutcomeFailed
	OutcomePartial          // フォルダの一部だけ処理できた
	OutcomeCopiedSourceKept // 移動: 移動先は完成したが、移動元の削除に失敗
)

func (o Outcome) String() string {
	switch o {
	case OutcomeDone:
		return "OutcomeDone"
	case OutcomeSkipped:
		return "OutcomeSkipped"
	case OutcomeFailed:
		return "OutcomeFailed"
	case OutcomePartial:
		return "OutcomePartial"
	case OutcomeCopiedSourceKept:
		return "OutcomeCopiedSourceKept"
	}
	return "Outcome(" + strconv.Itoa(int(o)) + ")"
}

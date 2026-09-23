# fsops 仕様書

ファイラーのファイル操作パッケージ `internal/fsops` の仕様。
§2 の不変条件は変更禁止。それ以外は、理由を示して承認を得れば変更してよい。

---

## 1. 目的と範囲

`internal/fsops` はファイラーのファイル操作を担う。UI から独立しており、単体でテストできる。

扱う操作:

- コピー
- 移動（同一ボリュームではリネーム、ボリュームをまたぐ場合はコピー後に移動元を削除）
- 名前の変更
- ごみ箱へ移動
- 完全削除

扱わないもの:

- フォルダ内容の一覧・表示・並べ替え・検索（UI 側の責務）
- 圧縮・展開（7-Zip を外部コマンドとして呼ぶ予定）
- ACL・所有者の保持
- ネットワークプロトコルの直接操作（マウント済みのドライブ・共有フォルダは通常のパスとして扱う）
- その他 §21

---

## 2. 不変条件

fsops のすべての操作は、正常終了・失敗・キャンセルのどの場合も以下を満たす。
テストはこれらを証明することを最優先とする（§18.4）。

- **I1 承認されていない上書きをしない**
  既存のファイルを置き換えるのは、計画時に検出した衝突に対して呼び出し側が `DecisionOverwrite` を設定した場合だけ。
  決定が未設定（ゼロ値）の衝突は Skip として扱う。
  計画の後に新しく現れた衝突は、上書きせず Skip して報告する。

- **I2 移動元は最後に、コピーした分だけ消す**
  ボリュームをまたぐ移動では、トップレベルの項目ごとに、移動先への書き込み・同期・検証がすべて完了した場合だけ移動元を削除する。
  1 つでも失敗した項目、キャンセルされた項目の移動元には手を付けない。
  移動元の削除では、コピーしたと記録したエントリだけを削除する。コピー中に移動元へ追加されたファイルは消さない。

- **I3 書きかけのファイルを最終名で残さない**
  ファイルの内容は一時名で書き込み、完了後にリネームで最終名にする。
  失敗・キャンセル時は一時ファイルを削除する。プロセスが強制終了した場合に残りうるのは一時名のファイルだけ。
  （フォルダは最終名で作成する。途中までの状態は結果で `OutcomePartial` として報告する。）

- **I4 リンクの先を操作しない**
  シンボリックリンク・ジャンクションの中に、削除・移動・ごみ箱のために入り込まない。操作の対象はリンクそのもの。

- **I5 黙って完全削除しない**
  ごみ箱が使えない場所では `KindTrashUnavailable` を返す。完全削除への切り替えは呼び出し側が明示的に行う。

- **I6 ファイル名を変換しない**
  Unicode 正規化・大文字小文字の変換をしない。名前を新しく作るのは自動リネーム（`name (2).ext`）の場合だけ。

- **I7 キャンセル後・失敗後も I1〜I6 が成り立つ**

この結果として、どの時点で処理が止まっても「データが失われる」ことはない。
起こりうるのは「移動元と移動先の両方に同じものがある（重複）」と「一時ファイルが残る」だけである。

---

## 3. 対象環境

- Windows 10 / 11（NTFS）。CI は `windows-latest`（Windows Server 2025）。
- macOS（APFS）。CI は `macos-latest`。
- Linux はコンパイルが通ることだけを保証する（ごみ箱は `KindTrashUnavailable`）。
- Go は `go.mod` に記載したバージョン（作業開始時点の最新安定版）。
- 開発者の手元は macOS のみ。Windows の確認は CI で行う。

---

## 4. 構成と依存

ファイル構成:

```
<repo>/
  CLAUDE.md
  docs/SPEC-fsops.md
  docs/PROMPTS.md
  go.mod
  .gitignore
  internal/fsops/
    doc.go
    types.go                  公開型（Request、Plan、Item、Conflict、Result など）
    errors.go                 Kind・OpError・KindOf・共通の分類
    errors_windows.go         Windows のエラー番号の分類
    errors_unix.go            //go:build unix。errno の分類
    path.go                   絶対パスの検査、祖先の判定（§8.3）
    path_windows.go           \\?\ 形式への変換（§8.2）、実パスの取得（GetFinalPathNameByHandle）
    path_unix.go              //go:build unix
    name.go                   新しい名前の検査の共通部（§11.3）
    name_windows.go
    name_unix.go              //go:build unix
    fileid.go                 同一性の記録と比較（§8.3）
    fileid_windows.go
    fileid_unix.go            //go:build unix
    entry.go                  エントリ種類の判定（共通部）
    entry_windows.go          属性・リパースタグによる判定
    entry_unix.go             //go:build unix
    rename.go                 排他リネーム・置換リネームの共通部
    rename_windows.go
    rename_darwin.go
    rename_linux.go
    volume_windows.go         ボリュームの識別、空き容量
    volume_unix.go            //go:build unix
    walk.go                   リンクに入り込まない走査（§13.1）
    plan.go                   NewPlan
    conflict.go               決定の検査（§9.1）、自動リネームの名前（§9.2）
    execute.go                Execute
    copy.go
    move.go
    remove.go                 完全削除・記録した項目だけの削除
    trash.go                  ごみ箱の共通部
    trash_windows.go
    trash_darwin_cgo.go       //go:build darwin && cgo
    trash_darwin_nocgo.go     //go:build darwin && !cgo（KindTrashUnavailable）
    trash_other.go            //go:build !windows && !darwin
    meta.go / meta_windows.go / meta_unix.go
    progress.go
    hooks.go                  テスト用フックの型（testHooks）
    deps_test.go              依存の許可リストの検査
    internal/testfs/          テスト用フィクスチャ
    internal/probe/           要検証事項のプローブ（テストのみ、フェーズ3）
  cmd/fsopsctl/               動作確認用 CLI（フェーズ11）
  .github/workflows/test.yml
```

ビルドのルール:

- 対応する GOOS は windows・darwin・linux の 3 つだけとする。
- ファイル名の `_unix`・`_other`・`_cgo`・`_nocgo` は Go のビルド条件として認識されないため、それらのファイルには必ず `//go:build` を書く（例: `//go:build unix`）。

依存のルール:

- `internal/fsops` が import してよいのは、標準ライブラリ、`golang.org/x/sys/...`、`golang.org/x/text/...`、および `internal/fsops` 配下のパッケージだけ。
- `golang.org/x/text` は、必要になるまで `go.mod` に入れない（I6 により、ファイル名の正規化には使わない）。
- `deps_test.go` は、`GOOS`（windows・darwin・linux）・`GOARCH`（amd64・arm64）・`CGO_ENABLED`（0・1）のすべての組み合わせで `go list -deps -test` を実行し、どの組み合わせでも許可リスト外の依存（テストの依存を含む）があればテストを失敗させる。
- 将来ほかのプロジェクトから使う必要が出たら、`internal/` の外へ移動するか別モジュールに切り出す。それまでは `internal/` に置く。

---

## 5. API

次の性質は変えない。

- 計画（ファイルシステムを変更しない）と実行の 2 段階に分かれている
- 衝突の決定のゼロ値は Skip 扱いである
- 実行結果を項目ごとに返す
- 計画の内容は、決定を除いて呼び出し側から変更できない

```go
package fsops

// ---- リクエストと計画 ----

type OpKind int

const (
	OpCopy OpKind = iota + 1
	OpMove
	OpTrash
	OpDelete // 完全削除。UI 側で明示的な確認を経た場合だけ使う
)

type Request struct {
	Op      OpKind
	Sources []string // 絶対パス。1 件以上
	DestDir string   // OpCopy / OpMove のみ。存在するフォルダの絶対パス。OpTrash / OpDelete では空
}

// NewPlan は計画を作る。ファイルシステムは一切変更しない。
// error を返すのはリクエスト全体が不正な場合だけ（§6.1）。項目ごとの問題は Item.Err に入れる。
func NewPlan(ctx context.Context, req Request) (*Plan, error)

// Plan のフィールドはすべて非公開。NewPlan 以外では作れない。変更できるのは決定だけ。
type Plan struct{ /* 非公開 */ }

func (p *Plan) Request() Request
func (p *Plan) Items() []Item         // トップレベル（Sources 1 件ごと）。コピーを返す
func (p *Plan) Conflicts() []Conflict // コピーを返す
func (p *Plan) TotalFiles() int
func (p *Plan) TotalBytes() int64
func (p *Plan) Warnings() []*OpError // 空き容量不足の見込み、フォルダ内の走査エラーなど。実行は妨げない

// Decide は衝突の決定を設定する。
// §9.1 で許されない決定、存在しない ID、Execute の開始後の呼び出しは error を返す。
func (p *Plan) Decide(id ConflictID, d Decision) error

type Item struct {
	Src, Dst string // Dst は OpTrash / OpDelete では空
	Info     EntryInfo
	Method   Method
	// Err は計画の時点で実行できないと分かった理由（KindNotFound、KindDestInsideSource、
	// KindSameFile、KindTrashUnavailable など）。Execute はこの項目を処理せず OutcomeFailed にする。
	Err *OpError
}

type Method int

const (
	MethodRename         Method = iota + 1 // 同一ボリュームの移動
	MethodCopy                             // コピー
	MethodCopyThenRemove                   // ボリュームをまたぐ移動
	MethodTrash
	MethodRemove
)

type EntryType int

const (
	TypeFile EntryType = iota + 1
	TypeDir
	TypeSymlink
	TypeJunction // Windows のマウントポイント
	TypeSpecial  // 上記以外（未知のリパースポイント、FIFO、デバイスなど）§14
)

type EntryInfo struct {
	Type    EntryType
	Size    int64 // TypeFile のときだけ意味を持つ
	ModTime time.Time
}

// ---- 衝突 ----

type ConflictID int // 1 から始まる

type Conflict struct {
	ID               ConflictID
	Parent           ConflictID // フォルダ同士の衝突の内側で見つかった場合、その親。0 ならトップレベル
	Item             int        // 対応する Items() の添字
	Src, Dst         string
	SrcInfo, DstInfo EntryInfo
	Self             bool     // コピー先がコピー元そのもの（同じフォルダへのコピー）
	Decision         Decision // 読み取り専用。変更は Plan.Decide で行う
}

type Decision int

const (
	DecisionUnset      Decision = iota // 未設定。Skip と同じ扱い（I1）
	DecisionSkip
	DecisionOverwrite  // ファイル同士のみ
	DecisionAutoRename // "name (2).ext"
	DecisionMerge      // フォルダ同士のみ
)

// ---- 実行 ----

type ExecOptions struct {
	Links    LinkPolicy     // ゼロ値 = LinkKeep
	Verify   VerifyMode     // ゼロ値 = VerifySize
	Sync     SyncMode       // ゼロ値 = SyncMoveOnly
	Progress func(Progress) // §16。すぐに戻ること。nil 可
	hooks    *testHooks     // 障害の注入用。パッケージ内のテストからだけ設定できる。nil なら何もしない
}

type LinkPolicy int

const (
	LinkKeep LinkPolicy = iota // リンクをリンクとして複製する
	LinkSkip                   // リンクは複製せず Skipped として報告する
)

type VerifyMode int

const (
	VerifySize VerifyMode = iota // サイズとコピー元の不変を確認
	VerifyHash                   // さらに SHA-256 で内容を比較
)

type SyncMode int

const (
	SyncMoveOnly SyncMode = iota // 移動のときだけ fsync する
	SyncAlways
)

// Execute は計画を実行する。開始時に決定を固定する。
// error を返すのは、何も実行しなかった場合だけ（§7.1）。
func (p *Plan) Execute(ctx context.Context, opt ExecOptions) (*Result, error)

type Result struct {
	Status Status
	Items  []ItemResult // Items() と同じ順・同じ件数
}

type Status int

const (
	StatusCompleted Status = iota + 1
	StatusCompletedWithErrors
	StatusCanceled
)

type ItemResult struct {
	Src, Dst    string          // Dst は自動リネーム後の実際のパス
	Outcome     Outcome
	Err         *OpError        // §7.4 の表に従う。衝突の決定による Skip では nil
	Warnings    []*OpError      // メタデータを保持できなかった等（データ自体は無事）
	TrashedPath string          // ごみ箱に入った後のパス（取得できた場合）
	Details     []EntryResult   // フォルダ内で Done 以外になったエントリ（名前順）
}

type EntryResult struct {
	Src, Dst string
	Outcome  Outcome  // OutcomeSkipped / OutcomeFailed / OutcomeCopiedSourceKept（移動元に残したもの）
	Err      *OpError // 衝突の決定による Skip では nil
}

type Outcome int

const (
	OutcomeDone Outcome = iota + 1
	OutcomeSkipped
	OutcomeFailed
	OutcomePartial          // フォルダの一部だけ処理できた
	OutcomeCopiedSourceKept // 移動: 移動先は完成したが、移動元の削除に失敗
)

// ---- 名前の変更 ----

// Rename は path の名前を newName に変える。上書きは一切しない（§11.3）。
func Rename(path, newName string) error

// ---- 進捗 ----

type Stage int

const (
	StageCopy Stage = iota + 1
	StageMove // 同一ボリュームの移動
	StageVerify
	StageRemoveSource
	StageTrash
	StageDelete
)

// Progress は §16。
```

- `Kind`・`Outcome`・`Status`・`Stage` は `String()` を実装する（テストの失敗表示と `OpError.Error()` で使う。英語の識別子でよい）。
- エラーの分類を取り出す `func KindOf(err error) Kind` を用意する（§17）。

---

## 6. 計画（NewPlan）

計画の作成中は、ファイルシステムを一切変更しない。`ctx` のキャンセルに応じて中断できる。

### 6.1 リクエストの検査

- `Sources` が 1 件以上であること。すべて絶対パスであること（§8.1）。
- 同じパスの重複、または一方が他方の内側にある組み合わせ（`/a` と `/a/b`）は `KindInvalidRequest`。
  判定は、各 Source の親を字句的に辿り（リンクは解決しない）、各段をほかの Source と fileID で比べて行う。
- ボリュームのルート、デバイスパスは `KindInvalidRequest`（§8.1）。
- `OpCopy` / `OpMove` では、`DestDir` が存在するフォルダであること（`os.Stat` で確認。`DestDir` 自体がリンクの場合は辿ってよい）。
- `OpTrash` / `OpDelete` では、`DestDir` が空文字であること。
- 以上を満たさない場合、`NewPlan` は error（`*OpError`）を返す。Kind は、`DestDir` が存在しなければ `KindNotFound`、それ以外は `KindInvalidRequest`。`NewPlan` が error を返すのは、この節で挙げたリクエスト全体の問題の場合だけとする。
  §6.2 以降の項目ごとの問題は `Item.Err` に入れ、ほかの項目の計画は続ける。

### 6.2 項目ごとの判定

- 各 `Source` は `os.Lstat` で調べる（リンクを辿らない）。種類の判定は §14.1。取得できなければ `Item.Err`（`KindNotFound` など）。
- `OpCopy` / `OpMove` の `Dst` は `DestDir` + コピー元の名前（バイト単位でそのまま、I6）。
- コピー元がフォルダで、`DestDir` がその内側にある場合は `KindDestInsideSource`（判定方法は §8.3）。
- `OpMove` で `DestDir` がコピー元の親フォルダそのもの（fileID で判定、§8.3）なら `KindSameFile`。
- `OpTrash` では、ごみ箱が使えるかの事前確認（§12）を行い、使えなければ `KindTrashUnavailable`。
- `OpCopy` で `Dst` がコピー元そのものなら、`Self: true` の衝突として扱う。
- `OpMove` の方式: コピー元と `DestDir` のボリュームが同じなら `MethodRename`、違えば `MethodCopyThenRemove`。
  ボリュームの判定は、Windows ではボリュームシリアル番号、Unix では `Stat_t.Dev` を使う。
  実行時に `MethodRename` がボリューム違いのエラーになった場合は、`MethodCopyThenRemove` に切り替える（§11.1）。

### 6.3 走査と衝突の検出

- `OpCopy`・`MethodCopyThenRemove`・`OpDelete` のフォルダは、§13.1 の走査で中身を数え、`TotalFiles` と `TotalBytes` を求める。
- `OpTrash` は走査しない（§12.1）。`TotalFiles` はトップレベルの項目数、`TotalBytes` は 0 とする。
- `MethodRename` の項目は、フォルダ同士の衝突がある場合だけ走査する（内側の衝突の検出のため）。バイト数は数えない（データを書かないため）。
  衝突がなければ走査せず、1 項目として数える。実行時に §11.1 のボリューム違いで §11.2 に切り替えた場合は、その時点で走査し、進捗の合計を増やす。
- フォルダ内の走査エラー（読み取り権限がないなど）は `Warnings` に入れ、計画は続ける。
- 計画時の走査結果は、数えることと衝突の検出にだけ使う。実行時はフォルダを改めて列挙する。
  計画後に追加されたエントリも処理し、その衝突は計画後に現れた衝突として扱う（§7.3）。計画後に消えたエントリは何もしない。
- `Dst` が既に存在すれば `Conflict` を作る。衝突ごとに、上書き先の fileID と種類を記録する（§7.3）。
- フォルダ同士の衝突では、中身も走査して内側の衝突を、親の `ConflictID` を `Parent` に入れて加える。
  内側の衝突は、親の決定が `DecisionMerge` のときだけ意味を持つ。

### 6.4 空き容量

- `OpCopy` と `MethodCopyThenRemove` では、書き込むバイト数とコピー先ボリュームの空き容量を比べ、足りなければ `Warnings` に `KindNoSpace` を加える。実行は妨げない。
- 空き容量は、Windows では `GetDiskFreeSpaceEx`、Unix では `statfs` の `Bavail` × ブロックサイズ（macOS は `Bsize`、Linux は `Frsize`）で求める。

---

## 7. 実行（Execute）と結果

### 7.1 実行前の検査

次の場合は何も実行せず error を返す。

- 同じ `Plan` を 2 回実行しようとした（並行して呼ばれた場合を含む）
- 許されない決定がある（§9.1 の表。`Decide` でも検査するが、実行前にもう一度確かめる）
- `Plan` が nil、またはゼロ値（`NewPlan` 以外で作られた）

### 7.2 処理順と失敗時の扱い

- トップレベルの項目を計画の順に 1 件ずつ処理する（並列化しない）。
- 1 件が失敗しても、残りの項目の処理は続ける。
- ただし容量不足（`KindNoSpace`）が起きたら、その後の書き込みを伴う項目（`MethodCopy`・`MethodCopyThenRemove`）はすべて `OutcomeSkipped`（`KindNoSpace`）にする。
  書き込みを伴わない項目（`MethodRename`）は続ける。
- キャンセルされたら、処理中の項目を安全に中断し（§16）、残りを `OutcomeSkipped`（`KindCanceled`）にして `StatusCanceled` で返す。

### 7.3 計画後の変化

- 計画から実行までの間にファイルシステムが変わることを前提にする。
- 「存在確認してから書く」ではなく、OS の排他的な操作（`O_EXCL` での作成、§8.4 の排他リネーム、`os.Mkdir`）で書き込むことで、計画後に現れた衝突を確実に検出する。
- 計画時になかった衝突が見つかったら、その項目を `OutcomeSkipped`（`KindExist`）にする（I1）。
- 上書き（`DecisionOverwrite`）の直前に上書き先を `Lstat` し、計画時に記録した fileID・種類・サイズ・更新日時がすべて一致する場合だけ上書きする。
  fileID だけで判定しないのは、削除と作り直しで同じ番号が再利用されるファイルシステムがあるため（Linux の ext4。V16）。
  一致しなければ計画後に現れた衝突とみなし、`OutcomeSkipped`（`KindExist`）にする（I1）。
  上書き先が消えていれば、衝突なしとして排他リネームで書く。
- マージ（`DecisionMerge`）の直前にも（内側の衝突のマージも含め、マージのたびに）マージ先を `Lstat` し、計画時の fileID と一致するフォルダ（`TypeDir`）であることを確かめる。
  一致しない場合（ファイル・リンク・ジャンクションに置き換えられた場合を含む）は `OutcomeSkipped`（`KindExist`）にする。
  消えていれば、衝突なしとして `os.Mkdir` から始める。
- 計画時にあったコピー元が消えていたら `OutcomeFailed`（`KindNotFound`）。

### 7.4 結果

- `Result.Items` は `Items()` と同じ順・同じ件数とする。フォルダ内で Done 以外になったエントリは `ItemResult.Details` に入れる。
- トップレベルの Outcome は次のとおり。

| 状況 | Outcome | Err |
|---|---|---|
| 完了した | Done | nil |
| `Details` が衝突の決定による Skip だけ（マージ移動で移動元フォルダが残った場合を含む） | Done | nil |
| `Item.Err` がある（`KindTrashUnavailable` を含む） | Failed | `Item.Err` |
| 項目が失敗し、途中までの結果も残らなかった（ファイル・リンク、作成できなかったフォルダ） | Failed | そのエラー |
| 書き込み中に容量不足になった | ファイルは Failed、フォルダは Partial | `KindNoSpace` |
| 衝突の決定による Skip | Skipped | nil |
| 計画後に現れた衝突、複製しないリンク・特殊ファイル（§14.2） | Skipped | 該当する Kind |
| 着手前にキャンセル・容量不足で打ち切られた | Skipped | `KindCanceled` / `KindNoSpace` |
| 処理中にキャンセルされ、途中までの結果が残らなかった | Skipped | `KindCanceled` |
| 処理中にキャンセルされ、途中までの結果が残った（コピー・移動では移動先の一部、完全削除では削除済みの一部） | Partial | `KindCanceled` |
| `Details` に Err 付きのエントリがある | Partial | 最初のエラー |
| 移動元の削除に一部失敗した、一部を保護した、または削除中にキャンセルされた（§13.3） | CopiedSourceKept | 最初のエラー |

- Status: キャンセルされたら `StatusCanceled`。
  それ以外で、Failed・Partial・CopiedSourceKept、または Err 付きの Skipped が 1 件でもあれば `StatusCompletedWithErrors`。それ以外は `StatusCompleted`。

---

## 8. パスの扱い

### 8.1 絶対パスのみ

- `filepath.IsAbs` が偽のパスは `KindInvalidRequest`。受け取ったパスは `filepath.Clean` する。
- Windows では `C:\...` と UNC（`\\server\share\...`）を受け付ける。
  ドライブ相対（`C:foo`）、ルート相対（`\foo`）、呼び出し側が付けた `\\?\` は拒否する。
  デバイスパス（`\\.\`）と NT 形式（`\??\`）、ドライブ指定以外に `:` を含むパス（代替データストリームの指定）も拒否する。
- ボリュームのルート（`C:\`、`\\server\share\`、`/`）は `Sources` に指定できない（`KindInvalidRequest`）。`DestDir` には指定してよい。

### 8.2 Windows のパス（`\\?\` 形式）

- Windows では、fsops の内部で OS に渡すパスを、`os` パッケージの関数に渡すものも含めて、すべて自前の helper で `\\?\` 形式に変換してから使う。
  UNC は `\\?\UNC\server\share\...` の形にする。
- 理由: `\\?\` のないパスは Win32 のパス正規化を受け、末尾の `.` と空白が取り除かれ、`CON` などの予約名がデバイスとして解釈される。
  NTFS 上には WSL などで作られたこうした名前のエントリが存在しうるため、走査で得た名前をそのまま結合すると、別のファイル（`foo.` に対する `foo`）やデバイスを操作してしまう。
  また、`os` パッケージは 248 文字未満のパスには `\\?\` を付けない。
- 呼び出し側から受け取るパスと、結果・エラー・進捗（`Progress.Current`）で返すパスは、`\\?\` の付かない形とする（§8.1）。
- シンボリックリンクのリンク先の文字列は変換しない（§14.2）。
- `\\?\` を受け付けない API（`SHFileOperationW` など）では、長いパスを扱えない場合がある（§12.2、V4）。
- `os` の各関数が `\\?\` 形式のパスを期待どおりに扱えることは V11 で確かめた。

### 8.3 同一性と祖先の判定

- パスの同一性を文字列で比較しない。大文字小文字、NFC/NFD、8.3 形式の短縮名、ジャンクション・`subst` による別名があるため。
- 同一性の判定はすべてパッケージ内の fileID で行う。`os.SameFile` は使わない。
  - Windows: `GetFileInformationByHandleEx` の `FileIdInfo`（ボリュームシリアル番号 64 ビットとファイル ID 128 ビット）。構造体は x/sys にないので自前で定義する。
    `FileIdInfo` が `ERROR_INVALID_PARAMETER` で失敗するボリューム（exFAT・FAT32。V14）では、`GetFileInformationByHandle` のボリュームシリアル番号（32 ビット）とファイルインデックス（64 ビット）を使う。
    どちらの方法で得た値かを fileID に記録し、同じ方法で得た値どうしだけを比べる。
    exFAT・FAT32 のファイルインデックスはディレクトリエントリの位置にもとづくため、別のフォルダへ移すと変わる（V16）。そのため、記録から比較までの間に fsops 自身が移動した項目の照合には使えない。
  - Unix: `Dev` と `Ino`。
  - fileID は記録する時点で確定させる。
  - `os.SameFile` を使わない理由: Windows の `os.SameFile` は、`FileInfo` の取得経路によってはファイル ID を比較時にパスから取り直す（`loadFileId`）ため、計画時の `FileInfo` を実行時に比べると実行時のファイル同士を比べることになる。
    また、比較に使う 64 ビットのファイルインデックスは ReFS（Windows 11 の Dev Drive など）では一意にならない。
- 「`DestDir` がコピー元の内側か」は次の手順で判定する。
  1. `DestDir` の実パスを求める（リンク・ジャンクション・`subst`・8.3 形式の短縮名を解決する）
     - Unix: `filepath.EvalSymlinks`
     - Windows: `DestDir` をリンクを辿って開き、`GetFinalPathNameByHandle`（`VOLUME_NAME_DOS`、失敗したら `VOLUME_NAME_GUID`）で得たパスを使う。
       Go 1.23 以降の `filepath.EvalSymlinks` はジャンクション（マウントポイント）を解決しないので使わない。
  2. その実パスの `DestDir` 自身から始めて親フォルダを順に辿り、各段でコピー元と同じファイルか（fileID）を調べる
  3. 一致すれば `KindDestInsideSource`

### 8.4 排他リネームと置換リネーム

- **排他リネーム**（移動先が存在すれば失敗する。アトミック）:
  - Windows: `MoveFileExW(src, dst, 0)`（`MOVEFILE_REPLACE_EXISTING` を付けない。`MOVEFILE_COPY_ALLOWED` も付けない）
    `dst` が `src` と同じファイルへのハードリンクのときは、`dst` が存在するのに成功して `src` の名前が消える（2026-09-23 の CI で確認）。そのため、呼ぶ前に `dst` の fileID を調べ、`src` と同じファイルで、下の「同じファイルの名前変更」でなければ `KindExist` にする。
  - macOS: `renamex_np(src, dst, RENAME_EXCL)`
  - Linux: `renameat2(..., RENAME_NOREPLACE)`
- **置換リネーム**（ファイル同士の上書き用）: `os.Rename`。フォルダを置き換える用途には使わない。
- **大文字小文字・正規化の違いだけの名前変更**:
  `dst` を `Lstat` して src と同じ fileID で、名前の文字列が異なり、src と dst の親フォルダが同じで、ファイルの場合はリンク数が 1 のときは、「同じファイルの名前変更」とみなして OS の通常のリネームで行う（V1、V2）。
  親フォルダとリンク数の条件は、ハードリンクを名前の違いと取り違えないため。フォルダのリンク数は Unix では 2 以上になるため、条件にしない。
- 排他リネームが「存在する」で失敗した場合は `KindExist`。
- **排他リネームが使えないボリュームでの代わりの手段**（名前を確保してから置き換える）:
  macOS の `RENAME_EXCL` は exFAT では常に `ENOTSUP` になる（V12）。Linux の `RENAME_NOREPLACE` が `EINVAL` を返す場合も同じ扱いにする。
  このときだけ、次の手順で排他リネームの代わりにする（ボリュームごとに結果を覚えてはならない。毎回、排他リネームを先に試す）。
  1. 移動先の名前を確保する。src がファイル・リンク・特殊なファイルなら `dst` を `O_CREAT|O_EXCL|O_WRONLY` で作って閉じる。フォルダなら `os.Mkdir(dst)`。
     「存在する」で失敗したら `KindExist`（排他リネームと同じ）。
  2. `dst` を `Lstat` し、手順 1 で作ったもの（fileID が一致し、ファイルならサイズ 0、フォルダなら空）であることを確かめる。
  3. 確かめられたら、通常の `rename(src, dst)` で置き換える（空のファイル・空のフォルダは `rename` で置き換えられる）。
     確かめられなければ、src には手を付けずに `KindExist` にする。手順 1 で作ったものが残っていれば、fileID が一致する場合だけ消す。
  4. 手順 3 の `rename` が失敗したら、手順 1 で作ったものを（fileID が一致する場合だけ）消す。
  - 残る危険: 手順 2 と 3 の間に、別のプロセスが確保した名前を消して同じ名前で作り直した場合に限り、それを上書きしうる（I1）。
    排他的な操作がないボリュームで許容する唯一の例外として、ここに明記する。手順と動作は V12 の追加確認で確かめた。

### 8.5 名前を変換しない

- fsops はファイル名を正規化しない。コピー先の名前はコピー元の名前をバイト単位でそのまま使う（I6）。
- 衝突の検出は OS の `Lstat` に任せる。APFS は NFC と NFD を同じ名前として扱い、NTFS は別の名前として扱うが、`Lstat` に任せればどちらでも正しく動く。
- 表示・検索のための正規化は UI 側で行う。
- 制限事項（V17）: macOS の exFAT では、NFC の名前で保存されたファイル（Windows などで作られたもの）を `ReadDir` が NFD の名前で返し、その名前では削除できない（`ENOENT`。`Lstat` や読み込みはできる）。
  fsops は名前を変換して探し直さず、その項目を `KindNotFound` の失敗として報告する（データは失われない）。
- macOS が FAT 系のボリュームに作る AppleDouble ファイル（`._名前`）は、ほかのファイルと同じ通常のファイルとして扱う。

---

## 9. 衝突

### 9.1 許される決定

| 衝突の種類 | Skip | Overwrite | AutoRename | Merge |
|---|---|---|---|---|
| ファイル（`TypeFile`）→ 既存のファイル（`TypeFile`） | ○ | ○ | ○ | × |
| フォルダ → 既存フォルダ | ○ | × | ○ | ○ |
| 種類が違う（ファイル ↔ フォルダ、リンクを含む） | ○ | × | ○ | × |
| 自分自身（`Self`） | ○ | × | ○ | × |

`TypeFile` 同士以外の組み合わせ（リンク同士、特殊なファイルを含むもの）は「種類が違う」の行に従う。
`DecisionUnset` はすべての種類で Skip として扱う。表の × は `Decide` が error を返し、実行前の検査でもエラーにする（§7.1）。

### 9.2 自動リネーム

- 形式は `name (2).ext`。使われていれば `(3)`、`(4)`… と増やす。
- 拡張子は最後の `.` 以降。先頭が `.` の名前（`.gitignore`）と拡張子のない名前は、末尾に付ける（`.gitignore (2)`）。フォルダは拡張子を区別しない。
- 元の名前に既に `(2)` などが付いていても解釈しない（`a (2).txt` の次の候補は `a (2) (2).txt`）。
- 候補の名前の確保も排他的に行う。コピーのファイルは一時ファイルの排他リネーム、コピーのフォルダは `os.Mkdir`、コピーのシンボリックリンクは候補名への直接作成（§14.2）、`MethodRename` の移動はファイル・フォルダとも排他リネーム。
  「存在する」で失敗したら次の番号を試す。上限は 9999 回で、見つからなければ `KindExist`。
- 候補の名前が長すぎる場合（名前の長さの上限を超える）は `KindInvalidName` で失敗にする。名前を切り詰めない（I6）。

### 9.3 上書き

- 一時ファイルに書き込んだあと、置換リネームで既存ファイルと入れ替える。
- 上書き先が読み取り専用なら、上書きせず `OutcomeFailed`（`KindReadOnly`）にする。
  読み取り専用とは、Windows では読み取り専用属性、Unix（macOS・Linux）ではオーナーの書き込み権限がないこと、または macOS のロック（`UF_IMMUTABLE`）を指す。
  （Unix の rename はファイル自身の権限を見ないため、両 OS で結果をそろえるために事前に確認する。）
- 上書き先が他のプロセスに使用されていて置き換えられない場合は `KindLocked`。上書き先は元のまま、一時ファイルは削除する。
  Windows の置換リネーム（`MoveFileExW` の `MOVEFILE_REPLACE_EXISTING`）は、上書き先が削除を許さずに開かれているとき `ERROR_ACCESS_DENIED` で失敗する（2026-09-24 の CI で確認）。
  そのため、`ERROR_ACCESS_DENIED` で上書き先が読み取り専用でなければ、上書き先を `DELETE` のアクセス権で開き直し、`ERROR_SHARING_VIOLATION` になれば `KindLocked` とする。
- 上書きされた元のファイルはどこにも退避しない（退避は §21 の将来の検討事項）。

### 9.4 マージ

- 既存のフォルダはそのまま使い、中身を 1 件ずつ処理する。内側の衝突はそれぞれの決定に従う。

---

## 10. コピー

### 10.1 ファイル

1. コピー元を開き、開いたファイルの `Stat` でサイズと更新日時を記録する。
   Unix は `O_NOFOLLOW|O_NONBLOCK` で開き、`fstat` で通常のファイルであることを確かめる（FIFO に置き換えられていた場合に open で止まらないため）。
   走査時の `Lstat` と fileID・種類が違えば `KindSourceChanged` で失敗にする。
2. コピー先のフォルダに一時ファイルを `O_CREATE|O_EXCL|O_WRONLY` で作る。
   名前は `.fsops-<ランダム16進>.tmp` の固定長にする（元の名前を含めると、名前の長さの上限を超えることがあるため）。
   名前が既に使われていれば（`KindExist`）、別の乱数で作り直す。
   作成時のパーミッションは `0o600`（Unix）とし、最終的な権限は手順 6 で設定する（他人が読めないファイルのコピー中に、途中の内容が読めるようにならないため）。
   手順 5・6 で一時ファイルを開き直すときに照合する fileID は、書き込んだ後（閉じる前）に記録する。
   macOS の exFAT・FAT32 では、空のファイルに最初のデータ領域を割り当てると fileID が変わるため（2026-09-24 に `hdiutil` のイメージで確認）。
3. 1 MiB のバッファで内容を書き込む。バッファごとに `ctx` を確認し、進捗を報告する。
4. 同期が必要なら（§10.5）`File.Sync` する。
5. 閉じて検証する（§10.4）。
6. メタデータを設定する（§15）。
7. 最終名にする。衝突なしは排他リネーム、上書きは置換リネーム、自動リネームは §9.2。
8. 2〜7 のどこかで失敗・キャンセルしたら、一時ファイルを削除する（I3）。
   一時ファイルに読み取り専用属性を設定した後で削除する場合は、属性を外してから削除する。
   一時ファイルは fsops が作ったものなので、§13.2 の「属性を勝手に外さない」は適用しない（Windows では読み取り専用のファイルを削除できず、一時ファイルが残るため）。

### 10.2 フォルダ

- コピー先のフォルダを `os.Mkdir` で作る（マージのときは既存のものを使う）。
- 中身を名前順に処理する。ファイルは §10.1、フォルダは再帰、リンクと特殊なファイルは §14。
- フォルダのメタデータ（更新日時・パーミッション・読み取り専用属性）は、中身をすべて処理した後に設定する（§15）。
- 一部のエントリが失敗しても残りは続け、トップレベルの結果を `OutcomePartial` にする。

### 10.3 容量不足

- 書き込み中の容量不足（Windows: `ERROR_DISK_FULL`、`ERROR_HANDLE_DISK_FULL`、Unix: `ENOSPC`、`EDQUOT`）は `KindNoSpace`。
- 処理中の一時ファイルを削除し、§7.2 に従って残りを Skipped にする。
  フォルダの途中で容量不足になったら、そのフォルダ（トップレベルの項目）の残りのエントリも処理しない。処理しなかったエントリは、キャンセルと同じく `Details` に 1 件ずつは入れない。

### 10.4 検証

- `VerifySize`（既定）: 書き込んだバイト数、一時ファイルのサイズ、コピー開始時のコピー元のサイズが一致すること。
  さらに、コピー後にコピー元を `Lstat` し直し、fileID・サイズ・更新日時が開始時から変わっていないこと。
  変わっていたら `KindSourceChanged` で失敗にし、一時ファイルを削除する。
- `VerifyHash`: 上記に加え、読み込み時に計算した SHA-256 と、一時ファイルを読み直して計算した SHA-256 を比べる。
  一致しなければ（書き込んだ内容が一時ファイルに残っていない）`KindUnknown` で失敗にし、一時ファイルを削除する。読み直しの間の進捗は `StageVerify` とする。

### 10.5 同期

- 移動（`MethodCopyThenRemove`）では、移動元を消す前に次を必ず行う。移動元を消した直後に電源が落ちても、移動先にデータと名前が残るようにするため。
  - 各ファイルを、最終名にする前に `File.Sync` する（macOS の Go は `F_FULLFSYNC` を使う）。
  - Unix: 最終名へのリネームやフォルダの作成を行ったフォルダを開いて `Sync` する（ディレクトリエントリの永続化）。
    トップレベルの項目ごとに、移動元を消す前にまとめて行ってよい。
  - Windows: 同じフォルダを `FILE_FLAG_BACKUP_SEMANTICS` で書き込み可能に開いて `FlushFileBuffers` する。失敗しても処理は続け、警告にもしない。
- コピーでは `SyncAlways` のときだけ、同じ手順で同期する（USB メモリなどで遅くなるため）。

---

## 11. 移動と名前の変更

### 11.1 同一ボリューム（`MethodRename`）

- 衝突なし: 排他リネーム。
- 上書き（ファイル同士）: 置換リネーム（§9.3 の事前確認を行う）。
- 自動リネーム: §9.2 の候補名へ排他リネーム。
- マージ（フォルダ同士）: 中身を 1 件ずつ移動する（内側の衝突はそれぞれの決定に従う）。
  最後に移動元のフォルダが空なら、§13.2 のフォルダの削除方法で削除する。空でなければ残して報告する（Outcome は §7.4）。
- トップレベルの項目のリネームがボリューム違いのエラー（Windows: `ERROR_NOT_SAME_DEVICE`、Unix: `EXDEV`）で失敗したら、その項目を §11.2 の方式でやり直す。
  マージの途中で内側のエントリがこのエラーになった場合は、そのエントリを失敗とする。

### 11.2 ボリュームをまたぐ移動（`MethodCopyThenRemove`）

1. §10 の手順でコピーする。同期は必ず行う（§10.5）。コピーしたエントリの一覧を、エントリごとの fileID・種類・サイズ・更新日時とともに記録する。
2. トップレベルの項目の中に、失敗・キャンセル、または決定によらない Skip（計画後に現れた衝突、`LinkSkip`、`KindLinkUnsupported`、`KindUnsupportedType`）が 1 件でもあれば、移動元に一切手を付けない（I2）。
   結果は `OutcomePartial` または `OutcomeFailed`。移動先に途中までコピーされたものは削除せず、そのまま報告する。
   衝突の決定（`DecisionSkip`・`DecisionUnset`）による Skip は妨げにならない。スキップしたエントリはコピーした一覧に入らないので、§13.3 により移動元に残る。
3. 手順 2 に当たらなければ、記録した一覧に沿って移動元を削除する（§13.3）。削除は完全削除（移動先で検証済みのため）。
4. 移動元の削除に一部でも失敗した場合（使用中など）、または §13.3 の照合で削除しなかったエントリがある場合は、`OutcomeCopiedSourceKept` にして、残ったパスを `Details` で報告する。

### 11.3 名前の変更（`Rename`）

- `newName` はフォルダ区切りを含まない名前であること。`.`、`..`、空文字は `KindInvalidName`。
- Windows では、使えない文字（`< > : " / \ | ? *` と制御文字）、末尾の `.` と空白、予約名（`CON`、`PRN`、`AUX`、`NUL`、`COM1`〜`COM9`、`LPT1`〜`LPT9`。拡張子付きも含む）を `KindInvalidName` にする。
- Unix（macOS・Linux）では `/` と NUL 文字を `KindInvalidName` にする。
- 名前の長さの上限を超える場合、ボリュームで使えない名前の場合など、OS が返したエラーは §17 の対応で `KindInvalidName` にする。
- 排他リネームで行う。上書きは一切しない。大文字小文字だけ・正規化だけの違いは §8.4 に従って許可する。
- Windows の exFAT・FAT32 では、大文字小文字だけの変更で `MoveFileExW` が成功を返しても名前が変わらない（V1）。
  そのため、大文字小文字だけ・正規化だけの変更の後は、親フォルダの列挙で新しい名前がバイト単位で現れたことを確かめる。
  現れなければ、同じフォルダ内の一時名（`.fsops-<ランダム16進>.tmp`）へ排他リネームし、続けて一時名から新しい名前へ排他リネームする。
  2 回目が失敗したら一時名から元の名前へ戻し、戻せなければ一時名のパスを `OpError` の `Dest` に入れて返す。

---

## 12. ごみ箱

### 12.1 共通

- `OpTrash` はトップレベルの項目だけを扱う（項目ごとごみ箱へ移すため）。中身は、Windows で §12.2 の最大サイズの事前確認のためにサイズを数える場合だけ、§13.1 の走査（リンクに入り込まない）で読む。変更はしない。
- 1 項目ずつ処理し、結果を項目ごとに返す。
- ごみ箱が使えないと判断したら、その項目には何もせず、`OutcomeFailed`（`KindTrashUnavailable`）にする（I5）。
- `NewPlan` は、ごみ箱が使えるかの事前確認（§12.2 の `GetDriveType` など、ファイルシステムを変更しないもの）を行い、使えない項目の `Item.Err` に `KindTrashUnavailable` を入れる。
  UI は実行前に「この項目はごみ箱に入りません」と示して、完全削除に切り替えるかを利用者に確認できる。`Execute` でも同じ確認をもう一度行う。

### 12.2 Windows

- 事前確認（`NewPlan` と `Execute` の両方で行う。ファイルシステムを変更しない）:
  - `GetVolumePathName` でボリュームのルートを求め、`GetDriveType` が `DRIVE_FIXED` の場合だけごみ箱を使う。
    それ以外（リムーバブル、ネットワーク、`\\server\share` など）は `KindTrashUnavailable`。
    ネットワーク上のパスは、確認なしに完全削除されることを確かめた（V4。`\\localhost\C$` 経由）。
  - パスの長さが 260 文字（`MAX_PATH`）以上なら `KindTrashUnavailable`。`SHFileOperationW` では確認なしに完全削除された（V4）。
    V18 で `IFileOperation` が長いパスを安全に扱えると確認できたら、この制限を見直す。
  - ごみ箱の設定と最大サイズ（V13、V18、V19）: ごみ箱の最大サイズを超える項目は、`IFileOperation` でも確認なしに完全削除される（V18）ため、事前に比べる。
    - グループポリシー（HKCU と HKLM の `Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`）の `NoRecycleFiles` が 1 なら `KindTrashUnavailable`。
    - ボリュームの設定（HKCU の `Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\{ボリューム GUID}`）の `NukeOnDelete` が 1 なら `KindTrashUnavailable`。
    - 最大サイズは、ポリシーの `RecycleBinSize`（ボリュームの容量に対する割合）があればそれを、なければボリュームの設定の `MaxCapacity`（MB）を使う。
      項目のサイズ（ファイルの大きさ。フォルダは中身のファイルの大きさの合計。§12.1）が最大サイズ（MB は 1048576 バイト）を超えるなら `KindTrashUnavailable`。
      最大サイズちょうどは入る。ごみ箱に既にある項目の量は影響しない（V19）。
    - 設定を読めない場合（キーや値がない、ボリューム GUID が取れない）は、分からないものとして `KindTrashUnavailable` にする。
    - 最大サイズを超えるフォルダでは、Windows はフォルダ自体を「入れられる」と通知したあと中身を 1 つずつ完全削除しようとし、その中身の `PreDeleteItem` にはフラグ `0x80` がない（V19）。
      手順 4 の中止により、中身は 1 つも消えずに残る。事前確認の見落としに対する二つ目の防御として扱う。
  - パスに Win32 の正規化で変わる名前（末尾の `.` や空白、予約名）が含まれる場合は `KindTrashUnavailable`。パスの各部分を §11.3 の名前の規則で調べ、さらに `GetFullPathNameW` の結果が元のパスと一致することを確かめる。
    （Windows 11 の `GetFullPathNameW` は、パスの途中の予約名（`CON` など）を変換しないため、`GetFullPathNameW` の比較だけでは見つからない。2026-09-23 の CI で確認。）
    `SHFileOperationW` では `foo.` を指定すると隣の別ファイル `foo` がごみ箱に入った（V4）。`\\?\` 付きのパスは受け付けられなかった。
- 実装: `IFileOperation`（COM）を使う（V13 の結果により、`SHFileOperationW` から移行した）。
  `SHFileOperationW` は、ごみ箱が「すぐに削除する」設定のボリュームと、ごみ箱の最大サイズを超える項目を、成功を返したまま確認なしに完全削除したため（V13）。
  1. `runtime.LockOSThread` した goroutine で `CoInitializeEx(COINIT_APARTMENTTHREADED)` を呼ぶ。
  2. `CoCreateInstance(CLSID_FileOperation)` で `IFileOperation` を作り、`SetOperationFlags` に `FOF_ALLOWUNDO | FOF_NOCONFIRMATION | FOF_SILENT | FOF_NOERRORUI | FOFX_RECYCLEONDELETE | FOF_WANTNUKEWARNING` を設定する。
     `FOF_WANTNUKEWARNING` は、事前確認が見落とした場合の安全装置（黙って完全削除される代わりに、Windows の確認ダイアログが出る。V18）。
     ダイアログは `Execute` を止め、`ctx` のキャンセルでは閉じられない。fsops のテストはダイアログが出る状況を作らない（事前確認で `KindTrashUnavailable` になる状況だけを使う）。
  3. `SHCreateItemFromParsingName` でパスから `IShellItem` を作り、自前の `IFileOperationProgressSink` を付けて `DeleteItem` する。1 回の操作で 1 項目だけ渡す。
  4. 進捗通知の `PreDeleteItem` で、フラグに `TSF_DELETE_RECYCLE_IF_POSSIBLE`（`0x80`）がなければ（ごみ箱に入らず完全削除になる場合）、`E_ABORT` を返して中止させ、その項目を `KindTrashUnavailable` にする（I5。V18 で、中止した項目が残ることを確認済み）。
  5. `PerformOperations` の後、`GetAnyOperationsAborted` と `PostDeleteItem` の結果で成否を決める。`PostDeleteItem` で渡されるごみ箱内の項目からパスが取れれば `TrashedPath` に入れる。
  - COM の vtable の呼び出しと進捗通知の実装は、cgo を使わず `syscall.SyscallN` と `syscall.NewCallback`（または x/sys/windows の同等のもの）で行う。
  - ごみ箱の最大サイズを超える項目は `PreDeleteItem` のフラグでは見分けられない（V18）。事前確認（上記）と `FOF_WANTNUKEWARNING` の併用で対処する（フェーズ3で承認）。
- 分類できない `HRESULT` は `KindUnknown` にして値を `Err` に残す。
- 既存ライブラリ（`hymkor/trash-go`、`rafshawn/go2trash` など）は実装の参考にしてよい。依存に加える場合は許可リストの変更になるので確認を取る。

### 12.3 macOS

- cgo と Objective-C で `NSFileManager` の `trashItemAtURL:resultingItemURL:error:` を呼ぶ（`#cgo LDFLAGS: -framework Foundation`）。autorelease pool で囲む。
- 成功したら、ごみ箱内のパスを `ItemResult.TrashedPath` に入れる。
- ネットワークボリュームなどで失敗した場合、エラーの内容から判断できれば `KindTrashUnavailable`、できなければ `KindUnknown`。
- `darwin && !cgo` のビルドでは常に `KindTrashUnavailable` を返す。

### 12.4 その他の OS

- 常に `KindTrashUnavailable`（Linux の FreeDesktop 方式は §21）。

---

## 13. 完全削除と走査

### 13.1 走査

`os.RemoveAll` や `filepath.WalkDir` の既定の動作に頼らず、自前で走査する。

- リンクを辿らずに列挙する。
  - Unix: `os.ReadDir` と `Lstat`（下記のハンドルで入ったフォルダでは `fstatat(AT_SYMLINK_NOFOLLOW)`）。
  - Windows: フォルダのハンドルに `GetFileInformationByHandleEx` の `FileIdExtdDirectoryInfo` を使い、名前・属性・リパースタグ・ファイル ID・サイズ・更新日時を 1 回の列挙で得る。
    ボリュームシリアル番号はフォルダのハンドルから取る。
    `FileIdExtdDirectoryInfo` が `ERROR_INVALID_PARAMETER` で失敗するボリューム（exFAT・FAT32。V14）では、`FileIdBothDirectoryInfo` に切り替える。
    このとき得られるファイル ID は 64 ビットで、§8.3 の `GetFileInformationByHandle` のファイルインデックスと同じ値になる。ボリュームシリアル番号は、フォルダのハンドルに `GetFileInformationByHandle` を使って得る。
    リパースタグは、属性に `FILE_ATTRIBUTE_REPARSE_POINT` があるときだけ `EaSize` の位置から読む。
- fileID は、計画の走査では衝突先と §8.3 の判定に必要なものだけ取得する。実行時の走査では列挙で得たものを使う。
- 入り込むのは、§14.1 で `TypeDir` と判定したエントリだけ。`TypeSymlink`、`TypeJunction`、`TypeSpecial` には入り込まない（I4）。
- 名前順に処理する。
- 走査は、計画（§6.3）、コピー（§10.2）、移動（§11）、完全削除（§13.2）で使う。
- 削除（§13.2、§13.3）と、同一ボリュームのマージ移動（§11.1）のために入り込むフォルダは、パスで `ReadDir` せず、開いたハンドルで確認してから列挙する。
  `Lstat` でフォルダと判定してから中に入るまでの間に、フォルダ（またはその途中の階層）がリンクに置き換えられても、リンク先に入り込まないようにするため（I4）。
  - Unix: トップレベルのフォルダはパスで、それより下は親フォルダのハンドルからの相対（`openat`）で、`O_RDONLY|O_DIRECTORY|O_NOFOLLOW` を付けて開く。
    `fstat` の fileID が `Lstat` 時と一致することを確かめる。
    中身の削除は `unlinkat`、マージ移動での中身の移動は `renameatx_np` / `renameat2`（どちらも開いたフォルダからの相対）で行う。
  - Windows: `FILE_FLAG_BACKUP_SEMANTICS|FILE_FLAG_OPEN_REPARSE_POINT` で、共有モードに `FILE_SHARE_DELETE` を含めずに開く。
    リパースポイントでないことと、fileID が一致することを確かめる。そのフォルダの処理が終わるまでハンドルを閉じない
    （開いている間、そのフォルダは名前の変更・削除・リンクへの置き換えができない）。フォルダ自体を削除する直前に閉じる。
  - 確認できなければ、そのフォルダには入らず `KindSourceChanged` で失敗にする。

### 13.2 完全削除（`OpDelete`）

- 後順（中身を先、フォルダを後）で削除する。
- 削除は、エントリの種類（§14.1）に応じて次の方法で行う。利用者のファイルの削除に `os.Remove` は使わない。

  | 種類 | Unix | Windows |
  |---|---|---|
  | ファイル・特殊なファイル・ファイル用のシンボリックリンク | `unlink`（§13.1 のハンドルで入ったフォルダの中では `unlinkat(fd, name, 0)`） | `DeleteFileW` |
  | フォルダ | `rmdir`（同 `unlinkat(fd, name, AT_REMOVEDIR)`） | `RemoveDirectoryW` |
  | フォルダ用のシンボリックリンク・ジャンクション（Windows） | — | `RemoveDirectoryW` |

  - Windows のフォルダ用・ファイル用の区別は、エントリの属性（`FILE_ATTRIBUTE_DIRECTORY`）で決める。`TypeSpecial` も同じ。
  - Windows では、パスは §8.2 の helper で `\\?\` 形式にしてから渡す。
  - `os.Remove` を使わない理由:
    - ファイルとしての削除とフォルダとしての削除を両方試すため、判定の後にフォルダがファイルに置き換えられると、そのファイルを消してしまう（逆も同様）。
    - Windows では、読み取り専用のファイルの削除に失敗すると属性を外して削除をやり直し、それも失敗すると属性を外したまま戻さない（Go 1.27 の `os/file_windows.go` で確認）。
  - fsops が作った一時ファイル（§10.1）の削除には `os.Remove` を使ってよい。
- リンクはリンク自体だけが消えることを確認する（V3）。
- フォルダは中身を消した後に削除する（空でなければ失敗するので、消し残しがあれば自然に残る。`KindNotEmpty`）。
- 種類に合わない方法での削除は、何も消さずに失敗する（V3）。判定の後にエントリが置き換えられた場合に誤った分類にしないため、
  削除が `ERROR_ACCESS_DENIED`・`ERROR_DIRECTORY`（Unix では `EISDIR`・`ENOTDIR`・`EPERM`）で失敗したら `Lstat` し直し、種類が変わっていれば `KindSourceChanged` とする。
- 1 件失敗しても残りは続け、トップレベルの結果を `OutcomePartial` にする。
- Windows の読み取り専用ファイルは削除に失敗する（`DeleteFileW` は `ERROR_ACCESS_DENIED` を返し、ファイルと属性は残る。V15 で確認済み）。属性を勝手に外さず `KindReadOnly` として報告する。
- Windows のフォルダの読み取り専用属性は保護を意味しないため、フォルダに限り属性を外してから削除する（読み取り専用属性の付いたフォルダは、空でも `RemoveDirectoryW` が `ERROR_ACCESS_DENIED` で失敗する。V15）。削除に失敗したら属性を元に戻す。

### 13.3 記録した項目だけの削除（移動元の削除）

- §11.2 で記録した一覧のエントリだけを削除する。
- 削除の直前に照合し、記録と一致するエントリだけを削除する。
  照合は、Unix では §13.1 のハンドルからの相対の `fstatat(AT_SYMLINK_NOFOLLOW)`、Windows では祖先のハンドルを開いたままの `Lstat` で行う。
  照合するのは、ファイルとリンクでは fileID・種類・サイズ・更新日時、フォルダでは fileID と種類だけとする（フォルダの更新日時は中身を消すと変わるため）。
  一致しないもの（コピー後に変更・置き換えられたもの）は削除せず、`Details` に `KindSourceChanged` で報告する。
  コピー後・削除前に移動元のファイルが編集・保存された場合に、その変更を失わないため（I2）。
- ファイルとリンクを先に消し、フォルダは深い順に消す。削除の方法は §13.2 と同じ。フォルダは空でなければ残す（コピー中に追加されたファイルがあると空にならないため、そのファイルは残る）。
- フォルダへの入り方は §13.1 に従う。
- Windows では、照合で一致したエントリに読み取り専用属性があれば、属性を外してから削除する。
  移動先には属性を保持した複製があり、利用者は移動を指示しているため。削除に失敗したら属性を元に戻す。macOS のロック（`UF_IMMUTABLE`）は外さない。

---

## 14. リンクと特殊なファイル

### 14.1 種類の判定

- Unix: `Lstat` のモードで判定する。シンボリックリンクは `TypeSymlink`。FIFO・ソケット・デバイスは `TypeSpecial`。
- Windows: Go のモードビットだけに頼らない。Go 1.23 以降、ジャンクションは `ModeSymlink` ではなくなり、シンボリックリンク以外のリパースポイントは `ModeIrregular` になるなど、Go のバージョンで扱いが変わってきたため。次の手順で判定する。
  1. ファイル属性（`FileInfo.Sys()` の `*syscall.Win32FileAttributeData`）に `FILE_ATTRIBUTE_REPARSE_POINT` があるか
  2. あれば、リパースタグを取得する（走査中は列挙で得た `ReparsePointTag`（§13.1）。トップレベルの項目は `FILE_FLAG_OPEN_REPARSE_POINT` で開いて `GetFileInformationByHandleEx` の `FileAttributeTagInfo`）
  3. タグで分類する:
     - `IO_REPARSE_TAG_SYMLINK` → `TypeSymlink`
     - `IO_REPARSE_TAG_MOUNT_POINT` → `TypeJunction`
     - OneDrive などのクラウドファイル（`IO_REPARSE_TAG_CLOUD` 系）と重複除去（`IO_REPARSE_TAG_DEDUP`）→ 通常のファイル・フォルダとして扱う
     - `IO_REPARSE_TAG_WOF`（0x80000017、Windows の透過圧縮。`compact /exe` や CompactOS で圧縮されたファイル）→ 通常のファイルとして扱う
       （WOF のフィルタが動いている通常の状態では、属性に `FILE_ATTRIBUTE_REPARSE_POINT` が現れず、手順 1 で通常のファイルと判定される（2026-09-23 の CI で確認）。この行は、フィルタが働いていない場合のため。）
     - それ以外 → `TypeSpecial`
- クラウドファイルを通常扱いにするのは、OneDrive でリダイレクトされたデスクトップ・ドキュメントを普通に操作できるようにするため。未ダウンロードのファイルは、読み込み時にダウンロードが発生する（V9）。

### 14.2 操作ごとの扱い

| 種類 | コピー（`LinkKeep`） | コピー（`LinkSkip`） | 移動（同一ボリューム） | ごみ箱・完全削除 |
|---|---|---|---|---|
| `TypeSymlink` | リンク先の文字列をそのまま使ってリンクを作る。権限不足なら `KindLinkUnsupported` で失敗 | Skipped（`KindLinkUnsupported`） | リンク自体を移動 | リンク自体だけ |
| `TypeJunction` | 複製しない。Skipped（`KindLinkUnsupported`） | 同左 | リンク自体を移動 | リンク自体だけ |
| `TypeSpecial` | 複製しない。Skipped（`KindUnsupportedType`） | 同左 | そのまま移動 | エントリ自体だけ |

- ボリュームをまたぐ移動は「コピー → 移動元の削除」なので、コピーの列に従う。リンクや特殊なファイルが Skipped になった項目は、移動元を削除しない（§11.2）。
- 相対パスのシンボリックリンクは、リンク先の文字列を書き換えない。
  Windows ではリンク先を `os.Readlink` で読むため、絶対パスのリンク先は `\??\C:\x` の形が `C:\x` の形になる（`CreateSymbolicLink` が同じリンク先として作り直す）。
- シンボリックリンクは一時名を使わず、最終名（自動リネームでは候補名）に直接作る。リンクの作成は不可分で、名前が存在すれば失敗するため、I1・I3 を満たす。
- Windows では、リンクのファイル用・フォルダ用の区別をコピー元のリンクの属性（`FILE_ATTRIBUTE_DIRECTORY`）に合わせる。
  `os.Symlink` はリンク先を調べて区別を決めるため使わず、`CreateSymbolicLink` に `SYMBOLIC_LINK_FLAG_DIRECTORY`（必要な場合）と `SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE` を指定する。

---

## 15. メタデータ

保持するもの（ファイルはデータの書き込み後・最終名にする前に、フォルダは中身の処理後に設定する）:

- 更新日時（`os.Chtimes`。アクセス日時は更新日時と同じ値にする）
- Unix のパーミッション（`0o777` の範囲。setuid・setgid・sticky は保持しない）
- Windows の読み取り専用属性（`os.Chmod`）と隠し属性（`SetFileAttributes`）
- フォルダの更新日時（§10.2）

設定の規則:

- フォルダのパーミッション・読み取り専用属性・更新日時は、中身をすべて処理した後にまとめて設定する（先に `0o555` などを設定すると中身を作れないため）。
- マージで既存のフォルダを使った場合、そのフォルダのメタデータは変更しない。
- キャンセル・容量不足で途中まで処理したフォルダには、メタデータを設定しない（読み取り専用などにすると、残った途中の結果を片付けにくくなるため）。
- シンボリックリンクにはメタデータを設定しない。`os.Chtimes` と `os.Chmod` はリンクを辿り、操作対象でないリンク先を変更してしまうため。

安全上、保持するもの（V6 で読み書きできることを確認済み。実装はフェーズ8）:

- Windows の `Zone.Identifier`（インターネットから取得したことを示す代替データストリーム）。
  コピー元に `\\?\` 形式のパス + `:Zone.Identifier` があれば読み、一時ファイルに同じ内容で書いてから最終名にする。
  コピー先のファイルシステムが代替データストリームに対応しない場合（exFAT・FAT32 では `ERROR_INVALID_NAME`）は、`Warnings` に `KindMetadata` を加える。
- macOS の `com.apple.quarantine`（同様の拡張属性）。`Getxattr` で読み、一時ファイルに `Setxattr`（`XATTR_NOFOLLOW`）してから最終名にする。
  exFAT・FAT32 では AppleDouble ファイル（`._名前`）に保存される。
- シンボリックリンクには付けない。フォルダには付けない。

これらが失われると、ダウンロードしたファイルをコピーした際に Office の保護ビューなどの警告が出なくなるため。

保持しないもの（§21）: ACL、所有者、作成日時、その他の代替データストリーム、その他の拡張属性、リソースフォーク。

メタデータの設定に失敗しても、データが無事なら項目は `OutcomeDone` とし、`Warnings` に `KindMetadata` を加える。

---

## 16. 進捗とキャンセル

```go
type Progress struct {
	Stage      Stage  // §5
	Current    string // 処理中のパス
	DoneFiles  int
	TotalFiles int
	DoneBytes  int64
	TotalBytes int64
}
```

- `Execute` は同期的に動く（呼び出し側が goroutine で動かす）。`Progress` は `Execute` を実行している goroutine から呼ぶ。
- 呼び出しは 100 ミリ秒に 1 回までに間引く。ただし、トップレベルの項目の区切りと終了時には必ず呼ぶ。
- キャンセルは、バッファ（1 MiB）ごとと、エントリごとに `ctx` を確認する。
- キャンセル時:
  - 処理中の一時ファイルを削除する
  - 処理中の移動の項目は、移動元に手を付けない
  - 完了済みの項目はそのまま残す
  - 処理中の項目の Outcome は §7.4 の表に従う（途中までの結果が残らなければ Skipped、残れば Partial）
  - 残りの項目は `OutcomeSkipped`（`KindCanceled`）
- 移動元の削除（§13.3）の途中でキャンセルされたら、削除をそこで止めて `OutcomeCopiedSourceKept`（`KindCanceled`）にする。移動先は完成しているので、データは失われない。
- `StatusCanceled` にするのは、未処理の項目または処理中の項目があるときにキャンセルを検出した場合だけとする。

---

## 17. エラー分類

```go
type Kind int

const (
	KindUnknown Kind = iota
	KindNotFound
	KindExist
	KindPermission
	KindLocked           // 他のプロセスが使用中
	KindReadOnly         // 読み取り専用のファイル・ボリューム、macOS のロック
	KindNoSpace
	KindNotEmpty         // 空でないフォルダを削除できなかった
	KindCrossDevice      // 内部用（移動方式の切り替えに使う）。結果には現れない
	KindLinkUnsupported  // リンクを作れない・扱えない
	KindUnsupportedType  // FIFO、未知のリパースポイントなど
	KindTrashUnavailable
	KindDestInsideSource
	KindSameFile
	KindSourceChanged
	KindInvalidName
	KindInvalidRequest
	KindCanceled
	KindMetadata
)

type OpError struct {
	Op   string // "copy", "rename", "remove" など
	Path string
	Dest string
	Kind Kind
	Err  error  // 元のエラー
}
```

- `OpError` は `Error()` と `Unwrap()` を実装する。`Error()` は英語の技術的な文字列でよい（ログ用）。
- `KindOf(err error) Kind` は `errors.As` で `*OpError` を探し、見つからなければ `KindUnknown` を返す。
  nil の `*OpError`（`ItemResult.Err` が nil のときなど）を `error` として渡した場合も `KindUnknown` を返す。
  `OpError` の `Error()`・`Unwrap()` も nil のレシーバで panic しない。
- UI は `Kind` から日本語のメッセージを作る。fsops はメッセージを作らない。

主な対応:

| Kind | Windows | Unix |
|---|---|---|
| NotFound | `ERROR_FILE_NOT_FOUND`、`ERROR_PATH_NOT_FOUND`、`ERROR_DIRECTORY` | `ENOENT`、`ENOTDIR` |
| Exist | `ERROR_FILE_EXISTS`、`ERROR_ALREADY_EXISTS` | `EEXIST` |
| Permission | `ERROR_ACCESS_DENIED`（読み取り専用でない場合） | `EACCES`、`EPERM` |
| Locked | `ERROR_SHARING_VIOLATION`、`ERROR_LOCK_VIOLATION` | `EBUSY` |
| ReadOnly | `ERROR_ACCESS_DENIED` かつ読み取り専用属性、`ERROR_WRITE_PROTECT` | `EROFS`、`EPERM` かつ `UF_IMMUTABLE` |
| NoSpace | `ERROR_DISK_FULL`、`ERROR_HANDLE_DISK_FULL` | `ENOSPC`、`EDQUOT` |
| NotEmpty | `ERROR_DIR_NOT_EMPTY` | `ENOTEMPTY` |
| CrossDevice | `ERROR_NOT_SAME_DEVICE` | `EXDEV` |
| LinkUnsupported | `ERROR_PRIVILEGE_NOT_HELD`、`ERROR_INVALID_FUNCTION`（リンク作成時） | `EPERM`（リンク作成時。読み取り専用でない場合） |
| InvalidName | `ERROR_INVALID_NAME`、`ERROR_FILENAME_EXCED_RANGE` | `ENAMETOOLONG`、`EILSEQ` |
| SourceChanged | — | `ELOOP`（`O_NOFOLLOW` でリンクに当たった場合） |

- 可能な場合は `errors.Is(err, fs.ErrNotExist)` なども使う。ただしエラー番号の対応を先に調べる（Unix では `ENOTEMPTY` も `fs.ErrExist` に当たるため）。
- 条件付きの対応は、呼び出し側が条件を指定したときだけ適用する。条件を満たさない場合は次のとおり。
  - `ERROR_PRIVILEGE_NOT_HELD`（リンク作成以外）→ Permission
  - `ERROR_INVALID_FUNCTION`（リンク作成以外）→ Unknown
  - `EPERM`（リンク作成以外）→ 読み取り専用なら ReadOnly、そうでなければ Permission
  - `ELOOP`（`O_NOFOLLOW` 以外。リンクの循環など）→ Unknown
  - `ERROR_ACCESS_DENIED`・`EPERM` で、対象が読み取り専用でない場合 → Permission
- `ctx.Err()`（`context.Canceled`、`context.DeadlineExceeded`）は `KindCanceled`。
- `ERROR_ACCESS_DENIED` は原因が複数あるため、対象の属性を調べて ReadOnly か Permission かを決める。

---

## 18. テスト

### 18.1 基本

- 不変条件に関わる処理は、テストを先に書く。
- テスト用フィクスチャは `internal/fsops/internal/testfs` に置く。
  - 構造を指定してツリーを作る
  - ジャンクションを作る（Windows: `cmd /c mklink /J`）
  - シンボリックリンクを作る（権限がなければ Skip）
  - ファイルをロックする（Windows: `CreateFile` を共有モード 0 で開いたままにする）
  - 読み取り専用にする
  - 260 文字を超えるパスを作る
  - 日本語・絵文字・NFD と NFC・大文字小文字だけ違う名前を作る
  - Windows: `\\?\` 経由で、末尾が `.`・空白の名前と予約名のエントリを作る
  - Unix: FIFO を作る（`TypeSpecial` の確認用）
- `t.TempDir()` は `filepath.EvalSymlinks` で正規化してから使う。
- 障害の注入は `ExecOptions` の非公開フィールド `hooks`（例: 書き込みのバッファごとに呼ばれる関数、移動元を消す直前に呼ばれる関数）で行う。
  パッケージレベルの変数にしないので、フックを使うテストも `t.Parallel()` できる。
- キャンセルのテストは、フックの中で `ctx` をキャンセルして時点を決める。スリープで時点を合わせない。

### 18.2 環境変数で有効にするテスト

| 変数 | 意味 | 未設定のとき |
|---|---|---|
| `FSOPS_CROSSVOL_DIR` | テスト用の別ボリューム上のフォルダの絶対パス | ボリュームをまたぐテストを Skip |
| `FSOPS_TEST_TRASH=1` | ごみ箱のテストを実行する | Skip（開発者のごみ箱を汚さないため） |
| `FSOPS_PROBE_EXFAT_DIR` | exFAT のボリューム上のフォルダ（Windows: VHD、macOS: hdiutil のイメージ） | そのボリュームを使うテスト・プローブを Skip |
| `FSOPS_PROBE_FAT32_DIR` | FAT32 のボリューム上のフォルダ（Windows: VHD、macOS: hdiutil のイメージ、Linux: loop マウントした vfat） | 同上 |
| `FSOPS_PROBE_TRASH_NUKE_DIR` | Windows: ごみ箱を「すぐに削除する」設定にしたボリューム上のフォルダ | 同上 |
| `FSOPS_PROBE_TRASH_SMALL_DIR` | Windows: ごみ箱の最大サイズを 1 MB にしたボリューム上のフォルダ | 同上 |

CI ではどちらも設定する（§19）。

### 18.3 手元（macOS）でボリュームをまたぐテストを実行する

```sh
hdiutil create -size 64m -fs APFS -volname fsopstest /tmp/fsopstest.dmg
hdiutil attach /tmp/fsopstest.dmg          # /Volumes/fsopstest にマウントされる
FSOPS_CROSSVOL_DIR=/Volumes/fsopstest go test -p 1 ./internal/fsops/...   # -p 1 は §19 と同じ理由
hdiutil detach /Volumes/fsopstest
```

### 18.4 テスト項目

「共通」は両 OS で実行する。CROSSVOL / TRASH は §18.2 の変数が必要。

| 対象 | テスト内容 | 条件 |
|---|---|---|
| I1 | 計画の後に同名ファイルを作ってから実行 → Skipped（`KindExist`）、既存ファイルの内容は元のまま | 共通 |
| I1 | 決定が未設定の衝突は Skip される | 共通 |
| I1 | 許されない決定（フォルダ同士に Overwrite など）→ Execute が何もせず error | 共通 |
| I2 | ボリュームをまたぐ移動の途中で障害を注入 → 移動元が完全に残る | CROSSVOL |
| I2 | ボリュームをまたぐ移動の途中でキャンセル → 移動元が完全に残る | CROSSVOL |
| I2 | コピー完了後・移動元の削除前に移動元へファイルを追加 → 追加したファイルは消えない | CROSSVOL |
| I2 | コピー完了後・移動元の削除前に移動元のファイルを書き換える → そのファイルは消えない、`OutcomeCopiedSourceKept` | CROSSVOL |
| I2 | ボリュームをまたぐマージ移動で、内側の衝突を Skip に決定 → スキップしたものだけ移動元に残り、ほかは移動される | CROSSVOL |
| I2 | 移動元の削除に失敗（ロック）→ `OutcomeCopiedSourceKept`、移動先は完全 | CROSSVOL・Windows |
| I2 | 移動元の削除中にキャンセル（フックで注入）→ `OutcomeCopiedSourceKept`、移動先は完全 | CROSSVOL |
| I2 | 読み取り専用のファイルをボリュームをまたいで移動 → 移動元が消え、移動先で属性が保持される | CROSSVOL |
| I3 | ファイルの途中でキャンセル（5 MiB 以上のファイル）→ 最終名のファイルも一時ファイルも残らない | 共通 |
| I3 | 書き込み途中に障害を注入 → 同上 | 共通 |
| I4 | 先に目印ファイルを置いたリンク（ジャンクション・シンボリックリンク）を含むツリーを完全削除 → 目印ファイルが残る | 共通（ジャンクションは Windows） |
| I4 | 同上をごみ箱・ボリュームをまたぐ移動で | TRASH / CROSSVOL |
| I4 | リンクを含むツリーのコピー → リンクの先の中身は複製されない | 共通 |
| I4 | 完全削除・同一ボリュームのマージ移動の走査で、フォルダと判定した後・入り込む前にリンクへ置き換える（フックで注入）→ リンク先の目印ファイルが残り、移動もされない | 共通（ジャンクションは Windows） |
| I5 | `\\localhost\C$\...` 経由のパスでごみ箱 → `KindTrashUnavailable`、ファイルは残る | Windows（V7） |
| I5 | `foo.` と `foo` が並ぶフォルダで `foo.` をごみ箱へ → `KindTrashUnavailable`、`foo` は残る | Windows（TRASH） |
| I5 | CGO なしの macOS ビルド・Linux でごみ箱 → `KindTrashUnavailable`（計画時の `Item.Err` と実行結果の両方） | macOS（`CGO_ENABLED=0`）・Linux（§19） |
| I6 | 日本語・絵文字・NFD の名前をコピー・移動 → 名前がバイト単位で一致 | 共通 |
| I6 | 大文字小文字だけ違う名前への `Rename`（ファイル・フォルダ） | 共通（V1、V2） |
| 衝突 | 自動リネームの連番（通常、`(3)` 以降、`.gitignore`、拡張子なし、フォルダ） | 共通 |
| 衝突 | 同じフォルダへのコピー（`Self`）を自動リネームで複製 | 共通 |
| 衝突 | マージ（内側の衝突の決定がそれぞれ反映される） | 共通 |
| 計画 | コピー先がコピー元の内側（リンク経由を含む）→ `KindDestInsideSource` | 共通 |
| 計画 | コピー先がジャンクション経由でコピー元の内側 → `KindDestInsideSource` | Windows |
| 計画 | 計画後に上書き先を別のファイルに置き換えてから実行 → Skipped（`KindExist`）、置き換えたファイルは元のまま | 共通 |
| 計画 | 計画後にマージ先を別の場所へのリンクに置き換えてから実行 → Skipped（`KindExist`）、リンク先に何も書かれない | 共通（ジャンクションは Windows） |
| 計画 | 重複・入れ子の `Sources` → `KindInvalidRequest` | 共通 |
| 計画 | ボリュームのルート、`\\.\` 形式の `Sources` → `KindInvalidRequest` | 共通・Windows |
| 計画 | 計画の作成前後でファイルシステムが変化しない | 共通 |
| 計画 | 同じ Plan を 2 回・並行して Execute → 2 回目は何もせず error | 共通 |
| パス | 相対パス、ドライブ相対パス（`C:foo`）、`\\?\` 付きの拒否 | 共通・Windows |
| パス | 260 文字を超えるパスのコピー・移動・完全削除 | Windows |
| パス | 末尾が `.`・空白の名前、予約名（`CON`）を含むツリーのコピー・移動・完全削除 → 同名の別ファイル（`foo`）に影響しない | Windows（V11） |
| ロック | コピー元が共有なしで開かれている → その項目は `KindLocked`、他の項目は続行 | Windows |
| ロック | 上書き先が使用中 → `KindLocked`、上書き先は元のまま、一時ファイルなし | Windows |
| 読み取り専用 | 読み取り専用の上書き先 → `KindReadOnly`（両 OS で同じ結果） | 共通 |
| 読み取り専用 | 読み取り専用ファイルのコピー → 属性が保持される | 共通 |
| 読み取り専用 | 読み取り専用ファイルを含むツリーの完全削除 → そのファイルは `KindReadOnly` で残り、読み取り専用属性も残る | Windows |
| 削除 | 削除の直前（フックで注入）にフォルダをファイルに置き換える → そのファイルは消えない | 共通 |
| メタデータ | ファイル・フォルダの更新日時が保持される | 共通 |
| メタデータ | リンクを含むツリーのコピー → リンク先の更新日時・権限が変わらない | 共通 |
| メタデータ | `0o600` のファイルのコピー中（フックで停止）に、一時ファイルの権限が `0o600` である | Unix |
| メタデータ | 読み取り専用のフォルダ（`0o555`）を含むツリーのコピー → 中身も含めて複製され、フォルダの権限が保持される | 共通 |
| リンク | Windows でフォルダ用のシンボリックリンク（リンク先がコピー先にない相対リンク）をコピー → フォルダ用のまま | Windows |
| リンク | シンボリックリンクの作成が `ERROR_PRIVILEGE_NOT_HELD` で失敗する（フックで注入。CI のランナーは昇格済みで権限不足を再現できないため。V8）→ その項目は `KindLinkUnsupported`、ほかは続行 | 共通 |
| 検証 | コピー中にコピー元が変更された → `KindSourceChanged`、一時ファイルなし | 共通 |
| 容量 | 空き容量不足の見込みが `Warnings` に入る | CROSSVOL |
| 容量 | 書き込み中の容量不足 → `KindNoSpace`、残りは Skipped | CROSSVOL（他のテストと並行実行しない） |
| 容量 | 容量不足の後も、同じボリュームへの移動（`MethodRename`）の項目は続行される | CROSSVOL |
| ごみ箱 | ごみ箱に入り、元の場所から消えている（Windows: `$I` ファイル、macOS: `TrashedPath`） | TRASH（V5、V10） |
| I5 | ごみ箱が「すぐに削除する」設定のボリューム、ごみ箱の最大サイズを超える項目 → `KindTrashUnavailable`、ファイルは残る | Windows（TRASH、`FSOPS_PROBE_TRASH_NUKE_DIR`・`FSOPS_PROBE_TRASH_SMALL_DIR`。V13、V18） |
| I5 | 260 文字以上のパスでごみ箱 → `KindTrashUnavailable`、ファイルは残る | Windows（TRASH。V4） |
| I1 | 排他リネームの代わりの手段（§8.4）: macOS の exFAT へのコピー・移動・`Rename` ができ、既存のファイルは上書きされない | macOS（`FSOPS_PROBE_EXFAT_DIR`。V12） |
| I6 | exFAT・FAT32 での大文字小文字だけの `Rename` → 名前が実際に変わる | Windows（`FSOPS_PROBE_EXFAT_DIR`・`FSOPS_PROBE_FAT32_DIR`。V1） |
| 同一性 | exFAT・FAT32 で fileID が取れ（§8.3 の代わりの方法）、走査（§13.1 の代わりの列挙）の ID と一致する | Windows（V14、V16） |
| 並行性 | 進捗コールバックまわりにデータ競合がない | macOS（`-race`） |

---

## 19. CI

`.github/workflows/test.yml` の方針:

- トリガー: `push`、`pull_request`、`workflow_dispatch`。
  リポジトリが非公開なら、macOS のジョブは `pull_request` と `workflow_dispatch` のときだけ実行する（macOS ランナーは料金が高く、開発者は macOS でテストを実行できるため）。公開なら両方とも毎回実行する。
- `actions/checkout` と `actions/setup-go` は、作成時点の最新メジャーバージョンを確認して使う。Go は `go-version-file: go.mod`。
- **windows ジョブ（`windows-latest`）**
  1. diskpart で 64 MB の VHD を作成・アタッチしてフォーマットし、ドライブ文字を割り当てる。
     `T:` NTFS（ボリュームをまたぐテスト）、`U:` exFAT、`V:` FAT32、`W:` NTFS（ごみ箱を「すぐに削除する」設定）、`X:` NTFS（ごみ箱の最大サイズ 1 MB）。
     `W:`・`X:` の設定は HKCU の `Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\{ボリューム GUID}` の `NukeOnDelete`・`MaxCapacity` で行い、C: と T: の設定は変えない。
  2. `FSOPS_CROSSVOL_DIR`、`FSOPS_TEST_TRASH=1`、§18.2 の `FSOPS_PROBE_*` を `GITHUB_ENV` に設定する
  3. `go vet ./...`
  4. `go test -p 1 ./...`（`-p 1` は、別ボリュームを一杯にするテスト（§18.4「容量」）を、ほかのパッケージのテストと並行させないため）
- **macos ジョブ（`macos-latest`）**
  1. `hdiutil` で 64 MB の APFS イメージを作成してマウントする（§18.3 と同じ）。さらに exFAT と FAT32 のイメージを作ってマウントする
  2. `FSOPS_CROSSVOL_DIR=/Volumes/fsopstest`、`FSOPS_TEST_TRASH=1`、`FSOPS_PROBE_EXFAT_DIR`、`FSOPS_PROBE_FAT32_DIR` を設定する
  3. `go vet ./...`
  4. `go test -race -p 1 ./...`
  5. `CGO_ENABLED=0 go vet ./...` と、ごみ箱が使えないことを確かめるテスト（§18.4 の I5 の行）の `CGO_ENABLED=0` での実行
- **ubuntu ジョブ（`ubuntu-latest`、公開・非公開に関わらず毎回実行）**
  1. `gofmt -l .` の出力が空であること
  2. `go vet ./...`、`GOOS=windows go vet ./...`、`GOOS=darwin CGO_ENABLED=0 go vet ./...`
  3. ごみ箱が使えないことを確かめるテスト（§18.4 の I5 の行）の実行（フェーズ10で追加）
  4. 64 MB の vfat のイメージを `sudo mount -o loop` でマウントして `FSOPS_PROBE_FAT32_DIR` に設定し、プローブ（`internal/probe`）を実行する
- 要検証事項のプローブは、結果をログに残すため、各ジョブで `go test -v ./internal/fsops/internal/probe/` を別の手順として実行する。
- VHD とディスクイメージの作成はフェーズ2で追加する。フェーズ1では `go vet`・`go test` と ubuntu ジョブだけ。

---

## 20. 要検証事項

以下は仕様作成時点で確証がない。推測で確定させず、確かめるテストを書いて CI の結果を報告し、それに基づいて方針を決める。結果はこの節に追記する。

- **V1** Windows: `MoveFileExW(src, dst, 0)` で、大文字小文字だけ違う名前への変更が成功するか。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** NTFS では大文字小文字だけの変更が成功し、名前が変わる。exFAT・FAT32 では成功を返すが名前は変わらない（§11.3 で対処）。
    移動先が既にあれば、NTFS・exFAT・FAT32 とも `ERROR_ALREADY_EXISTS`（183）で失敗し、移動先は変わらない。NTFS では NFC と NFD は別の名前として扱われる。
- **V2** macOS（APFS）: `renamex_np(RENAME_EXCL)` で、大文字小文字だけ・NFC/NFD だけ違う名前へ変更したときの動作。`golang.org/x/sys/unix` に `RenamexNp` があるか。
  （x/sys v0.38.0 に `unix.RenamexNp` と `RENAME_EXCL` があることはソースで確認済み。動作は未確認。）
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** APFS（一時フォルダ・`hdiutil` のイメージ）で、大文字小文字だけ・NFC/NFD だけの変更が成功し、新しい名前がバイト単位で残る。移動先が既にあれば `EEXIST`。
- **V3** Windows: 使用する Go のバージョンで、ジャンクションとディレクトリのシンボリックリンクが `Lstat` でどう見えるか（`ModeSymlink` / `ModeIrregular` / `ModeDir`）。§13.2 の削除方法（`RemoveDirectoryW`）でリンク自体だけが消え、リンク先の中身が残ること。
  - **結果（2026-09-23、Go 1.27.1、windows-latest）:** ジャンクションは `Lstat` で `ModeIrregular`（`IsDir()` は false、`ModeSymlink` なし）、フォルダ用・ファイル用のシンボリックリンクは `ModeSymlink`。§14.1 の属性とタグによる判定を維持する。
    `RemoveDirectoryW`（ジャンクション・フォルダ用のリンク）と `DeleteFileW`（ファイル用のリンク）でリンク自体だけが消え、リンク先の中身は残った。
    種類に合わない関数では何も消えずに失敗した（ジャンクションに `DeleteFileW` → `ERROR_ACCESS_DENIED`、ファイル用のリンクに `RemoveDirectoryW` → `ERROR_DIRECTORY`）。
- **V4** Windows: `SHFileOperationW` の動作。`MAX_PATH` を超えるパス、`\\?\` 付きのパス、固定ドライブ以外のパスでどうなるか（特に、黙って完全削除されないか）。goroutine から呼ぶ際に `runtime.LockOSThread` と `CoInitializeEx` が必要か。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** 固定ドライブ（NTFS・exFAT・FAT32 の VHD）ではごみ箱に入る（exFAT・FAT32 では `$Recycle.Bin` の直下）。
    260 文字を超えるパス（`\\?\` なし）と `\\localhost\C$` 経由のパスは、戻り値 0 のまま確認なしに完全削除された。`\\?\` 付きのパスは `0x7c` で失敗し、何も起きない。
    `foo.`・`foo ` を指定すると、隣の別ファイル `foo` がごみ箱に入った。goroutine から、`LockOSThread`・`CoInitializeEx` なしで呼んでも動作した。
    → §12.2 の事前確認（固定ドライブのみ、260 文字以上を拒否、`GetFullPathNameW` の一致）を維持・追加した。
- **V5** macOS の CI: `trashItemAtURL` が CI 上で成功するか。返されたパスを `Lstat` できるか（プライバシー保護による制限の有無）。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** CI 上で成功し、返されたパス（`~/.Trash/…`、イメージ上では `/Volumes/…/.Trashes/501/…`）を `Lstat` と読み込みで確かめられた。
    シンボリックリンクはリンク自体だけが入り、リンク先は残る。exFAT・FAT32 のイメージでも成功する。ネットワークボリュームは未確認。
    （プローブは `_test.go` で cgo を使えないため、同じ API を呼ぶ Objective-C の小さなプログラムを `clang` でビルドして使った。）
- **V6** Windows の `Zone.Identifier`（`path:Zone.Identifier`）を `os` で読み書きできるか。macOS の `com.apple.quarantine` を `golang.org/x/sys/unix` の `Getxattr` / `Setxattr` で読み書きできるか。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** Windows の `Zone.Identifier` は、NTFS なら `\\?\` 付き・なしとも `os` で読み書きでき、同じボリューム内のリネームで一緒に移る。exFAT・FAT32 では `ERROR_INVALID_NAME`（123）で書けない。
    macOS の `com.apple.quarantine` は APFS・exFAT・FAT32 とも `Getxattr` / `Setxattr` で読み書きできる（FAT 系では `._名前` が作られる）。`XATTR_NOFOLLOW` でリンク先に付かない。→ §15 で保持する。
- **V7** CI: Windows ランナーで diskpart による VHD の作成・マウントができるか。macOS ランナーで `hdiutil attach` ができるか。Windows ランナーで `\\localhost\C$` にアクセスでき、`GetDriveType` が `DRIVE_REMOTE` を返すか。
  - **結果（2026-09-23）:** diskpart で 64 MB の VHD を作成・アタッチでき、NTFS・`DRIVE_FIXED`・C: と別のシリアル番号になった。macOS では `hdiutil attach` で APFS のイメージをマウントできた。
    ボリュームをまたぐ `os.Rename` は Windows で `ERROR_NOT_SAME_DEVICE`、macOS で `EXDEV` になる。
    `\\localhost\C$\...` 経由で読み取りができ、`GetVolumePathName` のルートに対する `GetDriveType` は `DRIVE_REMOTE`（`\\?\UNC\` 形式でも同じ）。ボリュームシリアル番号は C: と同じ。
- **V8** Windows ランナーでシンボリックリンクの作成権限があるか。`mklink /J` が使えるか。
  - **結果（2026-09-23）:** ランナーは昇格済み（High Mandatory Level）。`SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE` の有無にかかわらず `CreateSymbolicLink` が成功し、`os.Symlink` と `mklink /J` も使える。
    リンクを使うテストは Windows の CI で Skip されずに実行される。権限不足（`KindLinkUnsupported`）は CI で再現できないため、フックで注入して確かめる（§18.4）。
- **V9** OneDrive の未ダウンロードファイルの扱い。CI では確認できないため、実機（仮想マシンの Windows など）での確認項目として記録するだけにする。
- **V10** Windows の `$Recycle.Bin\<SID>\` にある `$I` ファイルの形式（先頭から、版番号 8 バイト、元のサイズ 8 バイト、削除日時 8 バイト、版 2 ではパスの文字数 4 バイト、UTF-16 の元のパス）が想定どおりか。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** 想定どおり（版 2）。フォルダのサイズは中身の合計。`$R` に内容がある。
- **V11** Windows: 末尾が `.` や空白の名前、予約名（`CON` など）のエントリを `\\?\` 経由で作り、走査・コピー・移動・完全削除が同名の別ファイルに影響しないこと。
  `os` の各関数（`Lstat`、`ReadDir`、`Mkdir`、`Remove`、`Rename`、`Chtimes`、`Readlink`）が `\\?\` 形式のパスをそのまま扱えること（§8.2）。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** `\\?\` 付きのパスで、`Lstat`・`ReadDir`・`Mkdir`・`Remove`・`Rename`・`Chtimes`・`Readlink` が `foo.`・`foo `・`CON` を正しく扱い、`foo` に影響しない。`\\?\` なしの `Lstat(foo.)` は `foo` を返した。
- **V12** macOS: `renamex_np(RENAME_EXCL)` が APFS 以外（`hdiutil` で作る exFAT・FAT32 のイメージ、可能なら SMB）で使えるか。
  使えない場合の方式は結果を見て決める。Linux の `RENAME_NOREPLACE` が `EINVAL` を返す場合も同じ方針で扱う（§8.4）。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** macOS の FAT32 では `RENAME_EXCL` が使える。exFAT では新しい名前への変更でも常に `ENOTSUP`（移動先が既にあれば `EEXIST`）。
    Linux の `RENAME_NOREPLACE` は ext4 と vfat で使える（vfat では大文字小文字だけの変更が `EEXIST`）。SMB は CI で用意できず未確認。
    → §8.4 に「名前を確保してから置き換える」代わりの手段を加えた。
  - **追加確認（2026-09-23、macos-latest）:** exFAT で §8.4 の代わりの手段（手順 1〜4）が期待どおりに動いた。新しい名前へのファイル・中身のあるフォルダの変更が成功し、
    既存の移動先（ファイル・フォルダ）は手順 1 の `EEXIST` で失敗して上書きされない。大文字小文字だけの変更は、§8.4 の「同じファイルの名前変更」として通常の `rename` で成功した。
- **V13** Windows: ごみ箱の最大サイズを超える項目、およびボリュームのごみ箱が「ごみ箱にファイルを移動しないで、削除と同時にファイルを消去する」設定のときに、
  `SHFileOperationW`（`FOF_ALLOWUNDO | FOF_NOCONFIRMATION`）が確認なしに完全削除するか。
  完全削除する場合は、`IFileOperation` の進捗通知（`PreDeleteItem` のフラグ `TSF_DELETE_RECYCLE_IF_POSSIBLE`）で完全削除になる項目を中止する方式（§21 の移行を前倒しする）と、
  設定の事前確認のどちらにするかを、結果を見て決める（§12.2）。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** `SHFileOperationW`（`FOF_ALLOWUNDO | FOF_NOCONFIRMATION`）は、「すぐに削除する」設定のボリューム（`NukeOnDelete=1`）と、最大サイズ（1 MB）を超える項目（4 MiB）を、戻り値 0・`fAnyOperationsAborted` 偽のまま確認なしに完全削除した。
    最大サイズ以内の項目はごみ箱に入った。→ `IFileOperation` と `PreDeleteItem` による方式に移行する（§12.2）。その動作は V18 で確かめる。
- **V14** Windows: `FileIdExtdDirectoryInfo` による列挙が NTFS・exFAT・FAT32 の VHD で使えるか。得られるファイル ID・リパースタグが `FileIdInfo`・`FileAttributeTagInfo` と一致するか。
  使えないファイルシステムでは `FileIdBothDirectoryInfo` などに切り替えるかを、結果を見て決める（§13.1）。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** NTFS では使え、ファイル ID・リパースタグが `FileIdInfo`・`FileAttributeTagInfo` と一致する。
    exFAT・FAT32 では `FileIdExtdDirectoryInfo` も `FileIdInfo` も `ERROR_INVALID_PARAMETER` で失敗する。`FileIdBothDirectoryInfo` は使え、その 64 ビットのファイル ID は `GetFileInformationByHandle` のファイルインデックスと一致する。
    → §8.3・§13.1 に代わりの方法を加えた。
- **V15** Windows: 読み取り専用属性の付いた空のフォルダを `RemoveDirectoryW` で削除できるか。読み取り専用属性の付いたファイルに `DeleteFileW` が `ERROR_ACCESS_DENIED` を返し、ファイルと属性がそのまま残るか（POSIX 形式の削除を使う新しい Windows でも同じか）（§13.2）。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** 読み取り専用属性の付いたフォルダは、空でも `RemoveDirectoryW` が `ERROR_ACCESS_DENIED` で失敗する。
    読み取り専用属性の付いたファイルは `DeleteFileW` が `ERROR_ACCESS_DENIED` で失敗し、ファイルと属性が残る。→ §13.2 を確定した。
- **V16** fileID の取得可否と安定性（V14 の確認中に見つかった）。ボリュームの種類ごとに、fileID が取れるか、名前の変更・別フォルダへの移動・書き換え・削除して同じ名前で作り直した前後で変わるか。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** NTFS・APFS・macOS の exFAT/FAT32・Linux の vfat では、名前の変更・移動・書き換えで変わらず、作り直すと変わる。
    Windows の exFAT・FAT32 では `FileIdInfo` が取れず、`GetFileInformationByHandle` のファイルインデックスは同じフォルダ内の名前の変更では変わらないが、別のフォルダへ移すと変わる。
    Linux の ext4 では、削除して作り直すと同じ inode 番号が再利用された。→ §7.3 の照合を強め、§8.3 に注意を加えた。
- **V17** APFS・NTFS 以外での名前の扱い（V12 の確認中に見つかった）。NFC・NFD の名前がどう列挙されるか、列挙された名前で `Lstat`・削除ができるか。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）・macos-latest・ubuntu-latest、Go 1.27.1）:** macOS の exFAT では、NFC の名前で作ったファイルを `ReadDir` が NFD の名前で返し、その名前での削除は `ENOENT`（`Lstat` はできる。作ったときの名前でなら削除できる）。
    macOS の FAT32 も NFD の名前で返すが、その名前で削除できる。→ §8.5 に制限事項として記録した。
- **V18** Windows: `IFileOperation`（§12.2）の動作。
  通常の固定ドライブで `PreDeleteItem` のフラグに `TSF_DELETE_RECYCLE_IF_POSSIBLE` があるか。
  「すぐに削除する」設定のボリュームと、ごみ箱の最大サイズを超える項目で、そのフラグがないか。`PreDeleteItem` で中止した項目が完全に残るか。
  `PostDeleteItem` でごみ箱内の項目のパスが取れるか。260 文字を超えるパス、`\\?\` 付きのパス、`\\localhost\C$` 経由のパス、`foo.` を `SHCreateItemFromParsingName` に渡したときの動作。
  STA での初期化と `LockOSThread` が必要か。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100）、Go 1.27.1）:** `PreDeleteItem` のフラグは、ごみ箱に入れられる場合 `0x282`、入れられない場合 `0x202`（`TSF_DELETE_RECYCLE_IF_POSSIBLE` は `0x80`）。
    「すぐに削除する」設定のボリューム、`\\localhost\C$` 経由のパス、260 文字を超えるパスではこのフラグがなく、`E_ABORT` を返すと `PerformOperations` も `E_ABORT` になり、項目は残った。
    通常の固定ドライブ（NTFS・exFAT・FAT32）ではごみ箱に入り、`PostDeleteItem` の `psiNewlyCreated` から `$Recycle.Bin` 内のパスが取れた。
    **ごみ箱の最大サイズを超える項目では、フラグが「入れられる」（`0x282`）のまま完全削除された**（`PostDeleteItem` の `psiNewlyCreated` は NULL）。`PreDeleteItem` では防げない。
    `FOF_WANTNUKEWARNING` を付けると、完全削除の前に確認のダイアログが出て止まった（30 秒で強制終了。項目は残った）。
    `\\?\` 付きのパスは `SHCreateItemFromParsingName` が `E_INVALIDARG`。`foo.` は `foo` と解釈され、隣の `foo` がごみ箱に入った。
    COM は STA・MTA・初期化なしのいずれでも動作した。
    → 最大サイズを超える項目は、事前確認と `FOF_WANTNUKEWARNING` の併用で対処する（§12.2。事前確認の詳細は V19）。
- **V19** Windows: §12.2 のごみ箱の設定と最大サイズの事前確認が、実際の動作を正しく予測するか。
  レジストリ（ボリュームの設定・グループポリシー）が読めるか。最大サイズの境界（ちょうど・1 バイト超・割り当て単位での切り上げ）、フォルダ（中身の合計）、
  ごみ箱に既に項目がある場合（古い項目が消されて入るのか、完全削除されるのか）の動作。
  - **結果（2026-09-23、windows-latest（Windows 11 build 26100））:** レジストリの `BitBucket\Volume\{GUID}` は、テスト用の VHD（`MaxCapacity` は 5〜6 MB）と C:（9699 MB）のどれにもあり、読めた。ランナーにグループポリシーの設定はない。
    最大サイズ 1 MB（1048576 バイト）のボリュームで、1048576 バイトまでのファイルは入り、1048577 バイトのファイルは確認なしに完全削除された（ファイルの大きさで決まる）。
    ごみ箱の使用量が最大サイズを超えていても（12 MB）、最大サイズ以下の新しい項目は入り、古い項目は消されなかった。
    800000 バイトのフォルダは入った。1200000 バイトのフォルダは、Windows が中身を 1 つずつ完全削除しようとし、最初の中身の `PreDeleteItem`（フラグ `0x2`）で中止されて、中身を含めてすべて残った。
    §12.2 の事前確認（最大サイズを超えるなら `KindTrashUnavailable`）の予測は、すべての場合で実際の動作と一致した。→ §12.2 を確定した。

- **V20** シンボリックリンクを作れないボリューム（exFAT・FAT32・vfat）でシンボリックリンクを作ったときに返るエラー番号（§14.2、§17）。
  §17 では Unix に `KindLinkUnsupported` の対応がなく、`EPERM` などは `KindPermission` になる。対応を加えるかを、結果を見て決める。
  - **結果（2026-09-24、windows-latest・macos-latest・ubuntu-latest、Go 1.27.1）:** Windows の exFAT・FAT32 では `ERROR_INVALID_FUNCTION`（1）、Linux の vfat では `EPERM` で失敗した。
    macOS の exFAT・FAT32（`hdiutil` のイメージ）では作成できた。→ リンクの作成時に限り、どちらも `KindLinkUnsupported` にする（§17）。

---

## 21. 今回やらないこと（将来の検討事項）

- 並列コピー
- APFS の `clonefile`、Windows の `CopyFileEx` などの OS 固有の高速コピー
- 上書きされるファイルをごみ箱へ退避するオプション
- 元に戻す（Undo）
- ACL・所有者・作成日時の保持
- シンボリックリンク自体の更新日時の保持
- ジャンクションの複製
- 読み取り専用属性を外して削除するオプション
- Linux のごみ箱（FreeDesktop.org Trash 仕様）

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
- フォルダの作成（§11.4）
- 一覧のための、リンクを辿らない調べ・列挙（§14.3）。並べ替え・隠しファイルの扱い・表示は UI 側の責務

扱わないもの:

- フォルダ内容の一覧の表示・並べ替え・検索（UI 側の責務。調べ・列挙の基本操作だけを §14.3 で提供する）
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
    eintr_unix.go             //go:build unix。EINTR でのやり直し（§4 のシステムコールのルール）
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
    list.go                   一覧のための調べ・列挙（Lstat・ReadDir・Readlink。§14.3）
    mkdir.go                  フォルダの作成（Mkdir。§11.4）
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

システムコールのルール（Unix）:

- `golang.org/x/sys/unix` の関数を直接呼ぶときは、`eintr_unix.go` の `ignoringEINTR`（値を返すものは `ignoringEINTR2`）で包み、`EINTR` が返れば同じ引数でやり直す。
  Go のランタイムはシグナルハンドラを `SA_RESTART` で入れるが、SMB・NFS・FUSE などでは遅いシステムコールが `EINTR` で返ることがある。`os` パッケージは内部でやり直すが、`x/sys/unix` はやり直さない。
  `EINTR` は何も実行されなかったことを表すので、やり直しは I1〜I7 に影響しない（排他リネーム・`O_EXCL`・`mkdirat` をやり直して `EEXIST` になれば、衝突・失敗として安全側に扱われる）。
- ただし `unix.Close` は包まない（Linux では `EINTR` が返っても fd は閉じられており、やり直すと別の fd を閉じうる）。
- 値を変換するだけの関数（`unix.TimeToTimespec`・`unix.ByteSliceToString` など）は包まなくてよい。
- `os.File` を通す読み書き・列挙・同期は `os` がやり直すので、包まない。
- `eintr_test.go` は、`internal/fsops` のテスト以外の Go ファイル（ビルド条件にかかわらずすべて）を構文解析し、包まれていない `unix` の関数の呼び出しと、包まれた `unix.Close` があればテストを失敗させる。

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
	Method      Method          // 実際に使った方式（§11.1 でボリュームをまたぐ移動に切り替えた項目は MethodCopyThenRemove）。§7.4 の Outcome の意味を決める
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
	OutcomeTrashUnconfirmed // ごみ箱: 元の場所から消えたが、ごみ箱に入ったことを確かめられなかった（§12.1）
)

// ---- 名前の変更とフォルダの作成 ----

// Rename は path の名前を newName に変える。上書きは一切しない（§11.3）。
func Rename(path, newName string) error

// Mkdir は、フォルダ parent の中に、名前 name のフォルダを作る。上書きは一切しない（§11.4）。
func Mkdir(parent, name string) error

// ---- 調べる・列挙する（一覧のため。§14.3） ----

// Entry は、リンクを辿らずに調べた 1 つのエントリ。
type Entry struct {
	Name     string    // 列挙で得た名前（バイト列をそのまま。I6。UI はこれからパスを作る）
	Info     EntryInfo // 種類・サイズ・更新日時（§14.1 の判定）
	Hidden   bool      // Windows: FILE_ATTRIBUTE_HIDDEN。macOS: UF_HIDDEN。名前の . による判断は UI が行う
	ReadOnly bool      // Windows: ファイルの読み取り専用属性（フォルダの属性は保護を意味しないので見ない）。
	                   // Unix: オーナーの書き込み権限がない。macOS はロック（UF_IMMUTABLE）も含む
	Err      *OpError  // 列挙はできたが調べられなかった（Unix の fstatat の失敗）。Info・Hidden・ReadOnly は使えない
}

// Lstat は path をリンクを辿らずに調べる。
func Lstat(path string) (Entry, error)

// ReadDir は、フォルダ dir の中身をリンクを辿らずに列挙し、名前のバイト順で返す（§13.1 と同じ方法）。
// dir 自体がリンク・ジャンクションなら、そのリンク先を列挙する（利用者がリンクに入った場合）。
// AppleDouble の付属（§8.5）は含めない。
func ReadDir(dir string) ([]Entry, error)

// Readlink は、シンボリックリンク・ジャンクション path のリンク先を、書き換えずに返す（表示用）。
func Readlink(path string) (string, error)

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
中断したら `KindCanceled` のエラーを返し、計画は返さない（項目の確認、たとえばごみ箱の事前確認の途中でキャンセルされた場合も同じ）。

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
  書き込むバイト数は、`Warnings` を呼んだ時点の衝突の決定で計算し直す（決定の既定値は Skip で、計画の時点では衝突のファイルを書くか分からないため）。
  - 衝突のないファイルは数える。衝突のあるファイルは、Skip・未設定なら数えず、上書きならコピー元の大きさから上書き先の大きさを引いた分、
    自動リネームならコピー元の大きさを数える。
  - 衝突のあるフォルダの中身は、フォルダの決定が Skip・未設定なら数えず、自動リネームなら中の衝突に関係なくすべて数え、マージなら中の衝突ごとの決定に従う。
  - 空き容量は計画の時点で測った値を使う。UI は `Decide` の後に `Warnings` を呼び直せば、決定を反映した警告を得られる。
- 空き容量は、Windows では `GetDiskFreeSpaceEx`、Unix では `statfs` の `Bavail` × ブロックサイズ（macOS は `Bsize`、Linux は `Frsize`）で求める。
- 同じく、コピー先のファイルシステムのファイルの大きさの上限（§10.6）を超えるファイルがあれば、そのファイルごとに `Warnings` に `KindFileTooLarge`
  （`Path` はコピー元のファイル）を加える。実行は妨げない。上限は `DestDir` のファイルシステムで決める。
  書き込まないファイル（上の規則で数えないもの）には警告しない。

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
  ファイルの大きさの上限を超えた（`KindFileTooLarge`。§10.6）場合は、そのファイルだけを失敗にし、残りは続ける。
- キャンセルされたら、処理中の項目を安全に中断し（§16）、残りを `OutcomeSkipped`（`KindCanceled`）にして `StatusCanceled` で返す。

### 7.3 計画後の変化

- 計画から実行までの間にファイルシステムが変わることを前提にする。
- 「存在確認してから書く」ではなく、OS の排他的な操作（`O_EXCL` での作成、§8.4 の排他リネーム、`os.Mkdir`）で書き込むことで、計画後に現れた衝突を確実に検出する。
- 計画時になかった衝突が見つかったら、その項目を `OutcomeSkipped`（`KindExist`）にする（I1）。
- 上書き（`DecisionOverwrite`）の直前に上書き先を `Lstat` し、計画時に記録した fileID・種類・サイズ・更新日時がすべて一致する場合だけ上書きする。
  fileID だけで判定しないのは、削除と作り直しで同じ番号が再利用されるファイルシステムがあるため（Linux の ext4。V16）。
  一致しなければ計画後に現れた衝突とみなし、`OutcomeSkipped`（`KindExist`）にする（I1）。
  上書き先が消えていれば、衝突なしとして排他リネームで書く。
- 移動（`MethodRename`）の上書きで、上書き先が移動元と同じファイル（fileID が同じ。ハードリンク）なら、リネームせずに `OutcomeFailed`（`KindSameFile`）にする。
  Unix の `rename` は同じファイルへのリンク同士では何もせずに成功を返すので、そのままでは移動元が残ったまま Done と報告してしまうため。
  §8.3 の空のファイルの一定の fileID（macOS の exFAT・FAT32。ハードリンクのないボリューム）は、同じファイルとはみなさない。
  コピーは対象外（同じ内容の別のファイルになるだけで、Done の報告が正しい）。
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
| ファイルを最終名にした後、最終名にあるものが書いたものでなかった（§10.1 の手順 7。最終名に何かが置かれた） | Partial | `KindDestChanged` |
| 書き込み中に容量不足になった | ファイルは Failed、フォルダは Partial | `KindNoSpace` |
| 衝突の決定による Skip | Skipped | nil |
| 計画後に現れた衝突、複製しないリンク・特殊ファイル（§14.2） | Skipped | 該当する Kind |
| 着手前にキャンセル・容量不足で打ち切られた | Skipped | `KindCanceled` / `KindNoSpace` |
| 処理中にキャンセルされ、途中までの結果が残らなかった | Skipped | `KindCanceled` |
| 処理中にキャンセルされ、途中までの結果が残った（コピー・移動では移動先の一部、完全削除では削除済みの一部） | Partial | `KindCanceled` |
| `Details` に Err 付きのエントリがある | Partial | 最初のエラー |
| 移動元の削除に一部失敗した、一部を保護した、または削除中にキャンセルされた（§13.3） | CopiedSourceKept | 最初のエラー |
| ごみ箱へ移す操作の後、元の項目が元の場所になく、ごみ箱の中の項目も確かめられない（§12.1） | TrashUnconfirmed | ごみ箱へ移す呼び出しのエラー（なければ `KindUnknown`） |

- Partial・CopiedSourceKept が表す状態は、`ItemResult.Method`（実際に使った方式）で決まる。UI は方式に合わせて利用者に伝える。

| Method | Partial | CopiedSourceKept |
|---|---|---|
| `MethodCopy` | コピー先に途中までの結果がある。コピー元は変わらない | — |
| `MethodRename`（同一ボリュームの移動） | 一部は移動先へ移り、`Details` のものは移動元に残っている | — |
| `MethodCopyThenRemove`（ボリュームをまたぐ移動） | 移動元はすべて残っている（手を付けていない）。移動先に途中までコピーしたものがある | 移動先は完成し、移動元の一部（`Details`）が残っている |
| `MethodRemove` | 一部は削除され、`Details` のものは残っている | — |

- Status: キャンセルされたら `StatusCanceled`。
  それ以外で、Failed・Partial・CopiedSourceKept・TrashUnconfirmed、または Err 付きの Skipped（`KindLinkSkipped` を除く）が 1 件でもあれば `StatusCompletedWithErrors`。それ以外は `StatusCompleted`。

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
    macOS の exFAT・FAT32 では、空の通常のファイルの `Ino` が `2^63` 以上の仮の値で、操作のたびに変わる（V25）。
    そのため、`Ino` が `2^63` 以上の空の通常のファイルは、fileID を「そのボリュームの空のファイル」を表す一定の値に置き換える
    （照合は、fileID と一緒に比べる種類・大きさ（0）・更新日時で行うことになる。空のファイルはデータを持たないので、取り違えても失うデータはない）。
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
  書き込み先・移動元を §13.1 のハンドルで扱う場合は、手順 1〜4 も、そのハンドルからの相対（`mkdirat`・`openat`・`fstatat`・`renameat`・`unlinkat`）で行う。
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
    - 危険の大きさ: 起きうるのは、手順 2 と 3 の間の数回のシステムコールの間だけ。フォルダは、空でないフォルダへの `rename` が失敗するので、
      失われうるのは作り直された空のフォルダだけ。データを失いうるのはファイルの場合だけ。
    - 手順 1 と 3 の間にプロセスが強制終了すると、最終名に空のファイル・空のフォルダ（確保したもの）が残る。
      データは一時ファイル（コピー）または移動元（移動・`Rename`）に残っているので、失われない。
    - 閉じられない理由（V21）: この代わりの手段を使うのは実際には macOS の exFAT だけで、そこには排他リネームの代わりになる不可分な操作
      （ハードリンク、`RENAME_SWAP`、`clonefile`）が 1 つもない。Windows・Linux の exFAT・FAT32・vfat では排他リネームそのものが使える。
      そのため、この危険は受け入れる（総点検の穴 5）。

### 8.5 名前を変換しない

- fsops はファイル名を正規化しない。コピー先の名前はコピー元の名前をバイト単位でそのまま使う（I6）。
- 衝突の検出は OS の `Lstat` に任せる。APFS は NFC と NFD を同じ名前として扱い、NTFS は別の名前として扱うが、`Lstat` に任せればどちらでも正しく動く。
- 表示・検索のための正規化は UI 側で行う。
- 制限事項（V17）: macOS の exFAT では、NFC の名前で保存されたファイル（Windows などで作られたものに限らず、macOS 自身がパスで NFC の名前を
  指定して作ったものでも起きる。日本語の名前では珍しくない）を `ReadDir` が NFD の名前で返し、その名前では削除できない（`ENOENT`。`Lstat` や読み込みはできる）。
  fsops は名前を変換して探し直さず、その項目を `KindNameForm` の失敗として報告する（データは失われない）。
  削除が「見つからない」を返したのに、調べ直すと同じ fileID のエントリがある場合を `KindNameForm` とする（`KindNotFound`「見つかりません」は、実在するので誤り）。
  操作ごとの結果（2026-09-24 の CI で確認。総点検の穴 9）: 完全削除ではそのファイルが `KindNameForm` の失敗で残り、項目は `OutcomePartial`。
  ボリュームをまたぐ移動では、コピーはでき、移動元の削除が `KindNameForm` で失敗して `OutcomeCopiedSourceKept`（両方に残る）。
  コピーは、列挙が返した名前（NFD）でコピーされる。同じ exFAT の中のマージ移動（リネーム）はできる。
- AppleDouble ファイル（`._名前`。V22、総点検の穴 12）:
  - macOS で、拡張属性を AppleDouble ファイルに保存するボリューム（`statfs` のファイルシステム名が `msdos`・`exfat`）では、`name` と同じフォルダにある
    `._name` を `name` の付属として扱い、独立した項目にしない。OS は `name` の名前の変更・削除で `._name` を一緒に移す・消し、`name` の拡張属性を
    そこから読み書きする。付属を独立した項目として先に移すと、`name` を移したときに OS がそれを消し、§15 の `com.apple.quarantine` が失われるため。
  - 列挙（§13.1 の走査、コピー・移動の中身の処理、計画時の数え上げ・衝突の検出）で付属を飛ばす。
  - コピーでは付属をファイルとしてコピーしない。§15 の `com.apple.quarantine` は `name` から読んで設定する。
  - ボリュームをまたぐ移動で移動元を消すと、付属も OS が消す。§15 で保持しないそのほかの拡張属性・リソースフォークは、APFS からの移動と同じく失われる。
  - `name` のない `._name`（孤立したもの）と、ほかの OS・ほかのボリュームの `._name` は、ほかのファイルと同じ通常のファイルとして扱う。
  - 制限事項（受け入れる）: 移動先・コピー先に孤立した `._name` があるところへ `name` を作る・移すと、OS がそれを消すか置き換える（V22）。
    OS の扱いでは `._name` は `name` の拡張属性の保存先で、`name` を作ることは `name` の上書きではない。防ぐための不可分な操作もない。

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
   一時ファイルの作成・照合・削除、最終名へのリネームは、§13.1 の方法で開いて持っている書き込み先のフォルダのハンドルを使って行う（Unix では相対の `openat`・`renameat2` など）。
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
   最終名にするリネームの直前に（自動リネームでは候補ごとに）、一時ファイルが書き終えたもの（書き込んだ後に記録した fileID・通常のファイル・書き込んだ大きさ）の
   ままであることを確かめる。違えば（置き換えられていれば）最終名にせず `OutcomeFailed`（`KindDestChanged`）にし、置き換えたものは消さない。
   最終名にした後も、名前の変更で fileID が変わらないボリュームでは、最終名にあるものが一時ファイルだったことを確かめる
   （Windows の exFAT・FAT32 では、名前の長さが変わるリネームで fileID が変わるので確かめない。V16）。確かめてからリネームするまでの間は、ごく短い隙間として残る。
   確かめて違っていれば（確かめてからリネームするまでの間に一時ファイルが置き換えられた）、最終名には fsops が書いたものではないものが置かれている
   （上書きでは、元のファイルはすでに置き換えられている）ので、`OutcomeFailed` ではなく `OutcomePartial`（`KindDestChanged`）にする（§7.4 の「途中までの結果が残った」）。
8. 2〜7 のどこかで失敗・キャンセルしたら、一時ファイルを削除する（I3）。
   fileID を記録した後の失敗では、一時ファイルの名前にあるものの fileID が一致する場合だけ削除する。
   一時ファイルに読み取り専用属性を設定した後で削除する場合は、属性を外してから削除する。
   一時ファイルは fsops が作ったものなので、§13.2 の「属性を勝手に外さない」は適用しない（Windows では読み取り専用のファイルを削除できず、一時ファイルが残るため）。

### 10.2 フォルダ

- コピー先のフォルダを `os.Mkdir`（Unix では書き込み先のフォルダからの相対の `mkdirat`）で作る（マージのときは既存のものを使う）。
  作った直後に §13.1 の方法で開いて確かめ、中身はそのハンドルの中に書く。フォルダのメタデータは、ハンドルを閉じた後に、開いたときの fileID と照合して設定する。
- 中身を名前順に処理する。ファイルは §10.1、フォルダは再帰、リンクと特殊なファイルは §14。
- フォルダのメタデータ（更新日時・パーミッション・読み取り専用属性）は、中身をすべて処理した後に設定する（§15）。
- 一部のエントリが失敗しても残りは続け、トップレベルの結果を `OutcomePartial` にする。

### 10.3 容量不足

- 書き込み中の容量不足（Windows: `ERROR_DISK_FULL`、`ERROR_HANDLE_DISK_FULL`、Unix: `ENOSPC`、`EDQUOT`）は `KindNoSpace`。
- 処理中の一時ファイルを削除し、§7.2 に従って残りを Skipped にする。
  フォルダの途中で容量不足になったら、そのフォルダ（トップレベルの項目）の残りのエントリも処理しない。処理しなかったエントリは、キャンセルと同じく `Details` に 1 件ずつは入れない。

### 10.4 検証

- `VerifySize`（既定）: 書き込んだバイト数、一時ファイルのサイズ、コピー開始時のコピー元のサイズが一致すること。
  書き込んだバイト数と一時ファイルのサイズが一致しなければ `KindVerifyFailed`。
  さらに、コピー後にコピー元を `Lstat` し直し、fileID・サイズ・更新日時が開始時から変わっていないこと。
  変わっていたら `KindSourceChanged` で失敗にし、一時ファイルを削除する。
- `VerifyHash`: 上記に加え、読み込み時に計算した SHA-256 と、一時ファイルを読み直して計算した SHA-256 を比べる。
  一致しなければ（書き込んだ内容が一時ファイルに残っていない）`KindVerifyFailed` で失敗にし、一時ファイルを削除する。読み直しの間の進捗は `StageVerify` とする。

### 10.5 同期

- 移動（`MethodCopyThenRemove`）では、移動元を消す前に次を必ず行う。移動元を消した直後に電源が落ちても、移動先にデータと名前が残るようにするため。
  - 各ファイルを、最終名にする前に `File.Sync` する（macOS の Go は `F_FULLFSYNC` を使う）。
  - Unix: 最終名へのリネームやフォルダの作成を行ったフォルダを `Sync` する（ディレクトリエントリの永続化）。§13.1 で開いて持っている、書き込み先のフォルダのハンドルで行う。
    トップレベルの項目ごとに、移動元を消す前にまとめて行ってよい。
  - Windows: 同じフォルダを `FILE_FLAG_BACKUP_SEMANTICS` で書き込み可能に開いて `FlushFileBuffers` する。失敗しても処理は続け、警告にもしない。
- コピーでは `SyncAlways` のときだけ、同じ手順で同期する（USB メモリなどで遅くなるため）。
- 同期の失敗は `KindSyncFailed` にする（容量不足（`KindNoSpace`）に分類されるものは除く）。
  ファイルの同期の失敗は、そのファイルの失敗（最終名にしない）。フォルダの同期の失敗は、移動では失敗（移動元を消さない）、コピーでは `Warnings`
  （データは最終名で書き終えている）。


### 10.6 ファイルの大きさの上限

- FAT 系のファイルシステムには、ファイルの大きさの上限（4 GiB − 1 バイト）がある。Windows はそれを超える書き込みを容量不足（`ERROR_DISK_FULL`）で
  失敗させ、エラー番号では区別できない（V24）ので、コピー先のファイルシステムの種類と大きさで書く前に判断する。
- 上限は、§13.1 の方法で開いて持っている書き込み先のフォルダから、ファイルシステムの種類を調べて決める。FAT 系なら 4 GiB − 1 バイト、それ以外は上限なしとする。
  Windows は `GetVolumeInformationByHandleW` のファイルシステム名が `FAT`・`FAT32`、macOS は `fstatfs` の `f_fstypename` が `msdos`、
  Linux は `fstatfs` の `f_type` が `MSDOS_SUPER_MAGIC`（vfat・msdos）。調べられなければ上限なしとする。
- §10.1 の手順 1 で開いたコピー元の大きさが上限を超えていれば、一時ファイルを作らずに、そのファイルを `OutcomeFailed`（`KindFileTooLarge`）にする。
  フォルダの中なら、そのエントリだけを失敗にして残りを続ける（フォルダは `OutcomePartial`）。ボリュームをまたぐ移動では移動元は残る（§11.2）。
- コピー中にコピー元が大きくなって上限を超えた場合に備え、書き込み中の容量不足（§10.3）で、上限があり、書き込もうとした位置が上限を超えていれば、
  `KindNoSpace` ではなく `KindFileTooLarge` にする（§7.2 の打ち切りをしない）。
- Unix の `EFBIG` と Windows の `ERROR_FILE_TOO_LARGE` は `KindFileTooLarge` にする（§17）。
- テストでは、`hooks` の `fileSizeLimit` で実行時の上限を置き換えて、小さなファイルで確かめる。

---

## 11. 移動と名前の変更

### 11.1 同一ボリューム（`MethodRename`）

- 衝突なし: 排他リネーム。
- 上書き（ファイル同士）: 置換リネーム（§9.3 の事前確認を行う）。
- 自動リネーム: §9.2 の候補名へ排他リネーム。
- マージ（フォルダ同士）: 中身を 1 件ずつ移動する（内側の衝突はそれぞれの決定に従う）。
  移動元のフォルダとマージ先のフォルダの両方を §13.1 の方法で開いて確かめ、中身は両方のハンドルを使って移動する。
  最後に移動元のフォルダが空なら、§13.2 のフォルダの削除方法で削除する。空でなければ残して報告する（Outcome は §7.4）。
  中に残したもの（衝突の決定による Skip、失敗）があれば、その理由は報告済みなので、フォルダは報告せずに残す。
  残したものがないのに削除できない場合（移動中に追加されたなど）は、そのフォルダを `OutcomeFailed`（`KindNotEmpty` など）で報告する。
  中のエントリはフォルダのハンドルからの相対で移動する（§13.1）。移動の進捗の `DoneFiles` は移動したエントリの数で数える（フォルダごとリネームした場合は 1 件）。
- トップレベルの項目のリネームがボリューム違いのエラー（Windows: `ERROR_NOT_SAME_DEVICE`、Unix: `EXDEV`）で失敗したら、その項目を §11.2 の方式でやり直す。
  マージの途中で内側のエントリがこのエラーになった場合は、そのエントリを失敗とする。`KindCrossDevice` は結果に出さないので、`KindUnknown`（元のエラーは `Err`）にする。

### 11.2 ボリュームをまたぐ移動（`MethodCopyThenRemove`）

1. §10 の手順でコピーする。同期は必ず行う（§10.5）。Unix でフォルダの同期に失敗した場合は、移動元を削除しない（`OutcomePartial`）。コピーしたエントリの一覧を、エントリごとの fileID・種類・サイズ・更新日時とともに記録する。
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
- `newName` が今の名前とバイト単位で同じなら、何もせずに成功を返す（Windows の排他リネームは「既にある」と失敗するので、先に判定する）。
- Windows の exFAT・FAT32 では、大文字小文字だけの変更で `MoveFileExW` が成功を返しても名前が変わらない（V1）。
  そのため、大文字小文字だけ・正規化だけの変更の後は、親フォルダの列挙で新しい名前がバイト単位で現れたことを確かめる。
  現れなければ、同じフォルダ内の途中名（`.fsops-rename-<ランダム16進>`）へ排他リネームし、続けて途中名から新しい名前へ排他リネームする。
  途中名は一時ファイルの名前（`.fsops-<ランダム16進>.tmp`。§10.1）と形を変える。途中で止まって残った場合、それは利用者のファイル・フォルダで、
  一時ファイルと取り違えて消されないようにするため（`doc.go` の「途中で止まった場合」に書く）。
  2 回目が失敗したら途中名から元の名前へ戻し、戻せなければ途中名のパスを `OpError` の `Dest` に入れて返す。

### 11.4 フォルダの作成（`Mkdir`）

- `parent` は存在するフォルダの絶対パス。`\x00` を含む・絶対パスでないものは `KindInvalidRequest`。
- `name` の検査は `Rename` と同じ（§11.3）。使えない名前は `KindInvalidName`。名前は変換しない（I6）。
- `\\?\` 形式への変換を通す（§8.2）。途中の要素のリンクは辿る（利用者が表示しているフォルダの中に作るため）。
- OS の不可分なフォルダの作成（Unix の `mkdir`、Windows の `CreateDirectoryW`）で作る。同じ名前のエントリがあれば（ファイル・リンク・
  大文字小文字や正規化だけが違う名前を含む）、OS が失敗を返すので `KindExist` にする。上書きも、既存のものへの変更もしない（I1）。途中の状態は残らない（I3）。
- パーミッションは `0o777` から umask を引いたもの（Unix）。属性は既定のまま（Windows）。
- 分類は §17 のとおり。親フォルダがない場合は `KindNotFound`、読み取り専用のボリュームは `KindReadOnly`、
  macOS で親フォルダがロックされている場合（`EPERM` かつ `UF_IMMUTABLE`）は `KindReadOnly`、それ以外の拒否は `KindPermission`。
  Windows のフォルダの読み取り専用属性は保護を意味しないので、分類に使わない。
- エラーの `Path` は作ろうとしたフォルダのパス、`OnDest` は真にする。使えない名前のときは、`Path` に `parent` を入れる（使えない名前からパスを作らない）。
- 使用中の一時的な失敗のやり直し（§17.1）は行わない（ほかのプロセスが作るフォルダの中身を待つ理由がないため）。

---

## 12. ごみ箱

### 12.1 共通

- `OpTrash` はトップレベルの項目だけを扱う（項目ごとごみ箱へ移すため）。中身は、Windows で §12.2 の最大サイズの事前確認のためにサイズを数える場合だけ、§13.1 の走査（リンクに入り込まない）で読む。変更はしない。
- 1 項目ずつ処理し、結果を項目ごとに返す。
- ごみ箱が使えないと判断したら、その項目には何もせず、`OutcomeFailed`（`KindTrashUnavailable`）にする（I5）。
- `NewPlan` は、ごみ箱が使えるかの事前確認（§12.2 の `GetDriveType` など、ファイルシステムを変更しないもの）を行い、使えない項目の `Item.Err` に `KindTrashUnavailable` を入れる。
  UI は実行前に「この項目はごみ箱に入りません」と示して、完全削除に切り替えるかを利用者に確認できる。`Execute` でも同じ確認をもう一度行う。
  `KindTrashUnavailable` は、ごみ箱が使えないと確かめた場合（ネットワーク・リムーバブルのボリューム、「すぐに削除する」設定、最大サイズの超過、
  Win32 の正規化で変わる名前、ごみ箱のないビルド・OS など）だけに使う。事前確認の途中でエラーになり確かめられなかった場合（中のフォルダを読めない、
  設定を読めないなど）は、ごみ箱に入れない（I5）が、そのエラーの Kind（`KindPermission`・`KindUnknown` など）にする。
  UI が完全削除を勧めるのは `KindTrashUnavailable` のときだけにする（ごみ箱が使えるかもしれない項目で、取り消せない操作に誘導しないため）。
  `Execute` の確認は、計画時ではなく実行時の項目の大きさで行う（計画の後に大きくなった項目を見逃さないため）。
- ごみ箱へ移す呼び出しの結果は、呼び出しが返した成否ではなく、呼び出しの後の状態で決める（利用者に項目の状態を誤って伝えないため）。
  1. 元の場所に元の項目（fileID が同じもの）があれば `OutcomeFailed`。Err は、呼び出しがエラーを返していればそれ、成功を返していれば `KindUnknown`。
     元の場所に別のもの（fileID が違う）があり、呼び出しがエラーを返していれば、元の項目は別の場所へ移されたとみなし `OutcomeFailed`（`KindSourceChanged`）にする
     （「元の場所からも消えています」と誤って伝えないため）。
  2. 元の場所になく、ごみ箱の中のパスを得られて、そこに項目があれば（`Lstat`）`OutcomeDone`。呼び出しがエラーを返していても Done にする
     （ごみ箱に入ってから失敗が報告される場合がある）。ごみ箱の中の項目の fileID は比べない（Windows の exFAT・FAT32 では名前の変更で変わる。V16）。
  3. 元の場所になく、ごみ箱の中のパスも確かめられなければ `OutcomeTrashUnconfirmed`（完全に削除された可能性がある。V18）。
     Err は、呼び出しがエラーを返していればそれ、なければ `KindUnknown`。`TrashedPath` は空。
  4. 元の場所を調べられなければ（`Lstat` が NotFound 以外で失敗）、状態がわからないので `OutcomeFailed`（そのエラー）。

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
    - 設定を読めない場合（キーや値がない、ボリューム GUID が取れない）は、ごみ箱に入れない（I5）。ただし「使えない」とは確かめていないので
      `KindTrashUnavailable` にはせず、`KindUnknown`（読み取りのエラーがあればその Kind）にする（§12.1。UI に完全削除を勧めさせないため）。
    - 最大サイズを超えるフォルダでは、Windows はフォルダ自体を「入れられる」と通知したあと中身を 1 つずつ完全削除しようとし、その中身の `PreDeleteItem` にはフラグ `0x80` がない（V19）。
      ただし、`FOF_WANTNUKEWARNING` を付けた fsops の設定では、中身の `PreDeleteItem` より前に確認ダイアログが出て止まる（2026-09-24 の CI で確認。V19 の確認は `FOF_WANTNUKEWARNING` なし）。
      事前確認が見落とした場合の最後の防御は、ファイル・フォルダとも確認ダイアログ（黙って完全削除はされないが、Execute は止まる）で、手順 4 の中止は、そのダイアログが出ない場合の防御として残す。
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
     項目自体の `PostDeleteItem` が成功を通知しても、`psiNewlyCreated` が NULL なら、ごみ箱に入らず完全に削除されたとみられるので（V18）、
     `KindTrashUnavailable` のエラーを返す（結果は §12.1 の規則で決め、項目が消えていれば `OutcomeTrashUnconfirmed` になる。I5。黙って完全削除にしない）。
     `PerformOperations` などが失敗しても、`PostDeleteItem` でごみ箱の中のパスを得ていれば、エラーと一緒にそのパスも返す（§12.1 の規則 2）。
  - COM の vtable の呼び出しと進捗通知の実装は、cgo を使わず `syscall.SyscallN` と `syscall.NewCallback`（または x/sys/windows の同等のもの）で行う。
    `syscall.NewCallback` で作るものは解放できず数に上限があるので、進捗通知の vtable はパッケージの初期化時に 1 回だけ作り、以後は変更しない。
    進捗通知の状態は、操作ごとの通知のオブジェクトが持つ（パッケージレベルの可変状態を持たない）。
  - COM は、スレッドを固定した専用の goroutine で初期化し、終わったらスレッドごと破棄する（Unlock しない）。
    既に別の方式で初期化されている（`RPC_E_CHANGED_MODE`）場合もそのまま続ける（V18 で、どの方式でも動くことを確認済み）。
  - `HRESULT` の成否は `SUCCEEDED`（最上位ビット）で判定する。ごみ箱に入れるのに成功しても、`PostDeleteItem` は `S_OK` ではなく
    `COPYENGINE_S_DONT_PROCESS_CHILDREN`（`0x00270008`）を渡す（2026-09-24 の CI で確認）。
  - ごみ箱の最大サイズを超える項目は `PreDeleteItem` のフラグでは見分けられない（V18）。事前確認（上記）と `FOF_WANTNUKEWARNING` の併用で対処する（フェーズ3で承認）。
- 分類できない `HRESULT` は `KindUnknown` にして値を `Err` に残す。
- 既存ライブラリ（`hymkor/trash-go`、`rafshawn/go2trash` など）は実装の参考にしてよい。依存に加える場合は許可リストの変更になるので確認を取る。

### 12.3 macOS

- cgo と Objective-C で `NSFileManager` の `trashItemAtURL:resultingItemURL:error:` を呼ぶ（`#cgo LDFLAGS: -framework Foundation`）。autorelease pool で囲む。
- 成功したら、ごみ箱内のパスを `ItemResult.TrashedPath` に入れる。失敗しても `resultingItemURL` が得られていれば、エラーと一緒にそのパスも返す（結果は §12.1 の規則で決める）。
- ネットワークボリュームなどで失敗した場合、エラーの内容から判断できれば `KindTrashUnavailable`、できなければ `KindUnknown`。
  `NSCocoaErrorDomain` の `NSFeatureUnsupportedError`（3328）は `KindTrashUnavailable`。ほかは `NSFileNoSuchFileError` → NotFound、
  `NSFileWriteNoPermissionError` → Permission、`NSFileWriteOutOfSpaceError` → NoSpace、`NSFileWriteVolumeReadOnlyError` → ReadOnly、
  それ以外は下位の POSIX のエラー番号（`NSUnderlyingErrorKey`）を §17 で分類する。
- パスは、ファイルシステムの表現のまま（`fileURLWithFileSystemRepresentation`）渡し、名前を変換しない（I6）。
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
- 削除（§13.2、§13.3）では、マウントポイント（別のボリュームがマウントされたフォルダ）には入らず、削除もしない。
  そのエントリを `KindMountPoint` で失敗として報告する（中身は別のボリュームのもので、利用者が削除を指示したフォルダの一部ではないため）。
  - Unix: フォルダの `Dev` が、それを含むフォルダの `Dev` と違えばマウントポイントとみなす。同じデバイスのバインドマウント（Linux）は見分けられない（受け入れる）。
  - Windows: マウントされたフォルダはリパースポイント（`IO_REPARSE_TAG_MOUNT_POINT`。`TypeJunction`）なので、もともと入らない。
  - トップレベルの項目がマウントポイントなら、`OpDelete` と `OpMove` の計画で `Item.Err` に `KindMountPoint` を入れ、実行時にも確かめる。
  - 計画の走査（`OpDelete`）ではマウントポイントの中を数えず、`Warnings` に `KindMountPoint` を加える（削除の前に UI が示せるように）。
  - コピー（ボリュームをまたぐ移動のコピーを含む）はマウントポイントの中も複製する（`cp -R` と同じ）。移動元の削除（§13.3）では入らないので、
    移動元のマウントポイントは残り、`OutcomeCopiedSourceKept` になる。
- 走査は、計画（§6.3）、コピー（§10.2）、移動（§11）、完全削除（§13.2）で使う。
- 削除（§13.2、§13.3）と、同一ボリュームのマージ移動（§11.1）のために入り込むフォルダは、パスで `ReadDir` せず、開いたハンドルで確認してから列挙する。
  `Lstat` でフォルダと判定してから中に入るまでの間に、フォルダ（またはその途中の階層）がリンクに置き換えられても、リンク先に入り込まないようにするため（I4）。
  - Unix: トップレベルのフォルダはパスで、それより下は親フォルダのハンドルからの相対（`openat`）で、`O_RDONLY|O_DIRECTORY|O_NOFOLLOW` を付けて開く。
    `fstat` の fileID が `Lstat` 時と一致することを確かめる。
    中身の削除は `unlinkat`、マージ移動での中身の移動は `renameatx_np` / `renameat2`（どちらも開いたフォルダからの相対）で行う。
  - Windows: `FILE_FLAG_BACKUP_SEMANTICS|FILE_FLAG_OPEN_REPARSE_POINT` で、共有モードに `FILE_SHARE_DELETE` を含めずに開く。
    リパースポイントでないことと、fileID が一致することを確かめる。そのフォルダの処理が終わるまでハンドルを閉じない
    （開いている間、そのフォルダは名前の変更・削除・リンクへの置き換えができない）。フォルダ自体を削除する直前に閉じ、削除は §13.2 の確かめたハンドルで行う。
  - 確認できなければ、そのフォルダには入らず `KindSourceChanged` で失敗にする。
- 書き込み先のフォルダ（コピー・移動の DestDir、コピーで作ったフォルダ、マージ先）も同じ方法で開いて確かめ、そのフォルダの処理が終わるまでハンドルを持つ。
  中の作成・リネーム・削除・照合は、そのハンドルを使って行う。確かめた後に、フォルダ（またはその途中の階層）がリンクに置き換えられても、
  リンクの先に書き込み・移動しないため（総点検の穴 4）。
  - Unix: 中の操作を、開いたフォルダからの相対で行う（`openat(O_CREAT|O_EXCL|O_NOFOLLOW)`、`renameat2`・`renameatx_np`（排他）、
    `renameat`（置換）、`mkdirat`、`symlinkat`、`fstatat`、`unlinkat`、同期はその fd で `fsync`）。§8.4 の代わりの手段も相対で行う。
    開いた後にフォルダの名前を変えられた場合、書き込みは開いたフォルダ（名前を変えられた元のフォルダ）に入る。
  - Windows: 共有モードに `FILE_SHARE_DELETE` を含めずに開いたハンドルを持ち続け（その間、フォルダは名前の変更・削除・置き換えができない）、
    中の操作はパスで行う。
  - DestDir は、計画時に記録した fileID（リンクを辿った先。DestDir 自体はリンクでもよい）と一致することを確かめて開く。一致しなければ、その項目を
    `KindDestChanged` で失敗にする。
  - 作ったフォルダは、作った直後にリンクを辿らずに開き、開いたものの fileID を記録する。開けなければ（リンクに置き換えられていれば）`KindDestChanged` で失敗にする。
  - マージ先は、計画時の fileID と一致することを確かめて開く（§7.3 の照合）。一致しなければ、計画後に現れた衝突として `OutcomeSkipped`（`KindExist`）にする。
  - 残る隙間: 上書き先のエントリ自体を、照合と置換リネームの間に置き換えられる場合（§7.3 の `Lstat` による照合として許容する）。

### 13.2 完全削除（`OpDelete`）

- 後順（中身を先、フォルダを後）で削除する。
- 削除は、エントリの種類（§14.1）に応じて次の方法で行う。利用者のファイルの削除に `os.Remove` は使わない。

  | 種類 | Unix | Windows |
  |---|---|---|
  | ファイル・特殊なファイル・ファイル用のシンボリックリンク | `unlink`（§13.1 のハンドルで入ったフォルダの中では `unlinkat(fd, name, 0)`） | 確かめたハンドルでの削除（下記） |
  | フォルダ | `rmdir`（同 `unlinkat(fd, name, AT_REMOVEDIR)`） | 確かめたハンドルでの削除（下記） |
  | フォルダ用のシンボリックリンク・ジャンクション（Windows） | — | 確かめたハンドルでの削除（下記） |

  - Windows のエントリはすべて、`DeleteFileW`・`RemoveDirectoryW` をパスで呼ぶ代わりに、削除のアクセス権で `FILE_FLAG_OPEN_REPARSE_POINT` を付けて開き、
    fileID・フォルダ属性・種類（§14.1）が走査で得たものと一致することを確かめてから、同じハンドルに削除の印を付ける。一致しなければ削除せず `KindSourceChanged`。
    確かめた後（フォルダでは中身を処理してハンドルを閉じた後）に別のファイルやジャンクションへ置き換えられても、置き換えたものを消さないため
    （2026-09-24 の CI で、`RemoveDirectoryW` ではジャンクションが、`DeleteFileW` では置き換えたファイルが消えることを確認した。総点検の穴 2）。
    削除の印は、`DeleteFileW`・`RemoveDirectoryW` と同じ結果にするため、POSIX 形式の `FileDispositionInfoEx`（`FILE_DISPOSITION_DELETE | FILE_DISPOSITION_POSIX_SEMANTICS`。
    ほかのハンドルが開いていても名前がすぐ消える）で付け、それを受け付けないボリューム（exFAT・FAT32 は `ERROR_INVALID_PARAMETER` を返し、何もしない）では
    `FileDispositionInfo` で付ける（V23）。
    Unix の `rmdir`・`unlinkat(AT_REMOVEDIR)` は、シンボリックリンクに置き換えられていれば失敗するので、そのままでよい。
    Unix の `unlink`・`unlinkat` は名前で消すので、確かめた後に別のファイルへ置き換えられると、それを消しうる。Unix には開いたハンドルで削除する方法がないため、
    この危険は受け入れる。

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
- Windows で、フォルダの削除が `ERROR_DIR_NOT_EMPTY` で失敗し、残っている名前がすべて削除待ち（§17。exFAT・FAT32 では、fsops 自身が付けた
  削除の印も、ほかのハンドルが開いていると削除待ちで残る。V23）なら、`KindNotEmpty` ではなく `KindLocked` にし、§17.1 のとおりやり直す。
- Windows の読み取り専用ファイルは削除に失敗する（`DeleteFileW` もハンドルでの削除も `ERROR_ACCESS_DENIED` を返し、ファイルと属性は残る。V15、V23 で確認済み）。属性を勝手に外さず `KindReadOnly` として報告する。
- Windows のフォルダの読み取り専用属性は保護を意味しないため、フォルダに限り属性を外してから削除する（読み取り専用属性の付いたフォルダは、空でも `RemoveDirectoryW` が `ERROR_ACCESS_DENIED` で失敗する。V15）。
  属性は、上記の確かめたハンドルで外し、削除に失敗したら元に戻す。

### 13.3 記録した項目だけの削除（移動元の削除）

- §11.2 で記録した一覧のエントリだけを削除する。
- 削除の直前に照合し、記録と一致するエントリだけを削除する。
  照合は、Unix では §13.1 のハンドルからの相対の `fstatat(AT_SYMLINK_NOFOLLOW)`、Windows では祖先のハンドルを開いたままの `Lstat` で行う。
  照合するのは、ファイルとリンクでは fileID・種類・サイズ・更新日時、フォルダでは fileID と種類だけとする（フォルダの更新日時は中身を消すと変わるため）。
  一致しないもの（コピー後に変更・置き換えられたもの）は削除せず、`Details` に `KindSourceChanged` で報告する。
  コピー後・削除前に移動元のファイルが編集・保存された場合に、その変更を失わないため（I2）。
  Windows では、§13.2 の削除するハンドルで、同じ項目（ファイルとリンクでは大きさ・更新日時も）をもう一度照合する（照合から削除までの間の書き換え・置き換えも消さない）。
  Unix では、照合から `unlinkat` までの間の書き換え・置き換えは防げない（§13.2 と同じ理由で受け入れる）。
- ファイルとリンクを先に消し、フォルダは深い順に消す。削除の方法は §13.2 と同じ。フォルダは空でなければ残す（コピー中に追加されたファイルがあると空にならないため、そのファイルは残る）。
  記録の後に追加されたエントリ（記録にも、衝突の決定による Skip にもないもの。移動先にはない）は、フォルダの記録したエントリを処理した後に列挙して見つけ、
  消さずに `Details` に 1 件ずつ `KindSourceChanged` で報告する（§11.2 の手順 4 の「残ったパス」。フォルダが空でないことだけを報告すると、
  どのファイルが移動先になく移動元にだけ残ったかがわからないため）。そのフォルダは空にならないので削除を試みない。
- フォルダへの入り方は §13.1 に従う。
- Windows では、照合で一致したエントリに読み取り専用属性があれば、属性を外してから削除する（§13.2 の確かめたハンドルで外す）。
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
| `TypeSymlink` | リンク先の文字列をそのまま使ってリンクを作る。権限不足なら `KindLinkUnsupported` で失敗 | Skipped（`KindLinkSkipped`） | リンク自体を移動 | リンク自体だけ |
| `TypeJunction` | 複製しない。Skipped（`KindLinkUnsupported`） | 同左 | リンク自体を移動 | リンク自体だけ |
| `TypeSpecial` | 複製しない。Skipped（`KindUnsupportedType`） | 同左 | そのまま移動 | エントリ自体だけ |

- ボリュームをまたぐ移動は「コピー → 移動元の削除」なので、コピーの列に従う。リンクや特殊なファイルが Skipped になった項目は、移動元を削除しない（§11.2）。
- `LinkSkip` で複製しなかったリンクは、利用者が選んだ方針によるものなので、`KindLinkSkipped` で報告し、エラーとは扱わない。
  コピーでは、衝突の決定による Skip と同じく、フォルダの結果を Partial にせず、`Status` もエラーに数えない（`Details` には入れる）。
  移動では、移動元のリンクが移動されずに残るので、これまでどおりフォルダの結果を Partial にし、移動元に手を付けない（§11.2 の手順 2）。
- 相対パスのシンボリックリンクは、リンク先の文字列を書き換えない。
  Windows ではリンク先を `os.Readlink` で読むため、絶対パスのリンク先は `\??\C:\x` の形が `C:\x` の形になる（`CreateSymbolicLink` が同じリンク先として作り直す）。
- シンボリックリンクは一時名を使わず、最終名（自動リネームでは候補名）に直接作る。リンクの作成は不可分で、名前が存在すれば失敗するため、I1・I3 を満たす。
- Windows では、リンクのファイル用・フォルダ用の区別をコピー元のリンクの属性（`FILE_ATTRIBUTE_DIRECTORY`）に合わせる。
  `os.Symlink` はリンク先を調べて区別を決めるため使わず、`CreateSymbolicLink` に `SYMBOLIC_LINK_FLAG_DIRECTORY`（必要な場合）と `SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE` を指定する。

### 14.3 一覧のための調べ・列挙（`Lstat`・`ReadDir`・`Readlink`）

UI の一覧（filer §6）が、操作と同じ種類の判定・パスの扱い・エラーの分類を使えるようにする。読むだけで、ファイルシステムを変更しない。

- `Lstat` と `ReadDir` の種類の判定は §14.1 と同じ。`ReadDir` の中のエントリはリンクを辿らない（§13.1 と同じ方法で列挙する。
  Windows の FileIdExtdDirectoryInfo と、使えないボリュームでの切り替え（V14）を含む）。
- `ReadDir` は、`dir` 自体がリンク・ジャンクションなら、そのリンク先を列挙する（利用者がリンクのフォルダに入った場合。Finder・エクスプローラーと同じ）。
  I4 は削除・移動の走査に関するもので、fsops の中の走査（§13.1）は今までどおり、フォルダでないもの（リンク・ジャンクションを含む）に入らない。
  `dir` がフォルダでない（リンク先がファイルの場合を含む）ときは `KindNotFound`。
- `ReadDir` は AppleDouble の付属（§8.5）を含めない。操作が付属を独立した項目として扱わないので、一覧でも選べないようにする。
- `Hidden`・`ReadOnly` は、列挙で得た属性（Windows はファイル属性、Unix は `fstatat` の結果）から求める。
  `Lstat` は、1 つのハンドル（Windows）・`fstatat`（Unix）で、種類・属性を列挙と同じ方法で求める。
  ただし Windows の列挙の更新日時は、親フォルダの索引に記録された値で、フォルダの中身が変わった後などに `Lstat` の値より古いことがある（NTFS。フェーズ17の VM で確かめた）。
- `Readlink` は、リンク先の文字列を書き換えずに返す（§14.2 と同じく、Windows の絶対パスのリンク先は `\??\C:\x` の形が `C:\x` の形になる）。
  リンクでもジャンクションでもないときは、OS のエラーを §17 で分類したものを返す。
- エラーはすべて `*OpError`（Op は `lstat`・`readdir`・`readlink`）で、§17 のとおりに分類する。パスは `\\?\` の付かない形で返す（§8.2）。
- 列挙はできたが調べられなかったエントリ（Unix の `fstatat` の失敗）は、`Entry.Err` を付けて返す。1 件のために、フォルダ全体を失敗にしない。

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

保持しないもののうち、利用者が付けた情報でファイルごとに有無が分かるものがコピー元のファイル・フォルダにあれば、`Warnings` に `KindMetadata`
（`OnDest` は偽。`Err` に保持しなかった名前）を加える（コピーとボリュームをまたぐ移動。ボリュームをまたぐ移動では移動元が消えるので、黙って失わないため）。
同一ボリュームの移動はリネームで、すべて残るので調べない。調べられなければ（列挙の失敗）警告しない。
- macOS: 拡張属性 `com.apple.metadata:_kMDItemUserTags`（タグ）、`com.apple.metadata:kMDItemFinderComment`（コメント）、`com.apple.ResourceFork`（リソースフォーク）。
- Linux: `user.` で始まる拡張属性。
- Windows: `Zone.Identifier` 以外の名前付きの代替データストリーム（`GetFileInformationByHandleEx` の `FileStreamInfo`）。
- 作成日時・ACL・所有者はどのファイルにもあるので警告しない（§21 の「保持しない」のとおり）。

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
	KindFileTooLarge     // コピー先のファイルシステムの、ファイルの大きさの上限を超える（§10.6）
	KindMountPoint       // 別のボリュームがマウントされたフォルダ。削除のために中に入らない（§13.1）
	KindVerifyFailed     // コピーした内容が元と一致しない（§10.4）
	KindSyncFailed       // 同期（fsync）に失敗した。書いた内容が永続化されたか保証できない（§10.5）
	KindLinkSkipped      // LinkSkip の方針でリンクを複製しなかった（エラーではない。§14.2）
	KindNameForm         // 名前の文字の表現（NFC・NFD）の違いで扱えない（macOS の exFAT。§8.5、V17）
	KindDestChanged      // コピー先・移動先のフォルダやファイル（DestDir、作ったフォルダ、一時ファイル）が処理中に変更・置き換えられた
)

type OpError struct {
	Op     string // "copy", "rename", "remove" など
	Path   string
	Dest   string
	Kind   Kind
	OnDest bool   // エラーが Dest 側（コピー先・移動先）で起きた
	Err    error  // 元のエラー
}
```

- `OnDest` は、エラーがコピー先・移動先の側で起きたことが確かな場合に真にする（DestDir を開けない・変わった、一時ファイルの作成・書き込み・同期・
  照合、フォルダ・リンクの作成、最終名へのリネームの失敗、上書き先が使用中など）。偽は、Path の側で起きたか、どちらの側か分からない
  （移動のリネームの失敗など）ことを表す。UI は、Kind の文に「コピー先で」などを添えるのに使う（Path はコピー元のままなので、
  それだけでは利用者がコピー元の問題と受け取るため）。

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
| ReadOnly | `ERROR_ACCESS_DENIED` かつファイルの読み取り専用属性（フォルダの読み取り専用属性は保護を意味しないので見ない。§13.2）、`ERROR_WRITE_PROTECT` | `EROFS`、`EPERM` かつ `UF_IMMUTABLE`（削除では、エントリ自体かそれを含むフォルダ） |
| NoSpace | `ERROR_DISK_FULL`、`ERROR_HANDLE_DISK_FULL` | `ENOSPC`、`EDQUOT` |
| FileTooLarge | `ERROR_FILE_TOO_LARGE`（Windows は FAT32 の上限でも `ERROR_DISK_FULL` を返すので、§10.6 で書く前に判断する） | `EFBIG` |
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
- Windows で、削除（確かめて開くハンドル）と、エントリを調べる操作（`Lstat`）が `ERROR_ACCESS_DENIED` で失敗し、直後の NT ステータス
  （`RtlGetLastNtStatus`。失敗した呼び出しと同じ OS スレッドで読む）が `STATUS_DELETE_PENDING` なら、`KindLocked` にする（V26）。
  ほかのプロセスが開いたまま削除の印を付けたもので、そのプロセスが閉じれば消えるため（「権限がありません」ではない）。§17.1 のとおりやり直す。

### 17.1 使用中の一時的な失敗のやり直し（Windows）

ウイルス対策ソフト・検索インデクサ・同期クライアントは、書き終えたばかりのファイルなどを短い間開く。その間の失敗を `KindLocked` にしないため、やり直す。

- 次の操作が `KindLocked`（`ERROR_SHARING_VIOLATION`・`ERROR_LOCK_VIOLATION`、§9.3 の使用中の判定、§17 の削除待ちを含む）で失敗したら、やり直す。
  コピー元を開く（§10.1 手順 1・§10.4 の読み直し）、最終名へのリネーム（排他・置換・自動リネームの各候補）、同一ボリュームの移動のリネーム、
  §13.2 の開いて確かめて削除する一連（完全削除・記録した項目の削除・マージ移動の後の移動元のフォルダの削除）、fsops の一時ファイルの削除、`Rename`。
- やり直すのは「確かめてから操作するまで」の一連で、確認（一時ファイルの `check`、上書き先の照合、開いたハンドルの fileID とリパースの確認）も毎回やり直す。
- 間隔は 10 ms から倍にして最大 200 ms、1 操作の待ちの合計は 1 秒まで。1 回の `Execute` の待ちの合計が 10 秒を超えたら、それ以降はやり直さない。
  上限に達したら、今と同じく `KindLocked` にする。`Rename` は 1 回の呼び出しを 1 回の `Execute` と同じに扱う。
- 待っている間にキャンセルされたら、すぐに `KindCanceled` として §16 に従う。
  ただし fsops の一時ファイルの削除は、キャンセルされていても上限まで待ってやり直す（I3。キャンセル後も一時ファイルを残さないため）。
- やり直さないもの: 読み書きの途中の `ERROR_LOCK_VIOLATION`（アプリが意図して持つバイト範囲ロック）、`ERROR_ACCESS_DENIED`（読み取り専用・権限。削除待ちは §17 のとおり `KindLocked` にしてやり直す）、
  ごみ箱（シェル内部）、フォルダを開く操作、Unix の `EBUSY`。
- テストでは、`hooks` の `lockFault` でやり直しの対象の操作に使用中の失敗を注入し、`lockWait` で待ちを置き換える（実際には待たない）。
  注入した失敗は、どの OS でもやり直しの対象として扱う（やり直しの処理を 3 つの OS で確かめるため）。

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
| `FSOPS_PROBE_FAT32_LARGE_DIR` | Windows: 空きが 4 GiB を超える FAT32 のボリューム上のフォルダ（V24。CI の windows-fat32-large ジョブだけが設定する） | 同上 |

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
| 容量 | FAT32 の上限（4 GiB − 1 バイト）を超えるファイル → 計画が `KindFileTooLarge` で警告し、実行はそのファイルだけを書かずに `KindFileTooLarge` で失敗にして残りを続ける。移動では移動元に残る。コピー中に上限を超えた容量不足も `KindFileTooLarge`（§10.6） | 共通（フックの上限）・FAT32（`FSOPS_PROBE_FAT32_DIR`。V24） |
| I4 | 完全削除するフォルダの中のマウントポイント → 中に入らず、マウントされたボリュームの中身が残り、そのエントリが `KindMountPoint` で報告される。トップレベルのマウントポイントは計画で `KindMountPoint` | macOS（`hdiutil` でテストの中にイメージをマウントする） |
| 容量 | 容量不足の後も、同じボリュームへの移動（`MethodRename`）の項目は続行される | CROSSVOL |
| ごみ箱 | ごみ箱に入り、元の場所から消えている（Windows: `$I` ファイル、macOS: `TrashedPath`） | TRASH（V5、V10） |
| ごみ箱 | ごみ箱へ移す呼び出しの報告と実際の状態が違う（成功を返したのに残っている、失敗を返したのにごみ箱に入った、消えたのにごみ箱の中の項目がない）→ §12.1 の規則どおり Failed・Done・TrashUnconfirmed。`TrashUnconfirmed` は `StatusCompletedWithErrors` | Windows・macOS（`trashCall` フックで呼び出しを差し替える。本物のごみ箱は使わない） |
| ごみ箱 | ごみ箱へ移す操作が成功を返したのに元の場所に残っている（フックで呼び出しを差し替えて注入。本物のごみ箱には触れない）→ その項目は `OutcomeFailed`（`KindUnknown`）で元のまま、ほかは続行 | Windows・macOS（cgo） |
| 削除 | Windows で、確かめた後・削除の直前（フックで注入）にファイルを別のファイルに置き換える・移動元のファイルを書き換える → 置き換えたもの・書き換えたものは消えず（属性も変わらず）、`KindSourceChanged` | Windows |
| AppleDouble | macOS の exFAT・FAT32 の中のコピー・マージを含む移動・ボリュームをまたぐ移動・完全削除 → `com.apple.quarantine` が残り、付属（`._名前`）がファイルとして増えず、衝突の決定でスキップした項目は付属ごと残る。孤立した `._名前` は通常のファイルとして扱う | macOS（`FSOPS_PROBE_EXFAT_DIR`・`FSOPS_PROBE_FAT32_DIR`。V22） |
| AppleDouble | macOS 以外と APFS では、`名前` と並ぶ `._名前` も通常のファイルとしてコピー・移動される | 共通 |
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
- **windows-fat32-large ジョブ（`windows-latest`）**
  V24 のうち、FAT32 のファイルの大きさの上限まで実際に書くプローブ（数 GB を書くので時間がかかる）だけを、windows ジョブと並行して実行する。
  1. diskpart で 8 GB の容量可変の VHD を作成・アタッチして FAT32 でフォーマットし、`FSOPS_PROBE_FAT32_LARGE_DIR` に設定する。
  2. `go test -count=1 -v -run TestV24Large ./internal/fsops/internal/probe/`
- **macos ジョブ（`macos-latest`）**
  1. `hdiutil` で 64 MB の APFS イメージを作成してマウントする（§18.3 と同じ）。さらに exFAT と FAT32 のイメージを作ってマウントする
  2. `FSOPS_CROSSVOL_DIR=/Volumes/fsopstest`、`FSOPS_TEST_TRASH=1`、`FSOPS_PROBE_EXFAT_DIR`、`FSOPS_PROBE_FAT32_DIR` を設定する
  3. `go vet ./...`
  4. `go test -race -p 1 ./...`
  5. `CGO_ENABLED=0 go vet ./...` と、ごみ箱が使えないことを確かめるテスト（§18.4 の I5 の行）の `CGO_ENABLED=0` での実行
- **ubuntu ジョブ（`ubuntu-latest`、公開・非公開に関わらず毎回実行）**
  1. `gofmt -l .` の出力が空であること
  2. `go vet ./...`、`GOOS=windows go vet ./...`、`GOOS=darwin CGO_ENABLED=0 go vet ./...`
  3. 64 MB の vfat のイメージを `sudo mount -o loop` でマウントして `FSOPS_PROBE_FAT32_DIR` に設定する
  4. 64 MB の ext4 のイメージを同じくマウントして `FSOPS_CROSSVOL_DIR` に設定する（Linux の実装をボリュームをまたぐテストでも確かめるため。総点検の穴 8）。
     exFAT は、ランナーのカーネルに exfat モジュールがなく、`exfat-fuse` でもマウントできなかったので用意しない。
  5. `go test -race -p 1 ./...`（テスト一式。ごみ箱が使えないことのテスト（§18.4 の I5 の行）と、動作確認用 CLI（`cmd/fsopsctl`）のテストを含む）
  6. プローブ（`internal/probe`）と、環境によって Skip しうるテストを `-v` で実行する
- 要検証事項のプローブは、結果をログに残すため、各ジョブで `go test -v ./internal/fsops/internal/probe/` を別の手順として実行する。
  失敗の原因を調べられるように、テスト一式が失敗しても実行する（`if: ${{ !cancelled() }}`）。
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
  - **追加確認（2026-09-24、windows-latest）:** Windows の exFAT・FAT32 のファイルインデックスは、同じフォルダの中でも、名前の長さが変わるリネームで変わる
    （一時名 `.fsops-<16 進>.tmp` から最終名へのリネームで確認）。→ §10.1 の手順 7 の、最終名にした後の確認は、このボリュームでは行わない。
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
  - **追加確認（2026-09-24、windows-latest。総点検の穴 7）:** fsops の設定（`FOF_WANTNUKEWARNING` あり）で事前確認を飛ばすと、最大サイズを超える
    ファイル（2 MiB）もフォルダ（600000 バイト × 2）も、確認ダイアログで止まった（30 秒で強制終了）。どちらも完全に残った。

- **V20** シンボリックリンクを作れないボリューム（exFAT・FAT32・vfat）でシンボリックリンクを作ったときに返るエラー番号（§14.2、§17）。
  §17 では Unix に `KindLinkUnsupported` の対応がなく、`EPERM` などは `KindPermission` になる。対応を加えるかを、結果を見て決める。
  - **結果（2026-09-24、windows-latest・macos-latest・ubuntu-latest、Go 1.27.1）:** Windows の exFAT・FAT32 では `ERROR_INVALID_FUNCTION`（1）、Linux の vfat では `EPERM` で失敗した。
    macOS の exFAT・FAT32（`hdiutil` のイメージ）では作成できた。→ リンクの作成時に限り、どちらも `KindLinkUnsupported` にする（§17）。

- **V21** 排他リネーム（§8.4）の代わりになる不可分な操作が、ボリュームの種類ごとに使えるか（総点検の穴 5。§8.4 の代わりの手段の残る危険を閉じられるか）。
  - **結果（2026-09-24、windows-latest・macos-latest・ubuntu-latest、Go 1.27.1）:** macOS の exFAT では、`renamex_np` の `RENAME_EXCL`・`RENAME_SWAP`、
    ハードリンク（`link`）、`clonefile` のどれも `ENOTSUP`。macOS の FAT32 では `RENAME_EXCL`・`RENAME_SWAP` が使え、ハードリンクと `clonefile` は使えない。
    Windows の exFAT・FAT32 では `MoveFileExW(0)` が使え（既存の名前には `ERROR_ALREADY_EXISTS`）、ハードリンクは `ERROR_INVALID_FUNCTION`。
    Linux の vfat では `RENAME_NOREPLACE`・`RENAME_EXCHANGE` が使え、ハードリンクは `EPERM`。どの環境でも、既存のファイルは上書きされなかった。
    → §8.4 の代わりの手段が使われるのは macOS の exFAT だけで、そこには代わりになる不可分な操作がない。§8.4 の残る危険は受け入れる。
- **V23** Windows で、開いたハンドルでファイル・フォルダを削除する方法（`SetFileInformationByHandle` の `FileDispositionInfo`・`FileDispositionInfoEx`）が
  NTFS・exFAT・FAT32 で使えるか、ほかのハンドルが開いているときに名前がすぐ消えるか（`DeleteFileW`・`RemoveDirectoryW` と比べる）、読み取り専用のファイルでどう失敗するか
  （§13.2 のファイルの削除をハンドルで行うため。総点検の穴 2 の残り）。
  - **結果（2026-09-24、windows-latest（build 26100）、Go 1.27.1）:** NTFS では `DeleteFileW`・`RemoveDirectoryW` と `FileDispositionInfoEx`（POSIX 形式）は、
    ほかのハンドル（`FILE_SHARE_DELETE` つき）が開いていても名前がすぐ消えた。`FileDispositionInfo` では、そのハンドルが閉じるまで名前が削除待ちで残った
    （`Lstat` が `ERROR_ACCESS_DENIED`）。exFAT・FAT32 では `FileDispositionInfoEx`（POSIX 形式）が `ERROR_INVALID_PARAMETER` で何もせず失敗し、
    `DeleteFileW`・`RemoveDirectoryW` と `FileDispositionInfo` はどちらも削除待ちで残した。読み取り専用のファイルは、どの方法でも `ERROR_ACCESS_DENIED` で失敗し、残った。
    → §13.2 は POSIX 形式で印を付け、`ERROR_INVALID_PARAMETER` なら `FileDispositionInfo` にする（`DeleteFileW`・`RemoveDirectoryW` と同じ結果になる）。
- **V22** macOS の exFAT・FAT32 で、拡張属性を保存する AppleDouble ファイル（`._名前`）が、名前の変更・削除でどう扱われるか。そうしたボリュームを見分けられるか
  （総点検の穴 12。手元の Mac では、テストが作るファイルに OS が `com.apple.provenance` を付けるため `._名前` ができ、名前の一覧を比べるテストが失敗していた）。
  - **結果（2026-09-24、macos-latest（macOS 26）と手元の macOS 26、Go 1.27.1）:** exFAT・FAT32 とも同じ。`name` に拡張属性を付けると `._name` ができ、
    列挙には通常のファイルとして現れる。`name` の名前を変えると `._name` も一緒に移り（移動先の `._name` は置き換わる）、拡張属性は残る。
    `name` を削除すると `._name` も消える（フォルダの `rmdir` も同じ）。`._name` を先に移すと `name` の拡張属性は読めなくなり、続けて `name` を移すと、
    先に移した `._name` は OS に消される。拡張属性のない `name` を、`._name` だけがある場所へ移すと、その `._name` は消える。
    `statfs` のファイルシステム名は APFS が `apfs`、exFAT が `exfat`、FAT32 が `msdos`。`pathconf(_PC_XATTR_SIZE_BITS)` は 56 と 31 で、見分けには使わない。
    fsops は `._name` を独立した項目として扱っていたため、同一ボリュームのマージ移動で §15 の `com.apple.quarantine` が失われていた（`Done` と報告）。
    → §8.5 のとおり、`msdos`・`exfat` では `name` と並ぶ `._name` を付属として扱う。
- **V26** Windows で、ほかのハンドルが開いたまま削除の印を付けられた（削除待ちの）ファイルに対する操作（属性の取得・開く・削除・親フォルダの削除・列挙）が、
  どのエラーと NT ステータス（`RtlGetLastNtStatus`）を返すか（NTFS・exFAT・FAT32。印は exFAT・FAT32 で使う従来の `FileDispositionInfo` で付ける）。
  削除待ちは、ほかのプロセス（ウイルス対策ソフトなど）が閉じれば消えるのに、今は「権限がありません」や、フォルダの「空ではありません」と報告している
  （2026-09-25 の報告の見直し）。`STATUS_DELETE_PENDING`（0xC0000056）で見分けられれば、`KindLocked` にして §17.1 のやり直しの対象にする案を出す。
  - **結果（2026-09-25、windows-latest（build 26100）、Go 1.27.1）:** NTFS・exFAT・FAT32 とも同じ。削除待ちのファイルへの `GetFileAttributes`・
    `CreateFile`（`FILE_READ_ATTRIBUTES`・`DELETE`）・`DeleteFileW` は、どれも `ERROR_ACCESS_DENIED` で、NT ステータスは `STATUS_DELETE_PENDING`（0xC0000056）。
    親フォルダの `RemoveDirectoryW` は `ERROR_DIR_NOT_EMPTY`（NT ステータス 0xC0000101）で、列挙には名前が残る。ほかのハンドルを閉じると名前が消え、
    親フォルダも削除できた。→ 失敗の直後の NT ステータスで削除待ちを見分けられる。
- **V25** 空のファイルの fileID（§8.3）が、メタデータの設定・同じフォルダの中での名前の変更・書き込みで変わるか（ボリュームの種類ごと。中身のあるファイルと比べる）。
  macOS の exFAT・FAT32 へ空のファイルをコピーすると、メタデータを設定した後の照合（§10.1）が合わずに失敗し、一時ファイルが残ることが、
  2026-09-25 の報告の見直しで見つかった（V16 は中身のあるファイルだけを確かめていた）。
  - 手元の macOS 26（`hdiutil` のイメージ、Go 1.27.1）: exFAT・FAT32 とも、空のファイルの ino は `2^64 − n` の値で、操作のたびに変わった
    （作成・chmod・chtimes・名前の変更のそれぞれの後で別の値）。書き込むと小さな値になり、その後は変わらない。中身のあるファイルと APFS では変わらない。
  - **結果（2026-09-25、macos-latest・ubuntu-latest・windows-latest、Go 1.27.1。64 MB のボリューム）:**
    macOS の exFAT・FAT32 では手元と同じく、空のファイルの ino が `2^64 − n` の値で操作のたびに変わった。中身のあるファイルは変わらない。
    Linux の vfat では、空のファイルでも変わらなかった。Windows の exFAT・FAT32 では、空のファイルでもファイルインデックスは変わらなかった
    （名前の長さが変わる名前の変更で変わるのは、中身のあるファイルと同じ。V16）。
    → macOS の exFAT・FAT32 の空のファイルは、fileID で同一性を確かめられない（コピー元・コピー先・一時ファイル・移動元のどれでも）。
- **V24** FAT32 のファイルの大きさの上限（4 GiB − 1 バイト）を超える書き込みで返るエラー番号（§17。4 GiB を超えるファイルを FAT32 の USB メモリへコピーする場合）。
  容量不足（§10.3、§7.2 で残りの書き込みをすべて Skipped にする）と区別できるかを確かめ、区別できれば専用の Kind を設けるかを決める。
  CI のボリュームは 64 MB なので、上限の直前・直後の位置へ 1 バイト書く・大きさを変えることで、実際に 4 GiB を順に書く場合の代わりとする。
  上限ちょうど（大きさ 4 GiB − 1）は容量不足になるはずで、上限を超える場合と比べる。上限のない exFAT でも同じことをして比べる。
  - 手元の macOS 26（`hdiutil` の 64 MB のイメージ、Go 1.27.1）: FAT32 では上限を超える書き込み・大きさの変更が `EFBIG`（27）、上限ちょうどは `ENOSPC`（28）。
    exFAT ではどれも `ENOSPC`。どの場合もファイルの大きさは 0 のまま。
  - **結果（2026-09-24、windows-latest・macos-latest・ubuntu-latest、Go 1.27.1。64 MB のボリューム）:**
    macOS の FAT32 と Linux の vfat では、上限を超える書き込み・大きさの変更が `EFBIG`（27）、上限ちょうどは `ENOSPC`（28）で、区別できる
    （Linux の vfat は、上限ちょうどの失敗の後に、確保できた分（約 63 MB）だけ大きくなったファイルを残した。macOS はどれも大きさ 0 のまま）。
    macOS の exFAT はどれも `ENOSPC`。Windows では FAT32・exFAT とも、上限を超える場合も含めてどれも `ERROR_DISK_FULL`（112）で、大きさは 0 のまま。
    Windows は空き容量を先に確かめているとみられ、64 MB のボリュームでは上限を超えたときのエラー番号がわからない（未確定）。
    → Unix の `EFBIG` は容量不足と区別できる。Windows は、空きが 4 GiB を超える FAT32 のボリュームで確かめてから決める。
  - Windows の追加の確認（`TestV24Large`。§19 の windows-fat32-large ジョブ、`FSOPS_PROBE_FAT32_LARGE_DIR`）: 空きが 4 GiB を超える FAT32 で、
    上限を超える位置への 1 バイトの書き込み・大きさの変更と、コピーと同じく 1 MiB ずつ上限ちょうどまで順に書いた後の 1 バイトの書き込みのエラー番号を記録する。
  - **結果（2026-09-24、windows-latest、Go 1.27.1。8 GB の FAT32 の VHD、空き約 8 GB）:** 上限を超える位置への書き込み・大きさの変更も、
    1 MiB ずつ上限ちょうど（4294967295 バイト）まで順に書いた後の 1 バイトの書き込みも、`ERROR_DISK_FULL`（112）で失敗した（大きさは上限のまま）。
    → Windows では、上限を超えたことをエラー番号で容量不足と区別できない。今の分類では `KindNoSpace` になり、§7.2 により残りの書き込みを伴う項目がすべて Skipped になる。
    コピー先のファイルシステムの種類とファイルの大きさで、書く前に判断する必要がある。
  - → `KindFileTooLarge` を設け、コピー先のファイルシステムの種類とファイルの大きさで、計画時に警告し、実行時は書く前に失敗にする（§6.4、§10.6、§17）。

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

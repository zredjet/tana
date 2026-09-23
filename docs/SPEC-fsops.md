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

ファイル構成の案（フェーズ0で改善提案してよい）:

```
<repo>/
  CLAUDE.md
  docs/SPEC-fsops.md
  go.mod
  internal/fsops/
    doc.go
    types.go                  リクエスト・計画・結果などの型
    errors.go                 Kind・OpError・共通の分類
    errors_windows.go         Windows のエラー番号の分類
    errors_unix.go            Unix の errno の分類
    path.go                   絶対パスの検査、祖先の判定
    longpath_windows.go       \\?\ の付与
    entry.go                  エントリ種類の判定（共通部）
    entry_windows.go          属性・リパースタグによる判定
    entry_unix.go
    rename.go                 排他リネーム・置換リネームの共通部
    rename_windows.go
    rename_darwin.go
    rename_linux.go
    walk.go                   リンクに入り込まない走査
    plan.go                   NewPlan
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
    space_windows.go / space_unix.go
    progress.go
    deps_test.go              依存の許可リストの検査
    internal/testfs/          テスト用フィクスチャ
  cmd/fsopsctl/               動作確認用 CLI（フェーズ9）
  .github/workflows/test.yml
```

依存のルール:

- `internal/fsops` が import してよいのは、標準ライブラリ、`golang.org/x/sys/...`、`golang.org/x/text/...`、および `internal/fsops` 配下のパッケージだけ。
- `deps_test.go` で `go list -deps` を実行し、許可リスト外の依存があればテストを失敗させる。
- 将来ほかのプロジェクトから使う必要が出たら、`internal/` の外へ移動するか別モジュールに切り出す。それまでは `internal/` に置く。

---

## 5. API（案）

型名・関数名はフェーズ0で改善提案してよい。ただし次の性質は変えない。

- 計画（ファイルシステムを変更しない）と実行の 2 段階に分かれている
- 衝突の決定のゼロ値は Skip 扱いである
- 実行結果を項目ごとに返す

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
	DestDir string   // OpCopy / OpMove のみ。存在するフォルダの絶対パス
}

// NewPlan は計画を作る。ファイルシステムは一切変更しない。
func NewPlan(ctx context.Context, req Request) (*Plan, error)

type Plan struct {
	Req        Request
	Items      []Item      // トップレベル（Sources 1 件ごと）
	Conflicts  []*Conflict // 呼び出し側が Decision を書き込んでから Execute に渡す
	TotalFiles int
	TotalBytes int64
	Warnings   []*OpError // 空き容量不足の見込みなど。実行は妨げない
}

type Item struct {
	Src, Dst string // Dst は OpTrash / OpDelete では空
	Info     EntryInfo
	Method   Method
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
	Size    int64
	ModTime time.Time
}

// ---- 衝突 ----

type Conflict struct {
	Src, Dst string
	SrcInfo  EntryInfo
	DstInfo  EntryInfo
	Self     bool      // コピー先がコピー元そのもの（同じフォルダへのコピー）
	Parent   *Conflict // フォルダ同士の衝突の内側で見つかった場合、その親
	Decision Decision
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

// Execute は計画を実行する。
// error が返るのは、計画が不正で何も実行しなかった場合だけ（§7.1）。
func Execute(ctx context.Context, p *Plan, opt ExecOptions) (*Result, error)

type Result struct {
	Status Status
	Items  []ItemResult // トップレベルの結果と、フォルダ内で失敗・スキップした項目
}

type Status int

const (
	StatusCompleted Status = iota + 1
	StatusCompletedWithErrors
	StatusCanceled
)

type ItemResult struct {
	Src, Dst    string
	Outcome     Outcome
	Err         *OpError   // Outcome が Done 以外のとき
	Warnings    []*OpError // メタデータを保持できなかった等（データ自体は無事）
	TrashedPath string     // ごみ箱に入った後のパス（取得できた場合）
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
```

---

## 6. 計画（NewPlan）

計画の作成中は、ファイルシステムを一切変更しない。`ctx` のキャンセルに応じて中断できる。

### 6.1 リクエストの検査

- `Sources` が 1 件以上であること。すべて絶対パスであること（§8.1）。
- 同じパスの重複、または一方が他方の内側にある組み合わせ（`/a` と `/a/b`）は `KindInvalidRequest`。
- `OpCopy` / `OpMove` では、`DestDir` が存在するフォルダであること（`os.Stat` で確認。`DestDir` 自体がリンクの場合は辿ってよい）。

### 6.2 項目ごとの判定

- 各 `Source` は `os.Lstat` で調べる（リンクを辿らない）。種類の判定は §14.1。
- `OpCopy` / `OpMove` の `Dst` は `DestDir` + コピー元の名前（バイト単位でそのまま、I6）。
- コピー元がフォルダで、`DestDir` がその内側にある場合は `KindDestInsideSource`（判定方法は §8.3）。
- `OpMove` で `DestDir` がコピー元の親フォルダそのものなら `KindSameFile`。
- `OpCopy` で `Dst` がコピー元そのものなら、`Self: true` の衝突として扱う。
- `OpMove` の方式: コピー元と `DestDir` のボリュームが同じなら `MethodRename`、違えば `MethodCopyThenRemove`。
  ボリュームの判定は、Windows ではボリュームシリアル番号、Unix では `Stat_t.Dev` を使う。
  実行時に `MethodRename` がボリューム違いのエラーになった場合は、`MethodCopyThenRemove` に切り替える（§11.1）。

### 6.3 走査と衝突の検出

- フォルダは §13.1 の走査で中身を数え、`TotalFiles` と `TotalBytes` を求める。
  `MethodRename` の項目はバイト数を数えない（データを書かないため）。
- `Dst` が既に存在すれば `Conflict` を作る。
- フォルダ同士の衝突では、中身も走査して内側の衝突を `Parent` 付きで `Conflicts` に加える。
  内側の衝突は、親の決定が `DecisionMerge` のときだけ意味を持つ。

### 6.4 空き容量

- `OpCopy` と `MethodCopyThenRemove` では、書き込むバイト数とコピー先ボリュームの空き容量を比べ、足りなければ `Warnings` に `KindNoSpace` を加える。実行は妨げない。
- 空き容量は、Windows では `GetDiskFreeSpaceEx`、Unix では `statfs` の `Bavail * Bsize` で求める。

---

## 7. 実行（Execute）と結果

### 7.1 実行前の検査

次の場合は何も実行せず error を返す。

- 同じ `Plan` を 2 回実行しようとした
- 許されない決定がある（§9.1 の表）
- 計画が `NewPlan` 以外で作られた、または必要なフィールドが欠けている

### 7.2 処理順と失敗時の扱い

- トップレベルの項目を計画の順に 1 件ずつ処理する（並列化しない）。
- 1 件が失敗しても、残りの項目の処理は続ける。
- ただし容量不足（`KindNoSpace`）が起きたら、残りの書き込みを伴う項目はすべて `OutcomeSkipped`（`KindNoSpace`）にして終了する。
- キャンセルされたら、処理中の項目を安全に中断し（§16）、残りを `OutcomeSkipped`（`KindCanceled`）にして `StatusCanceled` で返す。

### 7.3 計画後の変化

- 計画から実行までの間にファイルシステムが変わることを前提にする。
- 「存在確認してから書く」ではなく、OS の排他的な操作（`O_EXCL` での作成、§8.4 の排他リネーム、`os.Mkdir`）で書き込むことで、計画後に現れた衝突を確実に検出する。
- 計画時になかった衝突が見つかったら、その項目を `OutcomeSkipped`（`KindExist`）にする（I1）。
- 計画時にあったコピー元が消えていたら `OutcomeFailed`（`KindNotFound`）。

### 7.4 結果

- `Result.Items` には、トップレベルの全項目の結果と、フォルダ内で失敗・スキップしたエントリの結果を入れる。
- 1 件以上の Failed / Partial / CopiedSourceKept があれば `StatusCompletedWithErrors`。

---

## 8. パスの扱い

### 8.1 絶対パスのみ

- `filepath.IsAbs` が偽のパスは `KindInvalidRequest`。受け取ったパスは `filepath.Clean` する。
- Windows では `C:\...` と UNC（`\\server\share\...`）を受け付ける。
  ドライブ相対（`C:foo`）、ルート相対（`\foo`）、呼び出し側が付けた `\\?\` は拒否する。

### 8.2 長いパス（Windows）

- `os` パッケージは Windows の長いパス（260 文字超）を内部で処理するので、`os` の関数はそのまま使える。
- `golang.org/x/sys/windows` の関数（`MoveFileEx` など）を直接呼ぶときは、自前の helper で `\\?\` を付ける。
  UNC は `\\?\UNC\server\share\...` の形にする。
- `\\?\` を受け付けない API（`SHFileOperationW` など）では、長いパスを扱えない場合がある（§12.2、V4）。

### 8.3 同一性と祖先の判定

- パスの同一性を文字列で比較しない。大文字小文字、NFC/NFD、8.3 形式の短縮名、ジャンクション・`subst` による別名があるため。
- 同じファイルかどうかは `os.SameFile` で判定する。
- 「`DestDir` がコピー元の内側か」は次の手順で判定する。
  1. `DestDir` を `filepath.EvalSymlinks` で実パスにする（リンク経由の指定を見抜くため）
  2. その実パスから親フォルダを順に辿り、各段で `os.SameFile(コピー元, 祖先)` を調べる
  3. 一致すれば `KindDestInsideSource`

### 8.4 排他リネームと置換リネーム

- **排他リネーム**（移動先が存在すれば失敗する。アトミック）:
  - Windows: `MoveFileExW(src, dst, 0)`（`MOVEFILE_REPLACE_EXISTING` を付けない。`MOVEFILE_COPY_ALLOWED` も付けない）
  - macOS: `renamex_np(src, dst, RENAME_EXCL)`
  - Linux: `renameat2(..., RENAME_NOREPLACE)`
- **置換リネーム**（ファイル同士の上書き用）: `os.Rename`。フォルダを置き換える用途には使わない。
- **大文字小文字・正規化の違いだけの名前変更**:
  `dst` を `Lstat` して `os.SameFile(src, dst)` が真で、かつ名前の文字列が異なる場合は、「同じファイルの名前変更」とみなして OS の通常のリネームで行う（V1、V2）。
- 排他リネームが「存在する」で失敗した場合は `KindExist`。

### 8.5 名前を変換しない

- fsops はファイル名を正規化しない。コピー先の名前はコピー元の名前をバイト単位でそのまま使う（I6）。
- 衝突の検出は OS の `Lstat` に任せる。APFS は NFC と NFD を同じ名前として扱い、NTFS は別の名前として扱うが、`Lstat` に任せればどちらでも正しく動く。
- 表示・検索のための正規化は UI 側で行う。

---

## 9. 衝突

### 9.1 許される決定

| 衝突の種類 | Skip | Overwrite | AutoRename | Merge |
|---|---|---|---|---|
| ファイル → 既存ファイル | ○ | ○ | ○ | × |
| フォルダ → 既存フォルダ | ○ | × | ○ | ○ |
| 種類が違う（ファイル ↔ フォルダ、リンクを含む） | ○ | × | ○ | × |
| 自分自身（`Self`） | ○ | × | ○ | × |

`DecisionUnset` はすべての種類で Skip として扱う。表の × を設定した計画は、実行前の検査でエラーにする（§7.1）。

### 9.2 自動リネーム

- 形式は `name (2).ext`。使われていれば `(3)`、`(4)`… と増やす。
- 拡張子は最後の `.` 以降。先頭が `.` の名前（`.gitignore`）と拡張子のない名前は、末尾に付ける（`.gitignore (2)`）。フォルダは拡張子を区別しない。
- 候補の名前の確保も排他的に行う（ファイルは排他リネーム、フォルダは `os.Mkdir`）。「存在する」で失敗したら次の番号を試す。上限は 9999 回。

### 9.3 上書き

- 一時ファイルに書き込んだあと、置換リネームで既存ファイルと入れ替える。
- 上書き先が読み取り専用なら、上書きせず `OutcomeFailed`（`KindReadOnly`）にする。
  読み取り専用とは、Windows では読み取り専用属性、macOS ではオーナーの書き込み権限がないこと、またはロック（`UF_IMMUTABLE`）を指す。
  （macOS の rename はファイル自身の権限を見ないため、両 OS で結果をそろえるために事前に確認する。）
- 上書き先が他のプロセスに使用されていて置き換えられない場合は `KindLocked`。上書き先は元のまま、一時ファイルは削除する。
- 上書きされた元のファイルはどこにも退避しない（退避は §21 の将来の検討事項）。

### 9.4 マージ

- 既存のフォルダはそのまま使い、中身を 1 件ずつ処理する。内側の衝突はそれぞれの決定に従う。

---

## 10. コピー

### 10.1 ファイル

1. コピー元を開き、`Lstat` でサイズと更新日時を記録する。
2. コピー先のフォルダに一時ファイルを `O_CREATE|O_EXCL|O_WRONLY` で作る。
   名前は `.fsops-<ランダム16進>.tmp` の固定長にする（元の名前を含めると、名前の長さの上限を超えることがあるため）。
3. 1 MiB のバッファで内容を書き込む。バッファごとに `ctx` を確認し、進捗を報告する。
4. 同期が必要なら（§10.5）`File.Sync` する。
5. 閉じて検証する（§10.4）。
6. メタデータを設定する（§15）。
7. 最終名にする。衝突なしは排他リネーム、上書きは置換リネーム、自動リネームは §9.2。
8. 2〜7 のどこかで失敗・キャンセルしたら、一時ファイルを削除する（I3）。

### 10.2 フォルダ

- コピー先のフォルダを `os.Mkdir` で作る（マージのときは既存のものを使う）。
- 中身を名前順に処理する。ファイルは §10.1、フォルダは再帰、リンクと特殊なファイルは §14。
- フォルダの更新日時は、中身をすべて処理した後に設定する。
- 一部のエントリが失敗しても残りは続け、トップレベルの結果を `OutcomePartial` にする。

### 10.3 容量不足

- 書き込み中の容量不足（Windows: `ERROR_DISK_FULL`、`ERROR_HANDLE_DISK_FULL`、Unix: `ENOSPC`、`EDQUOT`）は `KindNoSpace`。
- 処理中の一時ファイルを削除し、§7.2 に従って残りを Skipped にする。

### 10.4 検証

- `VerifySize`（既定）: 書き込んだバイト数、一時ファイルのサイズ、コピー開始時のコピー元のサイズが一致すること。
  さらに、コピー後にコピー元を `Lstat` し直し、サイズと更新日時が開始時から変わっていないこと。
  変わっていたら `KindSourceChanged` で失敗にし、一時ファイルを削除する。
- `VerifyHash`: 上記に加え、読み込み時に計算した SHA-256 と、一時ファイルを読み直して計算した SHA-256 を比べる。

### 10.5 同期

- 移動（`MethodCopyThenRemove`）では、移動元を消す前に必ず `File.Sync` する。移動元を消した直後に電源が落ちても、移動先のデータが残るようにするため。
- コピーでは `SyncAlways` のときだけ同期する（USB メモリなどで遅くなるため）。

---

## 11. 移動と名前の変更

### 11.1 同一ボリューム（`MethodRename`）

- 衝突なし: 排他リネーム。
- 上書き（ファイル同士）: 置換リネーム（§9.3 の事前確認を行う）。
- 自動リネーム: §9.2 の候補名へ排他リネーム。
- マージ（フォルダ同士）: 中身を 1 件ずつ移動する（内側の衝突はそれぞれの決定に従う）。
  最後に移動元のフォルダが空なら `os.Remove` で削除する。空でなければ残して報告する。
- リネームがボリューム違いのエラー（Windows: `ERROR_NOT_SAME_DEVICE`、Unix: `EXDEV`）で失敗したら、その項目を §11.2 の方式でやり直す。

### 11.2 ボリュームをまたぐ移動（`MethodCopyThenRemove`）

1. §10 の手順でコピーする。同期は必ず行う。コピーしたエントリの一覧を記録する。
2. トップレベルの項目の中身が 1 件でも失敗・スキップ・キャンセルになったら、移動元に一切手を付けない（I2）。
   結果は `OutcomePartial` または `OutcomeFailed`。移動先に途中までコピーされたものは削除せず、そのまま報告する。
3. すべて成功したら、記録した一覧に沿って移動元を削除する（§13.3）。削除は完全削除（移動先で検証済みのため）。
4. 移動元の削除に一部でも失敗したら（使用中など）、`OutcomeCopiedSourceKept` にして、残ったパスを報告する。

### 11.3 名前の変更（`Rename`）

- `newName` はフォルダ区切りを含まない名前であること。`.`、`..`、空文字は `KindInvalidName`。
- Windows では、使えない文字（`< > : " / \ | ? *` と制御文字）、末尾の `.` と空白、予約名（`CON`、`PRN`、`AUX`、`NUL`、`COM1`〜`COM9`、`LPT1`〜`LPT9`。拡張子付きも含む）を `KindInvalidName` にする。
- macOS では `/` と NUL 文字を `KindInvalidName` にする。
- 排他リネームで行う。上書きは一切しない。大文字小文字だけ・正規化だけの違いは §8.4 に従って許可する。

---

## 12. ごみ箱

### 12.1 共通

- `OpTrash` はトップレベルの項目だけを扱い、中身を走査しない（項目ごとごみ箱へ移すため）。
- 1 項目ずつ処理し、結果を項目ごとに返す。
- ごみ箱が使えないと判断したら、何もせず `KindTrashUnavailable` を返す（I5）。

### 12.2 Windows

- 事前確認:
  - `GetVolumePathName` でボリュームのルートを求め、`GetDriveType` が `DRIVE_FIXED` の場合だけごみ箱を使う。
    それ以外（リムーバブル、ネットワーク、`\\server\share` など）は `KindTrashUnavailable`。
    これらの場所では、Windows のごみ箱操作が確認なしに完全削除になりうるため。
  - パスが `MAX_PATH` を超える場合の扱いは V4 の結果で決める。
- 実装: shell32.dll の `SHFileOperationW` を `FO_DELETE` で呼ぶ。
  フラグは `FOF_ALLOWUNDO | FOF_NOCONFIRMATION | FOF_SILENT | FOF_NOERRORUI`。
  `pFrom` は NUL 文字 2 つで終わる形式。1 回の呼び出しで 1 項目だけ渡す。
  `golang.org/x/sys/windows` に定義がなければ `NewLazySystemDLL` で呼ぶ。
- 戻り値が 0 以外、または `fAnyOperationsAborted` が真なら失敗。戻り値は Win32 のエラー番号と一致しないことがあるので、分類できないものは `KindUnknown` にして値を `Err` に残す。
- スレッド・COM の初期化の要否は V4 で確認する。
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

- `os.ReadDir` と `os.Lstat` を使い、リンクを辿らない。
- 入り込むのは、§14.1 で `TypeDir` と判定したエントリだけ。`TypeSymlink`、`TypeJunction`、`TypeSpecial` には入り込まない（I4）。
- 名前順に処理する。
- 走査はコピーの計画（§6.3）と完全削除（§13.2）の両方で使う。

### 13.2 完全削除（`OpDelete`）

- 後順（中身を先、フォルダを後）で削除する。
- ファイル・リンク・特殊なファイルは `os.Remove`。リンクはリンク自体だけが消えることを確認する（V3）。
- フォルダは中身を消した後に `os.Remove`（空でなければ失敗するので、消し残しがあれば自然に残る）。
- 1 件失敗しても残りは続け、トップレベルの結果を `OutcomePartial` にする。
- Windows の読み取り専用ファイルは削除に失敗する。属性を勝手に外さず `KindReadOnly` として報告する。

### 13.3 記録した項目だけの削除（移動元の削除）

- §11.2 で記録した一覧のエントリだけを削除する。
- ファイルとリンクを先に消し、フォルダは深い順に `os.Remove` する。空でなければ残す（コピー中に追加されたファイルがあると空にならないため、そのファイルは残る）。

---

## 14. リンクと特殊なファイル

### 14.1 種類の判定

- Unix: `Lstat` のモードで判定する。シンボリックリンクは `TypeSymlink`。FIFO・ソケット・デバイスは `TypeSpecial`。
- Windows: Go のモードビットだけに頼らない。Go 1.23 以降、ジャンクションは `ModeSymlink` ではなくなり、シンボリックリンク以外のリパースポイントは `ModeIrregular` になるなど、Go のバージョンで扱いが変わってきたため。次の手順で判定する。
  1. ファイル属性（`FileInfo.Sys()` の `*syscall.Win32FileAttributeData`）に `FILE_ATTRIBUTE_REPARSE_POINT` があるか
  2. あれば、リパースタグを取得する（`FILE_FLAG_OPEN_REPARSE_POINT` で開いて `GetFileInformationByHandleEx` の `FileAttributeTagInfo`、または `FindFirstFile` の `dwReserved0`）
  3. タグで分類する:
     - `IO_REPARSE_TAG_SYMLINK` → `TypeSymlink`
     - `IO_REPARSE_TAG_MOUNT_POINT` → `TypeJunction`
     - OneDrive などのクラウドファイル（`IO_REPARSE_TAG_CLOUD` 系）と重複除去（`IO_REPARSE_TAG_DEDUP`）→ 通常のファイル・フォルダとして扱う
     - それ以外 → `TypeSpecial`
- クラウドファイルを通常扱いにするのは、OneDrive でリダイレクトされたデスクトップ・ドキュメントを普通に操作できるようにするため。未ダウンロードのファイルは、読み込み時にダウンロードが発生する（V9）。

### 14.2 操作ごとの扱い

| 種類 | コピー（`LinkKeep`） | コピー（`LinkSkip`） | 移動（同一ボリューム） | ごみ箱・完全削除 |
|---|---|---|---|---|
| `TypeSymlink` | リンク先の文字列をそのまま使ってリンクを作る。権限不足なら `KindLinkUnsupported` で失敗 | Skipped（`KindLinkUnsupported`） | リンク自体を移動 | リンク自体だけ |
| `TypeJunction` | 複製しない。Skipped（`KindLinkUnsupported`） | 同左 | リンク自体を移動 | リンク自体だけ |
| `TypeSpecial` | 複製しない。Skipped（`KindUnsupportedType`） | 同左 | そのまま移動 | エントリ自体だけ |

- ボリュームをまたぐ移動は「コピー → 移動元の削除」なので、コピーの列に従う。Skipped が 1 件でもあれば、I2 により移動元は削除しない。
- 相対パスのシンボリックリンクは、リンク先の文字列を書き換えない。

---

## 15. メタデータ

保持するもの（データの書き込み後、最終名にする前に設定する）:

- 更新日時（`os.Chtimes`。アクセス日時は更新日時と同じ値にする）
- Unix のパーミッション（`0o777` の範囲。setuid・setgid・sticky は保持しない）
- Windows の読み取り専用属性（`os.Chmod`）と隠し属性（`SetFileAttributes`）
- フォルダの更新日時（§10.2）

安全上、保持を検討するもの（V6 の結果を見てから、フェーズ5で扱いを決める）:

- Windows の `Zone.Identifier`（インターネットから取得したことを示す代替データストリーム）
- macOS の `com.apple.quarantine`（同様の拡張属性）

これらが失われると、ダウンロードしたファイルをコピーした際に Office の保護ビューなどの警告が出なくなるため。

保持しないもの（§21）: ACL、所有者、作成日時、その他の代替データストリーム、その他の拡張属性、リソースフォーク。

メタデータの設定に失敗しても、データが無事なら項目は `OutcomeDone` とし、`Warnings` に `KindMetadata` を加える。

---

## 16. 進捗とキャンセル

```go
type Progress struct {
	Stage      Stage  // StageCopy / StageVerify / StageRemoveSource / StageTrash / StageDelete
	Current    string // 処理中のパス
	DoneFiles  int
	TotalFiles int
	DoneBytes  int64
	TotalBytes int64
}
```

- `Execute` は同期的に動く（呼び出し側が goroutine で動かす）。`Progress` は `Execute` を実行している goroutine から呼ぶ。
- 呼び出しは 100 ミリ秒に 1 回までに間引く。ただし、項目の区切りと終了時には必ず呼ぶ。
- キャンセルは、バッファ（1 MiB）ごとと、エントリごとに `ctx` を確認する。
- キャンセル時:
  - 処理中の一時ファイルを削除する
  - 処理中の移動の項目は、移動元に手を付けない
  - 完了済みの項目はそのまま残す
  - 残りの項目は `OutcomeSkipped`（`KindCanceled`）

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
	KindCrossDevice      // 内部用（移動方式の切り替えに使う）
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
- UI は `Kind` から日本語のメッセージを作る。fsops はメッセージを作らない。

主な対応:

| Kind | Windows | Unix |
|---|---|---|
| NotFound | `ERROR_FILE_NOT_FOUND`、`ERROR_PATH_NOT_FOUND` | `ENOENT` |
| Exist | `ERROR_FILE_EXISTS`、`ERROR_ALREADY_EXISTS` | `EEXIST` |
| Permission | `ERROR_ACCESS_DENIED`（読み取り専用でない場合） | `EACCES`、`EPERM` |
| Locked | `ERROR_SHARING_VIOLATION`、`ERROR_LOCK_VIOLATION` | `EBUSY` |
| ReadOnly | `ERROR_ACCESS_DENIED` かつ読み取り専用属性、`ERROR_WRITE_PROTECT` | `EROFS`、`EPERM` かつ `UF_IMMUTABLE` |
| NoSpace | `ERROR_DISK_FULL`、`ERROR_HANDLE_DISK_FULL` | `ENOSPC`、`EDQUOT` |
| CrossDevice | `ERROR_NOT_SAME_DEVICE` | `EXDEV` |
| LinkUnsupported | `ERROR_PRIVILEGE_NOT_HELD`（リンク作成時） | — |

- 可能な場合は `errors.Is(err, fs.ErrNotExist)` なども使う。
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
- `t.TempDir()` は `filepath.EvalSymlinks` で正規化してから使う。
- 障害の注入は非公開のテスト用フック（例: 書き込みのバッファごとに呼ばれる関数、移動元を消す直前に呼ばれる関数）で行う。

### 18.2 環境変数で有効にするテスト

| 変数 | 意味 | 未設定のとき |
|---|---|---|
| `FSOPS_CROSSVOL_DIR` | テスト用の別ボリューム上のフォルダの絶対パス | ボリュームをまたぐテストを Skip |
| `FSOPS_TEST_TRASH=1` | ごみ箱のテストを実行する | Skip（開発者のごみ箱を汚さないため） |

CI ではどちらも設定する（§19）。

### 18.3 手元（macOS）でボリュームをまたぐテストを実行する

```sh
hdiutil create -size 64m -fs APFS -volname fsopstest /tmp/fsopstest.dmg
hdiutil attach /tmp/fsopstest.dmg          # /Volumes/fsopstest にマウントされる
FSOPS_CROSSVOL_DIR=/Volumes/fsopstest go test ./internal/fsops/...
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
| I2 | 移動元の削除に失敗（ロック）→ `OutcomeCopiedSourceKept`、移動先は完全 | CROSSVOL・Windows |
| I3 | ファイルの途中でキャンセル（5 MiB 以上のファイル）→ 最終名のファイルも一時ファイルも残らない | 共通 |
| I3 | 書き込み途中に障害を注入 → 同上 | 共通 |
| I4 | 先に目印ファイルを置いたリンク（ジャンクション・シンボリックリンク）を含むツリーを完全削除 → 目印ファイルが残る | 共通（ジャンクションは Windows） |
| I4 | 同上をごみ箱・ボリュームをまたぐ移動で | TRASH / CROSSVOL |
| I4 | リンクを含むツリーのコピー → リンクの先の中身は複製されない | 共通 |
| I5 | `\\localhost\C$\...` 経由のパスでごみ箱 → `KindTrashUnavailable`、ファイルは残る | Windows（V7） |
| I5 | CGO なしの macOS ビルド・Linux でごみ箱 → `KindTrashUnavailable` | 共通 |
| I6 | 日本語・絵文字・NFD の名前をコピー・移動 → 名前がバイト単位で一致 | 共通 |
| I6 | 大文字小文字だけ違う名前への `Rename` | 共通（V1、V2） |
| 衝突 | 自動リネームの連番（通常、`(3)` 以降、`.gitignore`、拡張子なし、フォルダ） | 共通 |
| 衝突 | 同じフォルダへのコピー（`Self`）を自動リネームで複製 | 共通 |
| 衝突 | マージ（内側の衝突の決定がそれぞれ反映される） | 共通 |
| 計画 | コピー先がコピー元の内側（リンク経由を含む）→ `KindDestInsideSource` | 共通 |
| 計画 | 重複・入れ子の `Sources` → `KindInvalidRequest` | 共通 |
| 計画 | 計画の作成前後でファイルシステムが変化しない | 共通 |
| パス | 相対パス、ドライブ相対パス（`C:foo`）、`\\?\` 付きの拒否 | 共通・Windows |
| パス | 260 文字を超えるパスのコピー・移動・完全削除 | Windows |
| ロック | コピー元が共有なしで開かれている → その項目は `KindLocked`、他の項目は続行 | Windows |
| ロック | 上書き先が使用中 → `KindLocked`、上書き先は元のまま、一時ファイルなし | Windows |
| 読み取り専用 | 読み取り専用の上書き先 → `KindReadOnly`（両 OS で同じ結果） | 共通 |
| 読み取り専用 | 読み取り専用ファイルのコピー → 属性が保持される | 共通 |
| メタデータ | ファイル・フォルダの更新日時が保持される | 共通 |
| 検証 | コピー中にコピー元が変更された → `KindSourceChanged`、一時ファイルなし | 共通 |
| 容量 | 空き容量不足の見込みが `Warnings` に入る | CROSSVOL |
| 容量 | 書き込み中の容量不足 → `KindNoSpace`、残りは Skipped | CROSSVOL（他のテストと並行実行しない） |
| ごみ箱 | ごみ箱に入り、元の場所から消えている（Windows: `$I` ファイル、macOS: `TrashedPath`） | TRASH（V5、V10） |
| 並行性 | 進捗コールバックまわりにデータ競合がない | macOS（`-race`） |

---

## 19. CI

`.github/workflows/test.yml` の方針:

- トリガー: `push`、`pull_request`、`workflow_dispatch`。
  リポジトリが非公開なら、macOS のジョブは `pull_request` と `workflow_dispatch` のときだけ実行する（macOS ランナーは料金が高く、開発者は macOS でテストを実行できるため）。公開なら両方とも毎回実行する。
- `actions/checkout` と `actions/setup-go` は、作成時点の最新メジャーバージョンを確認して使う。Go は `go-version-file: go.mod`。
- **windows ジョブ（`windows-latest`）**
  1. diskpart で 64 MB の VHD を作成・アタッチし、NTFS でフォーマットしてドライブ文字を割り当てる（例: `T:`）
  2. `FSOPS_CROSSVOL_DIR=T:\` と `FSOPS_TEST_TRASH=1` を `GITHUB_ENV` に設定する
  3. `go vet ./...`
  4. `go test ./...`
- **macos ジョブ（`macos-latest`）**
  1. `hdiutil` で 64 MB の APFS イメージを作成してマウントする（§18.3 と同じ）
  2. `FSOPS_CROSSVOL_DIR=/Volumes/fsopstest` と `FSOPS_TEST_TRASH=1` を設定する
  3. `go vet ./...`
  4. `go test -race ./...`
  5. `CGO_ENABLED=0 go vet ./...`
- 必要なら ubuntu のジョブで `GOOS=linux` のコンパイル確認を行う（安価）。
- VHD とディスクイメージの作成はフェーズ2で追加する。フェーズ1では `go vet` と `go test` だけ。

---

## 20. 要検証事項

以下は仕様作成時点で確証がない。推測で確定させず、確かめるテストを書いて CI の結果を報告し、それに基づいて方針を決める。結果はこの節に追記する。

- **V1** Windows: `MoveFileExW(src, dst, 0)` で、大文字小文字だけ違う名前への変更が成功するか。
- **V2** macOS（APFS）: `renamex_np(RENAME_EXCL)` で、大文字小文字だけ・NFC/NFD だけ違う名前へ変更したときの動作。`golang.org/x/sys/unix` に `RenamexNp` があるか。
- **V3** Windows: 使用する Go のバージョンで、ジャンクションとディレクトリのシンボリックリンクが `Lstat` でどう見えるか（`ModeSymlink` / `ModeIrregular` / `ModeDir`）。`os.Remove` でリンク自体だけが消え、リンク先の中身が残ること。
- **V4** Windows: `SHFileOperationW` の動作。`MAX_PATH` を超えるパス、`\\?\` 付きのパス、固定ドライブ以外のパスでどうなるか（特に、黙って完全削除されないか）。goroutine から呼ぶ際に `runtime.LockOSThread` と `CoInitializeEx` が必要か。
- **V5** macOS の CI: `trashItemAtURL` が CI 上で成功するか。返されたパスを `Lstat` できるか（プライバシー保護による制限の有無）。
- **V6** Windows の `Zone.Identifier`（`path:Zone.Identifier`）を `os` で読み書きできるか。macOS の `com.apple.quarantine` を `golang.org/x/sys/unix` の `Getxattr` / `Setxattr` で読み書きできるか。
- **V7** CI: Windows ランナーで diskpart による VHD の作成・マウントができるか。macOS ランナーで `hdiutil attach` ができるか。Windows ランナーで `\\localhost\C$` にアクセスでき、`GetDriveType` が `DRIVE_REMOTE` を返すか。
- **V8** Windows ランナーでシンボリックリンクの作成権限があるか。`mklink /J` が使えるか。
- **V9** OneDrive の未ダウンロードファイルの扱い。CI では確認できないため、実機（仮想マシンの Windows など）での確認項目として記録するだけにする。
- **V10** Windows の `$Recycle.Bin\<SID>\` にある `$I` ファイルの形式（先頭から、版番号 8 バイト、元のサイズ 8 バイト、削除日時 8 バイト、版 2 ではパスの文字数 4 バイト、UTF-16 の元のパス）が想定どおりか。

---

## 21. 今回やらないこと（将来の検討事項）

- 並列コピー
- APFS の `clonefile`、Windows の `CopyFileEx` などの OS 固有の高速コピー
- 上書きされるファイルをごみ箱へ退避するオプション
- 元に戻す（Undo）
- ACL・所有者・作成日時の保持
- ジャンクションの複製
- 読み取り専用属性を外して削除するオプション
- Linux のごみ箱（FreeDesktop.org Trash 仕様）
- Windows のごみ箱操作を `IFileOperation`（COM）に移行する

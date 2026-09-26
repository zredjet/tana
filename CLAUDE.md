# CLAUDE.md

Go 製のターミナルファイラー tana（Windows / macOS 対応）のリポジトリ。
ファイル操作パッケージ `internal/fsops` は完成した（フェーズ0〜11）。フェーズ12から、TUI の土台とファイラー本体を作る。
TUI のライブラリは使わず、端末の入出力から自作する。

- 仕様（フェーズの指示で指定された節を、着手前に必ず読む）:
  - `docs/SPEC-fsops.md`: ファイル操作。本書で「SPEC §n」と書くのはこれ。
  - `docs/SPEC-tui.md`: TUI の土台（term・textwidth・keys・screen・lineedit）。「tui §n」と書く。
  - `docs/SPEC-filer.md`: ファイラー本体。「filer §n」と書く。
- 開発者の手元は macOS のみで、Windows の実機はない。
  Windows での確認は GitHub Actions（windows-latest）と、Parallels Desktop の仮想マシン（「仮想マシンでの確認」）で行う。

## コマンド

- ビルド: `go build ./...`
- テスト: `go test ./...`（macOS では `go test -race ./...` も実行する）
- 整形: `gofmt -l .` の出力が空であること
- 静的チェック: `go vet ./...`
- Windows 向けのコンパイル確認（push 前に必ず実行）: `GOOS=windows GOARCH=amd64 go vet ./...`
- CGO なしのコンパイル確認: `CGO_ENABLED=0 go vet ./...`
- Linux 向けのコンパイル確認: `GOOS=linux GOARCH=amd64 go vet ./...`
- 仮想マシン用の Windows のビルド: `GOOS=windows GOARCH=arm64 go build -o <出力先>.exe ./cmd/<名前>`
- ファジング（TUI の土台。tui §10）: `go test -run '^$' -fuzz <テスト名> -fuzztime 10m ./internal/<パッケージ>/`
- CI の確認: `gh run list --limit 5`、`gh run watch`、失敗時は `gh run view <run-id> --log-failed`
- ボリュームをまたぐテストを手元で実行する方法: SPEC §18.3

## 不変条件（最重要・変更禁止）

詳細は SPEC §2。キャンセル時・失敗時も含め、どのフェーズでも守る。UI もこれを崩さない（filer §2）。

- I1 承認されていない上書きをしない。衝突の決定が未設定なら Skip。計画後に現れた衝突は Skip して報告する。
- I2 ボリュームをまたぐ移動では、移動先の書き込み・同期・検証が済んだ項目だけ移動元を消す。消すのはコピーした項目だけ。
- I3 書きかけのファイルを最終名で残さない（一時名で書いてからリネームする）。
- I4 シンボリックリンク・ジャンクションの先に入り込んで削除・移動しない。
- I5 ごみ箱が使えない場所で、黙って完全削除にしない。
- I6 ファイル名を変換しない（Unicode 正規化・大文字小文字の変換をしない）。
- I7 キャンセル後・失敗後も I1〜I6 が成り立つ。

## UI と TUI の土台の約束

詳細は filer §2 と tui §2。変えるときは、仕様の変更として確認を取る。

- U1 上書き・マージを利用者に代わって決めない。U2 完全削除は専用の確認で `y` を押した場合だけ。先行入力と貼り付けで確定しない。
- U3 結果を隠さない。U4 fsops に渡すパスは、列挙で得た名前から作る。U5 UI を止めない。U6 ファイル名に端末を操作させない。
- T1 端末を必ず元に戻す。T2 表示する文字列から制御シーケンスを出力しない。T3 入力を失わず、作らない。
- T4 幅の判断は `textwidth` だけで行う。T5 領域の外に描かない。T6 画面は格子と一致する。

## fsops の実装ルール

- import してよいのは標準ライブラリ、`golang.org/x/sys`、`golang.org/x/text` だけ。ほかが必要なら理由を添えて私に確認する。
- 画面やログへの出力、`os.Exit` を使わない。パッケージレベルの可変状態を持たない。
- 人向けのメッセージ文字列を作らない。エラーは `Kind` で分類して返す（SPEC §17）。
- 受け付けるパスは絶対パスだけ。
- パスの同一性を文字列比較で判定しない。同一性の判定はすべて fileID で行い、`os.SameFile` は使わない（SPEC §8.3）。
- 利用者のファイルに対して `os.RemoveAll` を使わない。削除は SPEC §13 の走査で行う。
- 利用者のファイルの削除に `os.Remove` も使わない。種類ごとに `unlink`・`rmdir`・`DeleteFileW`・`RemoveDirectoryW` を使い分ける（SPEC §13.2）。fsops が作った一時ファイルには使ってよい。
- `os.Rename` は既存ファイルを置き換える。上書きしてはいけない場面では SPEC §8.4 の排他リネームを使う。
- Windows では、OS に渡すパスは `os` の関数に渡すものも含めてすべて `\\?\` 形式に変換する helper を通す（SPEC §8.2）。結果・エラーで返すパスは `\\?\` の付かない形にする。
- OS ごとのコードはファイル名のサフィックスとビルドタグで分ける。Windows・macOS・Linux・CGO なしのどれでもコンパイルが通る状態を保つ。
  `_unix`・`_other`・`_cgo`・`_nocgo` はビルド条件として認識されないので、それらのファイルには必ず `//go:build` を書く（SPEC §4）。
- テスト用フック（障害の注入など）は `ExecOptions` の非公開フィールド `hooks` で渡し、パッケージ内のテストからだけ設定する（SPEC §5、§18.1）。

## UI の実装ルール

対象: `internal/` の term・textwidth・keys・screen・lineedit・listing・textfmt・msg・app・tui・platform と、`cmd/tana`・`cmd/tuiprobe`。

- import してよいのは標準ライブラリ、`golang.org/x/sys`、`golang.org/x/text` だけ（fsops と同じ）。ほかが必要なら理由を添えて私に確認する。
  TUI のライブラリは使わない（filer §12.3）。
- パッケージの境界と依存の向きは filer §4 と tui §3 に従い、`deps_test.go` で確かめる。
- 土台のパッケージ（term・textwidth・keys・screen・lineedit）の API に、ファイラー固有の概念（ペイン、ファイル）を入れない（tui §1）。
- OS ごとのコードは `term`（端末の入出力）と `platform`（関連付けで開く、実行ファイルの判定、隠しファイルの名前の規則）に閉じる。ビルドタグの規則は fsops と同じ。
- 画面の状態を変えるのは、イベントループの goroutine だけ。作業用の goroutine は panic を回収して、イベントループに渡す（filer §10）。
- fsops に渡すパスを、表示用に加工した文字列（置き換え・切り詰め・正規化をしたもの）から作らない（U4）。
- 書記素クラスタの区切りと幅は、`textwidth` だけで判断する（T4）。
- `app` を画面の構成（2 ペインなど）に依存させない（filer §4）。
- 人向けの文言は `internal/msg` にまとめる（filer §8.8）。

## テストのルール

- 不変条件に関わる処理は、テストを先に書いてから実装する。
- テストが触ってよいのは `t.TempDir()`、`FSOPS_CROSSVOL_DIR`、`FSOPS_PROBE_*_DIR`（CI が用意する特別なボリューム。SPEC §18.2）の中だけ。ごみ箱のテストは `FSOPS_TEST_TRASH=1` のときだけ実行する。
- `t.TempDir()` のパスは `filepath.EvalSymlinks` で正規化してから使う（Windows ランナーは `RUNNER~1` 形式の短縮名、macOS は `/var` → `/private/var`）。
- 特殊な環境や権限が必要なテストは、条件を満たさなければ理由を書いて `t.Skip` する。
- テストを通すためにテストの条件を弱めない。スリープやリトライでタイミングの問題を隠さない。必要なら理由を示して確認を取る。
- TUI の土台のパッケージには、表のテストと性質のテスト（ファジング）を書く（tui §10）。
- `textwidth` と `keys` の期待値は、端末での実測の結果（`docs/probe-results`）から作る。推測で書かない。
- `tui` の描画は、80×24 と 120×40 のゴールデンファイルと比べる（filer §10）。
- `app` のテストでは、本物の fsops を `t.TempDir()` の中で使う。
- 端末での見え方・キー・IME は手動で確かめ、何を・どの端末（版を含む）で確かめたかを記録する（filer §12）。

## 仮想マシンでの確認（Parallels Desktop）

- 仮想マシン「Windows 11」（ARM 版）を、`prlctl`（`prlctl start`・`prlctl exec` など）と computer use（Parallels Desktop の画面）で操作してよい。
- 端末は `prlctl exec "Windows 11" --current-user cmd /c "start \"\" wt.exe ..."` のように起動する。文字を打って起動しない（computer use の速い入力は文字が落ちる）。
  conhost は `conhost.exe` から直接起動する。`start "" conhost.exe ...` は窓が閉じるまで戻らないことがあるので、バックグラウンドで実行する。
- computer use は、画面の撮影と、マウスで行える操作（ウィンドウの大きさの変更など）に使う。
- 仮想マシンでのキー入力と IME の確認は私が行う（filer §12.2）。computer use で送るキーは当てにならないため。
  - Esc は computer use の停止のキーなので、仮想マシンに届かない。
  - 文字のキーは Windows 11 の長押しの候補を開き、後のキーを飲み込む。
  - Mac の Option が Ctrl として届くことがある。
  あなたは、ダブルクリックで起動できるファイル（`.cmd`）をプローブ用のフォルダに用意し、手順を示して私に頼む。
- 仮想マシンのシステムの設定（既定の端末、IME、ごみ箱、レジストリなど）は変えない。必要なら私に頼む。
- プローブは、仮想マシンのローカルの専用のフォルダ（例: `%USERPROFILE%\tana-probe`）の中だけで動かし、その外のファイルを作ったり消したりしない。
  共有フォルダ（`\\Mac\...`）はネットワークドライブなので、そこでは動かさない。
- ファイルの受け渡しは、共有フォルダ（`Z:` は Mac のホーム）の、リポジトリの `.vmstage`（git に入れない）を通し、使い終わったら消す。
- Mac の Terminal.app と iTerm2 には、computer use で文字を打てない。これらでのキー入力と IME の確認は私が行う（filer §12.2）。
- 結果は `docs/probe-results` に置き、OS と端末の版を記録する。

## 進め方

- フェーズ単位で進める（手順は `docs/PROMPTS.md`）。完了条件を満たしたら、変更の要約・テスト結果・CI の結果を報告して止まる。
- フェーズ6以降の完了報告には、そのフェーズで増えた「I1〜I7 を破りうるコード経路」と「それを防ぐテスト」の対応表を含める。
  TUI の土台のフェーズ（13〜16）では、T1〜T6 についての同じ対応表も含める（tui §10）。
- 仕様と違う実装が必要になったら、実装前に仕様の変更案を示して確認を取る。SPEC §2 の不変条件は変更しない。
- 要検証事項（SPEC §20、filer §12、tui §9）は推測で確定させない。確かめるテストやプローブを書き、結果を報告してから方針を決める。
- コミットは意味のある区切りごとに行う。push の前に「コマンド」の確認をすべて通す。
- push 後は `gh` で CI の結果を確認する。Windows だけ失敗した場合は、ログから原因を特定し、再現するテストを先に追加してから直す。

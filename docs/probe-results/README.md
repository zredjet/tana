# 端末の実測の結果

`cmd/tuiprobe` で測った結果（docs/SPEC-filer.md §12.1、docs/SPEC-tui.md §9）。ファイルは `<端末>-<日付>.json`。
各ファイルは、測った回ごとの節を持つ。

| 節 | 内容 |
|---|---|
| `width` | 文字列ごとに、端末が進めた桁数（`advance`）。カーソル位置の問い合わせ（`ESC[6n`）で得た値で、目で見た判断ではない。問い合わせの応答（`queries`）も持つ |
| `width_utf8cp`・`width_writefile` | Windows の出力の方法を変えて測ったもの（VT1）。既定の `width` は `WriteConsoleW` |
| `width_novtinput` | Windows で VT の入力モードを付けずに測ろうとしたもの。カーソル位置の報告が届かず、3 件で止まった（VT2） |
| `keys` | 案内に従って押したキーの記録。Unix はバイト列と読み取りの時刻、Windows は入力のレコード（VT の入力モードなし） |
| `keys_vtinput` | Windows で VT の入力モード（`ENABLE_VIRTUAL_TERMINAL_INPUT`）を付けた記録（VT2） |

`keys` の各手順の `hex` は、届いたものをつないだもの（Windows は、キーを押したレコードの文字を UTF-8 にしたもの）。
`reads` が読み取りごとの生の記録で、`t_ms` はその手順の最初のキーの入力からの時間。
`recorded_at` が違う手順は、`-steps` で撮り直したもの。

## 環境（2026-09-26）

| 端末 | 版 | OS | フォントなど | 測った人 |
|---|---|---|---|---|
| Terminal.app | 2.15（TERM_PROGRAM_VERSION 470.2） | macOS 26.5.1（25F80） | SF Mono Terminal 12pt（プロファイル Clear Dark）、120×30 | width: Claude、keys: 開発者 |
| iTerm2 | 3.7.2 | macOS 26.5.1（25F80） | BIZ UDGothic 15pt、幅が曖昧な文字を 2 桁にする設定はオフ、Option は Normal、80×25 | width: Claude、keys: 開発者 |
| Windows Terminal | 1.24.11911.0 | Windows 11（ARM 版、Parallels）10.0.26200 | Cascadia Mono 12pt（既定）、日本語のキーボード配列（00000411）、コードページ 932 | width: Claude、keys: 開発者 |
| conhost | 10.0.26100.8875 | 同上 | 既定のフォント。`conhost.exe` から直接起動 | width: Claude、keys: 開発者 |

キーボード: Mac の実物のキーボード。Windows は、Mac のキーボードから Parallels を通して入力した。

## 測り方

- width: Mac は `open -a` で起動した（`tuiprobe width -hold 45s` など）。Windows は `prlctl exec` で `wt.exe` と `conhost.exe` を起動した。
- keys: 開発者が案内に従って押した。Windows は `C:\Users\redjet\tana-probe` の起動用のファイル（`w-keys.cmd`・`c-keys.cmd`）から起動した。
  貼り付ける文字列（`paste_text`）は、Mac は `pbcopy`、Windows は起動用のファイルが `Set-Clipboard` でクリップボードに入れた。

## 注意

- F11 は、どの端末でも届かなかった（`no_input`）。macOS が F11 を取る（デスクトップを表示）。Windows Terminal も F11 を全画面の切り替えに使う。
- Claude が computer use で送るキーは、Windows の VM では当てにならなかった。
  Esc は computer use の停止のキーなので届かない。文字のキーは Windows 11 の長押しの候補（Å など）を開き、後のキーを飲み込んだ。
  そのため、keys はすべて開発者が実物のキーボードで記録した。
- conhost では、このプローブの入力モード（`ENABLE_PROCESSED_INPUT` と `ENABLE_LINE_INPUT` を外す）で Ctrl＋V が貼り付けにならず、キー（`^V`）として届く。
  conhost の貼り付けは、ウィンドウの右クリックのメニューから撮り直した（`recorded_at` が 11:06 以降のもの）。
- conhost は、貼り付けた BMP の外の文字（🍣）を、Alt＋テンキーの並びで届け、文字（サロゲートの半分）を Alt を離したレコードに載せる。
  conhost の貼り付けの手順の `hex` はキーを押したレコードの文字だけから作ったので、🍣 が欠けている（レコードの `reads` には残っている）。
  プローブは、この後で Alt を離したレコードの文字も含めるように直した。
- VM では、Mac の option＋A が Ctrl＋A として届くことがあった（Parallels のキーの対応によると思われる）。
  conhost（VT の入力モードあり）の `alt-a` は、撮り直しても Ctrl＋A だったので、未確認とする。
- Windows の VM は ARM 版の Windows 11 で、日本語の環境（コードページ 932）。x64 の PC や Windows 10 の conhost とは違いうる。
- 描画のずれの広がり（filer §12.1）は、`width` の最後の画面を Claude が撮影して確かめた（画像はリポジトリに置いていない）。見えたことは filer §12.5 に書いた。

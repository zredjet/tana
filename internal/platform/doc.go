// Package platform は、ファイラーの OS ごとに違う処理をまとめる（filer §4）。
//
//   - 関連付けで開く（Open。macOS は open、Windows は ShellExecute）
//   - 開く前に確認を出す実行ファイルの判定（IsExecutable。filer §7）
//   - 開いてよい名前かの判定（CanOpen。Windows の VU10。filer §7）
//   - 名前が . で始まるものを隠すか（DotFilesHidden。filer §6）
//
// OS ごとのコードは、term とこのパッケージに閉じる（CLAUDE.md）。
// 標準ライブラリと golang.org/x/sys だけを使い、ほかの tana のパッケージを import しない。
// 端末の入出力には触れない。Open と IsExecutable はファイルシステムや OS を呼ぶので、作業用の goroutine から呼ぶ（filer U5）。
package platform

// Package listing は、フォルダの一覧の項目を作る（filer §6）。
//
// fsops の ReadDir で得たエントリから、隠しファイルの判定と並べ替えをした項目の列を作る。純粋な関数で、ファイルシステムに触れない。
// 並べ替えのキーは比較にだけ使い、名前そのものは変えない（filer U4）。
package listing

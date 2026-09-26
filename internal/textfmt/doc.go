// Package textfmt は、一覧と状態行のための文字列の書式を作る（docs/SPEC-filer.md §9）。
//
// 名前とパスの切り詰め（TruncName・TruncPath）、サイズ・バイト数・日時の書式（Size・Bytes・Time・FullTime）、
// 状態行の表記（Escape）を持つ。幅は textwidth の表示幅で数え、書記素クラスタの途中では切らない（tui T4）。
// 作る文字列は表示のためだけのもので、パスを作ってはいけない（filer U4）。
package textfmt

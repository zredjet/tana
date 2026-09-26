// Package msg は、利用者に見せる文言をまとめる（docs/SPEC-filer.md §8.8、CLAUDE.md の「人向けの文言は internal/msg にまとめる」）。
//
// fsops はメッセージを作らない（fsops §17）。UI は、エラーの分類（fsops.Kind）から、この文言を作る。
// 画面の案内・確認の文なども、ここに置く。
package msg

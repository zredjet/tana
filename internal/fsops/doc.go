// Package fsops はファイラーのファイル操作（コピー・移動・名前の変更・ごみ箱・完全削除）を担う。
//
// 操作は計画（NewPlan。ファイルシステムを変更しない）と実行（Plan.Execute）の 2 段階に分かれる。
// 仕様は docs/SPEC-fsops.md を参照。
package fsops

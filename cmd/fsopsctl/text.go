package main

import (
	"errors"
	"fmt"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

// 表示用の日本語。fsops はメッセージを作らないので（SPEC §17）、Kind などから CLI が作る。

func opName(op fsops.OpKind) string {
	switch op {
	case fsops.OpCopy:
		return "copy"
	case fsops.OpMove:
		return "move"
	case fsops.OpTrash:
		return "trash"
	case fsops.OpDelete:
		return "delete"
	}
	return "unknown"
}

// 利用者向けの文言は internal/msg にまとめてある（filer §8.8）。ここでは CLI に固有の組み立てだけを行う。

func opText(op fsops.OpKind) string                      { return msg.Op(op) }
func methodText(m fsops.Method) string                   { return msg.Method(m) }
func typeText(t fsops.EntryType) string                  { return msg.Type(t) }
func decisionText(d fsops.Decision) string               { return msg.Decision(d) }
func stageText(s fsops.Stage) string                     { return msg.Stage(s) }
func statusText(s fsops.Status) string                   { return msg.Status(s) }
func outcomeText(o fsops.Outcome) string                 { return msg.Outcome(o) }
func kindText(k fsops.Kind) string                       { return msg.Kind(k) }
func partialText(m fsops.Method, o fsops.Outcome) string { return msg.Partial(m, o) }

func conflictKindText(c fsops.Conflict) string {
	if c.Self {
		return "同じフォルダへのコピー"
	}
	return typeText(c.SrcInfo.Type) + " -> 既存の" + typeText(c.DstInfo.Type)
}

// errorText は、エラーを「分類の文（元のエラー）」の形にする。
func errorText(err error) string {
	var oe *fsops.OpError
	if !errors.As(err, &oe) || oe == nil {
		return err.Error()
	}
	s := kindText(oe.Kind)
	if oe.OnDest {
		s = "コピー先・移動先で: " + s
	}
	if oe.Err != nil {
		s += fmt.Sprintf("（%v）", oe.Err)
	}
	return s
}

// sizeText は、バイト数を読みやすい単位で返す。
func sizeText(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	f, suffix := float64(n), ""
	for _, s := range []string{"KiB", "MiB", "GiB", "TiB"} {
		f /= unit
		suffix = s
		if f < unit {
			break
		}
	}
	return fmt.Sprintf("%.1f %s", f, suffix)
}

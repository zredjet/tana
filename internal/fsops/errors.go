package fsops

import (
	"context"
	"errors"
	"io/fs"
	"strconv"
)

// Kind はエラーの分類（SPEC §17）。UI は Kind から利用者向けのメッセージを作る。
type Kind int

const (
	KindUnknown Kind = iota
	KindNotFound
	KindExist
	KindPermission
	KindLocked   // 他のプロセスが使用中
	KindReadOnly // 読み取り専用のファイル・ボリューム、macOS のロック
	KindNoSpace
	KindNotEmpty        // 空でないフォルダを削除できなかった
	KindCrossDevice     // 内部用（移動方式の切り替えに使う）。結果には現れない
	KindLinkUnsupported // リンクを作れない・扱えない
	KindUnsupportedType // FIFO、未知のリパースポイントなど
	KindTrashUnavailable
	KindDestInsideSource
	KindSameFile
	KindSourceChanged
	KindInvalidName
	KindInvalidRequest
	KindCanceled
	KindMetadata
)

func (k Kind) String() string {
	switch k {
	case KindUnknown:
		return "KindUnknown"
	case KindNotFound:
		return "KindNotFound"
	case KindExist:
		return "KindExist"
	case KindPermission:
		return "KindPermission"
	case KindLocked:
		return "KindLocked"
	case KindReadOnly:
		return "KindReadOnly"
	case KindNoSpace:
		return "KindNoSpace"
	case KindNotEmpty:
		return "KindNotEmpty"
	case KindCrossDevice:
		return "KindCrossDevice"
	case KindLinkUnsupported:
		return "KindLinkUnsupported"
	case KindUnsupportedType:
		return "KindUnsupportedType"
	case KindTrashUnavailable:
		return "KindTrashUnavailable"
	case KindDestInsideSource:
		return "KindDestInsideSource"
	case KindSameFile:
		return "KindSameFile"
	case KindSourceChanged:
		return "KindSourceChanged"
	case KindInvalidName:
		return "KindInvalidName"
	case KindInvalidRequest:
		return "KindInvalidRequest"
	case KindCanceled:
		return "KindCanceled"
	case KindMetadata:
		return "KindMetadata"
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// OpError は fsops が返すエラー。
type OpError struct {
	Op   string // "copy", "rename", "remove" など
	Path string
	Dest string
	Kind Kind
	Err  error // 元のエラー
}

// Error はログ用の技術的な文字列を返す。利用者向けのメッセージではない。
func (e *OpError) Error() string {
	if e == nil {
		return "fsops: <nil>"
	}
	s := "fsops: " + e.Op
	if e.Path != "" {
		s += " " + e.Path
	}
	if e.Dest != "" {
		s += " -> " + e.Dest
	}
	s += ": " + e.Kind.String()
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

// Unwrap は元のエラーを返す。
// Item.Err などの nil の *OpError が error として渡された場合にも errors.Is などが panic しないよう、nil を許す。
func (e *OpError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// clone は e の浅いコピーを返す。e が nil なら nil を返す。
// 計画の内容を呼び出し側から変更できないようにするために使う。
func (e *OpError) clone() *OpError {
	if e == nil {
		return nil
	}
	c := *e
	return &c
}

// KindOf は err の連鎖から *OpError を探し、その Kind を返す。見つからなければ KindUnknown を返す。
// nil の *OpError（ItemResult.Err が nil の場合など）を渡しても KindUnknown を返す。
func KindOf(err error) Kind {
	if oe, ok := errors.AsType[*OpError](err); ok && oe != nil {
		return oe.Kind
	}
	return KindUnknown
}

// classifyOpts は、エラー番号だけでは分類が決まらない場合に使う情報。
type classifyOpts struct {
	// readOnly は、Windows の ERROR_ACCESS_DENIED と Unix の EPERM のときだけ呼ばれ、
	// 操作の対象が読み取り専用（Windows: 読み取り専用属性、macOS: UF_IMMUTABLE）なら true を返す。
	// nil なら読み取り専用でないとみなす（KindPermission）。
	readOnly func() bool
	// symlinkCreate は、シンボリックリンクの作成で起きたエラーであることを示す。
	// Windows の ERROR_PRIVILEGE_NOT_HELD・ERROR_INVALID_FUNCTION と Unix の EPERM（読み取り専用でない場合）は、
	// このときだけ KindLinkUnsupported にする（§17、V20）。
	symlinkCreate bool
	// noFollow は、O_NOFOLLOW などでリンクを辿らずに開いたときのエラーであることを示す。
	// Unix の ELOOP は、このときだけ KindSourceChanged にする（リンクに置き換えられていたことを示すため）。
	noFollow bool
}

// classify は OS などから返されたエラーを分類する（SPEC §17）。
// 既に *OpError を含むエラーは、その Kind をそのまま使う。
func classify(err error, o classifyOpts) Kind {
	if err == nil {
		return KindUnknown
	}
	if oe, ok := errors.AsType[*OpError](err); ok && oe != nil {
		return oe.Kind
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return KindCanceled
	}
	// エラー番号の対応を fs.Err* より先に調べる。
	// Unix では ENOTEMPTY も fs.ErrExist に当たるなど、fs.Err* では区別できないものがあるため。
	if k, ok := classifyErrno(err, o); ok {
		return k
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return KindNotFound
	case errors.Is(err, fs.ErrExist):
		return KindExist
	case errors.Is(err, fs.ErrPermission):
		return KindPermission
	}
	return KindUnknown
}

package fsops

import (
	"strings"
	"time"
)

// srcMeta は、コピー元から記録するもの（§10.1 の手順 1、§10.4、§15）。
type srcMeta struct {
	size  int64     // ファイルのとき、開いた時点の大きさ（§10.4 の検証に使う）
	mtime time.Time // 更新日時（アクセス日時にも同じ値を設定する）
	perm  uint32    // Unix: パーミッション（0o777 の範囲。setuid・setgid・sticky は含めない）
	attrs uint32    // Windows: ファイル属性（読み取り専用・隠し属性だけを保持する）
	// extra は、安全上保持するもの（Windows: Zone.Identifier の内容、macOS: com.apple.quarantine の値）。なければ nil。
	// ファイルだけに使い、フォルダとリンクには付けない（§15）。
	extra []byte
}

// unkeptMetadata は、コピー元にあったが fsops が保持しないメタデータの名前（§15）。KindMetadata の警告の Err に入れる（ログ用）。
type unkeptMetadata []string

func (u unkeptMetadata) Error() string { return "fsops: metadata not kept: " + strings.Join(u, ", ") }

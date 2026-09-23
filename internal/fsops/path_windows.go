package fsops

import (
	"path/filepath"
	"strings"
)

// sysPath は、OS に渡すためにパスを \\?\ 形式に変換する（SPEC §8.2）。
// C:\... は \\?\C:\...、\\server\share\... は \\?\UNC\server\share\... にする。
// \\?\ 形式では Win32 のパス正規化（末尾の . と空白の除去、予約名の解釈）が行われないため、
// 走査で得た名前を結合したパスを、別のファイルやデバイスと取り違えずに扱える。
//
// p は §8.1 の検査を通った絶対パスであること。それ以外の形式は KindInvalidRequest にする。
// 絶対パスの検査（ドライブ相対、デバイスパス、代替データストリームなどの拒否）はフェーズ4で作る。
func sysPath(p string) (string, error) {
	c := filepath.Clean(p)
	switch {
	case len(c) >= 3 && isDriveLetter(c[0]) && c[1] == ':' && c[2] == '\\':
		return `\\?\` + c, nil
	case strings.HasPrefix(c, `\\`) && !strings.HasPrefix(c, `\\?\`) && !strings.HasPrefix(c, `\\.\`):
		return `\\?\UNC\` + c[2:], nil
	}
	return "", &OpError{Op: "path", Path: p, Kind: KindInvalidRequest}
}

func isDriveLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

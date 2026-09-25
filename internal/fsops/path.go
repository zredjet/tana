package fsops

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// invalidPath は §8.1 の検査に通らなかったパスのエラー。
func invalidPath(p string) *OpError {
	return &OpError{Op: "path", Path: p, Kind: KindInvalidRequest}
}

// checkPath は §8.1 の検査を行い、Clean したパスを返す。受け付けない形式は KindInvalidRequest。
// ボリュームのルートは受け付ける（Sources に使えるかは呼び出し側が isVolumeRoot で判定する）。
func checkPath(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", invalidPath(p)
	}
	return checkPathOS(p)
}

// withUserPaths は、OS が返したエラーに含まれるパス（\\?\ 形式を含む）を、呼び出し側に返す形のパスに置き換える（§8.2）。
func withUserPaths(err error, src, dst string) error {
	if le, ok := errors.AsType[*os.LinkError](err); ok {
		le.Old, le.New = src, dst
		return err
	}
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		pe.Path = src
		return err
	}
	if oe, ok := errors.AsType[*OpError](err); ok { // OS ごとの関数が \\?\ 形式のパスで作った *OpError
		oe.Path = src
		if dst != "" {
			oe.Dest = dst
		}
	}
	return err
}

// destInside は、dest がコピー元 src の内側（src 自身を含む）かを判定する（§8.3）。
// dest の実パス（リンク・ジャンクション・短縮名などを解決したもの）から親を順に辿り、各段を src と fileID で比べる。
func destInside(src, dest string) (bool, error) {
	s, err := fileIDOf(src)
	if err != nil {
		return false, err
	}
	real, err := realPathSys(dest)
	if err != nil {
		return false, withUserPaths(err, dest, "")
	}
	for p := real; ; {
		st, err := statIDSys(p, false)
		if err != nil {
			return false, withUserPaths(err, dest, "")
		}
		if st.id == s.id {
			return true, nil
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false, nil
		}
		p = parent
	}
}

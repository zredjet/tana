package fsops

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// checkPathOS は Windows の §8.1 の検査。
// C:\... と UNC（\\server\share\...）だけを受け付ける。ドライブ相対（C:foo）、ルート相対（\foo）、
// 呼び出し側が付けた \\?\、デバイスパス（\\.\）、NT 形式（\??\）、ドライブ指定以外の : （代替データストリーム）は拒否する。
func checkPathOS(p string) (string, error) {
	q := strings.ReplaceAll(p, "/", `\`)
	if strings.HasPrefix(q, `\\?\`) || strings.HasPrefix(q, `\\.\`) || strings.HasPrefix(q, `\??\`) {
		return "", invalidPath(p)
	}
	switch {
	case len(q) >= 3 && isDriveLetter(q[0]) && q[1] == ':' && q[2] == '\\':
		if strings.ContainsRune(q[2:], ':') {
			return "", invalidPath(p)
		}
	case strings.HasPrefix(q, `\\`):
		parts := strings.SplitN(q[2:], `\`, 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" || strings.ContainsRune(q, ':') {
			return "", invalidPath(p)
		}
	default:
		return "", invalidPath(p)
	}
	return filepath.Clean(q), nil
}

// isVolumeRoot は、checkPath を通ったパス p がボリュームのルート（C:\、\\server\share）かを返す。
func isVolumeRoot(p string) bool {
	if len(p) == 3 && p[1] == ':' && p[2] == '\\' {
		return true
	}
	if strings.HasPrefix(p, `\\`) {
		return len(strings.Split(strings.TrimSuffix(p[2:], `\`), `\`)) == 2
	}
	return false
}

// sysPath は、OS に渡すためにパスを \\?\ 形式に変換する（SPEC §8.2）。
// C:\... は \\?\C:\...、\\server\share\... は \\?\UNC\server\share\... にする。
// \\?\ 形式では Win32 のパス正規化（末尾の . と空白の除去、予約名の解釈）が行われないため、
// 走査で得た名前を結合したパスを、別のファイルやデバイスと取り違えずに扱える。
//
// p は checkPath を通ったパスであること。それ以外の形式は KindInvalidRequest にする。
func sysPath(p string) (string, error) {
	c := filepath.Clean(p)
	switch {
	case len(c) >= 3 && isDriveLetter(c[0]) && c[1] == ':' && c[2] == '\\':
		return `\\?\` + c, nil
	case strings.HasPrefix(c, `\\`) && !strings.HasPrefix(c, `\\?\`) && !strings.HasPrefix(c, `\\.\`):
		return `\\?\UNC\` + c[2:], nil
	}
	return "", invalidPath(p)
}

// userPath は sysPath の逆で、\\?\ 形式のパスを呼び出し側に返す形にする（§8.2）。
// ドライブ文字のないボリューム（\\?\Volume{...}）のパスは戻せないので、そのまま返す。
func userPath(sys string) string {
	switch {
	case strings.HasPrefix(sys, `\\?\UNC\`):
		return `\\` + sys[len(`\\?\UNC\`):]
	case strings.HasPrefix(sys, `\\?\`) && len(sys) >= 6 && isDriveLetter(sys[4]) && sys[5] == ':':
		return sys[4:]
	}
	return sys
}

func isDriveLetter(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

// x/sys/windows に定義がない GetFinalPathNameByHandle のフラグ。
const (
	volumeNameDOS  = 0x0
	volumeNameGUID = 0x1
)

// realPathSys は、p をリンク・ジャンクションを辿って開き、GetFinalPathNameByHandle で得た実パス（\\?\ 形式）を返す（§8.3）。
// VOLUME_NAME_DOS で取れなければ VOLUME_NAME_GUID を使う。
func realPathSys(p string) (string, error) {
	s, err := sysPath(p)
	if err != nil {
		return "", err
	}
	s16, err := windows.UTF16PtrFromString(s)
	if err != nil {
		return "", err
	}
	h, err := windows.CreateFile(s16, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", &os.PathError{Op: "CreateFile", Path: s, Err: err}
	}
	defer windows.CloseHandle(h)
	var lastErr error
	for _, flags := range []uint32{volumeNameDOS, volumeNameGUID} {
		buf := make([]uint16, windows.MAX_LONG_PATH)
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), flags)
		if err == nil && int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		lastErr = err
	}
	return "", &os.PathError{Op: "GetFinalPathNameByHandle", Path: s, Err: lastErr}
}

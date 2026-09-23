package fsops

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	policiesExplorerKey = `Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`
	bitBucketVolumeKey  = `Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\`
)

// trashAvailable は Windows のごみ箱の事前確認（§12.2）。読むだけで、ファイルシステムもレジストリも変更しない。
func trashAvailable(ctx context.Context, src string, info EntryInfo) (bool, error) {
	// 260 文字以上のパスは、確認なしに完全削除される（V4）。
	if len(utf16.Encode([]rune(src))) >= windows.MAX_PATH {
		return false, nil
	}
	// Win32 の正規化で変わる名前（末尾の . や空白、予約名）を含むパスは、別のファイルをごみ箱に入れてしまう（V4、V18）。
	// Windows 11 の GetFullPathNameW はパスの途中の予約名（CON など）を変換しないので、各部分も §11.3 の規則で調べる。
	if hasWin32UnsafeComponent(src) {
		return false, nil
	}
	// GetFullPathNameW には、正規化されるかを調べるために \\?\ を付けないパスを渡す（この確認のための例外）。
	full, err := fullPathName(src)
	if err != nil || full != src {
		return false, err
	}
	s, err := sysPath(src)
	if err != nil {
		return false, err
	}
	root, err := volumePathName(s) // \\?\C:\ や \\?\UNC\server\share\ の形
	if err != nil {
		return false, err
	}
	r16, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return false, err
	}
	if windows.GetDriveType(r16) != windows.DRIVE_FIXED {
		return false, nil // リムーバブル・ネットワークなど（V4）
	}
	capacity, ok, err := recycleCapacity(root)
	if !ok || err != nil {
		return false, err
	}
	size, err := itemSize(ctx, src, info)
	if err != nil {
		return false, err
	}
	return size <= capacity, nil // 最大サイズちょうどは入る（V19）
}

// hasWin32UnsafeComponent は、p のボリューム名より後の部分に、Win32 の正規化で変わる名前
// （末尾の . や空白、予約名。§11.3 で新しい名前として拒否するもの）があるかを返す。
func hasWin32UnsafeComponent(p string) bool {
	for _, c := range strings.Split(p[len(filepath.VolumeName(p)):], `\`) {
		if c != "" && !validNameOS(c) {
			return true
		}
	}
	return false
}

// fullPathName は GetFullPathNameW の結果を返す。
func fullPathName(p string) (string, error) {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFullPathName(p16, uint32(len(buf)), &buf[0], nil)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n)
	}
}

// volumePathName は GetVolumePathNameW で p のボリュームのルート（末尾に \ 付き）を返す。
func volumePathName(p string) (string, error) {
	p16, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	if err := windows.GetVolumePathName(p16, &buf[0], uint32(len(buf))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(buf), nil
}

// readDWORD は key\name の整数値を読む。キーか値がなければ ok が false。
func readDWORD(root registry.Key, key, name string) (v uint64, ok bool, err error) {
	k, err := registry.OpenKey(root, key, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	defer k.Close()
	v, _, err = k.GetIntegerValue(name)
	if errors.Is(err, registry.ErrNotExist) {
		return 0, false, nil
	}
	return v, err == nil, err
}

// recycleCapacity は、ボリュームのルート root のごみ箱の最大サイズ（バイト）を返す（§12.2）。
// ごみ箱が使えない設定（ポリシーの NoRecycleFiles、ボリュームの NukeOnDelete）なら ok が false。
// 最大サイズを読めなければ、分からないものとして ok が false。
func recycleCapacity(root string) (capacity int64, ok bool, err error) {
	hives := []registry.Key{registry.CURRENT_USER, registry.LOCAL_MACHINE}
	for _, h := range hives {
		if v, found, err := readDWORD(h, policiesExplorerKey, "NoRecycleFiles"); err != nil {
			return 0, false, err
		} else if found && v == 1 {
			return 0, false, nil
		}
	}
	r16, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, false, err
	}
	buf := make([]uint16, 64)
	if err := windows.GetVolumeNameForVolumeMountPoint(r16, &buf[0], uint32(len(buf))); err != nil {
		return 0, false, err
	}
	// \\?\Volume{GUID}\ から {GUID} を取り出す。
	name := windows.UTF16ToString(buf)
	i, j := strings.IndexByte(name, '{'), strings.IndexByte(name, '}')
	if i < 0 || j < i {
		return 0, false, nil
	}
	guid := name[i : j+1]
	volKey := bitBucketVolumeKey + guid
	if v, found, err := readDWORD(registry.CURRENT_USER, volKey, "NukeOnDelete"); err != nil {
		return 0, false, err
	} else if found && v == 1 {
		return 0, false, nil
	}
	// ポリシーの RecycleBinSize（容量に対する割合）は、ユーザーとマシンの両方にあれば小さいほうを使う（安全側）。
	percent, havePolicy := uint64(0), false
	for _, h := range hives {
		if v, found, err := readDWORD(h, policiesExplorerKey, "RecycleBinSize"); err != nil {
			return 0, false, err
		} else if found && (!havePolicy || v < percent) {
			percent, havePolicy = v, true
		}
	}
	if havePolicy {
		var total uint64
		if err := windows.GetDiskFreeSpaceEx(r16, nil, &total, nil); err != nil {
			return 0, false, err
		}
		return int64(total / 100 * percent), true, nil
	}
	v, found, err := readDWORD(registry.CURRENT_USER, volKey, "MaxCapacity")
	if err != nil || !found {
		return 0, false, err
	}
	return int64(v) << 20, true, nil
}

// itemSize は、ごみ箱の最大サイズと比べる項目の大きさ（ファイルの大きさ、フォルダは中身のファイルの大きさの合計）を返す（§12.1）。
// リンクには入り込まない（§13.1）。
func itemSize(ctx context.Context, p string, info EntryInfo) (int64, error) {
	if info.Type != TypeDir {
		return info.Size, nil
	}
	entries, err := readDir(p)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := itemSize(ctx, filepath.Join(p, e.name), e.info)
		if err != nil {
			return 0, err
		}
		total += n
	}
	return total, nil
}

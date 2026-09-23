package probe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unsafe"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var procSHQueryRecycleBin = windows.NewLazySystemDLL("shell32.dll").NewProc("SHQueryRecycleBinW")

// shQueryRBInfo は SHQUERYRBINFO（x/sys/windows に定義がない）。
type shQueryRBInfo struct {
	cbSize   uint32
	_        uint32
	size     int64
	numItems int64
}

// binUsage は root のボリュームのごみ箱の使用量（バイト数と項目数）を返す。読むだけで変更しない。
func binUsage(t *testing.T, root string) string {
	t.Helper()
	info := shQueryRBInfo{cbSize: uint32(unsafe.Sizeof(shQueryRBInfo{}))}
	hr, _, _ := procSHQueryRecycleBin.Call(uintptr(unsafe.Pointer(u16(t, root))), uintptr(unsafe.Pointer(&info)))
	if hr != sOK {
		return fmt.Sprintf("SHQueryRecycleBin hr=%#x", uint32(hr))
	}
	return fmt.Sprintf("bin=%d bytes/%d items", info.size, info.numItems)
}

// readDword は key\name の DWORD を読む。ない場合は ok が false。
func readDword(root registry.Key, key, name string) (v uint64, ok bool, err error) {
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
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}

// trashPrediction は SPEC §12.2 の事前確認（ごみ箱の設定と最大サイズ）。
type trashPrediction struct {
	ok       bool   // ごみ箱に入ると予測する
	capacity int64  // 最大サイズ（バイト）。分からなければ -1
	reason   string // 予測の根拠
}

const policiesExplorer = `Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`

// predictTrash は、root のボリュームに size バイトの項目をごみ箱へ送ったときに、ごみ箱に入るかを予測する（SPEC §12.2）。
// 最大サイズ以下なら入る（V19 で確認: 最大サイズちょうどは入り、1 バイト超えると完全削除される。ごみ箱の使用量は影響しない）。
func predictTrash(t *testing.T, root string, size int64) trashPrediction {
	t.Helper()
	for _, hive := range []struct {
		name string
		key  registry.Key
	}{{"HKCU", registry.CURRENT_USER}, {"HKLM", registry.LOCAL_MACHINE}} {
		if v, ok, err := readDword(hive.key, policiesExplorer, "NoRecycleFiles"); err != nil {
			return trashPrediction{capacity: -1, reason: hive.name + " policy NoRecycleFiles: " + err.Error()}
		} else if ok && v == 1 {
			return trashPrediction{capacity: -1, reason: hive.name + " policy NoRecycleFiles=1"}
		}
	}
	buf := make([]uint16, 64)
	if err := windows.GetVolumeNameForVolumeMountPoint(u16(t, root), &buf[0], uint32(len(buf))); err != nil {
		return trashPrediction{capacity: -1, reason: "GetVolumeNameForVolumeMountPoint: " + err.Error()}
	}
	guid := regexp.MustCompile(`\{[^}]+\}`).FindString(windows.UTF16ToString(buf))
	volKey := `Software\Microsoft\Windows\CurrentVersion\Explorer\BitBucket\Volume\` + guid
	if v, ok, err := readDword(registry.CURRENT_USER, volKey, "NukeOnDelete"); err != nil {
		return trashPrediction{capacity: -1, reason: "NukeOnDelete: " + err.Error()}
	} else if ok && v == 1 {
		return trashPrediction{capacity: -1, reason: guid + " NukeOnDelete=1"}
	}
	var capacity int64 = -1
	reason := ""
	for _, hive := range []struct {
		name string
		key  registry.Key
	}{{"HKCU", registry.CURRENT_USER}, {"HKLM", registry.LOCAL_MACHINE}} {
		if v, ok, err := readDword(hive.key, policiesExplorer, "RecycleBinSize"); err == nil && ok {
			var total uint64
			if err := windows.GetDiskFreeSpaceEx(u16(t, root), nil, &total, nil); err == nil {
				capacity = int64(total) * int64(v) / 100
				reason = fmt.Sprintf("%s policy RecycleBinSize=%d%% of %d", hive.name, v, total)
				break
			}
		}
	}
	if capacity < 0 {
		v, ok, err := readDword(registry.CURRENT_USER, volKey, "MaxCapacity")
		switch {
		case err != nil:
			return trashPrediction{capacity: -1, reason: "MaxCapacity: " + err.Error()}
		case !ok:
			return trashPrediction{capacity: -1, reason: guid + " has no MaxCapacity (unknown)"}
		}
		capacity = int64(v) << 20
		reason = fmt.Sprintf("%s MaxCapacity=%d MB", guid, v)
	}
	return trashPrediction{ok: size <= capacity, capacity: capacity, reason: reason}
}

// TestV19 は、SPEC §12.2 の事前確認（ごみ箱の設定と最大サイズ）が実際の動作を正しく予測するかを記録する（SPEC §20 V19）。
// 予測が「入る」なのに完全削除された場合は I5 に関わるので、DANGEROUS としてログに残す（境界の扱いを確かめるためのプローブなので失敗にはしない）。
// ごみ箱は空にしない（完全削除になるため）。代わりに、各操作の前後でごみ箱の使用量を記録する。FSOPS_TEST_TRASH=1 のときだけ実行する。
func TestV19(t *testing.T) {
	testfs.RequireTrash(t)
	logWindowsVersion(t, "V19")

	// 各ボリュームの設定の読み取りと予測（操作はしない）。
	for _, env := range []string{testfs.CrossVolEnv, envExFAT, envFAT32, envTrashNuke, envTrashSmall} {
		if dir := os.Getenv(env); dir != "" {
			p := predictTrash(t, driveRoot(dir), 1)
			t.Logf("V19: %s=%s: prediction for 1 byte: ok=%v capacity=%d (%s); %s", env, dir, p.ok, p.capacity, p.reason, binUsage(t, driveRoot(dir)))
		}
	}
	c := driveRoot(testfs.TempDir(t))
	p := predictTrash(t, c, 1)
	t.Logf("V19: %s (temp dir): prediction for 1 byte: ok=%v capacity=%d (%s); %s", c, p.ok, p.capacity, p.reason, binUsage(t, c))

	// 最大サイズ 1 MB のボリュームでの境界。順に実行し、ごみ箱の使用量の変化も記録する。
	d := testfs.EnvDir(t, envTrashSmall)
	root := driveRoot(d)
	flags := uint32(defaultIFileOperationFlagsSet | fofxRecycleOnDelete) // ダイアログを出さず、実際の動作を見る
	cases := []struct {
		name  string
		files []int // 1 つならファイル、複数ならその大きさのファイルを含むフォルダ
	}{
		{"file 700000 (well below)", []int{700000}},
		{"file 700000 again (bin already has 700000)", []int{700000}},
		{"file 1040000 (below; allocation also below)", []int{1040000}},
		{"file 1048000 (below; allocation rounds up to exactly 1 MiB)", []int{1048000}},
		{"file 1048575 (1 MiB - 1)", []int{1048575}},
		{"file 1048576 (exactly 1 MiB)", []int{1048576}},
		{"file 1048577 (1 MiB + 1)", []int{1048577}},
		{"dir 2 x 400000 (800000 total)", []int{400000, 400000}},
		{"dir 3 x 400000 (1200000 total)", []int{400000, 400000, 400000}},
	}
	for i, tc := range cases {
		dir := filepath.Join(d, fmt.Sprintf("case%02d", i))
		var target string
		var total int64
		if len(tc.files) == 1 {
			testfs.MkdirAll(t, dir)
			target = filepath.Join(dir, "item.bin")
			testfs.WriteFile(t, target, strings.Repeat("x", tc.files[0]))
			total = int64(tc.files[0])
		} else {
			target = filepath.Join(dir, "item")
			for j, n := range tc.files {
				testfs.MkdirAll(t, target)
				testfs.WriteFile(t, filepath.Join(target, fmt.Sprintf("f%d.bin", j)), strings.Repeat("x", n))
				total += int64(n)
			}
		}
		pred := predictTrash(t, root, total)
		usageBefore := binUsage(t, root)
		before := readBin(t, root)
		s := ifoTrashFlags(target, coInitSTA, true, flags)
		verdict := trashVerdict(t, target, root, before)
		children := ""
		if len(tc.files) > 1 {
			children = fmt.Sprintf(" children left=%+q", listNamesIfExists(t, target))
		}
		t.Logf("V19: %s: predicted ok=%v (capacity=%d) | actual: %s%s | %s -> %s || %s",
			tc.name, pred.ok, pred.capacity, verdict, children, usageBefore, binUsage(t, root), s)
		switch {
		case pred.ok && strings.HasPrefix(verdict, "PERMANENTLY"):
			t.Logf("V19: %s: DANGEROUS: predicted to be recycled but was permanently deleted", tc.name)
		case !pred.ok && strings.HasPrefix(verdict, "trashed"):
			t.Logf("V19: %s: conservative: predicted not to be recycled but was recycled", tc.name)
		}
	}
}

func listNamesIfExists(t *testing.T, dir string) []string {
	t.Helper()
	if !testfs.Exists(t, dir) {
		return nil
	}
	return listNames(t, dir)
}

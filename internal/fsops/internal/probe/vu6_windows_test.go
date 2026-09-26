package probe

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// rawNames は、dir の中の名前を、UTF-16 のまま（Go の文字列に変えずに）返す。
func rawNames(t *testing.T, dir string) [][]uint16 {
	t.Helper()
	var data windows.Win32finddata
	h, err := windows.FindFirstFile(u16(t, testfs.ExtendedPath(dir)+`\*`), &data)
	if err != nil {
		t.Fatalf("FindFirstFile %s: %v", dir, err)
	}
	defer windows.FindClose(h)
	var names [][]uint16
	for {
		n := slices.Index(data.FileName[:], 0)
		name := slices.Clone(data.FileName[:n])
		if !slices.Equal(name, []uint16{'.'}) && !slices.Equal(name, []uint16{'.', '.'}) {
			names = append(names, name)
		}
		if err := windows.FindNextFile(h, &data); err != nil {
			if err == windows.ERROR_NO_MORE_FILES {
				return names
			}
			t.Fatalf("FindNextFile %s: %v", dir, err)
		}
	}
}

func hasRawName(names [][]uint16, want []uint16) bool {
	return slices.ContainsFunc(names, func(n []uint16) bool { return slices.Equal(n, want) })
}

// rawPath は、dir（Go の文字列）と UTF-16 の名前 name から、\\?\ 形式の UTF-16 のパス（NUL で終わる）を作る。
func rawPath(dir string, name []uint16) []uint16 {
	p := utf16.Encode([]rune(testfs.ExtendedPath(dir) + `\`))
	return append(append(p, name...), 0)
}

// fileIDOf は、UTF-16 のパス p（NUL で終わる）のファイルのボリュームのシリアル番号とファイルのインデックスを返す。
func fileIDOf(t *testing.T, p []uint16) [3]uint32 {
	t.Helper()
	h, err := windows.CreateFile(&p[0], windows.FILE_READ_ATTRIBUTES, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		t.Fatalf("CreateFile %q: %v", utf16.Decode(p), err)
	}
	defer windows.CloseHandle(h)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		t.Fatal(err)
	}
	return [3]uint32{info.VolumeSerialNumber, info.FileIndexHigh, info.FileIndexLow}
}

// createRaw は、UTF-16 の名前 name のファイルを dir に作り、data を書く。
func createRaw(t *testing.T, dir string, name []uint16, data string) {
	t.Helper()
	p := rawPath(dir, name)
	h, err := windows.CreateFile(&p[0], windows.GENERIC_WRITE, 0, nil, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("create %q in %s: %v", utf16.Decode(name), dir, err)
	}
	defer windows.CloseHandle(h)
	var n uint32
	if err := windows.WriteFile(h, []byte(data), &n, nil); err != nil {
		t.Fatal(err)
	}
}

// readRaw は、UTF-16 の名前 name のファイルの内容を返す。
func readRaw(t *testing.T, dir string, name []uint16) string {
	t.Helper()
	p := rawPath(dir, name)
	h, err := windows.CreateFile(&p[0], windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatalf("open %q in %s: %v", utf16.Decode(name), dir, err)
	}
	defer windows.CloseHandle(h)
	buf := make([]byte, 64)
	var n uint32
	if err := windows.ReadFile(h, buf, &n, nil); err != nil {
		t.Fatal(err)
	}
	return string(buf[:n])
}

// nameOf は、s の後ろに UTF-16 の単位 units を置き、その後ろに tail を置いた UTF-16 の名前を作る。
func nameOf(s string, units []uint16, tail string) []uint16 {
	return append(append(utf16.Encode([]rune(s)), units...), utf16.Encode([]rune(tail))...)
}

// TestVU6 は、対になっていないサロゲートを含む名前（不正な UTF-16）を os.ReadDir がどういう文字列で返すかと、
// その名前から作ったパスを fsops に渡して、同じエントリを指せるかを確かめる（filer VU6。golang/go#32334）。
// 名前の U+FFFD の位置にサロゲートがある名前と U+FFFD の名前を並べて置き、取り違えないことも確かめる。
func TestVU6(t *testing.T) {
	logWindowsVersion(t, "VU6")
	root := testfs.TempDir(t)
	src := filepath.Join(root, "src")
	testfs.MkdirAll(t, src)

	cases := []struct {
		label string
		units []uint16
		want  string // os.ReadDir の名前に期待する WTF-8
	}{
		{"high surrogate alone", []uint16{0xd800}, "\xed\xa0\x80"},
		{"low surrogate alone", []uint16{0xdc00}, "\xed\xb0\x80"},
		{"reversed pair", []uint16{0xdc00, 0xd800}, "\xed\xb0\x80\xed\xa0\x80"},
		{"high surrogate at the end", []uint16{0xdbff}, "\xed\xaf\xbf"},
	}
	// 取り違えの相手: U+FFFD の名前。
	decoy := nameOf("vu6-", []uint16{0xfffd}, ".txt")
	createRaw(t, src, decoy, "decoy")
	for i, c := range cases {
		createRaw(t, src, nameOf("vu6-", c.units, ".txt"), "data"+string(rune('0'+i)))
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, e := range entries {
		t.Logf("VU6: os.ReadDir name %q (hex %s, valid UTF-8 %v)", e.Name(), hex.EncodeToString([]byte(e.Name())), utf8.ValidString(e.Name()))
		listed[e.Name()] = true
	}
	decoyID := fileIDOf(t, rawPath(src, decoy))

	for i, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			raw := nameOf("vu6-", c.units, ".txt")
			name := "vu6-" + c.want + ".txt"
			if !listed[name] {
				t.Fatalf("VU6: os.ReadDir did not return %q for UTF-16 %#x", name, raw)
			}
			path := filepath.Join(src, name)
			data := "data" + string(rune('0'+i))

			// OS から見て同じエントリか: UTF-16 の名前で開いたものと、Go の文字列のパスで開いたもののファイル ID を比べる。
			wantID := fileIDOf(t, rawPath(src, raw))
			p16, err := windows.UTF16FromString(testfs.ExtendedPath(path))
			if err != nil {
				t.Fatalf("UTF16FromString(%q): %v", path, err)
			}
			if got := fileIDOf(t, p16); got != wantID || got == decoyID {
				t.Errorf("VU6: file ID via Go path = %v, want %v (decoy %v)", got, wantID, decoyID)
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != data {
				t.Errorf("VU6: os.ReadFile(%q) = %q, %v, want %q", path, got, err, data)
			}

			// fsops のコピー: 計画の大きさと、コピー先の UTF-16 の名前と内容。
			dst := filepath.Join(root, "dst"+string(rune('0'+i)))
			testfs.MkdirAll(t, dst)
			plan, err := fsops.NewPlan(context.Background(), fsops.Request{Op: fsops.OpCopy, Sources: []string{path}, DestDir: dst})
			if err != nil {
				t.Fatalf("VU6: NewPlan(copy): %v", err)
			}
			if it := plan.Items()[0]; it.Err != nil || it.Info.Size != int64(len(data)) {
				t.Fatalf("VU6: plan item = %+v (err %v), want size %d", it, it.Err, len(data))
			}
			res, err := plan.Execute(context.Background(), fsops.ExecOptions{})
			if err != nil || res.Items[0].Outcome != fsops.OutcomeDone {
				t.Fatalf("VU6: Execute(copy) = %+v, %v", res, err)
			}
			if names := rawNames(t, dst); len(names) != 1 || !slices.Equal(names[0], raw) {
				t.Errorf("VU6: copied names = %#x, want [%#x]", names, raw)
			}
			if got := readRaw(t, dst, raw); got != data {
				t.Errorf("VU6: copied content = %q, want %q", got, data)
			}

			// fsops の名前の変更: その名前のファイルだけが変わり、U+FFFD の名前は残る。
			if err := fsops.Rename(path, "vu6-renamed"+string(rune('0'+i))+".txt"); err != nil {
				t.Fatalf("VU6: Rename from the WTF-8 name: %v", err)
			}
			names := rawNames(t, src)
			if hasRawName(names, raw) || !hasRawName(names, decoy) {
				t.Errorf("VU6: after Rename, names = %#x; want %#x gone and the decoy kept", names, raw)
			}
			renamed := filepath.Join(src, "vu6-renamed"+string(rune('0'+i))+".txt")
			if got, err := os.ReadFile(renamed); err != nil || string(got) != data {
				t.Errorf("VU6: renamed content = %q, %v, want %q", got, err, data)
			}
			// WTF-8 の新しい名前への変更（記録のみ。名前の入力欄から不正な名前を作ることはない）。
			err = fsops.Rename(renamed, name)
			t.Logf("VU6: %s: Rename to the WTF-8 name %q: err=%v", c.label, name, err)
			if err == nil && !hasRawName(rawNames(t, src), raw) {
				t.Errorf("VU6: Rename to %q succeeded but the UTF-16 name %#x is not there: %#x", name, raw, rawNames(t, src))
			}

			// fsops の完全削除（コピー先で）: その名前のファイルだけが消える。
			dstPath := filepath.Join(dst, name)
			plan, err = fsops.NewPlan(context.Background(), fsops.Request{Op: fsops.OpDelete, Sources: []string{dstPath}})
			if err != nil {
				t.Fatalf("VU6: NewPlan(delete): %v", err)
			}
			res, err = plan.Execute(context.Background(), fsops.ExecOptions{})
			if err != nil || res.Items[0].Outcome != fsops.OutcomeDone {
				t.Fatalf("VU6: Execute(delete) = %+v, %v", res, err)
			}
			if names := rawNames(t, dst); len(names) != 0 {
				t.Errorf("VU6: after delete, names = %#x, want none", names)
			}
		})
	}
	if got := readRaw(t, src, decoy); got != "decoy" {
		t.Errorf("VU6: decoy content = %q, want %q", got, "decoy")
	}

	// ごみ箱と移動（ごみ箱は FSOPS_TEST_TRASH=1 のときだけ）。
	t.Run("move and trash", func(t *testing.T) {
		raw := nameOf("vu6-mv-", []uint16{0xd800}, ".txt")
		name := "vu6-mv-\xed\xa0\x80.txt"
		createRaw(t, src, raw, "mv")
		dst := filepath.Join(root, "dst-mv")
		testfs.MkdirAll(t, dst)
		plan, err := fsops.NewPlan(context.Background(), fsops.Request{Op: fsops.OpMove, Sources: []string{filepath.Join(src, name)}, DestDir: dst})
		if err != nil {
			t.Fatal(err)
		}
		res, err := plan.Execute(context.Background(), fsops.ExecOptions{})
		if err != nil || res.Items[0].Outcome != fsops.OutcomeDone {
			t.Fatalf("VU6: Execute(move) = %+v, %v", res, err)
		}
		if names := rawNames(t, dst); len(names) != 1 || !slices.Equal(names[0], raw) || hasRawName(rawNames(t, src), raw) {
			t.Errorf("VU6: after move, dst names = %#x, src names = %#x", names, rawNames(t, src))
		}

		testfs.RequireTrash(t)
		plan, err = fsops.NewPlan(context.Background(), fsops.Request{Op: fsops.OpTrash, Sources: []string{filepath.Join(dst, name)}})
		if err != nil {
			t.Fatal(err)
		}
		if it := plan.Items()[0]; it.Err != nil {
			t.Skipf("VU6: trash not available here: %v", it.Err)
		}
		res, err = plan.Execute(context.Background(), fsops.ExecOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("VU6: trash: outcome=%v err=%v trashed=%q", res.Items[0].Outcome, res.Items[0].Err, res.Items[0].TrashedPath)
		if res.Items[0].Outcome != fsops.OutcomeDone || len(rawNames(t, dst)) != 0 {
			t.Errorf("VU6: after trash, outcome %v, names %#x", res.Items[0].Outcome, rawNames(t, dst))
		}
	})
}

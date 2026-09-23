// Package testfs は fsops のテスト用フィクスチャ（SPEC §18.1）。
//
// テスト対象のコードに頼らないよう、fsops のパス変換などは使わず、この中で独自に実装する。
// 作るものはすべてテストが渡したフォルダ（TempDir・CrossVolDir の中）に置く。
//
// Windows では、OS に渡すパスは ExtendedPath で \\?\ 形式にしてから使う。
// 末尾が . や空白の名前、予約名のエントリを、別のファイルやデバイスと取り違えずに扱うため。
package testfs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TempDir は t.TempDir() を filepath.EvalSymlinks で正規化して返す
// （Windows ランナーの RUNNER~1 形式の短縮名、macOS の /var → /private/var）。
//
// テストの終了時に、読み取り専用を外してから \\?\ 形式のパスで削除する。
// t.TempDir の後片付けは \\?\ 形式を使わないため、Windows で末尾が . の名前などを削除できないことがあるため。
func TempDir(t testing.TB) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeTree(t, d) })
	return d
}

// removeTree は、読み取り専用を外してから dir を削除する（後片付け用）。
func removeTree(t testing.TB, dir string) {
	makeTreeWritable(dir)
	if err := os.RemoveAll(ExtendedPath(dir)); err != nil {
		t.Errorf("cleanup %s: %v", dir, err)
	}
}

// CrossVolEnv は、テスト用の別ボリューム上のフォルダを指定する環境変数（SPEC §18.2）。
const CrossVolEnv = "FSOPS_CROSSVOL_DIR"

// CrossVolDir は、FSOPS_CROSSVOL_DIR の中にこのテスト専用のフォルダを作って返す。テストの終了時に削除する。
// FSOPS_CROSSVOL_DIR が未設定なら t.Skip する。
func CrossVolDir(t testing.TB) string {
	t.Helper()
	base := os.Getenv(CrossVolEnv)
	if base == "" {
		t.Skipf("%s is not set; skipping a cross-volume test (SPEC §18.2)", CrossVolEnv)
	}
	d, err := os.MkdirTemp(base, "fsops-test-")
	if err != nil {
		t.Fatalf("%s=%q: %v", CrossVolEnv, base, err)
	}
	d, err = filepath.EvalSymlinks(d)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeTree(t, d) })
	return d
}

// ---- ツリー ----

// Kind は Tree のエントリの種類。
type Kind int

const (
	KindFile       Kind = iota + 1
	KindDir             // 中身は Tree の別のキーで指定する
	KindSymlink         // ファイル用のシンボリックリンク（Windows）。Unix ではフォルダ用と区別しない
	KindDirSymlink      // フォルダ用のシンボリックリンク（Windows）。Unix では KindSymlink と同じ
	KindJunction        // Windows のみ。ほかの OS では t.Skip する
	KindFIFO            // Unix のみ。Windows では t.Skip する
)

// Entry は Tree のエントリ。
type Entry struct {
	Kind Kind
	Data string // KindFile の内容
	// Target はリンク先。シンボリックリンクでは文字列をそのまま使う（相対パスも可）。
	// ジャンクションでは、相対パスなら Build の root からのパス（"/" 区切り）とみなす。
	Target   string
	ModTime  time.Time // ゼロ値なら設定しない。リンクには設定しない
	ReadOnly bool      // SetReadOnly と同じ。フォルダは中身をすべて作った後に設定する
}

// File、Dir、Symlink、DirSymlink、Junction、FIFO は Entry を作る。
func File(data string) Entry         { return Entry{Kind: KindFile, Data: data} }
func Dir() Entry                     { return Entry{Kind: KindDir} }
func Symlink(target string) Entry    { return Entry{Kind: KindSymlink, Target: target} }
func DirSymlink(target string) Entry { return Entry{Kind: KindDirSymlink, Target: target} }
func Junction(target string) Entry   { return Entry{Kind: KindJunction, Target: target} }
func FIFO() Entry                    { return Entry{Kind: KindFIFO} }
func (e Entry) At(m time.Time) Entry { e.ModTime = m; return e }
func (e Entry) RO() Entry            { e.ReadOnly = true; return e }

// Tree は、root からの相対パス（"/" 区切り）からエントリへの対応。親フォルダは自動で作る。
type Tree map[string]Entry

// Build は root の中に tree を作る。
// 作成はパスの順に行い、更新日時は作成後に、読み取り専用はさらにその後に深い順に設定する。
// 読み取り専用にしたものは、テストの終了時に書き込み可能に戻す（TempDir を削除できるように）。
func Build(t testing.TB, root string, tree Tree) {
	t.Helper()
	paths := slices.Sorted(func(yield func(string) bool) {
		for p := range tree {
			if !yield(p) {
				return
			}
		}
	})
	for _, rel := range paths {
		e := tree[rel]
		p := filepath.Join(root, filepath.FromSlash(rel))
		MkdirAll(t, filepath.Dir(p))
		switch e.Kind {
		case KindFile:
			WriteFile(t, p, e.Data)
		case KindDir:
			MkdirAll(t, p)
		case KindSymlink:
			CreateSymlink(t, e.Target, p, false)
		case KindDirSymlink:
			CreateSymlink(t, e.Target, p, true)
		case KindJunction:
			target := e.Target
			if !filepath.IsAbs(target) {
				target = filepath.Join(root, filepath.FromSlash(target))
			}
			CreateJunction(t, target, p)
		case KindFIFO:
			CreateFIFO(t, p)
		default:
			t.Fatalf("testfs.Build: %s: unknown kind %d", rel, e.Kind)
		}
	}
	for _, rel := range paths {
		e := tree[rel]
		if e.ModTime.IsZero() {
			continue
		}
		if e.Kind != KindFile && e.Kind != KindDir && e.Kind != KindFIFO {
			t.Fatalf("testfs.Build: %s: ModTime cannot be set on a link", rel)
		}
		p := ExtendedPath(filepath.Join(root, filepath.FromSlash(rel)))
		if err := os.Chtimes(p, e.ModTime, e.ModTime); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range slices.Backward(paths) {
		if tree[rel].ReadOnly {
			SetReadOnly(t, filepath.Join(root, filepath.FromSlash(rel)))
		}
	}
}

// MkdirAll は path とその親フォルダを作る。既にあるフォルダはそのまま使う。
// os.MkdirAll は \\?\ 形式のパスを扱えない Go のバージョンがあったため、自前で 1 段ずつ作る。
func MkdirAll(t testing.TB, path string) {
	t.Helper()
	if fi, err := os.Lstat(ExtendedPath(path)); err == nil {
		if !isRealDir(fi) {
			t.Fatalf("testfs.MkdirAll: %s exists and is not a directory", path)
		}
		return
	}
	parent := filepath.Dir(path)
	if parent != path {
		MkdirAll(t, parent)
	}
	if err := os.Mkdir(ExtendedPath(path), 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		t.Fatal(err)
	}
}

// WriteFile は path に data を書く（既存のファイルは置き換える）。
func WriteFile(t testing.TB, path, data string) {
	t.Helper()
	if err := os.WriteFile(ExtendedPath(path), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ReadFile は path の内容を返す。
func ReadFile(t testing.TB, path string) string {
	t.Helper()
	b, err := os.ReadFile(ExtendedPath(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Exists は、path にエントリがあるか（リンクを辿らずに）を返す。
func Exists(t testing.TB, path string) bool {
	t.Helper()
	_, err := os.Lstat(ExtendedPath(path))
	if err == nil {
		return true
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	t.Fatal(err)
	return false
}

// ---- 長いパスと名前 ----

// LongPath は、root の中に、全体の長さが 260 文字（Windows の MAX_PATH）を超えるフォルダを作って返す。
func LongPath(t testing.TB, root string) string {
	t.Helper()
	p := root
	for i := 0; len(p) <= 300; i++ {
		p = filepath.Join(p, fmt.Sprintf("long-%02d-%s", i, strings.Repeat("x", 40)))
	}
	MkdirAll(t, p)
	return p
}

// 日本語・絵文字・Unicode 正規化・大文字小文字の確認に使う名前（SPEC §18.1）。
// NFC と NFD、大文字と小文字は、APFS や NTFS の既定では同じフォルダに両方を作れないことがある
// （FoldsNormalization、FoldsCase で確かめる）。
const (
	NameJapanese = "日本語のファイル名.txt"
	NameEmoji    = "絵文字😀🎉.txt"
	NameNFC      = "caf\u00e9.txt"  // é を 1 文字（U+00E9）で表す
	NameNFD      = "cafe\u0301.txt" // e + 結合用アクセント（U+0301）
	NameLower    = "case.txt"
	NameUpper    = "CASE.txt"
)

// Windows の Win32 のパス正規化で変わる名前（SPEC §8.2、§18.1）。
// Windows では \\?\ 形式のパスでだけ作れる。Unix では普通の名前として作れる。
const (
	NameTrailingDot   = "foo."
	NameTrailingSpace = "foo "
	NamePlain         = "foo" // 上の 2 つが正規化されると、この名前と取り違えられる
	NameReservedCON   = "CON"
	NameReservedNUL   = "nul.txt"
)

// FoldsCase は、dir のあるボリュームが大文字小文字を区別しない（同じ名前とみなす）かを調べる。
func FoldsCase(t testing.TB, dir string) bool {
	t.Helper()
	return foldsName(t, dir, "fsops-case-probe", "FSOPS-CASE-PROBE")
}

// FoldsNormalization は、dir のあるボリュームが NFC と NFD を同じ名前とみなすかを調べる。
func FoldsNormalization(t testing.TB, dir string) bool {
	t.Helper()
	return foldsName(t, dir, "probe-\u00e9", "probe-e\u0301")
}

func foldsName(t testing.TB, dir, created, looked string) bool {
	t.Helper()
	d, err := os.MkdirTemp(dir, "fold-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	WriteFile(t, filepath.Join(d, created), "")
	return Exists(t, filepath.Join(d, looked))
}

// ---- 読み取り専用 ----

// SetReadOnly は path を読み取り専用にする（SPEC §9.3 の定義）。
// Windows では読み取り専用属性を付け、Unix ではすべての書き込み権限を外す。
// テストの終了時に元に戻す。
func SetReadOnly(t testing.TB, path string) {
	t.Helper()
	restore, err := setReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := restore(); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("restore %s: %v", path, err)
		}
	})
}

// makeTreeWritable は、削除できるように dir の中の読み取り専用を外す（後片付け用。エラーは無視する）。
func makeTreeWritable(dir string) {
	var walk func(p string)
	walk = func(p string) {
		fi, err := os.Lstat(ExtendedPath(p))
		if err != nil || isLink(fi) {
			return
		}
		clearReadOnly(p)
		if !isRealDir(fi) {
			return
		}
		entries, _ := os.ReadDir(ExtendedPath(p))
		for _, e := range entries {
			walk(filepath.Join(p, e.Name()))
		}
	}
	walk(dir)
}

// ---- スナップショット ----

// Node はスナップショットの 1 エントリ。
type Node struct {
	Type    string // "file"、"dir"、"link"（シンボリックリンク・ジャンクション・その他のリパースポイント）、"other"
	Perm    fs.FileMode
	Size    int64
	SHA256  string // file のとき内容のハッシュ
	Target  string // link のときリンク先（取得できた場合）
	ModTime int64  // UnixNano
}

// Snapshot は、root からの相対パス（"/" 区切り、root 自身は "."）からエントリへの対応。
type Snapshot map[string]Node

// Take は root の中をリンクを辿らずに走査し、スナップショットを返す。
// 計画の前後でファイルシステムが変わらないこと、リンク先の目印ファイルが残ることなどの確認に使う。
func Take(t testing.TB, root string) Snapshot {
	t.Helper()
	s := Snapshot{}
	var walk func(p, rel string)
	walk = func(p, rel string) {
		fi, err := os.Lstat(ExtendedPath(p))
		if err != nil {
			t.Fatal(err)
		}
		n := Node{Perm: fi.Mode().Perm(), ModTime: fi.ModTime().UnixNano()}
		switch {
		case isLink(fi):
			n.Type = "link"
			n.Target, _ = os.Readlink(ExtendedPath(p))
		case isRealDir(fi):
			n.Type = "dir"
		case fi.Mode().IsRegular():
			n.Type = "file"
			n.Size = fi.Size()
			n.SHA256 = hashFile(t, p)
		default:
			n.Type = "other"
		}
		s[rel] = n
		if n.Type != "dir" {
			return
		}
		entries, err := os.ReadDir(ExtendedPath(p))
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			walk(filepath.Join(p, e.Name()), strings.TrimPrefix(rel+"/"+e.Name(), "./"))
		}
	}
	walk(root, ".")
	return s
}

func hashFile(t testing.TB, p string) string {
	t.Helper()
	f, err := os.Open(ExtendedPath(p))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Diff は、a と b の違いを人が読める形で返す。違いがなければ nil。
func Diff(a, b Snapshot) []string {
	var d []string
	for _, k := range slices.Sorted(func(yield func(string) bool) {
		for k := range a {
			if !yield(k) {
				return
			}
		}
		for k := range b {
			if _, ok := a[k]; !ok && !yield(k) {
				return
			}
		}
	}) {
		na, inA := a[k]
		nb, inB := b[k]
		switch {
		case !inA:
			d = append(d, fmt.Sprintf("+ %s %+v", k, nb))
		case !inB:
			d = append(d, fmt.Sprintf("- %s %+v", k, na))
		case na != nb:
			d = append(d, fmt.Sprintf("~ %s %+v -> %+v", k, na, nb))
		}
	}
	return d
}

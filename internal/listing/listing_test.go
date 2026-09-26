package listing

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/fsops"
)

func entry(name string, t fsops.EntryType) fsops.Entry {
	return fsops.Entry{Name: name, Info: fsops.EntryInfo{Type: t, ModTime: time.Unix(0, 0)}}
}

func names(items []Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Name)
	}
	return out
}

// TestHidden は、隠しファイルの規則を確かめる（filer §6）。属性はどの OS でも見て、名前の . は dotHidden のときだけ見る。
func TestHidden(t *testing.T) {
	t.Parallel()
	attr := entry("a", fsops.TypeFile)
	attr.Hidden = true
	dot := entry(".profile", fsops.TypeFile)
	failed := fsops.Entry{Name: ".failed", Err: &fsops.OpError{Kind: fsops.KindPermission}}
	failedPlain := fsops.Entry{Name: "failed", Err: &fsops.OpError{Kind: fsops.KindPermission}}
	for _, tt := range []struct {
		e         fsops.Entry
		dotHidden bool
		want      bool
	}{
		{attr, false, true}, {attr, true, true},
		{dot, false, false}, {dot, true, true}, // Windows は名前の . で隠さない（エクスプローラーと同じ）
		{failed, true, true}, {failed, false, false}, {failedPlain, true, false},
		{entry("plain", fsops.TypeFile), true, false},
	} {
		if got := Hidden(tt.e, tt.dotHidden); got != tt.want {
			t.Errorf("Hidden(%q hidden=%v, dotHidden=%v) = %v, want %v", tt.e.Name, tt.e.Hidden, tt.dotHidden, got, tt.want)
		}
	}
}

// TestBuildOrder は、並び順を確かめる（filer §6）。.. → フォルダ・ジャンクション → リンク → その他。それぞれの中は名前のキーの順。
func TestBuildOrder(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(root(), "x")
	entries := []fsops.Entry{
		entry("zeta.txt", fsops.TypeFile),
		entry("Beta", fsops.TypeDir),
		entry("link-a", fsops.TypeSymlink),
		entry("alpha", fsops.TypeDir),
		entry("jct", fsops.TypeJunction),
		entry("fifo", fsops.TypeSpecial),
		{Name: "unreadable", Err: &fsops.OpError{Kind: fsops.KindPermission}},
		entry("Alpha.txt", fsops.TypeFile),
	}
	got := names(Build(dir, entries, false))
	want := []string{"..", "alpha", "Beta", "jct", "link-a", "Alpha.txt", "fifo", "unreadable", "zeta.txt"}
	if !slices.Equal(got, want) {
		t.Errorf("Build order = %q, want %q", got, want)
	}
	items := Build(dir, entries, false)
	if !items[0].Parent || items[1].Parent {
		t.Error("only the first item is the parent (..)")
	}
}

// TestBuildRoot は、ボリュームのルートでは .. を出さないことを確かめる。
func TestBuildRoot(t *testing.T) {
	t.Parallel()
	items := Build(root(), []fsops.Entry{entry("a", fsops.TypeFile)}, false)
	if got := names(items); !slices.Equal(got, []string{"a"}) {
		t.Errorf("at the root: %q, want no ..", got)
	}
	if !IsRoot(root()) || IsRoot(filepath.Join(root(), "a")) {
		t.Error("IsRoot")
	}
}

// TestSortKey は、名前の比較を確かめる。大文字小文字をそろえ、正規化し、数字の並びを数値として比べる（filer §6）。
func TestSortKey(t *testing.T) {
	t.Parallel()
	for _, want := range [][]string{
		{"file2", "file10", "file100"},                           // 数字の並びは数値で比べる
		{"a", "B", "c"},                                          // 大文字小文字をそろえる
		{"x1y", "x01y2", "x1y10"},                                // 数値が同じなら次を比べる
		{"img9.png", "img０１０.png"},                               // 全角の数字も数値（正規化）
		{"2026", "abc"},                                          // 数字は文字より前
		{"がa", "か\u3099z"},                                       // NFD も NFC と同じキーで比べる
		{"ﾃﾞｰﾀ", "データ2"},                                         // 半角カナは全角と同じキー
		{"file18446744073709551616", "file18446744073709551617"}, // 64 ビットを超える数
	} {
		got := slices.Clone(want)
		slices.Reverse(got)
		items := make([]Item, len(got))
		for i, n := range got {
			items[i] = newItem(entry(n, fsops.TypeFile), false)
		}
		slices.SortFunc(items, Compare)
		if g := names(items); !slices.Equal(g, want) {
			t.Errorf("sorted %q, want %q", g, want)
		}
	}
}

// TestSortKeyTieBreak は、キーが同じ名前（大文字小文字・正規化だけが違う）を、元の名前のバイト順で決まった順に並べることを確かめる。
// 名前そのものは変えない（U4）。
func TestSortKeyTieBreak(t *testing.T) {
	t.Parallel()
	a, b := newItem(entry("が", fsops.TypeFile), false), newItem(entry("か\u3099", fsops.TypeFile), false)
	if a.key != b.key {
		t.Fatalf("NFC and NFD have different keys: %q %q", a.key, b.key)
	}
	if Compare(a, b) == 0 || Compare(a, b) != -Compare(b, a) {
		t.Error("items with the same key must still have a fixed order")
	}
	if a.Name != "が" || b.Name != "か\u3099" {
		t.Error("the name must not be changed (U4)")
	}
}

// TestBuildKeepsNames は、Build が名前・種類・属性・エラーをそのまま持つことを確かめる（U4）。
func TestBuildKeepsNames(t *testing.T) {
	t.Parallel()
	bad := fsops.Entry{Name: "bad\xff\u202e", Info: fsops.EntryInfo{Type: fsops.TypeFile, Size: 42}, ReadOnly: true}
	failed := fsops.Entry{Name: "failed", Err: &fsops.OpError{Kind: fsops.KindPermission}}
	items := Build(root(), []fsops.Entry{bad, failed}, true)
	if items[0].Name != bad.Name || items[0].Info != bad.Info || !items[0].ReadOnly {
		t.Errorf("item = %+v, want the entry %+v", items[0], bad)
	}
	if items[1].Err == nil || items[1].Err.Kind != fsops.KindPermission {
		t.Errorf("the error of an entry is lost: %+v", items[1])
	}
}

// root は、テストで使うボリュームのルート。
func root() string {
	if v := filepath.VolumeName(`C:\`); v != "" {
		return `C:\`
	}
	return "/"
}

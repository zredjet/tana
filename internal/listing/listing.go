package listing

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/zredjet/tana/internal/fsops"
)

// Item は、一覧の 1 つの項目。
type Item struct {
	Name     string          // 列挙で得た名前（バイト列をそのまま。パスはこれから作る。filer U4）
	Info     fsops.EntryInfo // 種類・サイズ・更新日時
	Hidden   bool            // 隠しファイル（filer §6）
	ReadOnly bool
	Err      *fsops.OpError // 列挙はできたが調べられなかった。Info と ReadOnly は使えない
	Parent   bool           // 親のフォルダ（..）。Name は ".."

	key string // 並べ替えのキー
}

// IsDir は、Enter で入る項目（フォルダ・ジャンクション・..）かを返す。リンクは、リンク先を調べるまで分からないので含めない。
func (it Item) IsDir() bool {
	return it.Parent || it.Err == nil && (it.Info.Type == fsops.TypeDir || it.Info.Type == fsops.TypeJunction)
}

// Hidden は、エントリ e を隠しファイルとして扱うかを返す（filer §6）。
// 属性（Windows の FILE_ATTRIBUTE_HIDDEN、macOS の UF_HIDDEN）が付いたものと、dotHidden のときは名前が . で始まるもの。
func Hidden(e fsops.Entry, dotHidden bool) bool {
	return e.Err == nil && e.Hidden || dotHidden && strings.HasPrefix(e.Name, ".")
}

// IsRoot は、dir がボリュームのルート（.. を出さない）かを返す。
func IsRoot(dir string) bool { return filepath.Dir(dir) == dir }

// Build は、フォルダ dir の列挙の結果から、並べ替えた項目の列を作る。ルート以外では先頭に .. を置く（filer §6）。
func Build(dir string, entries []fsops.Entry, dotHidden bool) []Item {
	items := make([]Item, 0, len(entries)+1)
	if !IsRoot(dir) {
		items = append(items, Item{Name: "..", Parent: true, Info: fsops.EntryInfo{Type: fsops.TypeDir}})
	}
	k := newKeyer()
	for _, e := range entries {
		items = append(items, k.item(e, dotHidden))
	}
	slices.SortFunc(items, Compare)
	return items
}

// newItem は、エントリ e の項目を作る（テスト用）。
func newItem(e fsops.Entry, dotHidden bool) Item { return newKeyer().item(e, dotHidden) }

// group は、並び順の組（filer §6）。.. → フォルダ・ジャンクション → リンク → その他。
func group(it Item) int {
	switch {
	case it.Parent:
		return 0
	case it.Err != nil:
		return 3
	case it.Info.Type == fsops.TypeDir || it.Info.Type == fsops.TypeJunction:
		return 1
	case it.Info.Type == fsops.TypeSymlink:
		return 2
	}
	return 3
}

// Compare は、項目の並び順を返す。組、名前のキー、名前のバイト順の順に比べる（キーが同じ名前も、決まった順に並べる）。
func Compare(a, b Item) int {
	return cmp.Or(cmp.Compare(group(a), group(b)), strings.Compare(a.key, b.key), strings.Compare(a.Name, b.Name))
}

// keyer は、並べ替えのキーを作る。cases.Caser は状態を持つので、Build ごとに作る。
type keyer struct{ fold cases.Caser }

func newKeyer() *keyer { return &keyer{fold: cases.Fold()} }

func (k *keyer) item(e fsops.Entry, dotHidden bool) Item {
	return Item{Name: e.Name, Info: e.Info, Hidden: Hidden(e, dotHidden), ReadOnly: e.ReadOnly, Err: e.Err, key: k.key(e.Name)}
}

// key は、名前の並べ替えのキーを作る。互換分解の正規化（NFKC。全角の数字・半角カナもそろう）、大文字小文字の畳み込みをしてから、
// ASCII の数字の並びを「印 '0'、先頭の 0 を除いた桁数（2 バイト）、数字」に置き換える。
// こうすると、キーをバイト順で比べれば、数字の並びは数値で比べられ、数字は文字より前に来る。
func (k *keyer) key(name string) string {
	s := k.fold.String(norm.NFKC.String(name))
	b := make([]byte, 0, len(s)+8)
	for i := 0; i < len(s); {
		if !isDigit(s[i]) {
			b = append(b, s[i])
			i++
			continue
		}
		j := i
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		run := strings.TrimLeft(s[i:j], "0")
		if run == "" {
			run = "0"
		}
		n := min(len(run), 0xffff)
		b = append(b, '0', byte(n>>8), byte(n))
		b = append(b, run...)
		i = j
	}
	return string(b)
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

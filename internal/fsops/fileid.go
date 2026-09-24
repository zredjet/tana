package fsops

import (
	"encoding/binary"
	"path/filepath"
)

// fileID はエントリの同一性（§8.3）。パスの文字列ではなく、これで同一性を判定する。os.SameFile は使わない。
// 取得方法（method）が違う値どうしは比べない（== で比べれば、method が違えば一致しない）。
type fileID struct {
	method idMethod
	vol    uint64   // ボリュームの識別子（Windows: ボリュームシリアル番号、Unix: Dev）
	id     [16]byte // ボリューム内のファイルの識別子（Windows: ファイル ID、Unix: Ino）
}

// idMethod は fileID の取得方法。
type idMethod uint8

const (
	idMethodNone     idMethod = iota
	idMethodDevIno            // Unix: Stat_t の Dev と Ino
	idMethodFileID            // Windows: FileIdInfo（64 ビットのシリアル番号と 128 ビットのファイル ID）
	idMethodByHandle          // Windows: GetFileInformationByHandle（exFAT・FAT32 など FileIdInfo が使えないボリューム）
)

// syntheticIno は、macOS の exFAT・FAT32 で空の通常のファイルに付く仮の ino（2^63 以上。操作のたびに変わる。V25）の代わりに使う一定の値（§8.3）。
const syntheticIno = 1 << 63

// synthetic は、id が §8.3 の空のファイルの一定の fileID か（別々のファイルでも同じ値になるので、同じファイルの判定には使えない）。
func (id fileID) synthetic() bool {
	return id.method == idMethodDevIno && binary.LittleEndian.Uint64(id.id[:8]) == syntheticIno
}

// idStat は fileID と、同一性の判定（§8.4 の同じファイルの名前変更など）に使う属性。
type idStat struct {
	id    fileID
	isDir bool   // リンクでないフォルダ
	nlink uint64 // ハードリンクの数
}

// fileIDOf は path 自体（リンクを辿らない）の fileID を返す。path は checkPath を通ったパスであること。
func fileIDOf(path string) (idStat, error) { return fileIDPath(path, false) }

// fileIDFollow は path のリンクを辿った先の fileID を返す。
func fileIDFollow(path string) (idStat, error) { return fileIDPath(path, true) }

func fileIDPath(path string, follow bool) (idStat, error) {
	s, err := sysPath(path)
	if err != nil {
		return idStat{}, err
	}
	st, err := statIDSys(s, follow)
	return st, withUserPaths(err, path, "")
}

// onOtherVolume は、フォルダ child が、それを含むフォルダ parent と別のボリュームにある（マウントポイントである）かを返す（§13.1）。
// Unix の Dev で比べる。Windows のマウントされたフォルダはリパースポイント（TypeJunction）で、もともと入らないので偽を返す。
func onOtherVolume(child, parent fileID) bool {
	return child.method == idMethodDevIno && parent.method == idMethodDevIno && child.vol != parent.vol
}

// mountPoint は、トップレベルのフォルダ path（エントリ e）がマウントポイントかを返す（§13.1）。親フォルダはリンクを辿って調べる
// （親のパスがリンクでも、実際にそのフォルダを含むフォルダと比べるため）。調べられなければ偽を返す。
func mountPoint(path string, e dirEntry) bool {
	if e.info.Type != TypeDir {
		return false
	}
	parent, err := fileIDFollow(filepath.Dir(path))
	return err == nil && onOtherVolume(e.id, parent.id)
}

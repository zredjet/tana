package fsops

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

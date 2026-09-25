package fsops

// dirEntry は、フォルダの列挙で得た 1 エントリ（§13.1）。
type dirEntry struct {
	name string
	info EntryInfo // Size は TypeFile のときだけ
	id   fileID
	// dirAttr は、OS がフォルダとして扱うエントリか（削除の方法を決める。§13.2）。
	// Unix では TypeDir と同じ。Windows では FILE_ATTRIBUTE_DIRECTORY（フォルダ用のリンク・ジャンクションを含む）。
	dirAttr bool
	// statErr は、列挙はできたが調べられなかった（Unix の fstatat の失敗）ことを表す。info・id・dirAttr は使えない。
	// 使う側は、そのエントリだけを失敗として報告し、ほかのエントリは続ける（1 件のために、フォルダ全体を失敗にしない）。
	statErr error
}

// readDir は、フォルダ dir をリンクを辿らずに列挙し、名前のバイト順で返す（§13.1）。
// dir 自体がフォルダでない場合（リンク・ジャンクションを含む）は、中に入らずに error を返す（I4）。
// 列挙の途中で消えたエントリは含めない。
//
// 計画の走査（§6.3）で使う。削除・マージ移動のためにハンドルで確かめて入る走査はフェーズ6で作る。
func readDir(dir string) ([]dirEntry, error) {
	s, err := sysPath(dir)
	if err != nil {
		return nil, err
	}
	entries, err := readDirSys(s)
	return entries, withUserPaths(err, dir, "")
}

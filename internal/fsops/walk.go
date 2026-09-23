package fsops

// dirEntry は、フォルダの列挙で得た 1 エントリ（§13.1）。
type dirEntry struct {
	name string
	info EntryInfo // Size は TypeFile のときだけ
	id   fileID
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

//go:build unix

package fsops

import (
	"io/fs"
	"os"
)

// lstatEntrySys は、sysPath で変換済みのパス p を調べる。
func lstatEntrySys(p string) (EntryInfo, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return EntryInfo{}, err
	}
	return entryInfo(entryTypeFromMode(fi.Mode()), fi), nil
}

// entryTypeFromMode は Lstat のモードから種類を判定する（SPEC §14.1）。
// FIFO・ソケット・デバイスなど、通常のファイル・フォルダ・シンボリックリンク以外は TypeSpecial。
func entryTypeFromMode(m fs.FileMode) EntryType {
	switch m.Type() {
	case 0:
		return TypeFile
	case fs.ModeDir:
		return TypeDir
	case fs.ModeSymlink:
		return TypeSymlink
	}
	return TypeSpecial
}

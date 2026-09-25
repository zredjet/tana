package fsops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
)

// renameExclusive は排他リネーム（§8.4）。dst が存在すれば KindExist で失敗し、何も変えない。
// src と dst が同じファイルで、大文字小文字・正規化の違いだけの名前変更のときは、OS の通常のリネームで行う。
// src・dst は checkPath を通ったパスであること。エラーのパスは src・dst の形で返す。
func renameExclusive(src, dst string) error {
	s, err := sysPath(src)
	if err != nil {
		return err
	}
	d, err := sysPath(dst)
	if err != nil {
		return err
	}
	return withUserPaths(renameExclusiveSysSame(s, d), src, dst)
}

// renameExclusiveSysSame は、sysPath で変換済みのパスでの renameExclusive。
func renameExclusiveSysSame(s, d string) error {
	err := renameExclusiveSys(s, d)
	if err != nil && classify(err, classifyOpts{}) == KindExist && sameFileRename(s, d) {
		return renamePlainSys(s, d)
	}
	return err
}

// sameFileRename は、s から d への変更が「同じファイルの名前変更」（§8.4）かを返す。
// d が s と同じ fileID で、名前の文字列が異なり、親フォルダが同じで、ファイルならリンク数が 1 のとき真。
// 親フォルダとリンク数の条件は、ハードリンクを名前の違いと取り違えないため。
func sameFileRename(s, d string) bool {
	if filepath.Base(s) == filepath.Base(d) {
		return false
	}
	si, err := statIDSys(s, false)
	if err != nil {
		return false
	}
	di, err := statIDSys(d, false)
	if err != nil || di.id != si.id {
		return false
	}
	if !si.isDir && si.nlink != 1 {
		return false
	}
	sp, err := statIDSys(filepath.Dir(s), false)
	if err != nil {
		return false
	}
	dp, err := statIDSys(filepath.Dir(d), false)
	return err == nil && sp.id == dp.id
}

// renameReplace は置換リネーム（§8.4。ファイル同士の上書き用）。フォルダを置き換える用途には使わない。
func renameReplace(src, dst string) error {
	s, err := sysPath(src)
	if err != nil {
		return err
	}
	d, err := sysPath(dst)
	if err != nil {
		return err
	}
	return withUserPaths(os.Rename(s, d), src, dst)
}

// Rename は path の名前を newName に変える。上書きは一切しない（§11.3）。
func Rename(path, newName string) error {
	return renameWith(newLockRetrier(context.Background(), nil), path, newName)
}

// renameWith は Rename の本体。使用中の一時的な失敗は lr でやり直す（§17.1）。
func renameWith(lr *lockRetrier, path, newName string) error {
	p, err := checkPath(path)
	if err != nil {
		return &OpError{Op: "rename", Path: path, Kind: KindInvalidRequest}
	}
	if isVolumeRoot(p) {
		return &OpError{Op: "rename", Path: p, Kind: KindInvalidRequest}
	}
	if err := validateName(newName); err != nil {
		return &OpError{Op: "rename", Path: p, Kind: KindInvalidName} // 使えない名前からは Dest のパスを作らない
	}
	if newName == filepath.Base(p) {
		return nil // 今と同じ名前（バイト単位で同じ）。何もしない（§11.3）
	}
	dir := filepath.Dir(p)
	dst := filepath.Join(dir, newName)
	s, err := sysPath(p)
	if err != nil {
		return err
	}
	d, err := sysPath(dst)
	if err != nil {
		return err
	}
	fail := func(err error, dest string) error {
		return &OpError{Op: "rename", Path: p, Dest: dest, Kind: classify(err, classifyOpts{readOnly: readOnlySys(s)}),
			Err: withUserPaths(err, p, dest)}
	}
	// 名前の変更は、使用中の間はやり直す（§17.1）。
	retry := func(to string, rename func() error) error {
		return lr.retry(to, false, func() error {
			if err := lr.hooks.lockFaultErr("rename", to); err != nil {
				return err
			}
			return rename()
		}, lockedErr)
	}
	// 大文字小文字・正規化の違いだけの変更か（名前の変更の後の確認が要るか）を先に調べる。変更の方法自体は renameExclusiveSysSame が決める。
	same := sameFileRename(s, d)
	if same && lr.hooks.caseRenameNoopSet() {
		// テスト用: 成功を返しても名前が変わらないボリューム（V1）を再現する。
	} else if err := retry(dst, func() error { return renameExclusiveSysSame(s, d) }); err != nil {
		return fail(err, dst)
	}
	if !same {
		return nil
	}
	// 成功を返しても名前が変わらないボリュームがある（Windows の exFAT・FAT32。V1）ので、
	// 親フォルダの列挙で新しい名前がバイト単位で現れたことを確かめ、現れなければ途中名を経由して 2 回で変える（§11.3）。
	if ok, err := dirHasName(dir, newName); err != nil || ok {
		return nil // 列挙に失敗した場合は、名前の変更自体は成功しているので成功とする
	}
	tmpName, err := renameMidName()
	if err != nil {
		return fail(err, dst)
	}
	tmp := filepath.Join(dir, tmpName)
	t, err := sysPath(tmp)
	if err != nil {
		return err
	}
	if err := retry(tmp, func() error { return renameExclusiveSys(s, t) }); err != nil {
		return fail(err, dst)
	}
	if err := retry(dst, func() error { return renameExclusiveSys(t, d) }); err != nil {
		if rerr := retry(p, func() error { return renameExclusiveSys(t, s) }); rerr != nil {
			return fail(err, tmp) // 元の名前に戻せなかった。利用者のファイル・フォルダは途中名で残るので、そのパスを Dest で返す
		}
		return fail(err, dst)
	}
	return nil
}

// dirHasName は、フォルダ dir の列挙に name（バイト単位で同じ名前）があるかを返す。
func dirHasName(dir, name string) (bool, error) {
	s, err := sysPath(dir)
	if err != nil {
		return false, err
	}
	entries, err := os.ReadDir(s)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == name }), nil
}

// tempName は一時名（.fsops-<ランダム16進>.tmp。§10.1、§11.3）を返す。
func tempName() (string, error) {
	return randomName(".fsops-", ".tmp")
}

// renameMidName は、Rename の 2 段階の変更（§11.3）の途中名 .fsops-rename-<ランダム16進> を返す。
// 途中で止まって残った場合、それは利用者のファイル・フォルダなので、一時ファイルの名前（tempName）と形を変える。
func renameMidName() (string, error) {
	return randomName(".fsops-rename-", "")
}

// randomName は、prefix と 16 進 16 文字の乱数と suffix をつないだ名前を返す。
func randomName(prefix, suffix string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b[:]) + suffix, nil
}

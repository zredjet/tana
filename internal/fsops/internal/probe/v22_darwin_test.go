package probe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/unix"
)

// TestV22 は、macOS の exFAT・FAT32 で、拡張属性を保存する AppleDouble ファイル（`._名前`）が、名前の変更・削除でどう扱われるかを記録する
// （V22。総点検の穴 12 の確認中に見つかった問題。SPEC §8.5、§15 の前提）。
// 拡張属性には §15 で保持する com.apple.quarantine を使う。
func TestV22(t *testing.T) {
	const attr = "com.apple.quarantine"
	check := func(t *testing.T, label, dir string) {
		logf := func(format string, args ...any) { t.Logf("V22: %s: "+format, append([]any{label}, args...)...) }
		xattrOf := func(p string) string {
			b := make([]byte, 256)
			n, err := unix.Lgetxattr(p, attr, b)
			if err != nil {
				return "err=" + errString(err)
			}
			return string(b[:n])
		}
		// setup は、case の名前のフォルダに a/x（拡張属性つき）と空のフォルダ b を作る。
		setup := func(c string) string {
			d := filepath.Join(dir, c)
			testfs.Build(t, d, testfs.Tree{"a/x": testfs.File("x"), "b": testfs.Dir()})
			if err := unix.Lsetxattr(filepath.Join(d, "a", "x"), attr, []byte("q"), 0); err != nil {
				logf("%s: Lsetxattr: err=%v", c, err)
			}
			logf("%s: after setxattr: a=%+q", c, testfs.ListNames(t, filepath.Join(d, "a")))
			return d
		}
		// 1. x だけをリネームする。
		d := setup("rename-main")
		err := os.Rename(filepath.Join(d, "a", "x"), filepath.Join(d, "b", "x"))
		logf("rename-main: rename a/x -> b/x: err=%v; a=%+q b=%+q xattr=%s", errString(err),
			testfs.ListNames(t, filepath.Join(d, "a")), testfs.ListNames(t, filepath.Join(d, "b")), xattrOf(filepath.Join(d, "b", "x")))
		// 2. ._x を先にリネームしてから x をリネームする（名前の順に 1 つずつ移す場合）。
		d = setup("rename-companion-first")
		err = os.Rename(filepath.Join(d, "a", "._x"), filepath.Join(d, "b", "._x"))
		logf("rename-companion-first: rename a/._x -> b/._x: err=%v; b=%+q xattr(a/x)=%s", errString(err),
			testfs.ListNames(t, filepath.Join(d, "b")), xattrOf(filepath.Join(d, "a", "x")))
		err = os.Rename(filepath.Join(d, "a", "x"), filepath.Join(d, "b", "x"))
		logf("rename-companion-first: rename a/x -> b/x: err=%v; a=%+q b=%+q xattr=%s", errString(err),
			testfs.ListNames(t, filepath.Join(d, "a")), testfs.ListNames(t, filepath.Join(d, "b")), xattrOf(filepath.Join(d, "b", "x")))
		// 3. x を先にリネームしてから ._x をリネームする。
		d = setup("rename-main-first")
		err = os.Rename(filepath.Join(d, "a", "x"), filepath.Join(d, "b", "x"))
		logf("rename-main-first: rename a/x -> b/x: err=%v; a=%+q", errString(err), testfs.ListNames(t, filepath.Join(d, "a")))
		_, err = os.Lstat(filepath.Join(d, "a", "._x"))
		logf("rename-main-first: Lstat a/._x: err=%v; xattr(b/x)=%s", errString(err), xattrOf(filepath.Join(d, "b", "x")))
		// 4. 移動先に、無関係な ._x だけがある場所へ x をリネームする。
		d = setup("rename-onto-stale")
		testfs.WriteFile(t, filepath.Join(d, "b", "._x"), "stale")
		err = os.Rename(filepath.Join(d, "a", "x"), filepath.Join(d, "b", "x"))
		logf("rename-onto-stale: rename a/x -> b/x: err=%v; a=%+q b=%+q xattr=%s", errString(err),
			testfs.ListNames(t, filepath.Join(d, "a")), testfs.ListNames(t, filepath.Join(d, "b")), xattrOf(filepath.Join(d, "b", "x")))
		// 5. 拡張属性のない y を、._y だけがある場所へリネームする。
		d = setup("rename-plain-onto-stale")
		testfs.WriteFile(t, filepath.Join(d, "a", "y"), "y")
		testfs.WriteFile(t, filepath.Join(d, "b", "._y"), "stale")
		logf("rename-plain-onto-stale: before: a=%+q b=%+q", testfs.ListNames(t, filepath.Join(d, "a")), testfs.ListNames(t, filepath.Join(d, "b")))
		err = os.Rename(filepath.Join(d, "a", "y"), filepath.Join(d, "b", "y"))
		logf("rename-plain-onto-stale: rename a/y -> b/y: err=%v; b=%+q", errString(err), testfs.ListNames(t, filepath.Join(d, "b")))
		// 6. x を削除する。
		d = setup("unlink-main")
		err = unix.Unlink(filepath.Join(d, "a", "x"))
		logf("unlink-main: unlink a/x: err=%v; a=%+q", errString(err), testfs.ListNames(t, filepath.Join(d, "a")))
		// 7. ._x を削除する。
		d = setup("unlink-companion")
		err = unix.Unlink(filepath.Join(d, "a", "._x"))
		logf("unlink-companion: unlink a/._x: err=%v; a=%+q xattr=%s", errString(err), testfs.ListNames(t, filepath.Join(d, "a")), xattrOf(filepath.Join(d, "a", "x")))
		// 8. 拡張属性つきのフォルダ（親に ._f ができる）を削除する。
		d = filepath.Join(dir, "rmdir")
		testfs.Build(t, d, testfs.Tree{"f": testfs.Dir()})
		err = unix.Lsetxattr(filepath.Join(d, "f"), attr, []byte("q"), 0)
		logf("rmdir: Lsetxattr f: err=%v; names=%+q", errString(err), testfs.ListNames(t, d))
		err = unix.Rmdir(filepath.Join(d, "f"))
		logf("rmdir: rmdir f: err=%v; names=%+q", errString(err), testfs.ListNames(t, d))
	}
	for _, env := range []string{envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			check(t, env, testfs.EnvDir(t, env))
		})
	}
}

// TestV22Detect は、拡張属性を AppleDouble ファイルに保存するボリュームを見分ける手がかり（statfs のファイルシステム名と
// pathconf(_PC_XATTR_SIZE_BITS)）を、APFS・exFAT・FAT32 で記録する（V22）。
func TestV22Detect(t *testing.T) {
	const pcXattrSizeBits = 26 // <sys/unistd.h> の _PC_XATTR_SIZE_BITS
	check := func(label, dir string) {
		var st unix.Statfs_t
		err := unix.Statfs(dir, &st)
		name := unix.ByteSliceToString(st.Fstypename[:])
		v, perr := unix.Pathconf(dir, pcXattrSizeBits)
		t.Logf("V22: detect: %s: statfs err=%s fstypename=%q flags=%#x; pathconf(_PC_XATTR_SIZE_BITS)=%d err=%s", label, errString(err), name, st.Flags, v, errString(perr))
	}
	check("APFS (temp dir)", testfs.TempDir(t))
	for _, env := range []string{envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) { check(env, testfs.EnvDir(t, env)) })
	}
}

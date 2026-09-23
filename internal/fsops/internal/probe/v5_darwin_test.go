package probe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
)

// trashHelperSource は、NSFileManager の trashItemAtURL:resultingItemURL:error: を呼ぶ小さな Objective-C のプログラム。
// _test.go では cgo を使えないため、SPEC §12.3 と同じ API を呼ぶプログラムをテストの中で clang でビルドして使う。
const trashHelperSource = `#import <Foundation/Foundation.h>
int main(int argc, char **argv) {
  @autoreleasepool {
    NSURL *url = [NSURL fileURLWithPath:[NSString stringWithUTF8String:argv[1]]];
    NSURL *out = nil;
    NSError *err = nil;
    BOOL ok = [[NSFileManager defaultManager] trashItemAtURL:url resultingItemURL:&out error:&err];
    if (!ok) {
      printf("ERR %s %ld %s\n", err.domain.UTF8String, (long)err.code, err.localizedDescription.UTF8String);
      return 0;
    }
    printf("OK %s\n", out.path.UTF8String);
  }
  return 0;
}
`

// buildTrashHelper は trashHelperSource を dir の中でビルドし、実行ファイルのパスを返す。clang がなければ t.Skip する。
func buildTrashHelper(t *testing.T, dir string) string {
	t.Helper()
	src := filepath.Join(dir, "trash.m")
	bin := filepath.Join(dir, "trash")
	testfs.WriteFile(t, src, trashHelperSource)
	out, err := exec.Command("clang", "-fobjc-arc", "-framework", "Foundation", "-o", bin, src).CombinedOutput()
	if err != nil {
		t.Skipf("V5: cannot build the trash helper with clang: %v\n%s", err, out)
	}
	return bin
}

// trashItem は helper で path をごみ箱へ送り、ごみ箱の中のパスを返す。失敗したら errText にエラーの内容を入れる。
func trashItem(t *testing.T, helper, path string) (trashed, errText string) {
	t.Helper()
	out, err := exec.Command(helper, path).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return "", "helper: " + err.Error() + ": " + s
	}
	if p, ok := strings.CutPrefix(s, "OK "); ok {
		return p, ""
	}
	return "", s
}

// TestV5 は、trashItemAtURL が CI 上で成功するか、返されたパスを Lstat できるかを記録する（SPEC §20 V5、§12.3）。
// シンボリックリンクをごみ箱へ送ったときに、リンク先が残ることも確かめる（I4）。FSOPS_TEST_TRASH=1 のときだけ実行する。
func TestV5(t *testing.T) {
	testfs.RequireTrash(t)
	helper := buildTrashHelper(t, testfs.TempDir(t))
	check := func(t *testing.T, label, dir string) {
		testfs.Build(t, dir, testfs.Tree{
			"file.txt":       testfs.File("file content"),
			"dir/a.txt":      testfs.File("a"),
			"target/marker":  testfs.File("m"),
			"link-to-target": testfs.DirSymlink("target"),
		})
		for _, name := range []string{"file.txt", "dir", "link-to-target"} {
			p := filepath.Join(dir, name)
			trashed, errText := trashItem(t, helper, p)
			if errText != "" {
				t.Logf("V5: %s: %s: failed: %s; still exists=%v", label, name, errText, testfs.Exists(t, p))
				continue
			}
			fi, err := os.Lstat(trashed)
			s := "Lstat error: " + errString(err)
			if err == nil {
				s = "Lstat mode=" + fi.Mode().String()
				if name == "file.txt" {
					b, err := os.ReadFile(trashed)
					s += " content equal=" + strconv.FormatBool(err == nil && string(b) == "file content") + " readErr=" + errString(err)
				}
			}
			t.Logf("V5: %s: %s: trashed to %s; %s; original exists=%v", label, name, trashed, s, testfs.Exists(t, p))
		}
		if got := testfs.ReadFile(t, filepath.Join(dir, "target", "marker")); got != "m" {
			t.Errorf("V5: %s: the symlink target's marker changed: %q", label, got)
		}
	}
	check(t, "temp dir", testfs.TempDir(t))
	for _, env := range []string{testfs.CrossVolEnv, envExFAT, envFAT32} {
		t.Run(env, func(t *testing.T) {
			check(t, env+"="+os.Getenv(env), testfs.EnvDir(t, env))
		})
	}
}

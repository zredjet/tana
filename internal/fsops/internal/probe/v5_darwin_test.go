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

// trashJXA は、NSFileManager の trashItemAtURL:resultingItemURL:error: を呼ぶ JavaScript for Automation のスクリプト。
// _test.go では cgo を使えないため、SPEC §12.3 と同じ API を osascript の ObjC ブリッジから呼ぶ。
const trashJXA = `ObjC.import('Foundation');
function run(argv) {
  var url = $.NSURL.fileURLWithPath(argv[0]);
  var result = Ref();
  var error = Ref();
  var ok = $.NSFileManager.defaultManager.trashItemAtURLResultingItemURLError(url, result, error);
  if (!ok) {
    var e = error[0];
    return 'ERR ' + ObjC.unwrap(e.domain) + ' ' + e.code + ' ' + ObjC.unwrap(e.localizedDescription);
  }
  return 'OK ' + ObjC.unwrap(result[0].path);
}`

// trashItem は path をごみ箱へ送り、ごみ箱の中のパスを返す。失敗したら errText にエラーの内容を入れる。
func trashItem(t *testing.T, path string) (trashed, errText string) {
	t.Helper()
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", trashJXA, path).CombinedOutput()
	s := strings.TrimSpace(string(out))
	if err != nil {
		return "", "osascript: " + err.Error() + ": " + s
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
	check := func(t *testing.T, label, dir string) {
		testfs.Build(t, dir, testfs.Tree{
			"file.txt":       testfs.File("file content"),
			"dir/a.txt":      testfs.File("a"),
			"target/marker":  testfs.File("m"),
			"link-to-target": testfs.DirSymlink("target"),
		})
		for _, name := range []string{"file.txt", "dir", "link-to-target"} {
			p := filepath.Join(dir, name)
			trashed, errText := trashItem(t, p)
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

package probe

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zredjet/tana/internal/fsops/internal/testfs"
	"golang.org/x/sys/windows"
)

// TestV8 は、Windows ランナーでシンボリックリンクを作る権限があるか、mklink /J が使えるかを記録する（SPEC §20 V8）。
// 結果はログに残すだけで、作れなくても失敗にしない（作れない場合、リンクを使うテストは t.Skip する）。
func TestV8(t *testing.T) {
	root := testfs.TempDir(t)
	testfs.Build(t, root, testfs.Tree{"target/marker.txt": testfs.File("m"), "target.txt": testfs.File("t")})

	if out, err := exec.Command("whoami", "/priv").CombinedOutput(); err != nil {
		t.Logf("V8: whoami /priv: %v", err)
	} else {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "SeCreateSymbolicLinkPrivilege") {
				t.Logf("V8: whoami /priv: %s", strings.TrimSpace(line))
			}
		}
	}
	if out, err := exec.Command("whoami", "/groups").CombinedOutput(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "Mandatory Label") {
				t.Logf("V8: integrity level: %s", strings.TrimSpace(line))
			}
		}
	}

	create := func(name, target string, flags uint32) {
		link := filepath.Join(root, name)
		l16, _ := windows.UTF16PtrFromString(testfs.ExtendedPath(link))
		t16, _ := windows.UTF16PtrFromString(target)
		err := windows.CreateSymbolicLink(l16, t16, flags)
		t.Logf("V8: CreateSymbolicLink(%s, flags=%#x): %v", name, flags, err)
	}
	const allowUnprivileged = 0x2
	create("file-priv", "target.txt", 0)
	create("file-unpriv", "target.txt", allowUnprivileged)
	create("dir-priv", "target", windows.SYMBOLIC_LINK_FLAG_DIRECTORY)
	create("dir-unpriv", "target", windows.SYMBOLIC_LINK_FLAG_DIRECTORY|allowUnprivileged)

	err := os.Symlink("target.txt", filepath.Join(root, "os-symlink"))
	t.Logf("V8: os.Symlink: %v", err)

	out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "junction"), filepath.Join(root, "target")).CombinedOutput()
	t.Logf("V8: mklink /J: %v %s", err, strings.TrimSpace(string(out)))
	if err == nil {
		if got := testfs.ReadFile(t, filepath.Join(root, "junction", "marker.txt")); got != "m" {
			t.Errorf("V8: reading through the junction = %q", got)
		}
	}
}

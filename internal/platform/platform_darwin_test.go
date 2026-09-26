package platform

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsExecutableDarwin は、.app・.command・.tool と、実行属性の付いたファイルを実行ファイルとすることを確かめる（filer §7）。
func TestIsExecutableDarwin(t *testing.T) {
	t.Parallel()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	script := write("script", 0o755)
	plain := write("plain.txt", 0o644)
	command := write("run.command", 0o644)
	tool := write("x.TOOL", 0o644)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(script, link); err != nil {
		t.Fatal(err)
	}
	app := filepath.Join(dir, "Some.app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{script: true, plain: false, command: true, tool: true, link: true} {
		if got := IsExecutable(p, false); got != want {
			t.Errorf("IsExecutable(%s) = %v, want %v", filepath.Base(p), got, want)
		}
	}
	if !IsExecutable(app, true) {
		t.Error("a .app bundle is executable")
	}
	if IsExecutable(dir, true) {
		t.Error("a plain folder is not executable")
	}
	if !CanOpen(filepath.Join(dir, "foo.")) || !DotFilesHidden {
		t.Error("macOS opens any name, and hides names starting with a dot")
	}
}

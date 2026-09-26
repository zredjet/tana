package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsExecutableWindows は、拡張子で実行ファイルを判定することを確かめる（filer §7）。大文字小文字は区別しない。
func TestIsExecutableWindows(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"a.exe", "A.EXE", "b.com", "c.bat", "d.cmd", "e.ps1", "f.vbs", "g.js", "h.msi", "i.scr", "j.lnk", "k.hta", "l.url",
		"m.chm", "n.settingcontent-ms", "o.msix", "p.appinstaller", "q.wsc", "r.scf"} {
		if !IsExecutable(`C:\x\`+name, false) {
			t.Errorf("IsExecutable(%s) = false", name)
		}
	}
	for _, name := range []string{"a.txt", "b.docx", "exe", "c.exe.txt", "noext"} {
		if IsExecutable(`C:\x\`+name, false) {
			t.Errorf("IsExecutable(%s) = true", name)
		}
	}
	if IsExecutable(`C:\x\folder.exe`, true) {
		t.Error("a folder named .exe is not executable")
	}
}

// TestCanOpenWindows は、VU10 を確かめるまで、Win32 の正規化で変わる名前と長いパスを開かないことを確かめる（filer §7）。
func TestCanOpenWindows(t *testing.T) {
	t.Parallel()
	for _, p := range []string{`C:\x\report.docx`, `C:\x y\a b.txt`, `D:\data\.hidden`} {
		if !CanOpen(p) {
			t.Errorf("CanOpen(%s) = false", p)
		}
	}
	long := `C:\` + strings.Repeat("a", 300) + `\f.txt`
	for _, p := range []string{`C:\x\foo.`, `C:\x\foo `, `C:\dir.\f.txt`, `C:\dir \f.txt`, `C:\x\CON`, `C:\x\nul.txt`, `C:\x\com1.log`, `C:\x\Lpt9`, long} {
		if CanOpen(p) {
			t.Errorf("CanOpen(%.60s) = true", p)
		}
	}
	if DotFilesHidden {
		t.Error("DotFilesHidden is true on Windows (Explorer shows names starting with a dot)")
	}
}

// TestVU10ShellExecute は、ShellExecute に Win32 の正規化で変わる名前と長いパスを渡したとき、どのファイルが開かれるかを記録する（filer §12 の VU10）。
// プログラムを実際に起動するので、TANA_VU10=1 のときだけ、仮想マシンで動かす。結果は t.Log に出す。
// 同じフォルダに probe.cmd（A を書く）と、\\?\ で作った probe.cmd.（B を書く）などを置き、開いた後にどちらの印が書かれたかを見る。
func TestVU10ShellExecute(t *testing.T) {
	if os.Getenv("TANA_VU10") != "1" {
		t.Skip("set TANA_VU10=1 to run the VU10 probe (it starts programs; run it in the VM)")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vu10Probe(t, dir)
}

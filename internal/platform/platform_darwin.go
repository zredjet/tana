package platform

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DotFilesHidden は、名前が . で始まるものを隠しファイルとして扱うか（filer §6）。
const DotFilesHidden = true

// IsExecutable は、開くと実行されるものか（開く前に確認を出す。filer §7）を返す。
// .app（フォルダ）、.command・.tool と、実行属性の付いたファイル（リンクは辿る）。
func IsExecutable(path string, isDir bool) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if isDir {
		return ext == ".app"
	}
	if ext == ".command" || ext == ".tool" {
		return true
	}
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// CanOpen は、Open に渡してよいパスかを返す。macOS は名前を変換しないので、すべて開ける。
func CanOpen(path string) bool { return true }

// Open は、path を関連付けられたアプリで開く（open コマンド）。open が終わるまで待つ（アプリの終了は待たない）。
// 端末を子プロセスに渡さない（標準入出力は /dev/null。エラーの出力はエラーに含める）。
func Open(path string) error {
	cmd := exec.Command("open", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("open %s: %w: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

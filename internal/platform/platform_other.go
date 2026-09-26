//go:build !windows && !darwin

package platform

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// DotFilesHidden は、名前が . で始まるものを隠しファイルとして扱うか（filer §6）。
const DotFilesHidden = true

// IsExecutable は、開くと実行されるものか（開く前に確認を出す。filer §7）を返す。実行属性の付いたファイル（リンクは辿る）。
func IsExecutable(path string, isDir bool) bool {
	if isDir {
		return false
	}
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

// CanOpen は、Open に渡してよいパスかを返す。
func CanOpen(path string) bool { return true }

// Open は、path を関連付けられたアプリで開く（xdg-open）。Linux はコンパイルが通ることだけを保証する（filer §3）。
func Open(path string) error {
	cmd := exec.Command("xdg-open", path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("xdg-open %s: %w: %s", path, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

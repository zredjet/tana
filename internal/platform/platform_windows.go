package platform

import (
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// DotFilesHidden は、名前が . で始まるものを隠しファイルとして扱うか（filer §6）。Windows はエクスプローラーと同じく隠さない。
const DotFilesHidden = false

// executableExts は、開くと実行される拡張子（filer §7）。
var executableExts = []string{
	".exe", ".com", ".bat", ".cmd", ".ps1", ".vbs", ".vbe", ".js", ".jse", ".wsf", ".wsh",
	".msi", ".msp", ".msc", ".scr", ".lnk", ".pif", ".cpl", ".hta", ".reg", ".jar", ".url",
	".appref-ms", ".application",
}

// IsExecutable は、開くと実行されるものか（開く前に確認を出す。filer §7）を、拡張子で返す。大文字小文字は区別しない。
func IsExecutable(path string, isDir bool) bool {
	return !isDir && slices.Contains(executableExts, strings.ToLower(filepath.Ext(path)))
}

// reservedNames は、Win32 がデバイスとして扱う名前（拡張子の前の部分で比べる）。
var reservedNames = []string{"CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$",
	"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "COM¹", "COM²", "COM³",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9", "LPT¹", "LPT²", "LPT³"}

// CanOpen は、ShellExecute に渡してよいパスかを返す（filer §7）。
// VU10 を確かめるまでは、Win32 の正規化で別のものを指しうるパス（末尾が . や空白の要素、予約名）と、
// 260 文字（UTF-16）以上のパスを開かない。SHFileOperation では、foo. が隣の foo を指した（fsops の V4）。
func CanOpen(path string) bool {
	if len(utf16.Encode([]rune(path))) >= windows.MAX_PATH {
		return false
	}
	rest := path[len(filepath.VolumeName(path)):]
	for part := range strings.SplitSeq(rest, `\`) {
		if part == "" {
			continue
		}
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		stem, _, _ := strings.Cut(part, ".")
		stem = strings.TrimRight(stem, " ")
		for _, r := range reservedNames {
			if strings.EqualFold(stem, r) {
				return false
			}
		}
	}
	return true
}

// Open は、path を関連付けられたアプリで開く（ShellExecute の open）。作業フォルダは path のフォルダ。
// ShellExecute はシェルの拡張を呼びうるので、OS のスレッドを固定して COM を初期化してから呼ぶ（ShellExecute の文書の推奨。VU10）。
func Open(path string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// S_FALSE（すでに初期化されている）でも、対になる CoUninitialize が要る。RPC_E_CHANGED_MODE なら初期化されていない。
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err == nil || err == windows.Errno(1) {
		defer windows.CoUninitialize()
	}
	return shellExecute(path)
}

// shellExecute は、ShellExecute で path を開く。
func shellExecute(path string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("ShellExecute %s: %w", path, err)
	}
	dir, err := windows.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("ShellExecute %s: %w", path, err)
	}
	if err := windows.ShellExecute(0, verb, file, nil, dir, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("ShellExecute %s: %w", path, err)
	}
	return nil
}

package probe

// CI が用意する特別なボリューム・設定のフォルダを指す環境変数（SPEC §20 の V12・V13・V14。フェーズ3で承認済み）。
// 未設定ならそれを使うプローブは t.Skip する。
const (
	envExFAT      = "FSOPS_PROBE_EXFAT_DIR"       // exFAT のボリューム（Windows: VHD、macOS: hdiutil のイメージ）
	envFAT32      = "FSOPS_PROBE_FAT32_DIR"       // FAT32 のボリューム（Windows: VHD、macOS: hdiutil のイメージ、Linux: loop マウントした vfat）
	envTrashNuke  = "FSOPS_PROBE_TRASH_NUKE_DIR"  // Windows: ごみ箱を「すぐに削除する」設定にした NTFS のボリューム
	envTrashSmall = "FSOPS_PROBE_TRASH_SMALL_DIR" // Windows: ごみ箱の最大サイズを 1 MB にした NTFS のボリューム
)

// errString は、ログ用にエラーを文字列にする（nil は "ok"）。
func errString(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

// sanitize は、ラベルを小文字の英数字と - だけのフォルダ名にする。
func sanitize(s string) string {
	b := []byte(s)
	for i, c := range b {
		if !('a' <= c && c <= 'z' || '0' <= c && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

package fsops

import "os"

// readExtra は、Linux では何もしない（安全上保持するメタデータは Windows と macOS だけ。§15）。
func readExtra(*os.File, string) ([]byte, error) { return nil, nil }

// setExtraFd は、Linux では何もしない。
func setExtraFd(int, []byte) error { return nil }

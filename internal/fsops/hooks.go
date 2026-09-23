package fsops

// testHooks は障害の注入などに使うテスト用のフック（SPEC §5、§18.1）。
// ExecOptions の非公開フィールドで渡すため、パッケージ内のテストからだけ設定できる。
// nil のとき、または各フィールドが nil のときは何もしない。フックは Execute を実行している goroutine から呼ぶ。
type testHooks struct {
	// beforeEnterDir は、削除・マージ移動の走査（§13.1）とコピー（§10.2）で、エントリをフォルダと判定した後、そのフォルダを開く前に呼ばれる。
	// フォルダをリンクに置き換える注入（§18.4 の I4）に使う。path は \\?\ の付かない形（コピーではコピー元のパス）。
	beforeEnterDir func(path string)
	// beforeRemove は、削除（§13.2、§13.3）で各エントリを削除する直前に呼ばれる。
	// 置き換えやキャンセルの注入に使う。path は \\?\ の付かない形。
	beforeRemove func(path string)
	// onWrite は、コピー（§10.1）で一時ファイルにバッファ 1 つ分を書き込むたびに呼ばれる。
	// dst は計画時のコピー先のパス（自動リネームでも元の名前のまま）、written はそれまでに書き込んだバイト数。error を返すと、書き込みの障害として扱う（I3 の注入用）。
	onWrite func(dst string, written int64) error
	// beforeFinalRename は、一時ファイルを最終名にする直前に、その名前（自動リネームでは試す候補ごと）で呼ばれる（計画後に現れた衝突の注入用。I1）。
	beforeFinalRename func(dst string)
}

func (h *testHooks) enterDir(path string) {
	if h != nil && h.beforeEnterDir != nil {
		h.beforeEnterDir(path)
	}
}

func (h *testHooks) remove(path string) {
	if h != nil && h.beforeRemove != nil {
		h.beforeRemove(path)
	}
}

func (h *testHooks) write(dst string, written int64) error {
	if h != nil && h.onWrite != nil {
		return h.onWrite(dst, written)
	}
	return nil
}

func (h *testHooks) finalRename(dst string) {
	if h != nil && h.beforeFinalRename != nil {
		h.beforeFinalRename(dst)
	}
}

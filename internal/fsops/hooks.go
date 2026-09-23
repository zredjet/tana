package fsops

// testHooks は障害の注入などに使うテスト用のフック（SPEC §5、§18.1）。
// ExecOptions の非公開フィールドで渡すため、パッケージ内のテストからだけ設定できる。
// nil のとき、または各フィールドが nil のときは何もしない。フックは Execute を実行している goroutine から呼ぶ。
type testHooks struct {
	// beforeEnterDir は、削除・マージ移動の走査（§13.1）で、エントリをフォルダと判定した後、そのフォルダを開く前に呼ばれる。
	// フォルダをリンクに置き換える注入（§18.4 の I4）に使う。path は \\?\ の付かない形。
	beforeEnterDir func(path string)
	// beforeRemove は、削除（§13.2、§13.3）で各エントリを削除する直前に呼ばれる。
	// 置き換えやキャンセルの注入に使う。path は \\?\ の付かない形。
	beforeRemove func(path string)
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

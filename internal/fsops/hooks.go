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
	// beforeSymlink は、コピーでシンボリックリンクを作る直前に呼ばれる（§14.2）。error を返すと、作らずにそのエラーで失敗させる
	// （権限不足の注入用。CI のランナーは昇格済みで再現できないため。V8）。link は作るリンクのパス。
	beforeSymlink func(link string) error
	// beforeVerify は、コピーの検証（§10.4）の直前に、書き終えて閉じた一時ファイルのパスで呼ばれる（書き込みの破損の注入用）。
	beforeVerify func(tmp string)
	// beforeRemoveSource は、ボリュームをまたぐ移動（§11.2）で、コピーと同期・検証が済み、移動元の削除（§13.3）を始める直前に呼ばれる。
	// src はトップレベルの項目の移動元のパス。移動元への追加・書き換え・ロックの注入（§18.4 の I2）に使う。
	beforeRemoveSource func(src string)
	// beforeMoveRename は、同一ボリュームの移動（§11.1）でリネームする直前に呼ばれる。error を返すと、リネームせずにそのエラーで失敗させる
	// （ボリューム違いのエラーの注入用。§11.1 の §11.2 への切り替えを確かめるため）。
	beforeMoveRename func(src, dst string) error
	// beforeSyncDir は、フォルダの同期（§10.5）の直前に呼ばれる。error を返すと、同期せずにそのエラーで失敗させる
	// （Unix のフォルダの同期の失敗の注入用）。
	beforeSyncDir func(dir string) error
	// bypassTrashPrecheck は、実行時のごみ箱の事前確認（§12.2）を飛ばす。事前確認が見落とした場合の二つ目の防御
	// （Windows の PreDeleteItem での中止。§12.2 の手順 4）を確かめるためだけに使う。
	bypassTrashPrecheck bool
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

func (h *testHooks) symlink(link string) error {
	if h != nil && h.beforeSymlink != nil {
		return h.beforeSymlink(link)
	}
	return nil
}

func (h *testHooks) verify(tmp string) {
	if h != nil && h.beforeVerify != nil {
		h.beforeVerify(tmp)
	}
}

func (h *testHooks) removeSource(src string) {
	if h != nil && h.beforeRemoveSource != nil {
		h.beforeRemoveSource(src)
	}
}

func (h *testHooks) moveRename(src, dst string) error {
	if h != nil && h.beforeMoveRename != nil {
		return h.beforeMoveRename(src, dst)
	}
	return nil
}

func (h *testHooks) syncDir(dir string) error {
	if h != nil && h.beforeSyncDir != nil {
		return h.beforeSyncDir(dir)
	}
	return nil
}

func (h *testHooks) trashPrecheck() bool { return h == nil || !h.bypassTrashPrecheck }

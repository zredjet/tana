package fsops

import "time"

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
	// beforeOpenDest は、コピー・マージ移動で、書き込み先のフォルダを作った（マージでは照合した）後、それを開いて確かめる直前に、
	// そのパスで呼ばれる（書き込み先のフォルダをリンクへ置き換える注入用。総点検の穴 4）。
	beforeOpenDest func(dst string)
	// beforeDirMeta は、コピーで作ったフォルダのメタデータ（§15）を設定する直前に、そのパスで呼ばれる（フォルダの置き換えの注入用。総点検の穴 6）。
	beforeDirMeta func(dst string)
	// trashCall は、ごみ箱へ移す呼び出し（§12.2〜§12.4 の trashSys）の代わりに呼ばれる。ごみ箱へ移す操作が成功を返したのに
	// 元の場所に残っている場合（§12.1）の注入用で、本物のごみ箱には触れない（総点検の穴 11）。
	trashCall func(src string, info EntryInfo) (string, error)
	// dirMetaFault は、コピー元のフォルダのメタデータを読む直前に呼ばれる。error を返すと、読めなかったものとして扱う（総点検の穴 6）。
	dirMetaFault func(src string) error
	// lockFault は、§17.1 のやり直しの対象の操作を試すたびに、操作の種類 op（"open"・"verify"・"rename"・"remove"・"unlink-temp"）と
	// パスで呼ばれる。真を返すと、その回は操作をせずに使用中（KindLocked）で失敗させる。注入した失敗はどの OS でもやり直しの対象になる。
	lockFault func(op, path string) bool
	// lockWait は、§17.1 のやり直しの待ちの代わりに、待つ長さ d で呼ばれる（実際には待たない）。キャンセルや置き換えの注入にも使う。
	lockWait func(path string, d time.Duration)
	// fileSizeLimit は、0 より大きければ、実行時のコピー先のファイルの大きさの上限（§10.6）をこの値に置き換える
	// （FAT32 の上限を、小さなファイルで確かめるため）。計画時の警告（§6.4）には使わない。
	fileSizeLimit int64
	// beforeZoneWrite は、Windows でメタデータの設定（§15）の照合の後、Zone.Identifier を書く直前に、そのファイルのパスで呼ばれる
	// （照合の後に名前をリンクへ置き換える注入用。I4）。
	beforeZoneWrite func(path string)
	// beforeSyncFile は、一時ファイルの同期（§10.5）の直前に、そのパスで呼ばれる。error を返すと、同期せずにそのエラーで失敗させる
	// （ファイルの同期の失敗の注入用。I2）。
	beforeSyncFile func(tmp string) error
	// caseRenameNoop は、Rename の大文字小文字・正規化だけの変更で、1 回目の変更をせずに成功したものとして扱う
	// （成功を返しても名前が変わらない Windows の exFAT・FAT32（V1）を再現し、§11.3 の 2 段階の変更を通すため）。
	caseRenameNoop bool
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

func (h *testHooks) openDest(dst string) {
	if h != nil && h.beforeOpenDest != nil {
		h.beforeOpenDest(dst)
	}
}

func (h *testHooks) dirMeta(dst string) {
	if h != nil && h.beforeDirMeta != nil {
		h.beforeDirMeta(dst)
	}
}

func (h *testHooks) dirMetaRead(src string) error {
	if h != nil && h.dirMetaFault != nil {
		return h.dirMetaFault(src)
	}
	return nil
}

func (h *testHooks) trash(src string, info EntryInfo) (string, error) {
	if h != nil && h.trashCall != nil {
		return h.trashCall(src, info)
	}
	return trashSys(src, info)
}

// lockFaultErr は、lockFault が真を返せば、注入した使用中の失敗を返す。
func (h *testHooks) lockFaultErr(op, path string) error {
	if h != nil && h.lockFault != nil && h.lockFault(op, path) {
		return &OpError{Op: op, Path: path, Kind: KindLocked, Err: errInjectedLock}
	}
	return nil
}

// waitLock は、lockWait があればそれを呼んで真を返す（実際には待たない）。
func (h *testHooks) waitLock(path string, d time.Duration) bool {
	if h != nil && h.lockWait != nil {
		h.lockWait(path, d)
		return true
	}
	return false
}

func (h *testHooks) zoneWrite(path string) {
	if h != nil && h.beforeZoneWrite != nil {
		h.beforeZoneWrite(path)
	}
}

func (h *testHooks) syncFile(tmp string) error {
	if h != nil && h.beforeSyncFile != nil {
		return h.beforeSyncFile(tmp)
	}
	return nil
}

func (h *testHooks) caseRenameNoopSet() bool { return h != nil && h.caseRenameNoop }

// Package tui は、ファイラーの描画とキー入力（docs/SPEC-filer.md §4・§10）。
//
// フェーズ16の時点では土台だけを持つ: term・keys・screen を組み合わせたイベントループ（Loop）と、panic とシグナルからの復元。
// 画面の状態を持つ app と、その描画は、フェーズ18で加える。
//
// # API
//
//	l := tui.New(t)                 // t は *term.Term（テストでは偽物）
//	err := l.Run(h)                 // h.Handle でイベントを受け、h.Draw で描く。どの終わり方でも端末を戻してから返る
//	l.Go(f)                         // 作業用の goroutine。panic は回収して Run を PanicError で終える
//	l.Post(msg)                     // ほかの goroutine から、イベントループにメッセージを送る（待たない）
//	l.Wake()                        // ほかの goroutine から知らせる（進捗など。待たない）
//
// イベントは、キー（keys のイベント。貼り付けとカーソル位置の報告を含む）、大きさの変更、メッセージ、知らせ、シグナル。
// Run は、たまっているイベントをすべて処理してから 1 回だけ描く（入力が続いても描画が遅れない）。
// 描く前に画面を空白に戻すので、Draw は毎回、状態から画面の全体を描く。差分だけを端末に書くのは screen が行う。
//
// # 使い方
//
//	t, err := term.Open(term.Options{AltScreen: true, HideCursor: true, NoAutoWrap: true, BracketedPaste: true, VTInput: true, Signals: true})
//	if err != nil { ... }
//	defer t.Restore()
//	err = tui.New(t).Run(h)
//	var pe *tui.PanicError
//	if errors.As(err, &pe) { fmt.Fprint(os.Stderr, pe); os.Exit(2) } // 端末を戻した後で、panic とスタックを出す
//
// # 担う不変条件
//
// T1（端末を必ず元に戻す）: Run は、ハンドラが終える、シグナル、読み取りの失敗、書き込みの失敗、
// Handle・Draw の panic、Go で始めた goroutine の panic の、どの場合も端末を戻してから返る。
// panic はプロセスを止めずに回収し、PanicError として返す（戻す前に panic がプロセスを終わらせないように。filer §10）。
// シグナルは、ハンドラに知らせてから（実行中の操作の中止など）、SignalError を返して終わる。
// Go を通さずに始めた goroutine の panic と、強制終了では戻せない（tui §2）。
//
// T3（入力を失わず、作らない）: 端末からの入力は、届いた順にすべて keys に渡し、keys のイベントはすべてハンドラに渡す。
// Esc の待ち時間は入力が届いた時刻から数えるので、ループが読むのが遅れても、Esc と次のキーの区切りは変わらない。
package tui

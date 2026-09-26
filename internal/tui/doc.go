// Package tui は、ファイラーの描画とキー入力（docs/SPEC-filer.md §4・§10）。
//
// 2 つの部分からなる。
//   - 土台: term・keys・screen を組み合わせたイベントループ（Loop）と、panic とシグナルからの復元（フェーズ16）。
//   - ファイラーのメイン画面（Filer。フェーズ18）: app の状態を描き、キー入力を app の操作（app.Action）に変える。
//     画面の状態は app が持ち、Filer は持たない（filer §4）。app の Cmd は Loop.Go で動かし、結果を Post で app に返す（filer §10）。
//     ペインを横に並べる構成とキーの割り当ては、filer §15 で決めるまでの案。変えるときは Filer だけを変える。
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
// Run は、たまっているイベントをまとめて処理してから 1 回だけ描く（入力が続いても描画が遅れない）。
// イベントが途切れずに届き続けるときも、50 ミリ秒ごとには描く（描画が止まらない。filer U5）。
// 描く前に画面を空白に戻すので、Draw は毎回、状態から画面の全体を描く。差分だけを端末に書くのは screen が行う。
//
// # 使い方
//
//	t, err := term.Open(term.Options{AltScreen: true, HideCursor: true, NoAutoWrap: true, BracketedPaste: true, VTInput: true, Signals: true})
//	if err != nil { ... }
//	defer t.Restore()
//	err = tui.New(t).Run(h)          // ファイラーは tui.RunFiler(l, a, cmds)（cmd/tana）
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
// Run が終わった後に Go で始めた goroutine が panic したら、端末はもう戻っているので、そのまま panic させる（隠さない）。
//
// T3（入力を失わず、作らない）: 端末からの入力は、届いた順にすべて keys に渡し、keys のイベントはすべてハンドラに渡す。
// Esc の待ち時間は入力が届いた時刻から数えるので、ループが読むのが遅れても、Esc と次のキーの区切りは変わらない。
package tui

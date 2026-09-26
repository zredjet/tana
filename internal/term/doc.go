// Package term は、端末の入出力を OS ごとに扱う（docs/SPEC-tui.md §8）。OS ごとのコードは、このパッケージに閉じる。
//
// # API
//
//	t, err := term.Open(opts)       // raw モードにして、opts の状態（代替画面など）に変える
//	t.Write(p)                      // そのまま端末に書く（Windows は UTF-16 にして WriteConsoleW）
//	t.Size()                        // 表示の大きさ（桁数、行数）
//	in := t.StartInput()            // 別の goroutine で読み始める。入力・大きさの変更・シグナルが届く
//	t.SetBracketedPaste(on)         // bracketed paste の切り替え
//	t.QueryCursorPosition()         // カーソル位置の問い合わせ（応答は入力として届く）
//	t.Restore()                     // Open の前の状態に戻す（何度でも、どの goroutine からでも）
//	t.Info()                        // 記録用の端末と OS の情報
//
// Input は、1 回の読み取り（Bytes は keys に渡すバイト列。Windows はレコードの文字を UTF-8 にしたもの）、
// 大きさの変更（Resize）、シグナル（Signal。Options.Signals のとき）、読み取りの失敗（Err）を知らせる。
// Windows では、1 回の読み取りに大きさの変更のレコードとキーのレコードが一緒に入るので、Resize と Bytes が同時にありうる。
// Resize を見たときも Bytes を捨てないこと（T3）。
//
// # 使い方
//
//	t, err := term.Open(term.Options{AltScreen: true, HideCursor: true, NoAutoWrap: true, BracketedPaste: true, VTInput: true, Signals: true})
//	if err != nil { ... }
//	defer t.Restore()               // panic のときも戻す（作業用の goroutine の panic は、回収して呼び出し側に渡すこと）
//	for x := range t.StartInput() {
//		switch {
//		case x.Err != nil:    // 読み取りに失敗した
//		case x.Signal != nil: // 終わる（Restore を呼ぶ）
//		default:
//			if x.Resize { ... }      // t.Size() を読み直して描き直す
//			if len(x.Bytes) > 0 { ... } // x.Bytes を keys.Decoder に渡す（Resize と一緒に届くことがある）
//		}
//	}
//
// # 担う不変条件
//
// T1（端末を必ず元に戻す）: Restore は、Open で変えたもの（代替画面、カーソル、自動改行、bracketed paste、
// raw モード、Windows のコンソールモードとコードページ）を戻す。何度呼んでも、どの goroutine から呼んでもよい。
// 読み取りの goroutine を止め、シグナルを受け取るのをやめてから戻すので、戻した後の端末から読み取らない。
// シグナルは入力のチャネルに届くので、受けたら Restore を呼ぶ（Windows のコンソールを閉じる通知は、約 5 秒で強制終了される）。
// panic のときに Restore を呼ぶのは呼び出し側の役目（tui が行う）。
//
// T3（入力を失わず、作らない）: Windows の入力のレコードをバイト列にするとき、文字を持つレコードは必ずバイト列に含める
// （キーを押したレコードの文字と、conhost が Alt＋テンキーの並びの最後に送る Alt を離したレコードの文字。
// サロゲートの対は読み取りをまたいでも組み立て、対にならない半分は U+FFFD）。文字を持たないレコードは入力を表さないので捨てる。
//
// # OS ごとの方法
//
// Unix（macOS・Linux）: /dev/tty を開き、termios を cfmakeraw(3) と同じ設定にする。
// 読み取りは select で端末とパイプを待ち、Restore がパイプに書いて起こす（macOS の poll は端末のデバイスに使えない）。
// 大きさの変更は SIGWINCH で知る。
//
// Windows: CONIN$ と CONOUT$ を開く。入力は ENABLE_PROCESSED_INPUT・行の入力・エコー・マウス・簡易編集モードを外し、
// ENABLE_WINDOW_INPUT を付ける。Options.VTInput で ENABLE_VIRTUAL_TERMINAL_INPUT を付ける（VT2）。
// 出力は ENABLE_VIRTUAL_TERMINAL_PROCESSING と DISABLE_NEWLINE_AUTO_RETURN を付ける。
// 書き方は Options.Output で選ぶ（VT1）。読み取りは、入力とイベントを WaitForMultipleObjects で待ち、
// Restore がイベントを設定して起こす。大きさの変更は、大きさの変更のレコード（WINDOW_BUFFER_SIZE_EVENT）で知る（VT4）。
//
// # テスト
//
// Unix は疑似端末（/dev/ptmx）、Windows は疑似コンソール（CreatePseudoConsole）の中で動かして確かめる（VT3）。
package term

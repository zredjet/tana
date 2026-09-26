// Package term は、端末の入出力を OS ごとに扱う（docs/SPEC-tui.md §8）。OS ごとのコードは、このパッケージに閉じる。
//
// フェーズ12の時点では最小部分だけを持つ: raw モードとその復元、Windows のコンソールモード、
// 入力の生の読み取り（Unix はバイト列、Windows は ReadConsoleInputW のレコード）、大きさの取得、出力、
// カーソル位置の問い合わせ。シグナルと大きさの変更の通知は、フェーズ16で加える。
//
// # 使い方
//
//	t, err := term.Open(term.Options{AltScreen: true, HideCursor: true, NoAutoWrap: true})
//	if err != nil { ... }
//	defer t.Restore()          // シグナルを受けたときも Restore を呼ぶ
//	in := t.StartInput()       // 別の goroutine で読み始める
//	t.Write([]byte("..."))
//	for x := range in { ... }  // x.Bytes（Unix）か x.Records（Windows）
//
// # 担う不変条件
//
// T1（端末を必ず元に戻す）: Restore は、Open で変えたもの（代替画面、カーソル、自動改行、bracketed paste、
// raw モード、Windows のコンソールモードとコードページ）を戻す。何度呼んでも、どの goroutine から呼んでもよい。
// 読み取りの goroutine を止めてから戻すので、戻した後の端末から読み取らない。
// panic とシグナルのときに Restore を呼ぶのは呼び出し側の役目。
//
// # OS ごとの方法
//
// Unix（macOS・Linux）: /dev/tty を開き、termios を cfmakeraw(3) と同じ設定にする。
// 読み取りは select で端末とパイプを待ち、Restore がパイプに書いて起こす（macOS の poll は端末のデバイスに使えない）。
//
// Windows: CONIN$ と CONOUT$ を開く。入力は ENABLE_PROCESSED_INPUT・行の入力・エコー・マウス・簡易編集モードを外し、
// ENABLE_WINDOW_INPUT を付ける。Options.VTInput で ENABLE_VIRTUAL_TERMINAL_INPUT を付ける（VT2）。
// 出力は ENABLE_VIRTUAL_TERMINAL_PROCESSING と DISABLE_NEWLINE_AUTO_RETURN を付ける。
// 書き方は Options.Output で選ぶ（VT1）。読み取りは、入力とイベントを WaitForMultipleObjects で待ち、
// Restore がイベントを設定して起こす。
package term

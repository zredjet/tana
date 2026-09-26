// Package lineedit は、1 行の入力欄の状態と編集の操作を持つ（docs/SPEC-tui.md §7）。
//
// # API
//
//	e := lineedit.New(text, cursor)            // cursor はバイトの位置。書記素クラスタの途中なら、その終わりに動かす
//	e.Text() / e.Cursor()                      // 文字列（元のバイト列）とカーソルのバイトの位置
//	e.Insert(s)                                // カーソルの位置に s を入れる（改行と端末に出してはいけない文字は取り除く）
//	e.DeleteBackward() / e.DeleteForward()     // カーソルの前・後ろの書記素クラスタを消す
//	e.Left() / e.Right() / e.Home() / e.End()  // カーソルを書記素クラスタの単位で動かす
//	v := e.View(width)                         // 幅 width の欄に表示する範囲 Text()[v.Start:v.End] と、カーソルの桁 v.CursorCol
//	e.Changed()                                // 文字列を変えた操作があったか（文字列の比較ではない）
//
// View は、カーソルのための 1 桁を残して横に動かす。幅は textwidth の表示する形の幅で数え、右端にかかる幅 2 以上の書記素クラスタは含めない。
//
// # 使い方
//
//	e := lineedit.New(name, len(name)-len(ext)) // 名前の変更: カーソルは拡張子の前（filer §8.7）
//	// 文字のキー・貼り付けは e.Insert、Backspace・Delete は e.DeleteBackward・e.DeleteForward
//	v := e.View(w)
//	s.Put(field, 0, 0, e.Text()[v.Start:v.End], st) // screen が表示する形に置き換える
//	s.SetCursor(field.X+v.CursorCol, field.Y, true)
//	if e.Changed() { /* 名前を変える */ }
//
// 文字列は元のバイト列のまま持つ。表示用の置き換え（? や絵文字の簡略な形）は、screen が描くときに行う（filer U4）。
// カーソルは、書記素クラスタ（利用者から見た 1 文字）の単位で動き、常に境界にある。区切りと幅は textwidth で得る（T4）。
//
// # 担う不変条件
//
// lineedit は tui の T1〜T6 を直接は担わない。filer の U4 を支える: 表示用に加工した文字列を持たず、
// 変更したかどうかを操作で判断する（名前を変えずに Enter を押しても、表示の違いで名前が変わらない）。
package lineedit

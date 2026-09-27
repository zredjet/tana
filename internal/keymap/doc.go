// Package keymap は、キーの割り当ての表と、キー入力から操作への解決、キーの案内とヘルプの生成を持つ（filer §4 の「UI の骨格」）。
//
//   - Commands: 割り当てられる操作と、その安定した ID（例: paste-copy、decide-all:overwrite）。
//   - Maps: 役割（app.Role）ごとの表。Binding は Keys のどれを押しても同じ操作になり、先頭のキーを案内に出す。
//     Spec の ModPolicy で、修飾キーの一致の仕方を決める。入力欄の役割は Text で、文字のキーと貼り付けの入れ方を決める。
//   - Resolver.Lookup: フォーカスの道筋（app.FocusPath）の内側の役割から表を引き、解決した役割を Action.Role に入れる。
//     貼り付けは、入力欄の役割で文字として入れるほかは、何にもならない（tui §5。filer U2）。
//   - Guide・HelpRows: キーの案内とヘルプ。キーの名前は表から作る。
//
// 設定ファイルでの割り当ての変更（後の版。filer §14）は、この表を読み込みで作れば済む形にしてある。
package keymap

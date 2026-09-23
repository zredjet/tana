package fsops

// testHooks は障害の注入などに使うテスト用のフック（SPEC §5、§18.1）。
// ExecOptions の非公開フィールドで渡すため、パッケージ内のテストからだけ設定できる。
// nil のときは何もしない。フックは必要になったフェーズで追加する。
type testHooks struct{}

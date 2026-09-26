// gen は、internal/textwidth/ucd の Unicode のデータから internal/textwidth/tables.go を作る。
// internal/textwidth で go generate を実行すると呼ばれる。
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/zredjet/tana/internal/textwidth/internal/ucdgen"
)

func main() {
	ucd := flag.String("ucd", "ucd", "UCD のファイルのフォルダ")
	out := flag.String("o", "tables.go", "出力するファイル")
	flag.Parse()
	src, err := ucdgen.Generate(*ucd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

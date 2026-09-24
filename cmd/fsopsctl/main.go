// fsopsctl は、internal/fsops の動作を確かめるための CLI。
//
//	fsopsctl plan <copy|move|trash|delete> [-dest DIR] SRC...   計画と衝突の一覧を表示する（何も変更しない）
//	fsopsctl copy -dest DIR [-on-conflict=skip|overwrite|rename|merge] [-links=keep|skip] [-verify=size|hash] [-sync] SRC...
//	fsopsctl move -dest DIR [-on-conflict=skip|overwrite|rename|merge] SRC...
//	fsopsctl trash SRC...
//	fsopsctl delete -yes SRC...
//
// 進捗は標準エラー出力、結果の一覧は標準出力に出す。表示は日本語。
// 終了コード: 0 すべて完了、1 一部にエラーあり、2 使い方の誤り・実行しなかった、130 キャンセル（Ctrl-C）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/zredjet/tana/internal/fsops"
)

const (
	exitOK       = 0
	exitErrors   = 1
	exitUsage    = 2
	exitCanceled = 130
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	go func() {
		// 1 回目の Ctrl-C でキャンセルした後は、既定の動作に戻して 2 回目の Ctrl-C でプロセスを終わらせる
		// （Windows の確認ダイアログなど、ctx では中断できない待ちから抜けられるように）。
		<-ctx.Done()
		stop()
	}()
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

const usageText = `使い方:
  fsopsctl plan <copy|move|trash|delete> [-dest フォルダ] 対象...   計画と衝突の一覧を表示する（何も変更しない）
  fsopsctl copy -dest フォルダ [-on-conflict=skip|overwrite|rename|merge] [-links=keep|skip] [-verify=size|hash] [-sync] 対象...
  fsopsctl move -dest フォルダ [-on-conflict=skip|overwrite|rename|merge] 対象...
  fsopsctl trash 対象...
  fsopsctl delete -yes 対象...                                       完全削除（取り消せない）

衝突は、-on-conflict を指定しなければすべてスキップする。
その種類の衝突に使えない決定（フォルダ同士の上書きなど）になる衝突は、スキップのままにする。
`

// run は CLI の本体。args はコマンド名を除いた引数。終了コードを返す。
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usageText)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "plan":
		if len(rest) == 0 {
			fmt.Fprint(stderr, usageText)
			return exitUsage
		}
		op, ok := opOf(rest[0])
		if !ok {
			fmt.Fprintf(stderr, "plan の後には copy・move・trash・delete のどれかを指定してください: %q\n", rest[0])
			return exitUsage
		}
		return runCommand(ctx, op, true, rest[1:], stdout, stderr)
	case "copy", "move", "trash", "delete":
		op, _ := opOf(cmd)
		return runCommand(ctx, op, false, rest, stdout, stderr)
	case "help", "-h", "-help", "--help":
		fmt.Fprint(stdout, usageText)
		return exitOK
	}
	fmt.Fprintf(stderr, "不明なコマンドです: %q\n\n%s", cmd, usageText)
	return exitUsage
}

func opOf(s string) (fsops.OpKind, bool) {
	switch s {
	case "copy":
		return fsops.OpCopy, true
	case "move":
		return fsops.OpMove, true
	case "trash":
		return fsops.OpTrash, true
	case "delete":
		return fsops.OpDelete, true
	}
	return 0, false
}

// options はコマンドのフラグ。
type options struct {
	dest       string
	onConflict string
	links      string
	verify     string
	sync       bool
	yes        bool
}

// runCommand は、op の計画を作り、planOnly でなければ実行する。
func runCommand(ctx context.Context, op fsops.OpKind, planOnly bool, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(opName(op), flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usageText) }
	var o options
	if op == fsops.OpCopy || op == fsops.OpMove {
		fs.StringVar(&o.dest, "dest", "", "コピー先・移動先のフォルダ")
		fs.StringVar(&o.onConflict, "on-conflict", "", "衝突の扱い（skip|overwrite|rename|merge）")
	}
	if op == fsops.OpCopy {
		fs.StringVar(&o.links, "links", "keep", "シンボリックリンクの扱い（keep|skip）")
		fs.StringVar(&o.verify, "verify", "size", "検証（size|hash）")
		fs.BoolVar(&o.sync, "sync", false, "コピーでも同期する")
	}
	if op == fsops.OpDelete {
		fs.BoolVar(&o.yes, "yes", false, "完全削除を実行する（取り消せない）")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitUsage
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "対象を 1 つ以上指定してください。")
		return exitUsage
	}
	// flag は最初の対象で解析を止めるので、対象の後ろに書いたオプションは対象のパスになってしまう。
	// 取り違えて消したりしないよう、- で始まる対象は受け付けない（その名前のファイルは ./ を付けて指定する）。
	for _, a := range fs.Args() {
		if strings.HasPrefix(a, "-") {
			fmt.Fprintf(stderr, "オプションは対象より前に書いてください: %q（- で始まる名前のファイルは ./%s のように指定してください）\n", a, a)
			return exitUsage
		}
	}
	decision, ok := decisionOf(o.onConflict)
	if !ok {
		fmt.Fprintf(stderr, "-on-conflict には skip・overwrite・rename・merge のどれかを指定してください: %q\n", o.onConflict)
		return exitUsage
	}
	exec := fsops.ExecOptions{Sync: fsops.SyncMoveOnly}
	switch o.links {
	case "", "keep":
	case "skip":
		exec.Links = fsops.LinkSkip
	default:
		fmt.Fprintf(stderr, "-links には keep か skip を指定してください: %q\n", o.links)
		return exitUsage
	}
	switch o.verify {
	case "", "size":
	case "hash":
		exec.Verify = fsops.VerifyHash
	default:
		fmt.Fprintf(stderr, "-verify には size か hash を指定してください: %q\n", o.verify)
		return exitUsage
	}
	if o.sync {
		exec.Sync = fsops.SyncAlways
	}

	// fsops は絶対パスだけを受け付けるので、ここで絶対パスにする。
	req := fsops.Request{Op: op}
	for _, a := range fs.Args() {
		p, err := filepath.Abs(a)
		if err != nil {
			fmt.Fprintf(stderr, "パスを解決できません: %s: %v\n", a, err)
			return exitUsage
		}
		req.Sources = append(req.Sources, p)
	}
	if op == fsops.OpCopy || op == fsops.OpMove {
		if o.dest == "" {
			fmt.Fprintln(stderr, "-dest でコピー先・移動先のフォルダを指定してください。")
			return exitUsage
		}
		d, err := filepath.Abs(o.dest)
		if err != nil {
			fmt.Fprintf(stderr, "パスを解決できません: %s: %v\n", o.dest, err)
			return exitUsage
		}
		req.DestDir = d
	}

	plan, err := fsops.NewPlan(ctx, req)
	if err != nil {
		fmt.Fprintf(stderr, "計画を作れません: %s\n", errorText(err))
		if fsops.KindOf(err) == fsops.KindCanceled {
			return exitCanceled
		}
		return exitUsage
	}
	printPlan(stdout, plan)
	if planOnly {
		return exitOK
	}
	if op == fsops.OpTrash {
		for _, it := range plan.Items() {
			if fsops.KindOf(it.Err) == fsops.KindTrashUnavailable {
				fmt.Fprintf(stdout, "ごみ箱に入れられません（完全削除には切り替えません）: %s\n", it.Src)
			}
		}
	}
	if op == fsops.OpDelete && !o.yes {
		fmt.Fprintln(stderr, "完全削除は取り消せません。実行するには -yes を指定してください。何もしませんでした。")
		return exitUsage
	}
	applyDecision(stdout, plan, decision)

	progress, finish := progressPrinter(stderr)
	exec.Progress = progress
	res, err := plan.Execute(ctx, exec)
	finish()
	if err != nil {
		fmt.Fprintf(stderr, "実行できません: %s\n", errorText(err))
		return exitUsage
	}
	printResult(stdout, res)
	switch res.Status {
	case fsops.StatusCompleted:
		return exitOK
	case fsops.StatusCanceled:
		return exitCanceled
	}
	return exitErrors
}

func decisionOf(s string) (fsops.Decision, bool) {
	switch s {
	case "":
		return fsops.DecisionUnset, true
	case "skip":
		return fsops.DecisionSkip, true
	case "overwrite":
		return fsops.DecisionOverwrite, true
	case "rename":
		return fsops.DecisionAutoRename, true
	case "merge":
		return fsops.DecisionMerge, true
	}
	return 0, false
}

// applyDecision は、すべての衝突に d を設定する（Decide）。その種類の衝突に使えない決定になる衝突は、スキップのままにして表示する。
func applyDecision(w io.Writer, plan *fsops.Plan, d fsops.Decision) {
	cs := plan.Conflicts()
	if len(cs) == 0 {
		return
	}
	if d == fsops.DecisionUnset {
		fmt.Fprintf(w, "衝突 %d 件は、-on-conflict が指定されていないのでスキップします。\n", len(cs))
		return
	}
	for _, c := range cs {
		if err := plan.Decide(c.ID, d); err != nil {
			fmt.Fprintf(w, "衝突 #%d（%s → %s、%s）には「%s」を使えないので、スキップします。\n",
				c.ID, c.Src, c.Dst, conflictKindText(c), decisionText(d))
		}
	}
}

// progressPrinter は、進捗を表示する関数と、表示を終える関数を返す（fsops が 100 ミリ秒に 1 回までに間引く）。
// w が端末なら 1 行を上書きして表示し、そうでなければ（ファイルやログへのリダイレクト）1 行ずつ書き、制御文字を出さない。
func progressPrinter(w io.Writer) (progress func(fsops.Progress), finish func()) {
	tty := isTerminal(w)
	printed := false
	progress = func(p fsops.Progress) {
		line := fmt.Sprintf("[%s] %d/%d 件 %s/%s %s", stageText(p.Stage), p.DoneFiles, p.TotalFiles,
			sizeText(p.DoneBytes), sizeText(p.TotalBytes), shorten(p.Current, 60))
		if tty {
			fmt.Fprint(w, "\r"+line+"\x1b[K")
		} else {
			fmt.Fprintln(w, line)
		}
		printed = true
	}
	finish = func() {
		if tty && printed {
			fmt.Fprintln(w)
		}
	}
	return progress, finish
}

// isTerminal は、w が端末（キャラクタデバイス）かを返す。
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// shorten は、パスの末尾を n 文字（ルーン）までにする。
func shorten(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return "…" + string(r[len(r)-n+1:])
}

func printPlan(w io.Writer, plan *fsops.Plan) {
	req := plan.Request()
	fmt.Fprintf(w, "計画: %s %d 項目", opText(req.Op), len(req.Sources))
	if req.DestDir != "" {
		fmt.Fprintf(w, " → %s", req.DestDir)
	}
	fmt.Fprintln(w)
	for i, it := range plan.Items() {
		line := fmt.Sprintf("  [%d] %s", i+1, it.Src)
		if it.Dst != "" {
			line += " → " + it.Dst
		}
		if it.Err != nil {
			line += "  エラー: " + errorText(it.Err)
		} else {
			line += fmt.Sprintf("  （%s、%s）", typeText(it.Info.Type), methodText(it.Method))
		}
		fmt.Fprintln(w, line)
	}
	fmt.Fprintf(w, "合計: %d 件、%s\n", plan.TotalFiles(), sizeText(plan.TotalBytes()))
	for _, warn := range plan.Warnings() {
		fmt.Fprintf(w, "警告: %s\n", errorText(warn))
	}
	cs := plan.Conflicts()
	if len(cs) == 0 {
		return
	}
	fmt.Fprintf(w, "衝突 %d 件:\n", len(cs))
	for _, c := range cs {
		parent := ""
		if c.Parent != 0 {
			parent = fmt.Sprintf("（#%d の中）", c.Parent)
		}
		fmt.Fprintf(w, "  #%d%s %s → %s  %s  使える決定: %s\n", c.ID, parent, c.Src, c.Dst, conflictKindText(c), allowedText(plan, c))
	}
}

// allowedText は、衝突 c に使える決定を返す（§9.1）。
func allowedText(plan *fsops.Plan, c fsops.Conflict) string {
	var ds []string
	for _, d := range []fsops.Decision{fsops.DecisionSkip, fsops.DecisionOverwrite, fsops.DecisionAutoRename, fsops.DecisionMerge} {
		if allowed(c, d) {
			ds = append(ds, decisionText(d))
		}
	}
	return strings.Join(ds, "・")
}

// allowed は §9.1 の表。Plan.Decide で確かめると計画を変えてしまうので、表示のためにここで同じ判定をする。
func allowed(c fsops.Conflict, d fsops.Decision) bool {
	switch d {
	case fsops.DecisionSkip, fsops.DecisionAutoRename:
		return true
	case fsops.DecisionOverwrite:
		return !c.Self && c.SrcInfo.Type == fsops.TypeFile && c.DstInfo.Type == fsops.TypeFile
	case fsops.DecisionMerge:
		return !c.Self && c.SrcInfo.Type == fsops.TypeDir && c.DstInfo.Type == fsops.TypeDir
	}
	return false
}

func printResult(w io.Writer, res *fsops.Result) {
	fmt.Fprintf(w, "結果: %s\n", statusText(res.Status))
	for _, it := range res.Items {
		line := fmt.Sprintf("  [%s] %s", outcomeText(it.Outcome), it.Src)
		if it.Dst != "" {
			line += " → " + it.Dst
		}
		switch {
		case it.Err != nil:
			line += "  " + errorText(it.Err)
		case it.Outcome == fsops.OutcomeSkipped:
			line += "  （衝突の決定によるスキップ）"
		}
		fmt.Fprintln(w, line)
		if it.TrashedPath != "" {
			fmt.Fprintf(w, "      ごみ箱の中: %s\n", it.TrashedPath)
		}
		for _, d := range it.Details {
			dl := fmt.Sprintf("      [%s] %s", outcomeText(d.Outcome), d.Src)
			if d.Err != nil {
				dl += "  " + errorText(d.Err)
			} else if d.Outcome == fsops.OutcomeSkipped {
				dl += "  （衝突の決定によるスキップ）"
			}
			fmt.Fprintln(w, dl)
		}
		for _, warn := range it.Warnings {
			fmt.Fprintf(w, "      警告: %s\n", errorText(warn))
		}
	}
}

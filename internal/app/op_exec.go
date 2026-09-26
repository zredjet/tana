package app

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/zredjet/tana/internal/fsops"
	"github.com/zredjet/tana/internal/msg"
)

// ---- 実行と進捗（filer §8.4） ----

type executed struct {
	gen int
	res *fsops.Result
	err error
}

type opTick struct{ gen int }

// execute は、計画をその時点の決定で実行する処理を返す。Execute は作業用の goroutine で動く（filer §10）。
// 進捗は最新の値だけを progressSlot に置き、Wake でイベントループに知らせる（Execute を待たせない）。
func (a *App) execute() []Cmd {
	op := a.op
	a.show(ScreenProgress)
	ctx, cancel := context.WithCancel(context.Background())
	op.cancel = cancel
	op.slot = &progressSlot{}
	op.started = a.cfg.Now()
	op.done = make(chan struct{})
	plan, slot, wake, gen, done := op.plan, op.slot, a.cfg.Wake, op.gen, op.done
	run := func() any {
		defer close(done)
		res, err := plan.Execute(ctx, fsops.ExecOptions{Progress: func(p fsops.Progress) {
			slot.set(p)
			if wake != nil {
				wake()
			}
		}})
		return executed{gen: gen, res: res, err: err}
	}
	return []Cmd{{Run: run}, a.tick()}
}

func (a *App) tick() Cmd {
	gen := a.op.gen
	return Cmd{Delay: opTickInterval, Run: func() any { return opTick{gen: gen} }}
}

// SetWake は、実行中の進捗が届いたことをイベントループに知らせる関数を設定する（tui が Loop.Wake を渡す）。
func (a *App) SetWake(f func()) { a.cfg.Wake = f }

// Refresh は、最新の進捗を読む（tui が Wake の知らせを受けたときに呼ぶ）。
func (a *App) Refresh() {
	if op := a.op; op != nil && op.screen == ScreenProgress {
		op.progress = op.slot.get()
	}
}

func (a *App) opTick(m opTick) []Cmd {
	op := a.op
	if op == nil || op.gen != m.gen || op.screen != ScreenProgress {
		return nil
	}
	a.Refresh()
	if op.canceling && a.cfg.Now().Sub(op.cancelAt) >= unresponsiveWait {
		op.unresponsive = true // 中止しても Execute が戻らない（応答しないネットワークドライブなど。filer §8.4）
	}
	return []Cmd{a.tick()}
}

// ProgressView は、進捗の画面の内容。速度・残り時間・経過時間は UI が計算する（filer §8.4）。
type ProgressView struct {
	Op                    fsops.OpKind
	Stage                 fsops.Stage
	Current               string
	DoneFiles, TotalFiles int
	DoneBytes, TotalBytes int64
	Elapsed               time.Duration
	Speed                 float64       // 毎秒のバイト数（0 なら分からない）
	Remaining             time.Duration // 負なら分からない
	AskCancel, Canceling  bool
	Unresponsive          bool
}

// Progress は、進捗の画面の内容を返す（ScreenProgress のとき）。
func (a *App) Progress() ProgressView {
	op := a.op
	p := op.progress
	v := ProgressView{Op: op.req.Op, Stage: p.Stage, Current: p.Current, DoneFiles: p.DoneFiles, TotalFiles: p.TotalFiles,
		DoneBytes: p.DoneBytes, TotalBytes: p.TotalBytes, Elapsed: a.cfg.Now().Sub(op.started), Remaining: -1,
		AskCancel: op.askCancel, Canceling: op.canceling, Unresponsive: op.unresponsive}
	if v.Stage == 0 {
		v.Stage = fsops.StageCopy
		if op.req.Op == fsops.OpMove {
			v.Stage = fsops.StageMove
		}
	}
	if sec := v.Elapsed.Seconds(); sec >= 1 && p.DoneBytes > 0 {
		v.Speed = float64(p.DoneBytes) / sec
		if p.TotalBytes > p.DoneBytes {
			v.Remaining = time.Duration(float64(p.TotalBytes-p.DoneBytes) / v.Speed * float64(time.Second))
		}
	}
	return v
}

func (a *App) doProgress(act Action) []Cmd {
	op := a.op
	switch {
	case act.Kind == ActForceQuit && op.unresponsive:
		a.quit = true // 残りうるものは画面に示してある（filer §8.4）
	case op.canceling:
	case act.Kind == ActCancel && !op.askCancel:
		op.askCancel, op.askFrame = true, a.frames
	case act.Kind == ActYes && op.askCancel && a.frames > op.askFrame:
		op.askCancel, op.canceling, op.cancelAt = false, true, a.cfg.Now()
		op.cancel() // 処理中の項目は安全に中断され、残りはスキップになる（fsops）
	case (act.Kind == ActNo || act.Kind == ActCancel) && op.askCancel:
		op.askCancel = false
	}
	return nil
}

// Abort は、実行中のファイル操作を中止し、Execute が戻るのを最大 wait だけ待つ（シグナルで終わるとき。filer §10）。
func (a *App) Abort(wait time.Duration) {
	op := a.op
	if op == nil || op.cancel == nil {
		return
	}
	op.cancel()
	if op.done != nil {
		select {
		case <-op.done:
		case <-time.After(wait):
		}
	}
}

// ---- 結果（filer §8.5） ----

// resultState は、直前の操作の結果（L でもう一度出す）。
type resultState struct {
	op       fsops.OpKind
	from, to string
	res      *fsops.Result
	expanded map[int]bool // 詳細を展開した項目（Items の添字）
	english  bool
	cursor   int
	rows     int
	open     bool
	frame    int
}

func (a *App) executed(m executed) []Cmd {
	op := a.op
	if op == nil || op.gen != m.gen {
		return nil
	}
	a.op = nil
	if m.err != nil {
		a.logErr(m.err)
		a.setMessage(msg.CannotExecute(msg.Error(m.err)), true)
		return nil
	}
	res := m.res
	a.result = &resultState{op: op.req.Op, from: op.from, to: op.req.DestDir, res: res, expanded: map[int]bool{}}
	done, skipped, warned := 0, 0, false
	for _, it := range res.Items {
		a.logErr(errOrNil(it.Err))
		for _, w := range it.Warnings {
			a.logErr(w)
			warned = true
		}
		for _, d := range it.Details {
			a.logErr(errOrNil(d.Err))
		}
		switch {
		case it.Outcome == fsops.OutcomeDone:
			done++
			for _, d := range it.Details {
				if d.Err == nil {
					skipped++ // フォルダの中の、衝突の決定によるスキップ
				}
			}
		case it.Outcome == fsops.OutcomeSkipped && it.Err == nil:
			skipped++
		}
	}
	if needsResultScreen(res) {
		a.result.open, a.result.frame = true, a.frames
	} else {
		a.setMessage(msg.Done(op.req.Op, done, skipped, warned), false)
	}
	return a.afterOperation(op.req, res)
}

// errOrNil は、nil の *OpError を nil の error にする。
func errOrNil(e *fsops.OpError) error {
	if e == nil {
		return nil
	}
	return e
}

// needsResultScreen は、結果の画面を出すかを返す（filer §8.5。U3）。中止したとき、エラーのある項目（詳細の中を含む）があるとき、
// 完了・スキップ以外の項目があるとき。
func needsResultScreen(res *fsops.Result) bool {
	if res.Status == fsops.StatusCanceled {
		return true
	}
	for _, it := range res.Items {
		if it.Err != nil || it.Outcome != fsops.OutcomeDone && it.Outcome != fsops.OutcomeSkipped {
			return true
		}
		for _, d := range it.Details {
			if d.Err != nil {
				return true
			}
		}
	}
	return false
}

// afterOperation は、操作の後始末をする。完了した項目のマークを外し（それ以外は残す。filer §6）、
// 影響するペインを読み直す。移動の後は、覚えた項目を忘れる（filer §7）。
func (a *App) afterOperation(req fsops.Request, res *fsops.Result) []Cmd {
	for _, it := range res.Items {
		if it.Outcome != fsops.OutcomeDone {
			continue
		}
		for _, p := range a.panes {
			if filepath.Clean(p.dir) == filepath.Dir(it.Src) {
				delete(p.marks, filepath.Base(it.Src))
			}
		}
	}
	if req.Op == fsops.OpMove {
		a.yanked = nil
	}
	var cmds []Cmd
	for i, p := range a.panes {
		if p.loaded && affected(p.dir, req) {
			cmds = append(cmds, a.load(i, p.dir, loadReload, "")...)
		}
	}
	return cmds
}

// affected は、フォルダ dir の表示が操作 req で変わりうるかを返す（コピー先・移動先、項目のあったフォルダ、移動した項目の中）。
// 画面を読み直すかの判断だけに使う（fsops に渡すパスは作らない）。
func affected(dir string, req fsops.Request) bool {
	dir = filepath.Clean(dir)
	if dir == filepath.Clean(req.DestDir) {
		return true
	}
	for _, s := range req.Sources {
		if dir == filepath.Dir(s) || req.Op != fsops.OpCopy && (dir == s || strings.HasPrefix(dir, s+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

// ResultRow は、結果の一覧の 1 行。
type ResultRow struct {
	Item       int // Items の添字
	Detail     bool
	Outcome    fsops.Outcome
	Name       string
	Reason     string   // 日本語の理由（一覧に出す）
	English    []string // 英語の詳細（OpError.Error()。警告を含む）。e で画面の下に出す（filer §8.5）
	Details    int      // フォルダの中で完了以外になったエントリの数（Space で表示する）
	Expandable bool
	Expanded   bool
}

// ResultView は、結果の画面の内容。
type ResultView struct {
	Op       fsops.OpKind
	Status   fsops.Status
	From, To string
	Counts   []OutcomeCount // 問題のあるものから
	Rows     []ResultRow
	Cursor   int
	English  bool // カーソル行の英語の詳細を、画面の下に出す
}

// OutcomeCount は、結果の種類ごとの件数。
type OutcomeCount struct {
	Outcome fsops.Outcome
	N       int
}

// outcomeOrder は、結果の並べ方（問題のあるもの、スキップ、完了の順。filer §8.5）。
var outcomeOrder = []fsops.Outcome{fsops.OutcomeTrashUnconfirmed, fsops.OutcomeCopiedSourceKept, fsops.OutcomeFailed,
	fsops.OutcomePartial, fsops.OutcomeSkipped, fsops.OutcomeDone}

// Result は、結果の画面の内容を返す（ScreenResult のとき）。
func (a *App) Result() ResultView {
	r := a.result
	v := ResultView{Op: r.op, Status: r.res.Status, From: r.from, To: r.to, English: r.english}
	rows := r.rowsOf()
	for _, o := range outcomeOrder {
		n := 0
		for _, it := range r.res.Items {
			if it.Outcome == o {
				n++
			}
		}
		if n > 0 {
			v.Counts = append(v.Counts, OutcomeCount{o, n})
		}
	}
	r.cursor = max(min(r.cursor, len(rows)-1), 0)
	v.Rows, v.Cursor = rows, r.cursor
	return v
}

// rowsOf は、結果の一覧の行を作る。問題のあるものを先に、次にスキップ、最後に完了を並べ、同じ区分の中は計画の順にする。
func (r *resultState) rowsOf() []ResultRow {
	order := make([]int, len(r.res.Items))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(x, y int) int {
		return slices.Index(outcomeOrder, r.res.Items[x].Outcome) - slices.Index(outcomeOrder, r.res.Items[y].Outcome)
	})
	var rows []ResultRow
	for _, i := range order {
		it := r.res.Items[i]
		row := ResultRow{Item: i, Outcome: it.Outcome, Name: filepath.Base(it.Src), Details: len(it.Details),
			Expandable: len(it.Details) > 0, Expanded: r.expanded[i]}
		row.Reason = r.reason(it)
		if it.Err != nil {
			row.English = append(row.English, it.Err.Error())
		}
		for _, w := range it.Warnings {
			row.English = append(row.English, w.Error())
		}
		rows = append(rows, row)
		if !row.Expanded {
			continue
		}
		for _, d := range it.Details {
			name := d.Src
			if rel, err := filepath.Rel(it.Src, d.Src); err == nil {
				name = rel
			}
			row := ResultRow{Item: i, Detail: true, Outcome: d.Outcome, Name: name, Reason: msg.ResultError(d.Outcome, d.Err)}
			if d.Err != nil {
				row.English = []string{d.Err.Error()}
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// reason は、項目の結果の説明（filer §8.5）。
func (r *resultState) reason(it fsops.ItemResult) string {
	var parts []string
	switch {
	case it.Err != nil:
		parts = append(parts, msg.ResultError(it.Outcome, it.Err))
	case it.Outcome == fsops.OutcomeSkipped:
		parts = append(parts, msg.SkippedByChoice)
	}
	if p := msg.Partial(it.Method, it.Outcome); p != "" {
		parts = append(parts, p)
	}
	if it.Outcome == fsops.OutcomeDone {
		n := 0
		for _, d := range it.Details {
			if d.Err == nil {
				n++
			}
		}
		if n > 0 {
			parts = append(parts, msg.SkippedInside(n))
		}
	}
	if len(it.Warnings) > 0 {
		parts = append(parts, msg.MetadataWarning)
	}
	return strings.Join(parts, "。")
}

// SetResultRows は、結果の一覧を描いた行数を覚える（ページ単位の移動に使う）。
func (a *App) SetResultRows(n int) {
	if a.result != nil {
		a.result.rows = n
	}
}

func (a *App) doResult(act Action) []Cmd {
	r := a.result
	rows := r.rowsOf()
	switch act.Kind {
	case ActSubmit, ActCancel:
		if a.frames <= r.frame {
			return nil // 結果の画面を描く前に届いたキーでは閉じない（結果を隠さない。U3）
		}
		r.open = false
	case ActUp, ActDown, ActPageUp, ActPageDown, ActHome, ActEnd:
		r.cursor = moveCursor(r.cursor, len(rows), r.rows, act.Kind)
	case ActToggle:
		if r.cursor < len(rows) && rows[r.cursor].Expandable {
			i := rows[r.cursor].Item
			r.expanded[i] = !r.expanded[i]
		}
	case ActEnglish:
		r.english = !r.english
	}
	return nil
}

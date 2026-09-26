package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/zredjet/tana/internal/keys"
	"github.com/zredjet/tana/internal/lineedit"
	"github.com/zredjet/tana/internal/screen"
	"github.com/zredjet/tana/internal/tui"
)

// 確認用の画面（filer §12.1 の最後、フェーズ16）。tui.Loop の上で、次のことを手で確かめる。
//   - VU1: キーの区別、Esc の遅延、大きさの変更への追従、日本語の名前、10 万行の一覧のスクロール、正常終了・panic・シグナルの後の復元
//   - VU4: 名前の入力欄での IME（変換中の文字が入力欄の本物のカーソルの位置に出るか、確定、Backspace）
//   - VT4: 大きさを変えるたびに、大きさの変更のイベントが届くか（250 ミリ秒ごとの見回りと比べる）

// screenRows は、各ペインの一覧の行数（VU1 の 10 万行）。
const screenRows = 100_000

// sampleNames は、一覧に並べる名前。幅の違う文字、NFD、絵文字、端末に出してはいけない文字、不正なバイトを含む（filer §9）。
var sampleNames = []string{
	"報告書_2026年度.docx",
	"README.md",
	"がぎぐ（NFD の濁点）.txt",
	"ｶﾞｷﾞｸﾞ 半角カナ.txt",
	"○①※…αΩ 幅が曖昧な文字.txt",
	"😀 絵文字.png",
	"👨‍👩‍👧 家族（ZWJ）.jpg",
	"🇯🇵 国旗.txt",
	"❤️ VS16.txt",
	"制御文字\x1b[31m赤\x1b[0m.txt",
	"拡張子の偽装‮txt.exe",
	"不正なバイト\xff\xfe.bin",
	"한국어 가나다.txt",
	"क्षि デーヴァナーガリー.txt",
	"とても長い名前のファイルでペインの幅に収まらないものが切り詰められることを確かめるための名前です.txt",
	"漢字だけの名前",
	"spaces   and\ttab.txt",
}

type keyLogEntry struct {
	TMs   float64 `json:"t_ms"`
	Event string  `json:"event"`
	Raw   string  `json:"raw_hex"`
}

type inputEntry struct {
	TMs    float64 `json:"t_ms"`
	Result string  `json:"result"` // confirmed・cancelled
	Text   string  `json:"text,omitempty"`
	Hex    string  `json:"hex"`
}

type resizeEntry struct {
	TMs    float64 `json:"t_ms"`
	Source string  `json:"source"` // event（大きさの変更のイベント）・poll（見回りで見つけた変化）
	Cols   int     `json:"cols"`
	Rows   int     `json:"rows"`
}

type frameStats struct {
	Count        int     `json:"count"`
	MaxDrawMs    float64 `json:"max_draw_ms"`
	MaxFlushMs   float64 `json:"max_flush_ms"`
	MaxBytes     int     `json:"max_bytes"`
	MaxCoalesced int     `json:"max_coalesced"`
}

type screenSection struct {
	sectionHeader
	PID         int           `json:"pid"`
	Keys        []keyLogEntry `json:"keys"`
	KeysDropped int           `json:"keys_dropped,omitempty"`
	Inputs      []inputEntry  `json:"inputs"`
	Resizes     []resizeEntry `json:"resizes"`
	Frames      frameStats    `json:"frames"`
	Exit        string        `json:"exit"` // quit・signal・panic・error
	Error       string        `json:"error,omitempty"`
}

// keyLogMax は、記録するキーの数の上限（押し続けたキーで記録が膨らまないように）。
const keyLogMax = 5000

type pane struct{ cursor, top int }

// polledSize は、見回りの goroutine が見つけた大きさ。
type polledSize struct {
	cols, rows int
	at         time.Time
}

type progressDone struct{}

// probeScreen は、確認用の画面の状態と描画（tui.Handler）。
type probeScreen struct {
	res      *screenSection
	start    time.Time
	loop     *tui.Loop
	panes    [2]pane
	active   int
	edit     *lineedit.Editor
	lastKey  keys.Event
	progress atomic.Int64 // 0〜1000。作業用の goroutine が書き、描画が読む（filer §10 の「最新の値を 1 つだけ置く場所」）
	running  bool
	listRows int // 一覧の見えている行数（PgUp・PgDn の量）

	progressStep time.Duration // 進捗を 0.1% 進める間隔（進む様子を見せるため。テストでは 0）
}

func newProbeScreen(res *screenSection, l *tui.Loop) *probeScreen {
	return &probeScreen{res: res, start: time.Now(), loop: l, listRows: 10, progressStep: 5 * time.Millisecond}
}

func (p *probeScreen) ms(t time.Time) float64 { return ms(t.Sub(p.start)) }

func (p *probeScreen) Handle(l *tui.Loop, ev tui.Event) bool {
	now := time.Now()
	st := l.Stats()
	f := &p.res.Frames
	f.Count = st.Frames
	f.MaxDrawMs, f.MaxFlushMs = max(f.MaxDrawMs, ms(st.Draw)), max(f.MaxFlushMs, ms(st.Flush))
	f.MaxBytes, f.MaxCoalesced = max(f.MaxBytes, st.Bytes), max(f.MaxCoalesced, st.Coalesced)
	switch ev.Kind {
	case tui.KindKey:
		if len(p.res.Keys) < keyLogMax {
			p.res.Keys = append(p.res.Keys, keyLogEntry{TMs: p.ms(now), Event: ev.Key.String(), Raw: hex.EncodeToString([]byte(ev.Key.Raw))})
		} else {
			p.res.KeysDropped++
		}
		p.lastKey = ev.Key
		if p.edit != nil {
			p.editKey(ev.Key, now)
			return true
		}
		return p.listKey(l, ev.Key)
	case tui.KindResize:
		c, r := l.Size()
		p.res.Resizes = append(p.res.Resizes, resizeEntry{TMs: p.ms(now), Source: "event", Cols: c, Rows: r})
	case tui.KindMessage:
		switch m := ev.Msg.(type) {
		case polledSize:
			p.res.Resizes = append(p.res.Resizes, resizeEntry{TMs: p.ms(m.at), Source: "poll", Cols: m.cols, Rows: m.rows})
		case progressDone:
			p.running = false
		}
	case tui.KindSignal:
		p.res.Exit = "signal"
		p.res.Error = ev.Signal.String()
	}
	return true
}

// listKey は、一覧を見ているときのキー。false を返すと終わる。
func (p *probeScreen) listKey(l *tui.Loop, k keys.Event) bool {
	if k.Kind != keys.KeyEvent {
		return true
	}
	pn := &p.panes[p.active]
	switch k.Key {
	case keys.KeyUp:
		pn.cursor--
	case keys.KeyDown:
		pn.cursor++
	case keys.KeyPageUp:
		pn.cursor -= p.listRows
	case keys.KeyPageDown:
		pn.cursor += p.listRows
	case keys.KeyHome:
		pn.cursor = 0
	case keys.KeyEnd:
		pn.cursor = screenRows - 1
	case keys.KeyTab:
		p.active = 1 - p.active
	case keys.KeyRune:
		if k.Mod != 0 {
			return true
		}
		switch k.Rune {
		case 'q':
			p.res.Exit = "quit"
			return false
		case 'i':
			name := sampleNames[pn.cursor%len(sampleNames)]
			ext := 0
			if dot := strings.LastIndexByte(name, '.'); dot > 0 {
				ext = len(name) - dot
			}
			p.edit = lineedit.New(name, len(name)-ext) // カーソルは拡張子の前（filer §8.7）
		case 'p':
			if !p.running {
				p.running = true
				step := p.progressStep
				l.Go(func() {
					for i := range int64(1001) {
						p.progress.Store(i)
						l.Wake()
						time.Sleep(step) // 進捗が少しずつ進む様子を見せるため（約 5 秒）
					}
					l.Post(progressDone{})
				})
			}
		case '!':
			panic("tuiprobe: panic in the event loop (test)")
		case '@':
			l.Go(func() { panic("tuiprobe: panic in a worker goroutine (test)") })
		}
	}
	pn.cursor = min(max(pn.cursor, 0), screenRows-1)
	return true
}

// editKey は、入力欄を開いているときのキー（lineedit。tui §7）。
func (p *probeScreen) editKey(k keys.Event, now time.Time) {
	e := p.edit
	switch k.Kind {
	case keys.PasteEvent:
		e.Insert(k.Text)
		return
	case keys.KeyEvent:
	default:
		return
	}
	switch k.Key {
	case keys.KeyRune:
		if k.Mod == 0 || k.Mod == keys.ModShift {
			e.Insert(string(k.Rune))
		}
	case keys.KeyBackspace:
		e.DeleteBackward()
	case keys.KeyDelete:
		e.DeleteForward()
	case keys.KeyLeft:
		e.Left()
	case keys.KeyRight:
		e.Right()
	case keys.KeyHome:
		e.Home()
	case keys.KeyEnd:
		e.End()
	case keys.KeyEnter, keys.KeyEsc:
		result := "confirmed"
		if k.Key == keys.KeyEsc {
			result = "cancelled"
		}
		p.res.Inputs = append(p.res.Inputs, inputEntry{TMs: p.ms(now), Result: result, Text: validText(e.Text()), Hex: hex.EncodeToString([]byte(e.Text()))})
		p.edit = nil
	}
}

// validText は、正しい UTF-8 なら s を、そうでなければ空を返す（JSON に書くため。バイト列は hex に残す）。
func validText(s string) string {
	if strings.ToValidUTF8(s, "") == s {
		return s
	}
	return ""
}

var (
	styleNormal  = screen.Style{}
	styleTitle   = screen.Style{Attr: screen.AttrReverse}
	styleCursor  = screen.Style{Attr: screen.AttrReverse}
	styleCursor2 = screen.Style{Attr: screen.AttrUnderline}
	styleDim     = screen.Style{Attr: screen.AttrDim}
	styleBox     = screen.Style{FG: screen.ColorCyan}
)

func (p *probeScreen) Draw(s *screen.Screen) {
	cols, rows := s.Size()
	full := screen.Region{W: cols, H: rows}
	s.SetCursor(0, 0, false)
	if cols < 40 || rows < 10 {
		s.Put(full, 0, 0, "端末が小さすぎます", styleNormal)
		return
	}
	st := p.loop.Stats()
	s.Fill(screen.Region{W: cols, H: 1}, styleTitle)
	s.Put(full, 0, 0, fmt.Sprintf(" tuiprobe screen  %dx%d  pid %d  描画 %.1fms 出力 %.1fms %dB まとめた入力 %d  大きさの変更 %d",
		cols, rows, p.res.PID, ms(st.Draw), ms(st.Flush), st.Bytes, st.Coalesced, len(p.res.Resizes)), styleTitle)

	listTop, listH := 2, rows-5
	p.listRows = max(listH, 1)
	half := cols / 2
	regions := [2]screen.Region{{X: 0, Y: 1, W: half, H: listH + 1}, {X: half + 1, Y: 1, W: cols - half - 1, H: listH + 1}}
	for y := 1; y < listTop+listH; y++ {
		s.Put(full, half, y, "│", styleBox)
	}
	for i := range p.panes {
		p.drawPane(s, i, regions[i], listTop, listH)
	}

	// 進捗（作業用の goroutine が書いた最新の値を読む）。
	if p.running {
		v := p.progress.Load()
		barW := max(cols-30, 10)
		n := int(v) * barW / 1000
		s.Put(full, 0, rows-3, fmt.Sprintf(" 進捗 [%s%s] %5.1f%%", strings.Repeat("#", n), strings.Repeat(".", barW-n), float64(v)/10), styleNormal)
	}
	last := "(なし)"
	if p.lastKey.Kind != 0 {
		last = fmt.Sprintf("%s  raw %q", p.lastKey.String(), p.lastKey.Raw)
	}
	s.Put(full, 0, rows-2, " 最後のキー: "+last, styleNormal)
	s.Put(full, 0, rows-1, " ↑↓ PgUp PgDn Home End  Tab:ペイン  i:入力欄  p:進捗  !:panic  @:作業用 goroutine の panic  q:終わる", styleDim)

	if p.edit != nil {
		p.drawEdit(s, cols, rows)
	}
}

func (p *probeScreen) drawPane(s *screen.Screen, i int, r screen.Region, listTop, listH int) {
	pn := &p.panes[i]
	title := fmt.Sprintf(" %s  %d / %d 行", [2]string{"左", "右"}[i], pn.cursor+1, screenRows)
	s.Put(r, 0, 0, title, styleDim)
	if pn.cursor < pn.top {
		pn.top = pn.cursor
	}
	if pn.cursor >= pn.top+listH {
		pn.top = pn.cursor - listH + 1
	}
	list := screen.Region{X: r.X, Y: listTop, W: r.W, H: listH}
	for y := range listH {
		n := pn.top + y
		if n >= screenRows {
			break
		}
		st := styleNormal
		if n == pn.cursor {
			st = styleCursor2
			if i == p.active {
				st = styleCursor
			}
			s.Fill(screen.Region{X: list.X, Y: list.Y + y, W: list.W, H: 1}, st)
		}
		// 欄ごとに Put する（番号の欄と名前の欄。tui §6 の「欄の始め」）。
		s.Put(list, 0, y, fmt.Sprintf("%6d ", n+1), st)
		s.Put(list, 8, y, sampleNames[n%len(sampleNames)], st)
	}
}

// drawEdit は、中央に入力欄を描き、本物のカーソルを入力欄に置く（IME の変換中の文字はここに出る。filer VU4）。
func (p *probeScreen) drawEdit(s *screen.Screen, cols, rows int) {
	w := min(64, cols-4)
	box := screen.Region{X: (cols - w) / 2, Y: rows/2 - 2, W: w, H: 4}
	s.Fill(box, styleNormal)
	s.Put(box, 0, 0, "┌"+strings.Repeat("─", w-2)+"┐", styleBox)
	s.Put(box, 0, 1, "│", styleBox)
	s.Put(box, w-1, 1, "│", styleBox)
	s.Put(box, 0, 2, "└"+strings.Repeat("─", w-2)+"┘", styleBox)
	s.Put(box, 2, 0, " 名前の変更（Enter:確定 Esc:取り消し） ", styleBox)
	field := screen.Region{X: box.X + 2, Y: box.Y + 1, W: w - 4, H: 1}
	v := p.edit.View(field.W)
	s.Put(field, 0, 0, p.edit.Text()[v.Start:v.End], styleNormal)
	s.SetCursor(field.X+v.CursorCol, field.Y, true)
	s.Put(box, 0, 3, fmt.Sprintf(" %d バイト  カーソル %d  変更 %v", len(p.edit.Text()), p.edit.Cursor(), p.edit.Changed()), styleDim)
}

// runScreen は、確認用の画面を動かす。どの終わり方でも端末を戻してから（tui.Loop.Run が戻す）、記録を返す。
// panic は、記録に書いた後で呼び出し側が表示する。
func runScreen(t tui.Terminal, sec sectionHeader) (*screenSection, error) {
	res := &screenSection{sectionHeader: sec, PID: os.Getpid(), Keys: []keyLogEntry{}, Inputs: []inputEntry{}, Resizes: []resizeEntry{}}
	l := tui.New(t)
	l.SetNoColor(os.Getenv("NO_COLOR") != "")
	h := newProbeScreen(res, l)
	stop := make(chan struct{})
	pollSize(l, t, sec.Cols, sec.Rows, 250*time.Millisecond, stop)
	err := l.Run(h)
	close(stop)
	var pe *tui.PanicError
	var se *tui.SignalError
	switch {
	case err == nil:
		if res.Exit == "" {
			res.Exit = "quit"
		}
	case errors.As(err, &pe):
		res.Exit, res.Error = "panic", fmt.Sprint(pe.Value)
	case errors.As(err, &se):
		res.Exit, res.Error = "signal", se.Signal.String()
	default:
		res.Exit, res.Error = "error", err.Error()
	}
	return res, err
}

// pollSize は、interval ごとに端末の大きさを調べ、変わっていたら polledSize を送る（VT4: イベントが届かない大きさの変更を見つける）。
// stop が閉じられたら止まる。
func pollSize(l *tui.Loop, t tui.Terminal, cols, rows int, interval time.Duration, stop <-chan struct{}) {
	l.Go(func() {
		last := [2]int{cols, rows}
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			c, r, err := t.Size()
			if err != nil {
				return
			}
			if [2]int{c, r} != last {
				last = [2]int{c, r}
				l.Post(polledSize{cols: c, rows: r, at: time.Now()})
			}
		}
	})
}

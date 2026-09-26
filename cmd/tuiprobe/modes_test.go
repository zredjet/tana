package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/textwidth"
)

var (
	cupPattern = regexp.MustCompile(`\x1b\[(\d+);(\d+)H`)
	csiPattern = regexp.MustCompile(`\x1b\[[0-9;?$]*[A-Za-z]`)
)

// cursorModel は、書かれたものから、カーソル位置の問い合わせへの応答を作る（最後の位置の指定から、文字の幅だけ進める）。
// 自動改行が付いていて右端を越えたら、次の行に移る。join が true なら、位置を指定し直しても a と b を 1 つにまとめる端末にする。
func cursorModel(cols int, join map[string]bool) func(string) (string, bool) {
	wrap := false
	return func(w string) (string, bool) {
		if i := strings.LastIndex(w, "\x1b[?7"); i >= 0 {
			wrap = w[i+4] == 'h'
		}
		m := cupPattern.FindAllStringSubmatchIndex(w, -1)
		if len(m) == 0 {
			return "", false
		}
		last := m[len(m)-1]
		row, _ := strconv.Atoi(w[last[2]:last[3]])
		col, _ := strconv.Atoi(w[last[4]:last[5]])
		text := csiPattern.ReplaceAllString(w[last[1]:], "")
		col += textwidth.Width(text)
		// 位置を指定し直しても結合する端末: 直前の欄の文字と b をまとめ、b の幅だけ戻る。
		if len(m) >= 2 {
			prev := csiPattern.ReplaceAllString(w[m[len(m)-2][1]:last[0]], "")
			if join[prev+"|"+text] {
				col -= textwidth.Width(text)
			}
		}
		if col > cols+1 {
			if wrap {
				row, col = row+1, col-cols
			} else {
				col = cols
			}
		}
		return fmt.Sprintf("\x1b[%d;%dR", row, col), true
	}
}

func TestRunModes(t *testing.T) {
	t.Parallel()
	f := newFake(cursorModel(80, map[string]bool{"ᄀ|가": true}))
	f.onWrite = func(w string) {
		// 撮影の画面が出たら、キーで終える。
		if strings.Contains(w, "撮影") {
			f.send([]byte("x"))
		}
	}
	res, err := runModes(f, f.box(), sectionHeader{Cols: 80, Rows: 30}, true, 0, cprTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Completed || len(res.SetReset) != len(modeSeqs) || len(res.Queries) != len(modeQueries) || len(res.Joins) != len(joinCases) {
		t.Fatalf("result = %+v", res)
	}
	for _, m := range res.SetReset {
		if m.Printed || m.Col != 1 || m.Error != "" {
			t.Errorf("set/reset %s = %+v, want not printed", m.ID, m)
		}
	}
	if aw := res.AutoWrap; !aw.StaysOff || !aw.WrapsWhenOn || aw.OffCol != 80 || aw.OnRow != measureRow+1 {
		t.Errorf("autowrap = %+v", aw)
	}
	for _, j := range res.Joins {
		want := j.ID != "jamo-l+syllable"
		if j.Separated != want || j.ExpectedCol != 24+j.WidthA+j.WidthB || j.ColRTL != 24+j.WidthA {
			t.Errorf("join %s = %+v, want separated %v", j.ID, j, want)
		}
	}
	// 描いた後は、カーソルを隠し、自動改行を切った状態に戻す。
	if w := f.written.String(); !strings.Contains(w, "\x1b[?25l\x1b[?7l") {
		t.Error("modes did not restore the drawing settings")
	}
}

// TestRunModesNoDECRQM は、-decrqm=false で DECRQM を送らないことを確かめる（Terminal.app は問い合わせを文字として表示する）。
func TestRunModesNoDECRQM(t *testing.T) {
	t.Parallel()
	f := newFake(cursorModel(80, nil))
	f.onWrite = func(w string) {
		if strings.Contains(w, "撮影") {
			f.send([]byte("x"))
		}
	}
	res, err := runModes(f, f.box(), sectionHeader{Cols: 80, Rows: 30}, false, 0, cprTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Queries) != 0 || strings.Contains(f.written.String(), "$p") {
		t.Errorf("DECRQM sent with -decrqm=false: %+v", res.Queries)
	}
}

// TestRunModesNoResponse は、カーソル位置の問い合わせに応答しない端末でも、止まらずに誤りを記録することを確かめる。
func TestRunModesNoResponse(t *testing.T) {
	t.Parallel()
	f := newFake(func(string) (string, bool) { return "", false })
	f.onWrite = func(w string) {
		if strings.Contains(w, "撮影") {
			f.send([]byte("x"))
		}
	}
	res, err := runModes(f, f.box(), sectionHeader{Cols: 80, Rows: 30}, false, 0, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if res.SetReset[0].Error == "" || res.AutoWrap.Error == "" || res.Joins[0].Error == "" {
		t.Errorf("errors not recorded: %+v", res)
	}
}

// TestJoinCasesCanJoin は、VT7 の組が、続けて書くと 1 つの書記素クラスタになること（比較用の ascii を除く）と、
// 端末に出してよい通常の文字であることを確かめる（screen が位置を指定し直すのは、この場合）。
func TestJoinCasesCanJoin(t *testing.T) {
	t.Parallel()
	for _, jc := range joinCases {
		for _, s := range []string{jc.a, jc.b} {
			c, rest := textwidth.Next(s)
			if rest != "" || c.Class != textwidth.Normal {
				t.Errorf("%s: %q is not one normal cluster: %+v", jc.id, s, c)
			}
		}
		c, _ := textwidth.Next(jc.a + jc.b)
		if joined := c.Text == jc.a+jc.b; joined != (jc.id != "ascii+ascii") {
			t.Errorf("%s: %q+%q joined = %v", jc.id, jc.a, jc.b, joined)
		}
	}
}

// TestRunModesTooFewRows は、結合の確かめの行が収まらない端末では、測らずに理由を返すことを確かめる
// （画面の外の行は端末が最後の行に収めるので、行が重なって結果を誤る）。
func TestRunModesTooFewRows(t *testing.T) {
	t.Parallel()
	f := newFake(cursorModel(80, nil))
	res, err := runModes(f, f.box(), sectionHeader{Cols: 80, Rows: 24}, false, 0, cprTimeout)
	if err == nil || res.Completed || len(res.Joins) != 0 {
		t.Errorf("24 rows: err %v, completed %v, joins %d; want an error before measuring", err, res.Completed, len(res.Joins))
	}
}

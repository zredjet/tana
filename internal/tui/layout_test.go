package tui

import (
	"slices"
	"testing"

	"github.com/zredjet/tana/internal/screen"
)

// TestLayoutMatchesFormulas は、配置の補助が、フェーズ22までの画面ごとの式と同じ結果になることを、
// ゴールデンファイルにない大きさ（幅 80〜200・高さ 24〜60）で確かめる。
func TestLayoutMatchesFormulas(t *testing.T) {
	t.Parallel()
	for cols := 80; cols <= 200; cols++ {
		for rows := 24; rows <= 60; rows++ {
			d := dock{rest: screen.Region{W: cols, H: rows}}
			guide, message, status := d.bottom(1), d.bottom(1), d.bottom(1)
			if guide.Y != rows-1 || message.Y != rows-2 || status.Y != rows-3 || d.rest != (screen.Region{W: cols, H: rows - 3}) {
				t.Fatalf("%dx%d: dock %v %v %v %v", cols, rows, guide, message, status, d.rest)
			}
			for n := 1; n <= 3; n++ {
				for i, r := range splitCols(d.rest, n) {
					if x0, x1 := cols*i/n, cols*(i+1)/n; r.X != x0 || r.W != x1-x0 || r.H != rows-3 {
						t.Fatalf("%dx%d: pane %d of %d: %v", cols, rows, i, n, r)
					}
				}
			}
		}
		total := cols - 4 // Yazi 風の内側の幅から、列の間の罫線 2 本を除いた幅
		if got, want := ratioWidths(total, 1, 4, 3), []int{total / 8, total * 4 / 8, total - total/8 - total*4/8}; !slices.Equal(got, want) {
			t.Fatalf("cols %d: columns %v, want %v", cols, got, want)
		}
		// 衝突の画面の欄（名前・コピー元・コピー先・決定）。
		nameW := cols - 2 - (infoW + 1) - (infoW + 1) - 1 - decisionW
		wantX := []int{2, 2 + nameW + 1, 2 + nameW + 1 + infoW + 1, 2 + nameW + 1 + 2*(infoW+1)}
		if xs, ws := rowColumns(2, cols-2, 1, flex, infoW, infoW, decisionW); !slices.Equal(xs, wantX) || ws[0] != nameW {
			t.Fatalf("cols %d: conflict columns %v %v, want %v %d", cols, xs, ws, wantX, nameW)
		}
		// 結果の画面の欄（結果・名前・理由）。
		for outW := 1; outW <= 30; outW++ {
			nameW := min(24, cols/4)
			x := 2 + outW + 1
			wantX, wantW := []int{2, x, x + nameW + 1}, []int{outW, nameW, cols - x - nameW - 2}
			if xs, ws := rowColumns(2, cols-3, 1, outW, min(24, cols/4), flex); !slices.Equal(xs, wantX) || !slices.Equal(ws, wantW) {
				t.Fatalf("cols %d outcome %d: result columns %v %v, want %v %v", cols, outW, xs, ws, wantX, wantW)
			}
		}
	}
	// ペインの行の欄（名前の幅と、更新日時を出すか）。
	for w := 1; w <= 200; w++ {
		want, date := w-1-1-sizeW-1-dateW, true
		if want < minNameW {
			want, date = max(w-1-1-sizeW, 1), false
		}
		if got, gotDate := columns(w); got != want || gotDate != date {
			t.Fatalf("width %d: columns %d %v, want %d %v", w, got, gotDate, want, date)
		}
	}
}

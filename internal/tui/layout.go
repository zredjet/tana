package tui

import "github.com/zredjet/tana/internal/screen"

// 配置の補助（filer §4 の「UI の骨格」。G13）。画面ごとに座標の式を書かずに、ここの形で分ける。

// dock は、領域を、端に寄せる領域と、残りに分ける（作業場の配置）。
// 後の版でパネル（サイドバー、下段）を足すときは、寄せる領域を 1 つ足すだけで、ほかの配置の式は変えない。
type dock struct {
	rest screen.Region // まだ割り当てていない領域
}

// bottom は、残りの下端から高さ h の領域を取る。
func (d *dock) bottom(h int) screen.Region {
	r := d.rest
	h = min(h, r.H)
	d.rest.H -= h
	return screen.Region{X: r.X, Y: r.Y + r.H - h, W: r.W, H: h}
}

// left は、残りの左端から幅 w の領域を取る。
func (d *dock) left(w int) screen.Region {
	r := d.rest
	w = min(w, r.W)
	d.rest.X, d.rest.W = r.X+w, r.W-w
	return screen.Region{X: r.X, Y: r.Y, W: w, H: r.H}
}

// splitCols は、領域 r を横に n 個に分ける（i 番目の左端は r.W*i/n）。
func splitCols(r screen.Region, n int) []screen.Region {
	out := make([]screen.Region, n)
	for i := range out {
		x0, x1 := r.W*i/n, r.W*(i+1)/n
		out[i] = screen.Region{X: r.X + x0, Y: r.Y, W: x1 - x0, H: r.H}
	}
	return out
}

// ratioWidths は、幅 total を比 ratios で分けた幅を返す。最後の欄が端数を受ける（ほかの欄は total*比/合計）。
func ratioWidths(total int, ratios ...int) []int {
	sum := 0
	for _, r := range ratios {
		sum += r
	}
	out := make([]int, len(ratios))
	used := 0
	for i, r := range ratios[:len(ratios)-1] {
		out[i] = total * r / sum
		used += out[i]
	}
	out[len(out)-1] = total - used
	return out
}

// flex は、rowColumns で、残りの幅を使う欄の印。
const flex = -1

// rowColumns は、左端 x から幅 total の中に、幅 widths の欄を gap 桁ずつあけて並べ、各欄の左端と幅を返す。
// 幅が flex の欄（1 つまで）は、ほかの欄と間の残りの幅を使う。
func rowColumns(x, total, gap int, widths ...int) (xs, ws []int) {
	rest := total - gap*(len(widths)-1)
	for _, w := range widths {
		if w != flex {
			rest -= w
		}
	}
	for _, w := range widths {
		if w == flex {
			w = rest
		}
		xs, ws = append(xs, x), append(ws, w)
		x += w + gap
	}
	return xs, ws
}

package fsops

import (
	"syscall"
	"testing"
	"unsafe"
)

// growStack は、n 段の再帰で 1 段あたり約 2 KB のスタックを使う（ゴルーチンのスタックを伸ばして移させるため）。
//
//go:noinline
func growStack(n int) int {
	var buf [256]int
	buf[n%256] = n
	if n <= 0 {
		return buf[0]
	}
	return growStack(n-1) + buf[n%256]
}

// TestComCallPointerArgs は、comObj.call に uintptr にして渡したポインタが、呼び出し中にゴルーチンのスタックが移っても
// 指す先を保つことを確かめる。COM は、渡されたオブジェクト（§12.2 の進捗通知の受け取り口など）を呼び出しの間ずっと使い、
// 呼び出し中には Go のコールバック（進捗通知）が同じゴルーチンのスタックで動くため、そこでスタックが伸びて移りうる。
// 渡したものがスタックに置かれていると、COM は古い場所を指し続ける（CI の TestV19 で DeleteItem 中のアクセス違反として現れた）。
func TestComCallPointerArgs(t *testing.T) {
	t.Parallel()
	var got uintptr
	cb := syscall.NewCallback(func(this *comObj, p uintptr) uintptr {
		got = p
		growStack(1000) // 約 2 MB。呼び出し元のゴルーチンのスタックを移させる
		return sOK
	})
	var vtbl [32]uintptr
	vtbl[3] = cb
	obj := &comObj{vtbl: &vtbl}
	var x int32
	obj.call(3, uintptr(unsafe.Pointer(&x)))
	if want := uintptr(unsafe.Pointer(&x)); got != want {
		t.Fatalf("comObj.call passed %#x, but the variable is now at %#x (it was on the goroutine stack, which moved during the call)", got, want)
	}
}

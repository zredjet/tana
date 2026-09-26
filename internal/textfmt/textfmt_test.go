package textfmt

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zredjet/tana/internal/textwidth"
)

func TestSize(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		n    int64
		want string
	}{
		{0, "0"}, {1023, "1023"}, {1024, "1.0K"}, {3174, "3.1K"}, {10239, "10K"}, {49152, "48K"},
		{1048575, "1024K"}, {1258291, "1.2M"}, {134217728, "128M"}, {2469606195, "2.3G"}, {1 << 40, "1.0T"}, {5 << 50, "5120T"},
	} {
		if got := Size(tt.n); got != tt.want || len(got) > 5 {
			t.Errorf("Size(%d) = %q, want %q (at most 5 characters)", tt.n, got, tt.want)
		}
	}
}

func TestBytes(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		n    int64
		want string
	}{{0, "0"}, {999, "999"}, {1000, "1,000"}, {1234567, "1,234,567"}, {-1234, "-1,234"}} {
		if got := Bytes(tt.n); got != tt.want {
			t.Errorf("Bytes(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestTime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.Local)
	for _, tt := range []struct {
		t    time.Time
		want string
	}{
		{time.Date(2026, 9, 24, 11, 19, 5, 0, time.Local), "09-24 11:19"},
		{time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local), "01-02 03:04"},
		{time.Date(2025, 12, 28, 23, 59, 0, 0, time.Local), "2025-12-28"},
		{time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local), "2027-01-01"}, // 未来の年
	} {
		if got := Time(tt.t, now); got != tt.want {
			t.Errorf("Time(%v) = %q, want %q", tt.t, got, tt.want)
		}
	}
	if got := FullTime(time.Date(2026, 9, 24, 11, 19, 5, 0, time.Local)); got != "2026-09-24 11:19:05" {
		t.Errorf("FullTime = %q", got)
	}
}

func TestTruncName(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name  string
		width int
		want  string
	}{
		{"README.md", 20, "README.md"},
		{"見積書_2026年度版.xlsx", 17, "見積書_2026~.xlsx"}, // 拡張子を残し、途中を ~ にする（filer §9.2）
		{"abcdefghij.txt", 10, "abcde~.txt"},
		{"abcdefghij", 6, "abcde~"},                               // 拡張子なし
		{".bashrc-very-long", 8, ".bashrc~"},                      // 先頭の . は拡張子の区切りではない
		{"archive.tar.gz", 10, "archiv~.gz"},                      // 最後の . から後ろが拡張子
		{"x.verylongextension", 8, "x.veryl~"},                    // 拡張子が長すぎれば、残さない
		{"あいうえお.txt", 8, "あ~.txt"},                                // 全角は 2 桁。幅 8 に 7 桁で収まる
		{"か\u3099き\u3099く\u3099.txt", 8, "か\u3099~.txt"},          // NFD の濁点を切り離さない
		{"👨\u200d👩\u200d👧family.png", 8, "👨\u200d👩\u200d👧f~.png"}, // 家族の絵文字の表示は幅 2
		{"abc", 1, "~"},
		{"abc", 0, ""},
	} {
		got := TruncName(tt.name, tt.width)
		if got != tt.want {
			t.Errorf("TruncName(%q, %d) = %q, want %q", tt.name, tt.width, got, tt.want)
		}
		if w := textwidth.Width(got); w > tt.width {
			t.Errorf("TruncName(%q, %d) is %d wide", tt.name, tt.width, w)
		}
	}
}

func TestTruncPath(t *testing.T) {
	t.Parallel()
	sep := string(filepath.Separator)
	root := sep
	if filepath.VolumeName(`C:\`) != "" { // Windows
		root = `C:\`
	}
	p := root + filepath.Join("Users", "hiro", "projects", "tana")
	tail := func(parts ...string) string { return root + "~" + sep + filepath.Join(parts...) }
	for _, tt := range []struct {
		width int
		want  string
	}{
		{100, p},
		{len(p), p},
		{len(p) - 1, tail("hiro", "projects", "tana")}, // 先頭側の要素を ~ にする（filer §9.2）
		{len(tail("projects", "tana")), tail("projects", "tana")},
		{len(tail("tana")), tail("tana")},
	} {
		got := TruncPath(p, tt.width)
		if got != tt.want {
			t.Errorf("TruncPath(%q, %d) = %q, want %q", p, tt.width, got, tt.want)
		}
	}
	// 最後の要素も入らなければ、その後ろ側を残す。
	if got := TruncPath(p, 4); textwidth.Width(got) > 4 || got != "~ana" {
		t.Errorf("TruncPath(%q, 4) = %q, want ~ana", p, got)
	}
	if got := TruncPath(p, 0); got != "" {
		t.Errorf("TruncPath(%q, 0) = %q", p, got)
	}
}

// TestEscape は、状態行の表記を確かめる（filer §9.3）。表示で置き換える文字を、符号位置やバイトの形にする。
func TestEscape(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, want string
		replaced   bool
	}{
		{"報告書.docx", "報告書.docx", false},
		{"か\u3099.txt", "か\u3099.txt", false},
		{"a\x1b[31mb", `a\x1b[31mb`, true},
		{"x\u202etxt.exe", `x\u202etxt.exe`, true},
		{"bad\xff\xfe", `bad\xff\xfe`, true},
		{"z\u200bw", `z\u200bw`, true},
		{"\u0085", `\u0085`, true},
		{"👨\u200d👩\u200d👧.jpg", `\u{1f468}\u200d\u{1f469}\u200d\u{1f467}.jpg`, true},
		{"\U0001f1ef\U0001f1f5", `\u{1f1ef}\u{1f1f5}`, true},
		{"😀", "😀", false},
		// 置き換える文字があるときは、もとの \ を \\ にして、置き換えた表記と区別する（filer §9.3）
		{`a\u202eb`, `a\u202eb`, false}, // 置き換える文字がなければそのまま
		{"a\\u202e\u202e", `a\\u202e\u202e`, true},
		{`C:\x\y` + "\x1b", `C:\\x\\y\x1b`, true},
	} {
		if got := Escape(tt.name); got != tt.want {
			t.Errorf("Escape(%q) = %q, want %q", tt.name, got, tt.want)
		}
		if got := HasReplaced(tt.name); got != tt.replaced {
			t.Errorf("HasReplaced(%q) = %v, want %v", tt.name, got, tt.replaced)
		}
	}
}

// FuzzTruncName は、切り詰めた名前が幅に収まり、~ の前が元の名前の書記素クラスタの境界までの部分であることを確かめる。
func FuzzTruncName(f *testing.F) {
	for _, s := range []string{"見積書_2026年度版.xlsx", "か\u3099き\u3099.txt", "👨\u200d👩\u200d👧.png", "a\x1bb.exe", "\u0600x.y", ".dot", "a.b.c"} {
		f.Add(s, 7)
	}
	f.Fuzz(func(t *testing.T, name string, width int) {
		width %= 40
		if width < 0 {
			width = -width
		}
		got := TruncName(name, width)
		if textwidth.Width(got) > width {
			t.Fatalf("TruncName(%q, %d) = %q is %d wide", name, width, got, textwidth.Width(got))
		}
		if got == name || got == "" {
			return
		}
		// 差し込んだ ~ で分けると、前は元の名前の境界までの先頭、後ろは空か元の名前の末尾（拡張子）。
		// 元の名前にも ~ がありうるので、どこかの ~ でそう分けられればよい。
		for i := 0; i < len(got); i++ {
			if got[i] != '~' {
				continue
			}
			head, rest := got[:i], got[i+1:]
			if strings.HasPrefix(name, head) && boundary(name, len(head)) && (rest == "" || strings.HasSuffix(name, rest)) {
				return
			}
		}
		t.Fatalf("TruncName(%q, %d) = %q is not a head of the name, ~ and its extension", name, width, got)
	})
}

// boundary は、i が s の書記素クラスタの境界かを返す。
func boundary(s string, i int) bool {
	if i == 0 || i == len(s) {
		return true
	}
	n := 0
	for c := range textwidth.All(s) {
		n += len(c.Text)
		if n == i {
			return true
		}
	}
	return false
}

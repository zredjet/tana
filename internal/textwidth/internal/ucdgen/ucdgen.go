// Package ucdgen は、Unicode のデータ（internal/textwidth/ucd）から textwidth の表（tables.go）を作る。
// go generate（internal/textwidth/internal/gen）と、表が最新であることを確かめるテストから使う。
package ucdgen

import (
	"bufio"
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// 符号位置の性質の、ビットの割り当て。textwidth の props.go と同じ値にする。
const (
	GCBOther = iota
	GCBCR
	GCBLF
	GCBControl
	GCBExtend
	GCBZWJ
	GCBRegionalIndicator
	GCBPrepend
	GCBSpacingMark
	GCBL
	GCBV
	GCBT
	GCBLV
	GCBLVT
)

const (
	GCBMask = 0x000f

	InCBShift     = 4
	InCBMask      = 0x0030
	InCBConsonant = 1
	InCBExtend    = 2
	InCBLinker    = 3

	ExtPict       = 0x0040 // Extended_Pictographic
	EmojiModifier = 0x0080 // Emoji_Modifier（肌の色）

	WidthShift = 8
	WidthMask  = 0x0300
	WidthOne   = 0
	WidthZero  = 1
	WidthTwo   = 2

	Ambiguous        = 0x0400 // East Asian Width が A
	DefaultIgnorable = 0x0800 // Default_Ignorable_Code_Point
	Mark             = 0x1000 // 一般カテゴリが Mn・Me
	NewEmoji         = 0x2000 // Extended_Pictographic で、Unicode 16.0 より新しいか、未割り当て
	newEmojiAfterAge = "16.0"
	maxRune          = 0x10ffff
)

var gcbNames = map[string]int{
	"CR": GCBCR, "LF": GCBLF, "Control": GCBControl, "Extend": GCBExtend, "ZWJ": GCBZWJ,
	"Regional_Indicator": GCBRegionalIndicator, "Prepend": GCBPrepend, "SpacingMark": GCBSpacingMark,
	"L": GCBL, "V": GCBV, "T": GCBT, "LV": GCBLV, "LVT": GCBLVT,
}

var inCBNames = map[string]int{"Consonant": InCBConsonant, "Extend": InCBExtend, "Linker": InCBLinker}

// entry は、UCD のファイルの 1 行（範囲と、; で区切った値）。
type entry struct {
	lo, hi rune
	fields []string
}

// readUCD は、UCD の形式のファイルを読む。# 以降は注釈として捨てる。
func readUCD(path string) ([]entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []entry
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line, _, _ := strings.Cut(sc.Text(), "#")
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, ";")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		if len(parts) < 2 {
			return nil, fmt.Errorf("%s:%d: no field", path, n)
		}
		lo, hi, err := parseRange(parts[0])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		out = append(out, entry{lo, hi, parts[1:]})
	}
	return out, sc.Err()
}

func parseRange(s string) (rune, rune, error) {
	a, b, isRange := strings.Cut(s, "..")
	lo, err := strconv.ParseUint(a, 16, 32)
	if err != nil {
		return 0, 0, err
	}
	hi := lo
	if isRange {
		if hi, err = strconv.ParseUint(b, 16, 32); err != nil {
			return 0, 0, err
		}
	}
	if lo > hi || hi > maxRune {
		return 0, 0, fmt.Errorf("bad range %q", s)
	}
	return rune(lo), rune(hi), nil
}

var versionPattern = regexp.MustCompile(`(?m)^# (?:[A-Za-z]+-(\d+\.\d+\.\d+)\.txt|Version: (\d+\.\d+\.\d+))`)

// fileVersion は、UCD のファイルの先頭の注釈から版を得る。
func fileVersion(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	head := data[:min(len(data), 2000)]
	m := versionPattern.FindSubmatch(head)
	if m == nil {
		return "", fmt.Errorf("%s: no version line", path)
	}
	return string(m[1]) + string(m[2]), nil
}

// Files は、表を作るのに使う UCD のファイル（ucd のフォルダからの名前）。
var Files = []string{
	"GraphemeBreakProperty.txt",
	"emoji-data.txt",
	"EastAsianWidth.txt",
	"DerivedGeneralCategory.txt",
	"DerivedCoreProperties.txt",
	"DerivedAge.txt",
}

// Props は、dir の UCD のファイルから、すべての符号位置の性質と、Unicode の版を返す。
func Props(dir string) ([]uint16, string, error) {
	version := ""
	for _, name := range append(Files, "GraphemeBreakTest.txt") {
		v, err := fileVersion(filepath.Join(dir, name))
		if err != nil {
			return nil, "", err
		}
		if version == "" {
			version = v
		} else if v != version {
			return nil, "", fmt.Errorf("%s is Unicode %s, others are %s", name, v, version)
		}
	}
	p := make([]uint16, maxRune+1)
	load := func(name string, f func(e entry) error) error {
		entries, err := readUCD(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := f(e); err != nil {
				return fmt.Errorf("%s: %U..%U: %w", name, e.lo, e.hi, err)
			}
		}
		return nil
	}
	set := func(e entry, bits uint16) {
		for r := e.lo; r <= e.hi; r++ {
			p[r] |= bits
		}
	}
	if err := load("GraphemeBreakProperty.txt", func(e entry) error {
		g, ok := gcbNames[e.fields[0]]
		if !ok {
			return fmt.Errorf("unknown Grapheme_Cluster_Break %q", e.fields[0])
		}
		set(e, uint16(g))
		return nil
	}); err != nil {
		return nil, "", err
	}
	if err := load("emoji-data.txt", func(e entry) error {
		switch e.fields[0] {
		case "Extended_Pictographic":
			set(e, ExtPict)
		case "Emoji_Modifier":
			set(e, EmojiModifier)
		}
		return nil
	}); err != nil {
		return nil, "", err
	}
	// East Asian Width は、書いていない符号位置を N とする（ファイルの @missing と同じ）。
	eaw := make([]string, maxRune+1)
	if err := load("EastAsianWidth.txt", func(e entry) error {
		for r := e.lo; r <= e.hi; r++ {
			eaw[r] = e.fields[0]
		}
		return nil
	}); err != nil {
		return nil, "", err
	}
	gc := make([]string, maxRune+1)
	if err := load("DerivedGeneralCategory.txt", func(e entry) error {
		for r := e.lo; r <= e.hi; r++ {
			gc[r] = e.fields[0]
		}
		return nil
	}); err != nil {
		return nil, "", err
	}
	if err := load("DerivedCoreProperties.txt", func(e entry) error {
		switch e.fields[0] {
		case "Default_Ignorable_Code_Point":
			set(e, DefaultIgnorable)
		case "InCB":
			if len(e.fields) < 2 {
				return fmt.Errorf("InCB without a value")
			}
			v, ok := inCBNames[e.fields[1]]
			if !ok {
				return fmt.Errorf("unknown InCB %q", e.fields[1])
			}
			set(e, uint16(v<<InCBShift))
		}
		return nil
	}); err != nil {
		return nil, "", err
	}
	age := make([]string, maxRune+1)
	if err := load("DerivedAge.txt", func(e entry) error {
		for r := e.lo; r <= e.hi; r++ {
			age[r] = e.fields[0]
		}
		return nil
	}); err != nil {
		return nil, "", err
	}
	for r := rune(0); r <= maxRune; r++ {
		if gc[r] == "Mn" || gc[r] == "Me" {
			p[r] |= Mark
		}
		if eaw[r] == "A" {
			p[r] |= Ambiguous
		}
		if p[r]&ExtPict != 0 && (age[r] == "" || newerThan(age[r], newEmojiAfterAge)) {
			p[r] |= NewEmoji
		}
		// 幅の規則（tui §4）: 結合文字・異体字セレクタ・既定で無視される文字・ハングルの中声と終声の字母は 0、W・F は 2、ほかは 1。
		// 異体字セレクタは Mn で、既定で無視される文字でもある。
		switch {
		case p[r]&(Mark|DefaultIgnorable) != 0, 0x1160 <= r && r <= 0x11ff, 0xd7b0 <= r && r <= 0xd7ff:
			p[r] |= WidthZero << WidthShift
		case eaw[r] == "W" || eaw[r] == "F":
			p[r] |= WidthTwo << WidthShift
		}
	}
	return p, version, nil
}

// newerThan は、Unicode の版 a（"17.0" など）が b より新しいかを返す。
func newerThan(a, b string) bool {
	parse := func(s string) (int, int) {
		x, y, _ := strings.Cut(s, ".")
		i, _ := strconv.Atoi(x)
		j, _ := strconv.Atoi(y)
		return i, j
	}
	ai, aj := parse(a)
	bi, bj := parse(b)
	return ai > bi || ai == bi && aj > bj
}

// Generate は、dir の UCD のファイルから tables.go の内容を作る。
func Generate(dir string) ([]byte, error) {
	p, version, err := Props(dir)
	if err != nil {
		return nil, err
	}
	var starts []uint32
	var vals []uint16
	for r := range p {
		if r == 0 || p[r] != p[r-1] {
			starts = append(starts, uint32(r))
			vals = append(vals, p[r])
		}
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by go run ./internal/gen from Unicode %s (ucd/); DO NOT EDIT.\n\n", version)
	b.WriteString("package textwidth\n\n")
	fmt.Fprintf(&b, "// UnicodeVersion は、表を作った Unicode のデータの版。\nconst UnicodeVersion = %q\n\n", version)
	b.WriteString("// propStarts[i] から propStarts[i+1]-1 までの符号位置の性質が props[i]（props.go）。\n")
	b.WriteString("var propStarts = [...]uint32{")
	for i, s := range starts {
		if i%8 == 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "0x%05x, ", s)
	}
	b.WriteString("\n}\n\nvar props = [...]uint16{")
	for i, v := range vals {
		if i%8 == 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "0x%04x, ", v)
	}
	b.WriteString("\n}\n")
	return format.Source(b.Bytes())
}

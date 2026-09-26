package textwidth

import "slices"

//go:generate go run ./internal/gen

// 符号位置の性質（tables.go の props）のビットの割り当て。internal/ucdgen と同じ値（TestPropBits）。
const (
	gcbOther = iota // Grapheme_Cluster_Break
	gcbCR
	gcbLF
	gcbControl
	gcbExtend
	gcbZWJ
	gcbRI
	gcbPrepend
	gcbSpacingMark
	gcbL
	gcbV
	gcbT
	gcbLV
	gcbLVT
)

const (
	gcbMask = 0x000f

	incbShift     = 4 // Indic_Conjunct_Break
	incbMask      = 0x0030
	incbConsonant = 1
	incbExtend    = 2
	incbLinker    = 3

	extPict       = 0x0040 // Extended_Pictographic
	emojiModifier = 0x0080 // Emoji_Modifier（肌の色）

	widthShift = 8 // 幅の規則（tui §4）で数えた、符号位置の幅
	widthMask  = 0x0300
	widthOne   = 0
	widthZero  = 1
	widthTwo   = 2

	ambiguous        = 0x0400 // East Asian Width が A
	defaultIgnorable = 0x0800 // Default_Ignorable_Code_Point
	mark             = 0x1000 // 一般カテゴリが Mn・Me
	newEmoji         = 0x2000 // Extended_Pictographic で、Unicode 16.0 より新しいか、未割り当て
)

// lookup は、符号位置 r の性質を返す。範囲の外の値は 0（その他、幅 1）とする。
func lookup(r rune) uint16 {
	if r < 0 || r > 0x10ffff {
		return 0
	}
	i, found := slices.BinarySearch(propStarts[:], uint32(r))
	if !found {
		i--
	}
	return props[i]
}

// runeWidth は、幅の規則で数えた r の幅（0・1・2）を返す。
func runeWidth(p uint16) int {
	switch p & widthMask >> widthShift {
	case widthZero:
		return 0
	case widthTwo:
		return 2
	}
	return 1
}

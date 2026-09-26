package textwidth

import "unicode/utf8"

// clusterLen は、s の先頭の書記素クラスタのバイト数を返す（Unicode の UAX #29 の extended grapheme cluster）。
// 不正な UTF-8 のバイトは、1 バイトずつ独立した書記素クラスタにする（tui §4）。s が空なら 0 を返す。
func clusterLen(s string) int {
	if s == "" {
		return 0
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError && size == 1 {
		return 1
	}
	p := lookup(r)
	prev := p & gcbMask
	var st breakState
	st.update(p)
	i := size
	for i < len(s) {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			break // 不正なバイトの前で区切る
		}
		p := lookup(r)
		if st.isBreak(prev, p) {
			break
		}
		st.update(p)
		prev = p & gcbMask
		i += size
	}
	return i
}

// breakState は、直前の文字だけでは決まらない規則（GB9c、GB11、GB12・GB13）のための状態。
type breakState struct {
	incb  uint8 // GB9c: 1 なら、直前が InCB=Linker と、それに続く InCB=Extend だけ
	emoji uint8 // GB11: 0 なし、1 Extended_Pictographic Extend* の後、2 その後に ZWJ があった
	ri    int   // GB12・GB13: 直前まで続いている Regional_Indicator の数
}

// update は、符号位置の性質 p を状態に加える。
func (st *breakState) update(p uint16) {
	switch (p & incbMask) >> incbShift {
	case incbLinker:
		st.incb = 1
	case incbExtend:
		// Linker の後の Extend は、状態を変えない
	default:
		st.incb = 0
	}
	switch g := p & gcbMask; {
	case p&extPict != 0:
		st.emoji = 1
	case g == gcbExtend && st.emoji == 1:
	case g == gcbZWJ && st.emoji == 1:
		st.emoji = 2
	default:
		st.emoji = 0
	}
	if p&gcbMask == gcbRI {
		st.ri++
	} else {
		st.ri = 0
	}
}

// isBreak は、直前の Grapheme_Cluster_Break が prev の文字と、性質が p の文字の間で区切るかを返す（UAX #29 の GB3〜GB999）。
func (st *breakState) isBreak(prev, p uint16) bool {
	cur := p & gcbMask
	switch {
	case prev == gcbCR && cur == gcbLF: // GB3
		return false
	case prev == gcbControl || prev == gcbCR || prev == gcbLF: // GB4
		return true
	case cur == gcbControl || cur == gcbCR || cur == gcbLF: // GB5
		return true
	case prev == gcbL && (cur == gcbL || cur == gcbV || cur == gcbLV || cur == gcbLVT): // GB6
		return false
	case (prev == gcbLV || prev == gcbV) && (cur == gcbV || cur == gcbT): // GB7
		return false
	case (prev == gcbLVT || prev == gcbT) && cur == gcbT: // GB8
		return false
	case cur == gcbExtend || cur == gcbZWJ: // GB9
		return false
	case cur == gcbSpacingMark: // GB9a
		return false
	case prev == gcbPrepend: // GB9b
		return false
	case (p&incbMask)>>incbShift == incbConsonant && st.incb == 1: // GB9c（Unicode 18.0: Linker Extend* × Consonant）
		return false
	case prev == gcbZWJ && p&extPict != 0 && st.emoji == 2: // GB11
		return false
	case prev == gcbRI && cur == gcbRI && st.ri%2 == 1: // GB12・GB13
		return false
	}
	return true // GB999
}

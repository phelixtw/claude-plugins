package difflib

import (
	"math"
	"strings"
	"testing"
)

// Expected values come from CPython: difflib.SequenceMatcher(None, a, b).ratio().
func TestRatioMatchesPython(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"abcd", "bcde", 0.75},
		{"", "", 1.0},
		{"a", "", 0.0},
		{"", "a", 0.0},
		{"same", "same", 1.0},
		{"中文测试", "中文試験", 0.5},
		{"private Thread currentThread;", "private volatile Thread currentThread;", 0.8656716417910447},
		{strings.Repeat("abcdefghij", 30), strings.Repeat("abcdefghij", 29) + "xyzxyzxyz", 0.9682804674457429},
		// b has 250 elements, so autojunk drops the popular 'a' and 'b' from the index.
		// several blocks: the matcher recurses left and right of the longest one
		{"abXcdYef", "abZcdWef", 0.75},
		{"qabxcd", "abycdq", 0.6666666666666666},
		// popular elements are extended over around a match found through rare ones
		{"xyz" + strings.Repeat("a", 5) + "q", "pq" + strings.Repeat("a", 300) + "xyz", 0.01910828025477707},
		{"k" + strings.Repeat("a", 20) + "m", strings.Repeat("a", 5) + "k" + strings.Repeat("a", 200) + "m" + strings.Repeat("a", 3), 0.1896551724137931},
		{"xyABCD", "yxABCDx", 0.7692307692307693},                                                 // left part ends before later indexes of x
		{"aaZaa", strings.Repeat("a", 100) + "Z" + strings.Repeat("a", 100), 0.04854368932038835}, // extended over popular a on both sides
		// random pairs over a skewed alphabet (autojunk active), values from CPython
		{"fddddaedaaaaeaacgaaagafacgaaagdabcgceaae", "aaadaagbaacabaaaaaaaabaagcaaddcaccaaaabgaaaabacabgdaaccdaaabeacacaadbafaacaaaafaefaacabaaeaacaabaafaaaaadafbcfgaaeacacfaagaaaedaaeeadcdgaaeadaaaaacaaaafaaeaaagaaaaabaagagbaeaadaaaaaaadaaagcaaaaaabaccaaegbcddeaa", 0.0},
		{"afaaabaafefaaecgfgfabeffefafbfaeadadecaa", "aaaaaaaeaeaageabaabaaebafbadgaegabaaafabbfbadacfffgafagaefaabaaeaafaaaaecaafeaaaaaaaaaaaccgaadafdagdaafefaaaafdaafeaaaeaeaaaaacafdacgcadaabbaaafedabeaagaggaaaaabafcaabagaaeaadcgbagbababbagafacaffaaaaceabaadbbba", 0.008},
		{"caacdaebfaafaabaadgadabbaagfagdceabgaafd", "aaadafafaeddaaaaaaeaaaadgaaeabaaaaaaaeaaabaaeaaabcabaafaaaaaafaafagagdgaaabbcaeaaaafeaaaaadafaacgaaagaaaabaacaabeaaaaaaaadabfaaabdaaafaaaaaebbabaaaadgdadabgabaeaaaageedaaaagaaadeaaaaadgagbdacaeaaaaaaaaaaabaaaaa", 0.0},
		{"dadffaaadegabeafaaedcbbbbdabefdaaaaafefa", "eefbabfbcggfagdcfededaaaaadaaagabadadbdaaaaafaebbadbaeeaafagaaefaaedaagaaadafacddaacaaadeeaccaaaaaadaeadaaebaaaafabaaaaaaaagbaaaaaacaaebaaacgdbaaeaaaaaaaaadaaaeaaaaaagaaafagaaaeaeaaaaaagcaaafagaaaagdadaaaabaafa", 0.0},
		{"aaadecaaacaafeabdcceaaaabacdafadcbdaaeac", "aafaaabaaaaabaaaaafcaaegaaaebaaaafaaacaadbbdfggaaaaaadaaagggaaaaeffacaaaagbgaaafaaaabdageedgfaabfaafaacadeaadabdaefaaaabcaaaafcaabaaaadgaababaaaedaaaaaddaaaadaaaaaeaeaadaaafaegbaaaaafgaaaaafaacacaaaadacaaaaacaa", 0.016},
		{"acdcagbaaaefeadadfafaadbdbbdabgcddacadda", "baaaaeaadaafdfaaaaaafaaaeacaaaacaaeeeaaaaefcfdaagaaaeafafaagaaaaafeagefacaagaaaffcabaafaaaadaabbaaaaaacaaaaaeaaaaaacdaebgfdfafgaaacaaaeaaaaaaaaacaaaaaabbadfadaaaaagagaaaaaaaacgcaaabgaacaffdaadcecaaaaaaaaaacedag", 0.0},
		{"abcdabafaceffgabfdcbdcgaccaeaagabfbbgcaa", "aaaaagaacaafaaaabadfaaccaebaaaaabaaaaaffffaaagaaaacdaaecegadfgacacgagaacaaabaaaaaaegaabgfdadgaaacabgdfadaacaaadaabaaaacaaaafcfgagafaaaaaaaaafabbdaadaaeafebaafbadfaagaceegaaaaaffadfeaagacfaaacagaaabaacacbaaaaaaa", 0.008},
		{"aaaagcafadaaaaaaaabeaaaabccdbacbbaccgfeb", "aaacdaabaaaaacdaaaacaaababaacacaaacgaaaafaeaaaadagdfaafaaadgbacdcacbeaaaaaabaaaaaafaacbdaaaabcdbddagcabaaadaefaebaagaeaaedaaaaaadaaaeegaeaadbdadfbaadaabaaaageaaeaaaacaagdedgbaagbeagaaeacafaaccacaaaaaacaaaeaaaae", 0.024},
		{strings.Repeat("a", 250), strings.Repeat("a", 100) + strings.Repeat("b", 150), 0.4},
	} {
		if got := Ratio(c.a, c.b); math.Abs(got-c.want) > 1e-15 {
			t.Errorf("Ratio(%.12q, %.12q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestRatioCountsCodePoints(t *testing.T) {
	// One emoji is one element, not four bytes: 2*1/(2+2).
	if got := Ratio("\U0001F600a", "\U0001F600b"); got != 0.5 {
		t.Errorf("Ratio = %v, want 0.5", got)
	}
}

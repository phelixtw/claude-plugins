package pyre

import (
	"reflect"
	"testing"
)

func TestSearchAndSpanAreByteOffsets(t *testing.T) {
	// Python: finditer spans (0, 7) and (10, 15) in code points.
	r := Compile(`(\w+)@(\d+)`)
	s := "中文ab@12 x c3@45\n"
	ms := r.Iter(s)
	if len(ms) != 2 {
		t.Fatalf("matches = %d, want 2", len(ms))
	}
	if ms[0].Group(0) != "中文ab@12" || ms[0].Group(1) != "中文ab" || ms[0].Group(2) != "12" || ms[0].Groups() != 2 {
		t.Errorf("first match: %q %q %q", ms[0].Group(0), ms[0].Group(1), ms[0].Group(2))
	}
	if a, b := ms[1].Span(0); s[a:b] != "c3@45" {
		t.Errorf("second span covers %q", s[a:b])
	}
	if a, b := ms[0].Span(1); a != 0 || b != len("中文ab") {
		t.Errorf("Span(1) = %d,%d", a, b)
	}
	if got := r.Search("zz a1@2"); got == nil || got.Group(0) != "a1@2" {
		t.Error("Search")
	}
	if r.Search("none") != nil || r.Has("none") || !r.Has("x@1") {
		t.Error("Search/Has miss")
	}
}

func TestMatchStartVsFullMatch(t *testing.T) {
	r := Compile(`a+`)
	if r.MatchStart("baa") != nil {
		t.Error("re.match must anchor at the start")
	}
	if m := r.MatchStart("aab"); m == nil || m.Group(0) != "aa" {
		t.Error("MatchStart prefix")
	}
	if r.FullMatch("aab") != nil || r.FullMatch("aa") == nil {
		t.Error("re.fullmatch must cover the whole string")
	}
	// Python: re.fullmatch(r"a|ab", "ab") backtracks into the second branch.
	if Compile(`a|ab`).FullMatch("ab") == nil {
		t.Error("fullmatch must backtrack across alternatives")
	}
}

func TestGroupParticipation(t *testing.T) {
	m := Compile(`(a)|(b)`).Search("b")
	if m.Has(1) || !m.Has(2) || m.Group(1) != "" || m.Group(2) != "b" {
		t.Error("group 1 did not take part (Python: None)")
	}
	if a, b := m.Span(1); a != -1 || b != -1 {
		t.Errorf("Span of a missing group = %d,%d", a, b)
	}
	if m.Has(9) || m.Group(9) != "" {
		t.Error("out-of-range group")
	}
}

func TestFlags(t *testing.T) {
	if !Compile(`HELLO`, I).Has("hello") || Compile(`HELLO`).Has("hello") {
		t.Error("IGNORECASE")
	}
	if !Compile(`a.b`, S).Has("a\nb") || Compile(`a.b`).Has("a\nb") {
		t.Error("DOTALL")
	}
	if got := Compile(`^\w`, M).FindAll("ab\ncd"); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Errorf("MULTILINE FindAll = %v", got)
	}
	if !Compile(`a.b`, S|I).Has("A\nB") {
		t.Error("combined flags")
	}
}

func TestPythonSemantics(t *testing.T) {
	// $ matches before a final newline in Python.
	if !Compile(`^b$`).Has("b\n") {
		t.Error("$ before final newline")
	}
	if m := Compile(`\bfoo\b`).Search("é foo é"); m == nil {
		t.Error("Unicode word boundary")
	}
	if !Compile(`\w`).Has("é") || !Compile(`\d`).Has("٣") {
		t.Error("Unicode \\w and \\d")
	}
	if got := Compile(`\s+`).Sub("a  b\t　c", "-"); got != "a-b-c" {
		t.Errorf("Unicode \\s Sub = %q", got)
	}
	if m := Compile(`(?<=x)y`).Search("xy"); m == nil || m.Group(0) != "y" {
		t.Error("lookbehind")
	}
	if got := Compile(`(?=b)`).Sub("abab", "|"); got != "a|ba|b" {
		t.Errorf("empty-match Sub = %q (Python: a|ba|b)", got)
	}
	if got := Compile(`a*`).FindAll("baac"); !reflect.DeepEqual(got, []string{"", "aa", "", ""}) {
		t.Errorf("empty matches FindAll = %q", got)
	}
}

func TestFindAllShapes(t *testing.T) {
	if got := Compile(`\d+`).FindAll("a1b22c"); !reflect.DeepEqual(got, []string{"1", "22"}) {
		t.Errorf("no group = %v", got)
	}
	if got := Compile(`x(\d)`).FindAll("x1 x2"); !reflect.DeepEqual(got, []string{"1", "2"}) {
		t.Errorf("one group = %v", got)
	}
	if got := Compile(`(\d)(\w)`).FindAllGroups("1a2b"); !reflect.DeepEqual(got, [][]string{{"1", "a"}, {"2", "b"}}) {
		t.Errorf("groups = %v", got)
	}
	if Compile(`z`).FindAll("abc") != nil || len(Compile(`z`).Iter("abc")) != 0 {
		t.Error("no match")
	}
}

func TestSubAndSplit(t *testing.T) {
	r := Compile(`\d`)
	if got := r.SubFunc("a1b2", func(m *Match) string { return "<" + m.Group(0) + ">" }); got != "a<1>b<2>" {
		t.Errorf("SubFunc = %q", got)
	}
	if got := Compile(`,\s*`).Split("a, b,c,"); !reflect.DeepEqual(got, []string{"a", "b", "c", ""}) {
		t.Errorf("Split = %q", got)
	}
	if got := Compile(`,`).Split("abc"); !reflect.DeepEqual(got, []string{"abc"}) {
		t.Errorf("Split without match = %q", got)
	}
	// Non-ASCII text between matches survives (byte offsets, not rune offsets).
	if got := Compile(`\d`).Sub("中1文2", ""); got != "中文" {
		t.Errorf("Sub on multibyte = %q", got)
	}
}

func TestCompilePanicsOnBadPattern(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Compile must panic on a bad pattern")
		}
	}()
	Compile(`(`)
}

func TestLiteralZ(t *testing.T) {
	// \z is the end-of-text anchor Python spells \Z.
	if !Compile(`a\z`).Has("a") || Compile(`a\z`).Has("a\n") {
		t.Error(`\z`)
	}
}

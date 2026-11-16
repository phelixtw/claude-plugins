package py

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"agentperform/internal/ojson"
)

func TestFloatRepr_Extremes(t *testing.T) {
	for f, want := range map[float64]string{
		1e21: "1e+21", 123456789012345680.0: "1.2345678901234568e+17", 9007199254740992: "9007199254740992.0",
		5e-324: "5e-324", math.MaxFloat64: "1.7976931348623157e+308", math.Copysign(0, -1): "-0.0", 0: "0.0",
		math.Inf(-1): "-inf", math.NaN(): "nan",
	} {
		if got := FloatRepr(f); got != want {
			t.Errorf("FloatRepr(%v) = %s, want %s", f, got, want)
		}
	}
}

func TestRound_Special(t *testing.T) {
	if got := Round(2.5, 0); got != 2 {
		t.Errorf("round(2.5) = %v, want 2 (ties to even)", got)
	}
	if got := Round(-1.5, 0); got != -2 {
		t.Errorf("round(-1.5) = %v", got)
	}
	if !math.IsNaN(Round(math.NaN(), 2)) || !math.IsInf(Round(math.Inf(1), 2), 1) {
		t.Error("Round must pass NaN/Inf through")
	}
}

func TestStringHelpers(t *testing.T) {
	if got := CollapseSpace("a \t\n b　　c  "); got != "a b c " {
		t.Errorf("CollapseSpace = %q", got)
	}
	if LStrip("\x1f  \n x ") != "x " {
		t.Error("LStrip")
	}
	if Len("\U0001F600a") != 2 {
		t.Error("Len counts code points")
	}
	if Head("\U0001F600ab", 2) != "\U0001F600a" || Head("ab", 0) != "" || Head("ab", -1) != "" {
		t.Error("Head")
	}
	if Tail("ab\U0001F600", 1) != "\U0001F600" || Tail("abc", 5) != "abc" || Tail("abc", -1) != "abc" {
		t.Error("Tail")
	}
	if got := Split(" a　b\t\nc "); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("Split = %q", got)
	}
}

func TestSplitlines_Python(t *testing.T) {
	got := Splitlines("a\r\n\r\nb\x0bc\x1d\u2029d\u0085")
	if !reflect.DeepEqual(got, []string{"a", "", "b", "c", "", "d"}) {
		t.Errorf("Splitlines = %q", got)
	}
	if len(Splitlines("")) != 0 {
		t.Error(`"".splitlines() is []`)
	}
	if !reflect.DeepEqual(Splitlines("\n"), []string{""}) || !reflect.DeepEqual(Splitlines("x\n\n"), []string{"x", ""}) {
		t.Error("trailing newline handling")
	}
}

func TestFormatters(t *testing.T) {
	cases := []struct{ got, want string }{
		{Comma(1234567), "1,234,567"}, {Comma(-1234), "-1,234"}, {Comma(999), "999"}, {Comma(0), "0"},
		{CommaF(1234.5, 2), "1,234.50"}, {CommaF(-1234567.891, 1), "-1,234,567.9"}, {CommaF(12, 0), "12"},
		{CommaF(math.Inf(1), 1), "inf"}, {CommaF(math.NaN(), 1), "nan"},
		{Pct(0.256, 1), "25.6%"},
		{G(1234.5678), "1234.57"}, {G(1e-5), "1e-05"}, {G(1e20), "1e+20"}, {G(math.Inf(-1)), "-inf"},
		{SignedF(0.5, 0), "+0"}, {SignedF(-0.5, 1), "-0.5"},
		{F(math.Inf(1), 2), "inf"},
		{CommaNum(1234567.0, false), "1,234,567.0"}, {CommaNum(-1234.5, false), "-1,234.5"}, {CommaNum(1e-5, false), "1e-05"},
		{CommaNum(1e22, false), "1e+22"}, {CommaNum(1234567, true), "1,234,567"}, {CommaNum(0.00001234, false), "1.234e-05"},
		{NumStr(3, true), "3"}, {NumStr(3, false), "3.0"},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Errorf("case %d: got %q, want %q", i, c.got, c.want)
		}
	}
}

func TestNumberHelpers(t *testing.T) {
	for in, want := range map[string]string{"7": "7", "+7": "7", "12345678901234567890123": "12345678901234567890123", "2.50": "2.5", "1e5": "100000.0", "1E-7": "1e-07"} {
		if got := Num(in); got != want {
			t.Errorf("Num(%s) = %s, want %s", in, got, want)
		}
	}
	if !IsInt(json.Number("3")) || IsInt(json.Number("3.0")) || IsInt(json.Number("1e3")) || !IsInt(3) || !IsInt(int64(3)) || IsInt("3") || IsInt(2.0) {
		t.Error("IsInt")
	}
	for _, c := range []struct {
		v    any
		want float64
		ok   bool
	}{{json.Number("2.5"), 2.5, true}, {1.5, 1.5, true}, {3, 3, true}, {int64(4), 4, true}, {true, 1, true}, {false, 0, true}, {"x", 0, false}, {nil, 0, false}} {
		if f, ok := Number(c.v); f != c.want || ok != c.ok {
			t.Errorf("Number(%v) = %v,%v", c.v, f, ok)
		}
	}
}

func TestRepr_Python(t *testing.T) {
	for in, want := range map[string]string{
		"it's":   `"it's"`,
		"a\"b'c": `'a"b\'c'`,
		"tab\t\x01\x7f " + string(rune(0xe9)) + " " + string(rune(0x200b)) + " " + string(rune(0xa0)) + " " + string(rune(0x4e2d)) + " " + string(rune(0x1f600)) + " " + string(rune(0xd7ff)) + `\`: `'tab\t\x01\x7f ` + string(rune(0xe9)) + ` \u200b \xa0 ` + string(rune(0x4e2d)) + " " + string(rune(0x1f600)) + ` \ud7ff\\'`,
		string(rune(0x85)) + string(rune(0x2028)) + "\r\n": `'\x85\u2028\r\n'`,
		string(rune(0xe0001)):                              `'\U000e0001'`,
	} {
		if got := StrRepr(in); got != want {
			t.Errorf("StrRepr(%q) = %s, want %s", in, got, want)
		}
	}
	v, _ := ojson.Parse([]byte(`{"a": [1, 2.5, 1e3, null, true, false, "x"], "b": {}}`))
	if got := Repr(v); got != `{'a': [1, 2.5, 1000.0, None, True, False, 'x'], 'b': {}}` {
		t.Errorf("Repr = %s", got)
	}
	if Repr(7) != "7" || Repr(int64(8)) != "8" || Repr(1.5) != "1.5" || Repr(struct{}{}) != "{}" {
		t.Error("Repr scalars")
	}
	if Str("raw") != "raw" || Str(nil) != "None" {
		t.Error("Str")
	}
}

func TestDumps_Python(t *testing.T) {
	v, _ := ojson.Parse([]byte(`{"b":1,"a":[1e22,"\u00e9\u007f\ud83d\ude00"]}`))
	if got, want := DumpsSorted(v), "{\"a\": [1e+22, \"\u00e9\x7f\U0001F600\"], \"b\": 1}"; got != want {
		t.Errorf("DumpsSorted = %q, want %q", got, want)
	}
	if got := DumpsASCII([]any{"\u00e9\x7f\U0001F600\x01"}); got != `["\u00e9\u007f\ud83d\ude00\u0001"]` {
		t.Errorf("DumpsASCII = %s", got)
	}
	got := Dumps([]any{math.NaN(), math.Inf(1), math.Inf(-1), 5, int64(6), 0.5, []string{"a"}, []float64{1, 2.5}, json.Number("3"), nil, false, map[string]int{"k": 1}})
	if want := `[NaN, Infinity, -Infinity, 5, 6, 0.5, ["a"], [1.0, 2.5], 3, null, false, {"k":1}]`; got != want {
		t.Errorf("Dumps = %s, want %s", got, want)
	}
	if DumpsIndent([]any{}, 2) != "[]" || DumpsIndent(ojson.NewObject(), 2) != "{}" || DumpsIndent(true, 2) != "true" {
		t.Error("DumpsIndent empties")
	}
}

func TestCounter(t *testing.T) {
	c := NewCounter()
	for _, k := range []string{"x", "y", "z", "y", "x", "w"} {
		c.Add(k, 1)
	}
	c.Add("z", 3)
	// Counter.most_common keeps insertion order on ties.
	if got := c.MostCommon(-1); !reflect.DeepEqual(got, []string{"z", "x", "y", "w"}) {
		t.Errorf("MostCommon = %v", got)
	}
	if got := c.MostCommon(2); !reflect.DeepEqual(got, []string{"z", "x"}) {
		t.Errorf("MostCommon(2) = %v", got)
	}
	if c.Total() != 9 || c.Len() != 4 {
		t.Errorf("Total/Len = %d/%d", c.Total(), c.Len())
	}
	cl := c.Clone()
	cl.Add("x", 10)
	if c.N["x"] != 2 {
		t.Error("Clone must not share the map")
	}
	raw, _ := c.GobEncode()
	var d Counter
	if err := d.GobDecode(raw); err != nil || !reflect.DeepEqual(d.Keys, c.Keys) || d.N["z"] != 4 {
		t.Errorf("gob round trip: %v %+v", err, d)
	}
	var empty Counter
	raw, _ = NewCounter().GobEncode()
	if err := empty.GobDecode(raw); err != nil || empty.N == nil {
		t.Error("an empty counter must decode with a usable map")
	}
	for _, bad := range [][]byte{nil, {2}, {1, 5, 'a'}, {1, 1, 'a'}} {
		if err := new(Counter).GobDecode(bad); err == nil {
			t.Errorf("GobDecode(%v) should fail", bad)
		}
	}
}

func TestHTMLEscape(t *testing.T) {
	if got := HTMLEscape(`<a href='x'>&"</a>`); got != "&lt;a href=&#x27;x&#x27;&gt;&amp;&quot;&lt;/a&gt;" {
		t.Errorf("HTMLEscape = %s", got)
	}
	if HTMLEscape("plain") != "plain" {
		t.Error("plain")
	}
}

func TestShlexSplit(t *testing.T) {
	got, err := ShlexSplit(`a 'b c' "d\"e\f" g\ h '' x"y z"`)
	if want := []string{"a", "b c", `d"e\f`, "g h", "", "xy z"}; err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ShlexSplit = %q, %v", got, err)
	}
	for in, msg := range map[string]string{"a 'b": "No closing quotation", `a\`: "No escaped character", `"a\`: "No escaped character"} {
		if _, err := ShlexSplit(in); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("ShlexSplit(%q) err = %v, want %s", in, err, msg)
		}
	}
	if got, _ := ShlexSplit("  "); len(got) != 0 {
		t.Errorf("blank = %q", got)
	}
}

package ojson

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestParse_Errors(t *testing.T) {
	for _, in := range []string{
		"", "{", "[1,", `{"a"`, `{"a":`, `{"a":1`, `{a:1}`, `{"a" 1}`, `{"a":1 "b":2}`, "[1 2]", "[1,]x",
		`"abc`, `"a\`, `"a\x"`, `"a\u12"`, `"a\u12zz"`, "\"a\nb\"", "tru", "nul", "falsy", "-", "-x", "01x", "1.", "1.x", "1e", "1e+", "{} {}", "1 2", "?",
	} {
		if v, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) = %v, want error", in, v)
		}
	}
}

func TestParse_Values(t *testing.T) {
	v, err := Parse([]byte(" \t\n{\"a\" : [ ] , \"b\":{ }, \"a\": \"dup\", \"n\": -0.5e+2, \"s\": \"\\b\\f\\n\\r\\t\\/\\\\\\\"\\u00e9\\ud83d\\ude00\" } \n"))
	if err != nil {
		t.Fatal(err)
	}
	o := Obj(v)
	// Python dict: a duplicate key keeps its first position and takes the last value.
	if strings.Join(o.Keys, ",") != "a,b,n,s" || Field(v, "a") != "dup" {
		t.Errorf("keys = %v, a = %v", o.Keys, Field(v, "a"))
	}
	if Field(v, "s") != "\b\f\n\r\t/\\\"é\U0001F600" {
		t.Errorf("s = %q", Field(v, "s"))
	}
	if f, ok := Float(Field(v, "n")); !ok || f != -50 {
		t.Errorf("n = %v %v", f, ok)
	}
}

func TestParse_InvalidUTF8AndSurrogates(t *testing.T) {
	v, err := Parse([]byte("\"a\xffb\xe4\xb8\""))
	if err != nil || v != "a�b��" {
		t.Errorf("invalid UTF-8: %q %v", v, err)
	}
	// A lone surrogate escape cannot be a Go string: it becomes U+FFFD and the next escape stands alone.
	v, _ = Parse([]byte(`"\ud800A"`))
	if v != "�A" {
		t.Errorf("lone surrogate: %q", v)
	}
	v, _ = Parse([]byte(`"\udc00"`))
	if v != "�" {
		t.Errorf("lone low surrogate: %q", v)
	}
}

func TestObjectNilSafe(t *testing.T) {
	var o *Object
	if _, ok := o.Get("a"); ok || o.Len() != 0 {
		t.Error("nil object")
	}
	if Obj("x") != nil || Field("x", "a") != nil || Has("x", "a") || Has(NewObject().Set("k", nil), "k") != true {
		t.Error("Obj/Field/Has on non-objects")
	}
	n := NewObject().Set("a", 1).Set("b", 2).Set("a", 3)
	if strings.Join(n.Keys, "") != "ab" || n.Vals["a"] != 3 {
		t.Error("Set replaces in place")
	}
}

func TestStrFloatTruthy(t *testing.T) {
	if s, ok := Str("x"); !ok || s != "x" {
		t.Error("Str")
	}
	if _, ok := Str(1); ok {
		t.Error("Str on non-string")
	}
	if f, ok := Float(json.Number("1e999")); !ok || !math.IsInf(f, 1) {
		t.Errorf("out of range number is inf: %v %v", f, ok)
	}
	if _, ok := Float("x"); ok {
		t.Error("Float on string")
	}
	if _, ok := Float(json.Number("abc")); ok {
		t.Error("Float on bad number")
	}
	for v, want := range map[string]bool{"0": false, "0.0": false, "-0": false, "1": true, "0.1": true} {
		if Truthy(json.Number(v)) != want {
			t.Errorf("Truthy(%s) != %v", v, want)
		}
	}
	if Truthy(false) || !Truthy(true) || Truthy("") || !Truthy("0") || Truthy([]any{}) || !Truthy([]any{nil}) || Truthy(NewObject()) || !Truthy(1.5) {
		t.Error("Truthy")
	}
}

func TestPyStrAndCompact(t *testing.T) {
	v, _ := Parse([]byte(`[1, 2.50, "é", {"k": null}]`))
	if got := PyStr(v); got != `[1,2.5,"é",{"k":null}]` {
		t.Errorf("PyStr(list) = %s", got)
	}
	if PyStr(false) != "False" || PyStr("s") != "s" {
		t.Error("PyStr scalars")
	}
}

func TestCompactScalarsAndEscapes(t *testing.T) {
	got := Compact([]any{nil, true, false, 7, int64(8), 2.5, json.Number("1.50"), "q\"\\\n\r\t\b\f\x01\x1f\x7fé", map[string]int{"z": 1}})
	want := "[null,true,false,7,8,2.5,1.5,\"q\\\"\\\\\\n\\r\\t\\b\\f\\u0001\\u001f\x7fé\",{\"z\":1}]"
	if got != want {
		t.Errorf("Compact =\n%s\nwant\n%s", got, want)
	}
}

func TestNum(t *testing.T) {
	for in, want := range map[string]string{
		"5": "5", "+5": "5", "-5": "-5", "18446744073709551615": "18446744073709551615",
		"123456789012345678901234567890": "1.2345678901234568e29", "1.0": "1.0", "2.50": "2.5", "1e2": "100.0", "1e999": "null",
	} {
		if got := Num(in); got != want {
			t.Errorf("Num(%s) = %s, want %s", in, got, want)
		}
	}
	if Num("garbage") != "garbage" {
		t.Error("unparseable number is passed through")
	}
}

func TestRyu_Specials(t *testing.T) {
	for f, want := range map[float64]string{
		0: "0.0", math.Copysign(0, -1): "-0.0", math.NaN(): "null", math.Inf(1): "null", math.Inf(-1): "null",
		1e22: "1e22", 1.5e22: "1.5e22", 123456789012345680: "1.2345678901234568e17", 1.2345678901234567e16: "1.2345678901234568e16",
		5e-324: "5e-324", 0.00012: "0.00012", 1e-5: "0.00001", -1e-7: "-1e-7",
	} {
		if got := Ryu(f); got != want {
			t.Errorf("Ryu(%v) = %s, want %s", f, got, want)
		}
	}
}

func TestIndentNested(t *testing.T) {
	v, _ := Parse([]byte(`{"a":[],"b":{"c":[1,{}]}}`))
	if got, want := Indent(v, 2), "{\n  \"a\": [],\n  \"b\": {\n    \"c\": [\n      1,\n      {}\n    ]\n  }\n}"; got != want {
		t.Errorf("Indent =\n%s\nwant\n%s", got, want)
	}
}

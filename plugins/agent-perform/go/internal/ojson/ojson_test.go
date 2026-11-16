package ojson

import "testing"

func TestRoundTrip(t *testing.T) {
	in := `{"b": 1, "a": [1.5, "x\né", null, true], "c": {}, "d": 1e20, "e": 0.000012}`
	v, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := Compact(v), `{"b":1,"a":[1.5,"x\né",null,true],"c":{},"d":1e20,"e":0.000012}`; got != want {
		t.Errorf("Compact = %s\nwant      %s", got, want)
	}
	want := "{\n \"b\": 1,\n \"a\": [\n  1.5,\n  \"x\\né\",\n  null,\n  true\n ],\n \"c\": {},\n \"d\": 1e20,\n \"e\": 0.000012\n}"
	if got := Indent(v, 1); got != want {
		t.Errorf("Indent = %s\nwant     %s", got, want)
	}
}

func TestRyu(t *testing.T) {
	for f, want := range map[float64]string{
		3: "3.0", 1e15: "1000000000000000.0", 1e16: "1e16", 0.1: "0.1", 1234.5678: "1234.5678",
		0.0001: "0.0001", 0.00001: "0.00001", 0.000001: "1e-6", 0.0000015: "1.5e-6", 1760870000.25: "1760870000.25", -2.5: "-2.5", 1.5e300: "1.5e300",
	} {
		if got := Ryu(f); got != want {
			t.Errorf("Ryu(%v) = %s, want %s", f, got, want)
		}
	}
}

func TestTruthyAndPyStr(t *testing.T) {
	v, _ := Parse([]byte(`{"z": 0, "s": "", "a": [], "o": {}, "n": null, "t": 2}`))
	for _, k := range []string{"z", "s", "a", "o", "n", "missing"} {
		if GetOr(v, k) != nil {
			t.Errorf("%s should be falsy", k)
		}
	}
	if PyStr(GetOr(v, "t")) != "2" || PyStr(nil) != "None" || PyStr(true) != "True" {
		t.Error("PyStr")
	}
}

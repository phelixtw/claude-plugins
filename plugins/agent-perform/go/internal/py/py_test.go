package py

import (
	"math"
	"reflect"
	"testing"

	"agentperform/internal/ojson"
)

func TestFloatRepr(t *testing.T) {
	for f, want := range map[float64]string{
		1: "1.0", 0.1: "0.1", 1e16: "1e+16", 1e15: "1000000000000000.0", 1.5e16: "1.5e+16",
		0.0001: "0.0001", 0.00001: "1e-05", 1.5e-7: "1.5e-07", 123.456: "123.456", -2.5: "-2.5",
		1792400000.25: "1792400000.25", 1e100: "1e+100", math.Inf(1): "inf",
	} {
		if got := FloatRepr(f); got != want {
			t.Errorf("FloatRepr(%v) = %s, want %s", f, got, want)
		}
	}
}

func TestRound(t *testing.T) {
	for _, c := range []struct {
		x    float64
		n    int
		want float64
	}{{2.675, 2, 2.67}, {0.125, 2, 0.12}, {0.375, 2, 0.38}, {1.005, 2, 1.0}, {0.65551234, 4, 0.6555}} {
		if got := Round(c.x, c.n); got != c.want {
			t.Errorf("Round(%v, %d) = %v, want %v", c.x, c.n, got, c.want)
		}
	}
}

func TestStrings(t *testing.T) {
	if Strip("\x1c 　 a b \n") != "a b" {
		t.Error("Strip")
	}
	if Head("中文abc", 3) != "中文a" || Tail("中文abc", 2) != "bc" || Tail("ab", 0) != "ab" || Head("ab", 9) != "ab" {
		t.Error("Head/Tail")
	}
	if !reflect.DeepEqual(Splitlines("a\r\nb c\rd\n"), []string{"a", "b", "c", "d"}) {
		t.Error("Splitlines")
	}
}

func TestDumps(t *testing.T) {
	v, _ := ojson.Parse([]byte(`{"a": [1, 2.50, "x\"y"], "b": {}, "c": 1e5, "d": null, "e": true}`))
	if got := Dumps(v); got != `{"a": [1, 2.5, "x\"y"], "b": {}, "c": 100000.0, "d": null, "e": true}` {
		t.Errorf("Dumps = %s", got)
	}
	if got := DumpsIndent(v, 2); got != "{\n  \"a\": [\n    1,\n    2.5,\n    \"x\\\"y\"\n  ],\n  \"b\": {},\n  \"c\": 100000.0,\n  \"d\": null,\n  \"e\": true\n}" {
		t.Errorf("DumpsIndent = %s", got)
	}
}

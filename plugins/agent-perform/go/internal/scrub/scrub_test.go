package scrub

import (
	"testing"

	"agentperform/internal/ojson"
)

func parse(t *testing.T, s string) any {
	t.Helper()
	v, err := ojson.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestRemoveKey(t *testing.T) {
	o := ojson.Obj(parse(t, `{"a":1,"b":2,"c":3}`))
	RemoveKey(o, "b")
	RemoveKey(o, "missing")
	if got := ojson.Compact(o); got != `{"a": 1, "c": 3}` && got != `{"a":1,"c":3}` {
		t.Errorf("got %s", got)
	}
	if len(o.Keys) != 2 {
		t.Errorf("keys = %v", o.Keys)
	}
}

// Delegates drops the task text and script bodies but keeps the numbers.
func TestDelegates(t *testing.T) {
	blob := parse(t, `{"s1":{"launches":[{"what":"fix the login bug for ACME","script_calls":["rm x"],"tokens":42,"agent":"impl"},{"what":"x","tokens":7}]},"s2":{"other":1},"s3":5}`)
	Delegates(blob)
	l := ojson.GetOr(ojson.GetOr(blob, "s1"), "launches").([]any)
	for _, e := range l {
		if ojson.Has(e, "what") || ojson.Has(e, "script_calls") {
			t.Errorf("text kept: %s", ojson.Compact(e))
		}
	}
	if f, _ := ojson.Float(ojson.GetOr(l[0], "tokens")); f != 42 {
		t.Errorf("tokens lost: %s", ojson.Compact(l[0]))
	}
	if a, _ := ojson.Str(ojson.GetOr(l[0], "agent")); a != "impl" {
		t.Error("agent lost")
	}
	if !ojson.Has(blob, "s2") {
		t.Error("other session lost")
	}
	// non-object blobs pass through
	if got := Delegates("str"); got != "str" {
		t.Errorf("got %v", got)
	}
	if got := Delegates(nil); got != nil {
		t.Errorf("got %v", got)
	}
}

// Health blanks the command/path/URL parts of each repeat reason and keeps the code.
func TestHealth(t *testing.T) {
	blob := parse(t, `{"sessions":{"s1":{"turns":9,"reasons":{"r1":{"code":"repeat","1":"curl https://x.io/?k=1","2":"/home/u/secret.txt","3":"","n":3},"r2":"plain"},"x":1},"s2":{"no":"reasons"},"s3":3}}`)
	Health(blob)
	r1 := ojson.GetOr(ojson.GetOr(ojson.GetOr(ojson.GetOr(blob, "sessions"), "s1"), "reasons"), "r1")
	for _, c := range []string{"1", "2", "3"} {
		if s, _ := ojson.Str(ojson.GetOr(r1, c)); s != "" {
			t.Errorf("field %s kept: %q", c, s)
		}
	}
	if s, _ := ojson.Str(ojson.GetOr(r1, "code")); s != "repeat" {
		t.Errorf("code lost: %s", ojson.Compact(r1))
	}
	if f, _ := ojson.Float(ojson.GetOr(r1, "n")); f != 3 {
		t.Error("count lost")
	}
	if f, _ := ojson.Float(ojson.GetOr(ojson.GetOr(ojson.GetOr(blob, "sessions"), "s1"), "turns")); f != 9 {
		t.Error("turns lost")
	}
	// key order: "1" stays where it was, no duplicates
	if got := len(ojson.Obj(r1).Keys); got != 5 {
		t.Errorf("keys = %d, want 5", got)
	}
	for _, v := range []any{nil, "x", parse(t, `{"sessions":3}`), parse(t, `{"other":1}`)} {
		Health(v)
	}
}

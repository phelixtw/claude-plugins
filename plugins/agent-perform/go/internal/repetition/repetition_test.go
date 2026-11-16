package repetition

import (
	"strconv"
	"testing"

	"agentperform/internal/ojson"
)

const p = "/repo/a.py"

type call struct {
	name string
	inp  *ojson.Object
}

func o(kv ...any) *ojson.Object {
	x := ojson.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		x.Set(kv[i].(string), kv[i+1])
	}
	return x
}

var (
	r = call{"Read", o("file_path", p)}
	w = call{"Write", o("file_path", p, "content", "x = 1\n")}
	e = call{"Edit", o("file_path", p, "old_string", "x = 1", "new_string", "x = 2")}
)

// flagged is the turns rule 3 (reread) flagged.
func flagged(turns ...[]call) map[int]bool {
	var calls []Call
	for i, g := range turns {
		for _, c := range g {
			calls = append(calls, Call{Turn: i + 1, Name: c.name, Inp: c.inp})
		}
	}
	got := Measure(calls, len(turns))
	out := map[int]bool{}
	reasons := ojson.Obj(ojson.Field(got, "reasons"))
	if reasons == nil {
		return out
	}
	for _, k := range reasons.Keys {
		if ojson.Has(reasons.Vals[k], "3") {
			n, _ := strconv.Atoi(k)
			out[n] = true
		}
	}
	return out
}

// Rule 3 for files: only re-reading a file that has not changed since it was last read.
func TestReread(t *testing.T) {
	if !flagged([]call{r}, []call{r})[2] {
		t.Error("reading an unchanged file again counts")
	}
	for name, got := range map[string]map[int]bool{
		"write then edit":  flagged([]call{w}, []call{e}),
		"edit twice":       flagged([]call{e}, []call{e}),
		"read back, same":  flagged([]call{r, e}, []call{r}),
		"read back, next":  flagged([]call{r}, []call{e, r}),
		"edit in between ": flagged([]call{r}, []call{e}, []call{r}),
	} {
		if len(got) != 0 {
			t.Errorf("%s: %v", name, got)
		}
	}
	if !flagged([]call{e}, []call{r}, []call{r})[3] {
		t.Error("two reads after the edit")
	}
	u := call{"WebFetch", o("url", "https://x.test/a")}
	g := call{"Grep", o("pattern", "foo", "path", "/repo")}
	if !flagged([]call{u}, []call{u})[2] || !flagged([]call{g}, []call{g})[2] {
		t.Error("urls and searches")
	}
}

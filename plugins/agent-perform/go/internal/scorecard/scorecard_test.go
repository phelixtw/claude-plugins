package scorecard

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentperform/internal/ojson"
)

func p(v float64) *float64 { return &v }

func TestHelpers(t *testing.T) {
	eq := func(got, want string) {
		t.Helper()
		if got != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
	eq(countdown(100, 110), "已重置")
	eq(countdown(3*3600+600, 0), "3h10m")
	eq(countdown(50*3600, 0), "2d2h")
	eq(countdown(125, 0), "2m")
	eq(toks(86_400), "86k")
	eq(toks(1_250_000), "1.2M")
	eq(toks(999.9), "999")
	eq(signed(-2500), "-2k")
	eq(quote("/a b/中"), "/a%20b/%E4%B8%AD")
	eq(basename("/a/b/"), "")
	if got := alerts(p(41), p(10), p(400_000), p(2000)); !reflect.DeepEqual(got, []string{"ctx", "cache", "burst"}) {
		t.Errorf("alerts %v", got)
	}
	if got := alerts(nil, p(0), p(10), nil); len(got) != 0 {
		t.Errorf("alerts %v", got)
	}
}

func TestAlertThresholds(t *testing.T) {
	cases := []struct {
		args [4]*float64
		want []string
	}{
		{[4]*float64{p(40), nil, nil, nil}, nil},
		{[4]*float64{p(41), nil, nil, nil}, []string{"ctx"}},
		{[4]*float64{p(0), p(300), nil, nil}, nil},
		{[4]*float64{p(0), p(299), nil, nil}, []string{"cache"}},
		{[4]*float64{p(0), p(1), nil, nil}, []string{"cache"}},
		{[4]*float64{p(0), p(0), nil, nil}, nil},
		{[4]*float64{p(0), nil, p(100_000), p(20_000)}, nil},
		{[4]*float64{p(0), nil, p(100_001), p(20_000)}, []string{"burst"}},
		{[4]*float64{p(0), nil, p(30_001), p(6_000)}, nil},
		{[4]*float64{p(0), nil, p(59_999), p(10_000)}, nil},
		{[4]*float64{p(0), nil, p(60_000), p(10_000)}, []string{"burst"}},
		{[4]*float64{p(0), nil, p(86_000), nil}, nil},
		{[4]*float64{p(50), p(10), p(90_000), p(1_000)}, []string{"ctx", "cache", "burst"}},
	}
	for i, c := range cases {
		if got := alerts(c.args[0], c.args[1], c.args[2], c.args[3]); !reflect.DeepEqual(got, c.want) {
			t.Errorf("case %d: %v want %v", i, got, c.want)
		}
	}
}

func obj(t *testing.T, s string) any {
	v, err := ojson.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestQuota(t *testing.T) {
	for _, c := range []struct {
		in, key, want string
		now           float64
	}{
		{`{"five_hour": {"used_percentage": 4}}`, "five_hour", "已用 4%", 0},
		{`{"seven_day": {"used_percentage": 7, "resets_at": 3600}}`, "seven_day", "已用 7%(1h00m)", 0},
		{`{"five_hour": {"used_percentage": 99, "resets_at": 5}}`, "five_hour", "已用 99%(已重置)", 10},
		{`{}`, "five_hour", "--", 0},
	} {
		if got := quota(obj(t, c.in), c.key, c.now); got != c.want {
			t.Errorf("quota(%s) = %q want %q", c.in, got, c.want)
		}
	}
}

func TestDelegationSinceTheLastPrompt(t *testing.T) {
	dir := t.TempDir()
	agent := func(typ, model string) string {
		in := fmt.Sprintf(`{"subagent_type": %q}`, typ)
		if model != "" {
			in = fmt.Sprintf(`{"subagent_type": %q, "model": %q}`, typ, model)
		}
		return `{"type": "assistant", "message": {"content": [{"type": "tool_use", "name": "Agent", "input": ` + in + `}]}}`
	}
	prompt := `{"type": "user", "message": {"role": "user", "content": "go"}}`
	n := 0
	write := func(rows ...string) string {
		n++
		path := filepath.Join(dir, fmt.Sprintf("%d.jsonl", n))
		os.WriteFile(path, []byte(strings.Join(rows, "\n")), 0o644)
		return path
	}
	path := write(prompt, agent("old", ""), prompt, agent("code-finder", "haiku"), agent("reviewer", ""))
	if got := delegatedNow(path); !reflect.DeepEqual(got, []call{{"code-finder", "haiku"}, {"reviewer", ""}}) {
		t.Errorf("delegatedNow %v", got)
	}
	for _, c := range []struct{ path, want string }{
		{path, "本回合已委派 code-finder（haiku）等 2 個"},
		{write(prompt, agent("code-finder", "haiku")), "本回合已委派 code-finder（haiku）"},
		{write(prompt), ""},
		{"/nonexistent.jsonl", ""},
	} {
		if got := subagentLine(c.path); got != c.want {
			t.Errorf("subagentLine = %q want %q", got, c.want)
		}
	}
}

func TestRowLine(t *testing.T) {
	row := ojson.Obj(obj(t, `{"id": "abcdef12xyz", "turn_n": 10, "cost": 3.0, "delegate_cost": 0.75, "waste": 0.0}`))
	line := func() string {
		s, ok := scorecardLine(row)
		if !ok {
			t.Fatal("no line")
		}
		return s
	}
	if got := line(); got != "abcdef12 #10 · 花費 $3.75" {
		t.Errorf("got %q", got)
	}
	n := func(s string) any { v, _ := ojson.Parse([]byte(s)); return v }
	row.Set("waste", n("0.42")).Set("ctx_delta", n("86000")).Set("ctx_usual", n("6000"))
	if got := line(); got != "abcdef12 #10 · 花費 $3.75 · 浪費 $0.42 · 本回合 context +86k（平常 +6k）" {
		t.Errorf("got %q", got)
	}
	row.Set("ctx_usual", nil)
	if got := line(); !strings.HasSuffix(got, "本回合 context +86k") {
		t.Errorf("got %q", got)
	}
	row.Set("turn_n", n("0"))
	if got := line(); !strings.HasPrefix(got, "abcdef12 #— ") {
		t.Errorf("got %q", got)
	}
	full := obj(t, `{"id": "abcdef0123456789", "turn_n": 12, "cost": 3.14159, "delegate_cost": 0.5, "waste": 0.25, "ctx_delta": 86400, "ctx_usual": 6000}`)
	if got, _ := scorecardLine(full); got != "abcdef01 #12 · 花費 $3.64 · 浪費 $0.25 · 本回合 context +86k（平常 +6k）" {
		t.Errorf("got %q", got)
	}
	if _, ok := scorecardLine(obj(t, `{"id": "x"}`)); ok {
		t.Error("missing fields should fail")
	}
}

// A dashboard from a version before 2.0 still sends corrections and judged; the Mod ignores them.
func TestRowLineIgnoresOldCorrectionFields(t *testing.T) {
	row := obj(t, `{"id": "abcdef12xyz", "turn_n": 10, "corrections": 2, "judged": 9, "cost": 3.0, "waste": 0.0}`)
	if got, _ := scorecardLine(row); got != "abcdef12 #10 · 花費 $3.00" {
		t.Errorf("got %q", got)
	}
}

package common

import (
	"testing"

	"agentperform/internal/ojson"
)

func TestIsoTS(t *testing.T) {
	cases := map[string]float64{
		"1970-01-01T00:00:00Z":           0,
		"2026-01-01T00:10:00Z":           1767226200,
		"2026-01-01T08:10:00.5+08:00":    1767226200.5,
		"2026-10-19T03:04:05.123456789Z": 1792379045.123456,
	}
	for in, want := range cases {
		if got, ok := IsoTS(in); !ok || got != want {
			t.Errorf("IsoTS(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"bad", "", "2026-13-01", "2026-01-01X"} {
		if _, ok := IsoTS(bad); ok {
			t.Errorf("IsoTS(%q) should fail", bad)
		}
	}
}

func parse(t *testing.T, s string) any {
	v, err := ojson.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPrompts(t *testing.T) {
	if !IsPrompt(parse(t, `{"type": "user", "message": {"content": "幫我改這個"}}`)) {
		t.Error("plain prompt")
	}
	for _, s := range []string{
		`{"type": "user", "toolUseResult": {}, "message": {"content": "x"}}`,
		`{"type": "user", "toolUseResult": null, "message": {"content": "x"}}`,
		`{"type": "user", "message": {"content": "<command-name>/clear</command-name>"}}`,
		`{"type": "user", "isMeta": true, "message": {"content": "x"}}`,
		`{"type": "user", "message": {"content": "<task-notification>\\n<task-id>a</task-id></task-notification>"}}`,
		`{"type": "user", "message": {"content": [{"type": "text", "text": " <scheduled-wakeup>x</scheduled-wakeup>"}]}}`,
		`{"type": "user", "message": {"content": "<background-task>x</background-task>"}}`,
		`{"type": "user", "isCompactSummary": true, "message": {"content": "summary"}}`,
	} {
		if IsPrompt(parse(t, s)) {
			t.Errorf("not a prompt: %s", s)
		}
	}
	got := TextOf(parse(t, `{"content": [{"type": "text", "text": "a"}, {"type": "image"}, {"type": "text", "text": "b"}]}`))
	if got != "a b" {
		t.Errorf("TextOf = %q", got)
	}
}

package toollimits

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"agentperform/internal/ojson"
)

const (
	persist  = "<persisted-output>\nOutput too large (39.8KB). Full output saved to: /x.txt\n\nPreview"
	bg       = "Command did not complete within its 120s timeout and was moved to the background (ID: b1)."
	redirect = "REDIRECT DETECTED: The URL redirects to a different host.\n\nOriginal URL: https://a"
	cut      = "[WebFetch note: this page's text is 116497 characters long and the answer above covers only " +
		"characters 0 to 100000; the final 16497 were not read — to read on, call WebFetch again]"
)

func inp(kv ...any) *ojson.Object {
	o := ojson.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name string
		in   *ojson.Object
		text string
		want []string
	}{
		{"Bash", inp(), persist, []string{"bash_out"}},
		{"Bash", inp(), bg, []string{"bash_timeout"}},
		{"Bash", inp(), "160:* moved: `Command did not complete within its 120s timeout and was moved to the background`", nil},
		{"WebFetch", inp(), "the docs say the final 5 were not read", nil},
		{"Edit", inp(), "<tool_use_error>File has not been read yet. Read it first", []string{"edit"}},
		{"Edit", inp(), "<tool_use_error>String to replace not found in file.", []string{"edit"}},
		{"Edit", inp(), "<tool_use_error>Found 2 matches of the string to replace", []string{"edit"}},
		{"WebFetch", inp(), redirect, []string{"fetch"}},
		{"WebFetch", inp(), cut, []string{"fetch"}},
		{"Read", inp("file_path", "a.py"), "PARTIAL view: 30000 tokens vs 25,000", []string{"read"}},
		{"Glob", inp(), "a\n(Results are truncated.)", []string{"glob"}},
		{"Read", inp(), "1\thello", nil},
		{"Bash", inp(), "ok", nil},
		{"Grep", inp(), persist, nil},
	}
	for _, c := range cases {
		if got := Detect(c.name, c.in, c.text); len(got)+len(c.want) > 0 && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %.40q: got %v want %v", c.name, c.text, got, c.want)
		}
	}
}

func use(id, name string, kv ...any) Event {
	return Event{Kind: "use", ID: id, Name: name, Inp: inp(kv...)}
}
func result(id, text string) Event { return Event{Kind: "result", ID: id, Text: text} }
func prompt(text string) Event     { return Event{Kind: "prompt", Text: text} }

func hits(ev ...Event) []Hit { return Scan(ev) }

func TestScan(t *testing.T) {
	if got := hits(use("a", "Bash", "command", "x"), result("a", persist)); len(got) != 0 {
		t.Errorf("preview was enough: %v", got)
	}
	got := hits(use("a", "Bash", "command", "x"), result("a", persist), use("b", "Read", "file_path", "/x.txt"))
	if want := []Hit{{"bash_out", "result", "a", true}, {"bash_out", "use", "b", false}}; !reflect.DeepEqual(got, want) {
		t.Errorf("follow key: %v", got)
	}

	red := redirect + "\nRedirect URL (from the server's Location header): https://b/x\nStatus: 302"
	ev := []Event{use("a", "WebFetch", "url", "https://a"), result("a", red)}
	if n := len(hits(append(ev, use("b", "WebFetch", "url", "https://zzz"))...)); n != 0 {
		t.Errorf("unrelated fetch: %d", n)
	}
	if n := len(hits(append(ev, use("b", "WebFetch", "url", "https://b/x"))...)); n != 2 {
		t.Errorf("redirect target: %d", n)
	}
	cutEv := []Event{use("a", "WebFetch", "url", "https://a/p"), result("a", cut)}
	if n := len(hits(append(cutEv, use("b", "WebFetch", "url", "https://a/p", "offset", 1))...)); n != 2 {
		t.Errorf("same url: %d", n)
	}

	editErr := "<tool_use_error>File has not been read yet. Read it first"
	ev = []Event{use("a", "Edit", "file_path", "/f.py"), result("a", editErr)}
	if n := len(hits(append(ev, use("b", "Read", "file_path", "/other.py"))...)); n != 0 {
		t.Errorf("other file: %d", n)
	}
	if n := len(hits(append(ev, use("b", "Read", "file_path", "/f.py"))...)); n != 2 {
		t.Errorf("same file: %d", n)
	}

	ev = []Event{use("a", "Bash", "command", "make"), result("a", bg)}
	if n := len(hits(append(ev, use("b", "Bash", "command", "ls"))...)); n != 0 {
		t.Errorf("no poll: %d", n)
	}
	if got := hits(append(ev, use("b", "Bash", "command", "sleep 20; tail x.output"))...); len(got) == 0 || got[0].Key != "bash_timeout" {
		t.Errorf("poll: %v", got)
	}

	readErr := "File content (25513 tokens) exceeds maximum allowed tokens (25000). Use offset"
	got = hits(use("a", "Read", "file_path", "/b"), result("a", readErr),
		use("b", "Read", "file_path", "/b", "offset", 1, "limit", 50),
		use("c", "Read", "file_path", "/other", "offset", 1))
	if want := []Hit{{"read", "result", "a", true}, {"read", "use", "b", true}}; !reflect.DeepEqual(got, want) {
		t.Errorf("read: %v", got)
	}

	fetch := use("f", "WebFetch", "url", "https://docs.x.dev/a?q=1")
	p := prompt("read https://docs.x.dev/a please")
	if got := hits(p, use("s", "WebSearch", "query", "x"), fetch); !reflect.DeepEqual(got, []Hit{{"search", "use", "f", true}}) {
		t.Errorf("search: %v", got)
	}
	if n := len(hits(p, fetch)); n != 0 {
		t.Errorf("no search: %d", n)
	}
	if n := len(hits(prompt("hi"), use("s", "WebSearch", "query", "x"), fetch)); n != 0 {
		t.Errorf("url not in prompt: %d", n)
	}
}

func TestEventsOf(t *testing.T) {
	raw := `[{"type": "user", "message": {"content": "do it https://a.b/c"}},
{"type": "assistant", "message": {"content": [{"type": "tool_use", "id": "t", "name": "Read", "input": {}}]}},
{"type": "user", "message": {"content": [{"type": "tool_result", "tool_use_id": "t", "content": [{"type": "text", "text": "ok"}]}]}}]`
	v, err := ojson.Parse([]byte(strings.TrimSpace(raw)))
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range EventsOf(slices.Values(v.([]any))) {
		kinds = append(kinds, e.Kind)
	}
	if strings.Join(kinds, " ") != "prompt use result" {
		t.Errorf("kinds %v", kinds)
	}
}

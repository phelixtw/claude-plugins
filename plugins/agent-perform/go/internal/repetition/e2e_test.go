package repetition

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
)

func user(text string) string {
	return `{"type":"user","message":{"role":"user","content":"` + text + `"}}`
}

func tool(name, input string) string {
	return `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"` + name + `","input":` + input + `}]}}`
}

// wasteTranscript: turn 1 runs a command, turn 2 repeats it and rereads a file, turn 3 is clean.
func wasteTranscript() string {
	return strings.Join([]string{
		tool("Bash", `{"command":"ls ignored-before-first-prompt"}`), // before any prompt: not counted
		user("one"),
		tool("Bash", `{"command":"make SECRET_TOKEN=abc"}`),
		tool("Read", `{"file_path":"/repo/SECRETFILE.go"}`),
		`{"isSidechain":true,"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"x"}}]}}`,
		"not json",
		user("two"),
		tool("Bash", `{"command":"make SECRET_TOKEN=abc"}`),
		tool("Read", `{"file_path":"/repo/SECRETFILE.go"}`),
		tool("Skill", `{"skill":"a"}`),
		user("three"),
		tool("Skill", `{"skill":"a"}`),
		tool("mcp__srv__q", `{"a":1}`),
		tool("WebFetch", `{"url":"http://x"}`),
		tool("Grep", `{"pattern":"p","path":"."}`),
		user("four"),
		tool("mcp__srv__q", `{"a":1}`),
		tool("WebFetch", `{"url":"http://x"}`),
		tool("Grep", `{"pattern":"p","path":"."}`),
		`{"type":"user","message":{"content":"<command-name>/clear"}}`,
		"",
	}, "\n")
}

func setup(t *testing.T) (home, transcript string) {
	home = t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".claude", "projects", "proj")
	os.MkdirAll(filepath.Join(dir, "subagents"), 0o755)
	transcript = filepath.Join(dir, "abcdef1234.jsonl")
	os.WriteFile(transcript, []byte(wasteTranscript()), 0o644)
	// a subagent transcript must be skipped by Main
	os.WriteFile(filepath.Join(dir, "subagents", "agent-x.jsonl"), []byte(wasteTranscript()), 0o644)
	return
}

func TestScanCountsTurnsAndSkipsSidechain(t *testing.T) {
	_, tr := setup(t)
	calls, total := Scan(tr)
	if total != 4 || len(calls) != 12 {
		t.Errorf("turns=%d calls=%d", total, len(calls))
	}
}

func TestMainWritesHealthWithoutCommandsOrPaths(t *testing.T) {
	home, _ := setup(t)
	Main()
	raw, err := os.ReadFile(Out())
	if err != nil {
		t.Fatal(err)
	}
	blob, err := ojson.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	sessions := ojson.Obj(ojson.Field(blob, "sessions"))
	if sessions.Len() != 1 || !ojson.Has(sessions, "abcdef12") {
		t.Fatalf("one session keyed by first 8 chars expected: %s", raw)
	}
	row := ojson.Field(sessions, "abcdef12")
	flagged, _ := ojson.Field(row, "flagged").([]any)
	if len(flagged) != 3 { // turns 2, 3, 4
		t.Errorf("flagged = %v", flagged)
	}
	totals := ojson.Field(blob, "totals")
	if tt, _ := py.Number(ojson.Field(totals, "turns")); tt != 4 {
		t.Errorf("totals = %v", totals)
	}
	if strings.Contains(string(raw), "SECRET") {
		t.Errorf("command or path text leaked into agent_health.json: %s", raw)
	}
	if !strings.Contains(string(raw), `"1": ""`) || !strings.Contains(string(raw), `"3": ""`) {
		t.Errorf("reason codes must stay with blanked detail: %s", raw)
	}
	_ = home
}

func TestLabels(t *testing.T) {
	for _, c := range []struct {
		name, inp, tool, detail string
	}{
		{"Bash", `{"command":"  ls   -la "}`, "Bash", "ls -la"},
		{"mcp__srv__q", `{"a":1}`, "mcp:srv", ""},
		{"Read", `{"file_path":"/a/b.go"}`, "Read", "/a/b.go"},
		{"WebFetch", `{"url":"http://x"}`, "WebFetch", "http://x"},
		{"Grep", `{"pattern":"p","path":"d"}`, "Grep", "p|d"},
		{"Skill", `{"skill":"s"}`, "Skill", "s"},
		{"Other", `{"b":2}`, "Other", `{"b": 2}`},
	} {
		inp, _ := ojson.Parse([]byte(c.inp))
		tool, detail := Label(c.name, inp)
		if tool != c.tool || (c.detail != "" && detail != c.detail) {
			t.Errorf("%s -> %q %q", c.name, tool, detail)
		}
	}
	if Tail("abcdef", 3) != "…ef" {
		t.Error("Tail keeps the end")
	}
}

func TestSimilarWritesFlagged(t *testing.T) {
	body := strings.Repeat("the quick brown fox jumps ", 4)
	mk := func(s string) *ojson.Object { return o("file_path", "/x", "content", s) }
	got := Measure([]Call{{1, "Write", mk(body)}, {2, "Write", mk(body + "!")}, {3, "Write", mk("zzz totally different 123456789012345678901234567890")}}, 3)
	r := ojson.Obj(ojson.Field(got, "reasons"))
	if !ojson.Has(r.Vals["2"], "4") || r.Len() != 1 {
		t.Errorf("reasons = %v", r)
	}
}

package ctxprobe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentperform/internal/ojson"
)

const fakeTable = "\n**Tokens:** 30.7k / 200k (15%)\n| Category | Tokens | Percentage |\n|----------|--------|------------|\n" +
	"| System prompt | 6.7k | 3.3% |\n| MCP tools (deferred) | 24.5k | 12.2% |\n| Free space | 166.3k | 83.2% |\n"

// probeEnv is a HOME with ~/.claude and a fake `claude` on PATH that logs its calls.
func probeEnv(t *testing.T) (home, cwd, calls string) {
	home, cwd = t.TempDir(), t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	bin := filepath.Join(home, "bin")
	os.MkdirAll(bin, 0o755)
	calls = filepath.Join(home, "calls")
	script := "#!/bin/sh\necho \"$@ guard=$CLAUDE_CTX_PROBE\" >> \"" + calls + "\"\ncat <<\"E\"\n" + fakeTable + "\nE\n"
	os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755)
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	t.Setenv(guard, "")
	return
}

func nCalls(calls string) int {
	raw, err := os.ReadFile(calls)
	if err != nil {
		return 0
	}
	return len(strings.Split(strings.TrimSpace(string(raw)), "\n"))
}

// Probe `claude -p /context` once per config and cache the table.
func TestProbeCachesOncePerConfig(t *testing.T) {
	home, cwd, calls := probeEnv(t)
	hook := `{"cwd": "` + cwd + `", "model": {"id": "claude-opus-5-5"}}`
	if Main(hook, false) != 0 {
		t.Fatal("probe failed")
	}
	raw, _ := os.ReadFile(filepath.Join(home, ".claude", "ctx-cache.json"))
	v, _ := ojson.Parse(raw)
	entry := ojson.Field(v, cwd+"|claude-opus-5-5")
	if got := ojson.Compact(ojson.Field(entry, "cats")); got != `{"System prompt":6700,"MCP tools (deferred)":24500,"Free space":166300}` {
		t.Errorf("cats %s", got)
	}
	if n, _ := ojson.Float(ojson.Field(entry, "limit")); n != 200_000 {
		t.Errorf("limit %v", n)
	}
	if got, _ := os.ReadFile(calls); strings.TrimSpace(string(got)) != "-p /context --model claude-opus-5-5 guard=1" {
		t.Errorf("call %q", got)
	}
	Main(hook, false)
	if nCalls(calls) != 1 {
		t.Error("nothing moved: no second probe")
	}
	os.MkdirAll(filepath.Join(cwd, "rules"), 0o755) // a watched path appears
	Main(hook, false)
	if nCalls(calls) != 2 {
		t.Error("a watched path changed: probe again")
	}
}

func TestModelFromTranscriptKeepsOtherEntries(t *testing.T) {
	home, cwd, _ := probeEnv(t)
	tr := filepath.Join(home, "t.jsonl")
	os.WriteFile(tr, []byte(`{"message": {"model": "claude-sonnet-5-5"}}`+"\n"+`{"message": {"model": "claude-haiku-5-5"}}`+"\n"), 0o644)
	cache := filepath.Join(home, ".claude", "ctx-cache.json")
	os.WriteFile(cache, []byte(`{"other|m": {"cats": {"A": 1}}}`), 0o644)
	Main(`{"cwd": "`+cwd+`", "transcript_path": "`+tr+`"}`, false)
	raw, _ := os.ReadFile(cache)
	v, _ := ojson.Parse(raw)
	if keys := strings.Join(ojson.Obj(v).Keys, ","); keys != "other|m,"+cwd+"|claude-haiku-5-5" {
		t.Errorf("keys %s", keys)
	}
}

func TestGuardStopsNestedProbes(t *testing.T) {
	home, cwd, calls := probeEnv(t)
	t.Setenv(guard, "1")
	Main(`{"cwd": "`+cwd+`", "model": {"id": "m"}}`, false)
	if nCalls(calls) != 0 {
		t.Error("nested probe ran")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "ctx-cache.json")); err == nil {
		t.Error("cache written")
	}
}

func TestParsesTheTable(t *testing.T) {
	sample := "\n**Tokens:** 30.7k / 200k (15%)\n| Category | Tokens | Percentage |\n|----------|--------|------------|\n" +
		"| System prompt | 6.7k | 3.3% |\n| MCP tools (deferred) | 24.5k | 12.2% |\n| Free space | 166.3k | 83.2% |\n" +
		"| mcp__weather__get_forecast | weather | 74 |\n"
	cats, limit, ok := Parse(sample)
	if !ok || limit != 200_000 {
		t.Fatalf("limit %d ok %v", limit, ok)
	}
	if got, want := ojson.Compact(cats), `{"System prompt":6700,"MCP tools (deferred)":24500,"Free space":166300}`; got != want {
		t.Errorf("cats = %s", got)
	}
	if _, _, ok := Parse("nothing"); ok {
		t.Error("nothing should not parse")
	}
}

package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionEndWritesCostsAndHealthWithoutText(t *testing.T) {
	home := tempHome(t)
	t.Setenv("AP_PLUGIN_ROOT", t.TempDir())
	isolateSessionEndLock(t)
	dir := filepath.Join(home, ".claude", "projects", "proj")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "sess1234-aaaa.jsonl"), []byte(strings.Join([]string{
		`{"type":"user","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-10-01T10:00:01Z","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Task","input":{"description":"SECRET-TASK-TEXT","subagent_type":"reviewer"}},{"type":"tool_use","name":"Bash","input":{"command":"echo SECRET-CMD"}}]}}`,
		`{"type":"user","timestamp":"2026-10-01T10:00:09Z","message":{"role":"user","content":"again"}}`,
		`{"type":"assistant","timestamp":"2026-10-01T10:00:10Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"echo SECRET-CMD"}}]}}`,
	}, "\n")+"\n"), 0o644)

	if code := SessionEnd(); code != 0 {
		t.Fatalf("code %d", code)
	}
	read := func(name string) string { b, _ := os.ReadFile(filepath.Join(home, ".claude", name)); return string(b) }
	for _, name := range []string{"subagent_costs.json", "agent_health.json"} {
		got := read(name)
		if got == "" || strings.Contains(got, "SECRET") {
			t.Errorf("%s empty or leaked task text: %s", name, got)
		}
	}
	if !strings.Contains(read("subagent_costs.json"), "sess1234") || !strings.Contains(read("agent_health.json"), `"flagged": [`) {
		t.Errorf("per-session rows missing: %s / %s", read("subagent_costs.json"), read("agent_health.json"))
	}
	if !strings.Contains(read("nightly.log"), "session_end") {
		t.Error("nightly log entry missing")
	}
}

func TestSessionEndSilentInOptimizeChild(t *testing.T) {
	home := tempHome(t)
	isolateSessionEndLock(t)
	t.Setenv("CLAUDE_AP_CHILD", "1")
	SessionEnd()
	if _, err := os.Stat(filepath.Join(home, ".claude", "nightly.log")); err == nil {
		t.Error("the optimize page's claude -p child must not run session end")
	}
}

func TestStatuslinePrintsScorecardRow(t *testing.T) {
	tempHome(t)
	t.Setenv("AP_DASH_URL", "http://127.0.0.1:1")
	out := captureStdout(t, func() { Statusline(`{"model": {"display_name": "Opus 4"}}` + "\n") })
	if !strings.HasPrefix(out, "Opus 4(0K)") {
		t.Errorf("statusline %q", out)
	}
}

// isolateSessionEndLock keeps the test off the lock a live session end may hold.
func isolateSessionEndLock(t *testing.T) {
	old := sessionEndLock
	sessionEndLock = filepath.Join(t.TempDir(), "se.lock")
	t.Cleanup(func() { sessionEndLock = old })
}

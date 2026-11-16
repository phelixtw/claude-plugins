package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// captureStdout runs fn and returns what it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	fn()
	w.Close()
	os.Stdout = old
	return <-done
}

func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	t.Setenv("HOME", home)
	return home
}

func settings(t *testing.T, home, raw string) string {
	p := filepath.Join(home, ".claude", "settings.json")
	os.WriteFile(p, []byte(raw), 0o644)
	return p
}

func TestNudge(t *testing.T) {
	home := tempHome(t)
	manifest, err := os.ReadFile("../../../.claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var plugin struct{ Name string }
	if err := json.Unmarshal(manifest, &plugin); err != nil {
		t.Fatal(err)
	}
	if out := captureStdout(t, func() { Nudge() }); !strings.Contains(out, "/"+plugin.Name+":setup") {
		t.Errorf("no nudge: %q", out)
	}
	os.WriteFile(filepath.Join(home, ".claude", "agent-perform-setup-done"), nil, 0o644)
	if out := captureStdout(t, func() { Nudge() }); out != "" {
		t.Errorf("nudged after setup: %q", out)
	}
}

func TestScrubAllStripsTextOnce(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "subagent_costs.json"), []byte(`{"s1": {"launches": [{"what": "task", "script_calls": [1], "cost": 1.5}]}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "agent_health.json"), []byte(`{"sessions": {"s1": {"reasons": {"Bash": {"1": "ls -la", "4": "x"}}}}}`), 0o644)
	ScrubAll(dir)
	read := func(n string) string { b, _ := os.ReadFile(filepath.Join(dir, n)); return string(b) }
	if got := read("subagent_costs.json"); got != `{"s1": {"launches": [{"cost": 1.5}]}}` {
		t.Errorf("costs %q", got)
	}
	if got := read("agent_health.json"); got != `{"sessions": {"s1": {"reasons": {"Bash": {"1": "", "4": "x"}}}}}` {
		t.Errorf("health %q", got)
	}
	st, _ := os.Stat(filepath.Join(dir, "agent_health.json"))
	time.Sleep(10 * time.Millisecond)
	ScrubAll(dir)
	if st2, _ := os.Stat(filepath.Join(dir, "agent_health.json")); !st2.ModTime().Equal(st.ModTime()) {
		t.Error("a clean file must not be rewritten")
	}
}

func TestTrimLog(t *testing.T) {
	p := filepath.Join(t.TempDir(), "log")
	var b strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	os.WriteFile(p, []byte(b.String()), 0o644)
	trimLog(p, 3)
	if got, _ := os.ReadFile(p); string(got) != "line 7\nline 8\nline 9\n" {
		t.Errorf("got %q", got)
	}
}

func TestTakeLock(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	if !takeLock(p, time.Hour) || takeLock(p, time.Hour) {
		t.Fatal("a fresh lock is exclusive")
	}
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(p, old, old)
	if !takeLock(p, time.Hour) {
		t.Error("a stale lock is taken over")
	}
}

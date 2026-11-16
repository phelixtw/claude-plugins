package pollgate

import (
	"io"
	"os"
	"strings"
	"testing"

	"agentperform/internal/ojson"
)

// As the hook runs it: one stderr line and exit 2; bad input never blocks.
func TestMainExitCode(t *testing.T) {
	t.Setenv("AP_EXP_POLLING_BLOCK", "1")
	r, w, _ := os.Pipe()
	old := os.Stderr
	os.Stderr = w
	code := Main(ojson.Compact(bash("until curl -sf localhost:8787/health; do " + "sleep 2; done")))
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	if code != 2 || len(strings.Split(strings.TrimSpace(string(out)), "\n")) != 1 || !strings.Contains(string(out), "run_in_background") {
		t.Errorf("exit %d, stderr %q", code, out)
	}
	for _, stdin := range []string{"not json", "", "[]", `{"tool_name": "Read", "tool_input": {}}`} {
		if Main(stdin) != 0 {
			t.Errorf("blocked on %q", stdin)
		}
	}
	for _, cmd := range []string{"x\nsleep 40", "(sleep 9)"} {
		if Verdict(bash(cmd)) == "" {
			t.Errorf("should block: %q", cmd)
		}
	}
	for _, cmd := range []string{"sleep 6_", ""} {
		if Verdict(bash(cmd)) != "" {
			t.Errorf("should pass: %q", cmd)
		}
	}
}

func bash(cmd string) any {
	return ojson.NewObject().Set("tool_name", "Bash").Set("tool_input", ojson.NewObject().Set("command", cmd))
}

func TestVerdict(t *testing.T) {
	t.Setenv("AP_EXP_POLLING_BLOCK", "1")
	block := []string{
		"until curl -s localhost:8787/health; do sleep 2; done",
		"while true; do ls; sleep 1; done",
		"for i in 1 2 3; do check; sleep 1; done",
		"sleep 30 && curl localhost",
		"cd x && sleep 2m; ls",
		"sleep 6",
		"timeout 60 bash -c 'sleep 20; ls'",
	}
	pass := []string{"sleep 5", "sleep 3; echo hi", "sleep 6x", "sleep 1.5.1", "ls -la",
		"git commit -m 'sleep 30 handling'", "for中 x; do sleep 1; done"}
	for _, c := range block {
		if Verdict(bash(c)) == "" {
			t.Errorf("should block: %s", c)
		}
	}
	for _, c := range pass {
		if Verdict(bash(c)) != "" {
			t.Errorf("should pass: %s", c)
		}
	}
	bg := ojson.NewObject().Set("tool_name", "Bash").Set("tool_input",
		ojson.NewObject().Set("command", "sleep 30").Set("run_in_background", true))
	sub := bash("sleep 30").(*ojson.Object).Set("agent_id", "a1")
	read := ojson.NewObject().Set("tool_name", "Read").Set("tool_input", ojson.NewObject())
	for _, h := range []any{bg, sub, read} {
		if Verdict(h) != "" {
			t.Errorf("should pass: %s", ojson.Compact(h))
		}
	}
	t.Setenv("AP_EXP_POLLING_BLOCK", "0")
	if Verdict(bash("sleep 30")) != "" {
		t.Error("flag off should pass")
	}
}

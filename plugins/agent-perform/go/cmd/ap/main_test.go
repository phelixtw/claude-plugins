package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunUsageAndVersion(t *testing.T) {
	e2eHome(t)
	for _, args := range [][]string{nil, {"bogus"}} {
		_, errOut, code := apRun(t, "", args...)
		if code != 64 || !strings.Contains(errOut, "usage: ap poll-gate") {
			t.Fatalf("args %v: code=%d stderr=%q", args, code, errOut)
		}
	}
	out, _, code := apRun(t, "", "--version")
	if code != 0 || strings.TrimSpace(out) != "ap "+version {
		t.Fatalf("--version: code=%d out=%q", code, out)
	}
}

func TestRunSessionStartSteps(t *testing.T) {
	home := e2eHome(t)
	if _, _, code := apRun(t, "", "session-start"); code != 64 {
		t.Fatalf("no step: code=%d", code)
	}
	if _, _, code := apRun(t, "", "session-start", "nope"); code != 64 {
		t.Fatalf("unknown step: code=%d", code)
	}

	// nudge prints the setup reminder until the setup-done marker exists
	out, _, code := apRun(t, "", "session-start", "nudge")
	if code != 0 || !strings.Contains(out, "/agent-perform:setup") {
		t.Fatalf("nudge: code=%d out=%q", code, out)
	}
	os.WriteFile(filepath.Join(home, ".claude", "agent-perform-setup-done"), nil, 0o644)
	out, _, code = apRun(t, "", "session-start", "nudge")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Fatalf("nudge after setup: code=%d out=%q", code, out)
	}

}

func TestRunStdinHooks(t *testing.T) {
	e2eHome(t)
	for _, c := range [][]string{{"poll-gate"}, {"scorecard"}, {"scorecard", "--mod"}, {"ctx-probe", "--print"}, {"statusline"}} {
		if _, _, code := apRun(t, "{}", c...); code != 0 {
			t.Fatalf("%v with empty JSON: code=%d", c, code)
		}
	}
}

// Stdin that is not valid UTF-8 makes poll-gate and scorecard skip silently.
func TestRunInvalidUTF8Stdin(t *testing.T) {
	e2eHome(t)
	for _, cmd := range []string{"poll-gate", "scorecard"} {
		out, errOut, code := apRun(t, "\xff\xfe", cmd)
		if code != 0 || out != "" || errOut != "" {
			t.Fatalf("%s: code=%d out=%q err=%q", cmd, code, out, errOut)
		}
	}
}

func TestRunDoctor(t *testing.T) {
	e2eHome(t)
	out, _, _ := apRun(t, "", "doctor")
	if strings.TrimSpace(out) == "" {
		t.Fatal("doctor printed nothing")
	}
}

func TestRunDashboardArgs(t *testing.T) {
	e2eHome(t)
	if _, errOut, code := apRun(t, "", "dashboard"); code != 64 || !strings.Contains(errOut, "usage: ap dashboard --serve") {
		t.Fatalf("no --serve: code=%d err=%q", code, errOut)
	}
	if _, _, code := apRun(t, "", "dashboard", "--refresh-stats"); code != 0 {
		t.Fatalf("--refresh-stats: code=%d", code)
	}
}

package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// apRun drives one subcommand through run() and returns what it printed.
func apRun(t *testing.T, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cap := func(f **os.File) (restore func() string) {
		r, w, _ := os.Pipe()
		old := *f
		*f = w
		done := make(chan string)
		go func() { b, _ := io.ReadAll(r); done <- string(b) }()
		return func() string { w.Close(); *f = old; return <-done }
	}
	restoreOut, restoreErr := cap(&os.Stdout), cap(&os.Stderr)
	code = run(args, strings.NewReader(stdin))
	return restoreOut(), restoreErr(), code
}

// e2eHome points HOME at a temp dir and keeps real state out of reach.
func e2eHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude", "agent-perform"), 0o755)
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_AP_CHILD", "")
	return home
}

func TestE2EUnknownCommand(t *testing.T) {
	e2eHome(t)
	if _, errOut, code := apRun(t, "", "bogus"); code != 64 || !strings.Contains(errOut, "usage") {
		t.Errorf("code=%d stderr=%q", code, errOut)
	}
	if out, _, _ := apRun(t, "", "--version"); !strings.HasPrefix(out, "ap ") {
		t.Errorf("version %q", out)
	}
	if _, _, code := apRun(t, "", "session-start", "nope"); code != 64 {
		t.Errorf("bad session-start step: %d", code)
	}
}

// The commands of the removed beta features are gone, so a stale hooks.json
// from an old install gets a usage error rather than a judgement.
func TestE2ERemovedCommandsAreUnknown(t *testing.T) {
	e2eHome(t)
	for _, args := range [][]string{{"prompt"}, {"judge-now"}, {"judge", "--serve"}, {"agents", "status"}, {"session-start", "judge"}} {
		out, errOut, code := apRun(t, `{"session_id": "s", "prompt": "hi"}`, args...)
		if code != 64 || out != "" || (args[0] != "session-start" && !strings.Contains(errOut, "usage")) {
			t.Errorf("%v: code=%d out=%q err=%q", args, code, out, errOut)
		}
	}
}

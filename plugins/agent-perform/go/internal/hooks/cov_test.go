package hooks

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentperform/internal/common"
	"agentperform/internal/ojson"
)

func TestEnsureDashboardLeavesCurrentOneAlone(t *testing.T) {
	home := tempHome(t)
	t.Setenv("AP_PLUGIN_ROOT", "/plugin/root")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-AP-Root", common.PluginRoot())
	}))
	defer srv.Close()
	old := DashURL
	DashURL = srv.URL
	t.Cleanup(func() { DashURL = old })
	if EnsureDashboard() != 0 {
		t.Fatal("must return 0")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "agent-perform-dashboard.log")); err == nil {
		t.Error("a current dashboard must not be relaunched")
	}
}

func TestKillListenerTermsListeningPids(t *testing.T) {
	sleeper := exec.Command("sleep", "60")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- sleeper.Wait() }()
	t.Cleanup(func() { sleeper.Process.Kill() })
	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "lsof"), []byte("#!/bin/sh\necho "+strconv.Itoa(sleeper.Process.Pid)+"\necho notapid\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	killListener(8799)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "terminated") {
			t.Errorf("wait = %v, want killed by SIGTERM", err)
		}
	case <-time.After(3 * time.Second):
		t.Error("listener was not terminated")
	}
}

func TestScrubJSONOnlyRewritesWhenChanged(t *testing.T) {
	dir := t.TempDir()
	strip := func(v any) {
		ojson.Obj(v).Set("secret", "")
	}
	scrubJSON(filepath.Join(dir, "missing.json"), strip) // no file: nothing happens
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("not json"), 0o644)
	scrubJSON(bad, strip)
	if b, _ := os.ReadFile(bad); string(b) != "not json" {
		t.Errorf("bad json must stay: %q", b)
	}
	p := filepath.Join(dir, "a.json")
	os.WriteFile(p, []byte(`{"keep": 1, "secret": "x"}`), 0o644)
	scrubJSON(p, strip)
	if b, _ := os.ReadFile(p); strings.Contains(string(b), `"x"`) || !strings.Contains(string(b), "keep") {
		t.Errorf("scrubbed = %q", b)
	}
	st1, _ := os.Stat(p)
	time.Sleep(10 * time.Millisecond)
	scrubJSON(p, strip)
	if st2, _ := os.Stat(p); !st2.ModTime().Equal(st1.ModTime()) {
		t.Error("an unchanged file must not be rewritten")
	}
}

func TestAppendLog(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.log")
	appendLog(p, "a\n")
	appendLog(p, "b\n")
	if b, _ := os.ReadFile(p); string(b) != "a\nb\n" {
		t.Errorf("log = %q", b)
	}
	appendLog(filepath.Join(t.TempDir(), "no", "dir", "l.log"), "x") // unwritable: silently dropped
}

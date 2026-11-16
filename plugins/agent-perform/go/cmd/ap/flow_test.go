package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentperform/internal/hooks"
)

// flowEnv is a temp HOME plus stand-ins for everything the hooks reach out to.
type flowEnv struct {
	t                     *testing.T
	home, sid, transcript string
	killed                string
}

func newFlowEnv(t *testing.T, sid string) *flowEnv {
	t.Helper()
	f := &flowEnv{t: t, home: t.TempDir(), sid: sid}
	root := t.TempDir()
	bin := t.TempDir()
	t.Setenv("HOME", f.home)
	t.Setenv("AP_PLUGIN_ROOT", root)
	t.Setenv("CLAUDE_AP_CHILD", "")
	t.Setenv("CLAUDE_CTX_PROBE", "")
	os.MkdirAll(filepath.Join(f.home, ".claude", "agent-perform"), 0o755)

	// lsof and claude stubs: no real process is ever signalled or started
	f.killed = filepath.Join(f.home, "killed")
	stub := func(name, script string) {
		os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+script+"\n"), 0o755)
	}
	stub("lsof", "touch "+f.killed)
	stub("claude", `printf '2k / 1m tokens (1%%)\n| Category | Tokens | Percentage |\n| System prompt | 2k | 0.2%% |\n'`)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))

	// dashboard: healthy for this plugin root, and the per-session row for the mod
	dash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/health"):
			w.Header().Set("X-AP-Root", root)
			fmt.Fprint(w, "ok")
		case strings.HasPrefix(r.URL.Path, "/type"):
			fmt.Fprintf(w, `{"row": {"id": %q, "cost": 1.5, "waste": 0.4, "turn_n": 3, "ctx_delta": 86000, "ctx_usual": 6000}}`, sid+"-0000")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(dash.Close)
	t.Setenv("AP_DASH_URL", dash.URL)
	oldDash := hooks.DashURL
	t.Cleanup(func() { hooks.DashURL = oldDash })
	hooks.DashURL = dash.URL

	f.transcript = filepath.Join(f.home, ".claude", "projects", "p", sid+"-0000.jsonl")
	os.MkdirAll(filepath.Dir(f.transcript), 0o755)
	os.WriteFile(f.transcript, []byte(
		`{"type":"user","timestamp":"2026-10-01T10:00:00Z","message":{"role":"user","content":"first question"}}`+"\n"+
			`{"type":"assistant","timestamp":"2026-10-01T10:00:05Z","message":{"id":"m1","model":"claude-opus-5-5","role":"assistant","content":[{"type":"text","text":"answer"}],"usage":{"input_tokens":1000}}}`+"\n"), 0o644)
	return f
}

func (f *flowEnv) claude(rel string) string { return filepath.Join(f.home, ".claude", rel) }

func (f *flowEnv) exists(rel string) bool { _, err := os.Stat(f.claude(rel)); return err == nil }

// step runs one hook command: exit 0 and a silent stderr are part of every flow.
func (f *flowEnv) step(stdin string, args ...string) string {
	f.t.Helper()
	out, errOut, code := apRun(f.t, stdin, args...)
	if code != 0 || errOut != "" {
		f.t.Errorf("ap %v: code=%d stderr=%q", args, code, errOut)
	}
	return out
}

// hookCommands is every `bin/ap ...` argument list hooks.json registers, per event.
func hookCommands(t *testing.T) map[string][][]string {
	t.Helper()
	raw, err := os.ReadFile("../../../hooks/hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	out := map[string][][]string{}
	for event, groups := range cfg.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				_, rest, ok := strings.Cut(h.Command, `/bin/ap" `)
				if !ok {
					t.Fatalf("%s hook does not run bin/ap: %q", event, h.Command)
				}
				rest, _, _ = strings.Cut(rest, " 2>")
				out[event] = append(out[event], strings.Fields(rest))
			}
		}
	}
	return out
}

func (f *flowEnv) hookJSON(event string, extra map[string]any) string {
	m := map[string]any{"session_id": f.sid + "-0000", "transcript_path": f.transcript, "cwd": f.home, "hook_event_name": event}
	for k, v := range extra {
		m[k] = v
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// session runs every hook hooks.json registers, in event order, and returns what they printed.
func (f *flowEnv) session() string {
	f.t.Helper()
	var out strings.Builder
	stdin := map[string]string{
		"PreToolUse": f.hookJSON("PreToolUse", map[string]any{"tool_name": "Bash", "tool_input": map[string]any{"command": "ls -la"}}),
		"Stop":       f.hookJSON("Stop", map[string]any{"stop_hook_active": false}),
	}
	cmds := hookCommands(f.t)
	for _, event := range []string{"SessionStart", "PreToolUse", "Stop", "SessionEnd"} {
		for _, args := range cmds[event] {
			out.WriteString(f.step(stdin[event], args...))
		}
	}
	return out.String()
}

// modOut is what the Claude Mod and the legacy statusline show.
func (f *flowEnv) modOut() string {
	f.t.Helper()
	in := fmt.Sprintf(`{"session_id": %q, "transcript_path": %q, "model": {"display_name": "Opus 5.5"}, "workspace": {"current_dir": %q},
		"context_window": {"context_window_size": 1000000, "remaining_percentage": 65}, "rate_limits": {}}`, f.sid+"-0000", f.transcript, f.home)
	return f.step(in, "scorecard", "--mod") + f.step(in, "statusline")
}

func (f *flowEnv) commonChecks(hookOut, mod string) {
	f.t.Helper()
	for _, bad := range []string{"agent-perform·本輪", "自己做", "派 subagent", "更正", "beta"} {
		if strings.Contains(hookOut, bad) {
			f.t.Errorf("hook output must not contain %q:\n%s", bad, hookOut)
		}
		if strings.Contains(mod, bad) {
			f.t.Errorf("mod must not contain %q:\n%s", bad, mod)
		}
	}
	for _, want := range []string{"花費 $1.50", "本回合 context +86k"} {
		if !strings.Contains(mod, want) {
			f.t.Errorf("mod lost %q:\n%s", want, mod)
		}
	}
	if _, err := os.Stat(f.killed); err == nil {
		f.t.Error("something was stopped on a machine with nothing running")
	}
	if !f.exists("ctx-cache.json") {
		f.t.Error("ctx-probe should have run")
	}
}

func TestHooksRegisterNoPromptHook(t *testing.T) {
	cmds := hookCommands(t)
	if _, ok := cmds["UserPromptSubmit"]; ok {
		t.Error("UserPromptSubmit is registered: nothing in the plugin injects per-turn context any more")
	}
	for event, list := range cmds {
		for _, args := range list {
			if strings.Contains(strings.Join(args, " "), "judge") {
				t.Errorf("%s still runs %v", event, args)
			}
		}
	}
}

// A first-time installer sees every registered hook succeed and the setup nudge.
func TestFlowFreshInstall(t *testing.T) {
	f := newFlowEnv(t, "a1a1a1a1")
	out := f.session()
	if !strings.Contains(out, "/agent-perform:setup") {
		t.Errorf("no setup nudge:\n%s", out)
	}
	f.commonChecks(out, f.modOut())
}

// A user upgrading from a version with the beta features on keeps every file
// those features left: this version neither reads nor deletes them.
func TestFlowUpgradeLeavesOldFeatureFilesAlone(t *testing.T) {
	f := newFlowEnv(t, "b2b2b2b2")
	old := []string{"agent-perform-delegate-on", "agent-perform-correction-on",
		"agent-perform/bge-m3-onnx/model.onnx_data", "agent-perform/onnxruntime/libonnxruntime.1.29.0.dylib",
		"agent-perform/agency-installed.json", "agents/agency-a.md", "corr-clf/model.npz",
		"auto_corrections.jsonl", "brain_decisions.jsonl"}
	for _, p := range old {
		os.MkdirAll(filepath.Dir(f.claude(p)), 0o755)
		os.WriteFile(f.claude(p), []byte(`{"key": "x", "correction": true}`+"\n"), 0o644)
	}
	out := f.session()
	f.commonChecks(out, f.modOut())
	for _, p := range old {
		if !f.exists(p) {
			t.Errorf("%s was removed", p)
		}
	}
}

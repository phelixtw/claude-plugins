package hooks

import (
	"agentperform/internal/dash"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"agentperform/internal/common"
	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/repetition"
	"agentperform/internal/scorecard"
	"agentperform/internal/scrub"
)

func claudeFile(name string) string { return filepath.Join(common.Home(), ".claude", name) }

// tilde shortens a path under HOME to ~/...
func tilde(p string) string {
	if home := os.Getenv("HOME"); home != "" && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

// DashURL is the dashboard; tests point it elsewhere.
var DashURL = "http://127.0.0.1:8787"

func appendLog(path, text string) {
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer fh.Close()
	fh.WriteString(text)
}

// detach starts a long-lived process with its output appended to log, the
// way `nohup cmd >>log 2>&1 & disown` did.
func detach(dir, log string, name string, args ...string) {
	out, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer out.Close()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if cmd.Start() == nil {
		cmd.Process.Release()
	}
}

// takeLock creates a lock directory; one left behind by a killed run (older
// than stale) is taken over instead of blocking every later run.
// ponytail: mtime-based staleness, a pid file if a run can ever legitimately exceed stale
func takeLock(path string, stale time.Duration) bool {
	if os.Mkdir(path, 0o755) == nil {
		return true
	}
	if st, err := os.Stat(path); err == nil && time.Since(st.ModTime()) > stale {
		os.Remove(path)
		return os.Mkdir(path, 0o755) == nil
	}
	return false
}

const lockStale = 6 * time.Hour

// ── SessionStart ──────────────────────────────────────────────────────────

// Nudge reminds the user (systemMessage) and the agent (additionalContext)
// to run /agent-perform:setup until it has passed once.
func Nudge() int {
	if _, err := os.Stat(claudeFile("agent-perform-setup-done")); err == nil {
		return 0
	}
	m := "agent-perform 剛裝好，還沒跑過初次設定。請輸入 /agent-perform:setup，確認 dashboard 與 bin/ap 已就緒。"
	out := ojson.NewObject().Set("systemMessage", m).Set("hookSpecificOutput", ojson.NewObject().
		Set("hookEventName", "SessionStart").Set("additionalContext", m+" 在回覆使用者的第一句話時提醒使用者這件事。"))
	common.Say(py.Dumps(out))
	return 0
}

// EnsureDashboard strips text older versions left behind, then makes sure the
// localhost:8787 dashboard from this plugin version is up; one from another
// version (or the old Python one) is replaced, a current one makes this a no-op.
func EnsureDashboard() int {
	ScrubAll(filepath.Join(common.Home(), ".claude"))
	client := http.Client{Timeout: 2 * time.Second}
	if resp, err := client.Get(DashURL + "/health"); err == nil {
		resp.Body.Close()
		if resp.Header.Get("X-AP-Root") == common.PluginRoot() {
			return 0
		}
		killListener(8787)
		time.Sleep(time.Second)
	}
	exe, err := os.Executable()
	if err != nil {
		return 0
	}
	detach("", claudeFile("agent-perform-dashboard.log"), exe, "dashboard", "--serve", "8787")
	return 0
}

func killListener(port int) {
	out, _ := exec.Command("lsof", "-ti", "tcp:"+strconv.Itoa(port), "-sTCP:LISTEN").Output()
	for _, f := range strings.Fields(string(out)) {
		if pid, err := strconv.Atoi(f); err == nil {
			syscall.Kill(pid, syscall.SIGTERM)
		}
	}
}

// ── SessionEnd ────────────────────────────────────────────────────────────

// sessionEndLock keeps two overlapping session ends from racing; empty means the one under HOME.
var sessionEndLock = ""

// SessionEnd refreshes the per-session caches the dashboard reads and trims the
// nightly log.
func SessionEnd() int {
	if os.Getenv("CLAUDE_AP_CHILD") != "" { // the optimize page's `claude -p` run
		return 0
	}
	lock := sessionEndLock
	if lock == "" {
		lock = claudeFile("agent-perform-session-end.lock")
	}
	if !takeLock(lock, lockStale) {
		return 0
	}
	defer os.Remove(lock)
	log := claudeFile("nightly.log")
	appendLog(log, "===== "+time.Now().Format("2006-01-02 15:04:05")+" session_end =====\n")
	repetition.Main()
	dash.SubagentsMain()
	dash.TokenmodelRefresh()
	trimLog(log, 5000)
	return 0
}

func trimLog(path string, keep int) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.SplitAfter(string(raw), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= keep {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, []byte(strings.Join(lines[len(lines)-keep:], "")), 0o644) == nil {
		os.Rename(tmp, path)
	}
}

// ── statusline ────────────────────────────────────────────────────────────

// Statusline is the legacy statusLine command: the scorecard rows.
func Statusline(stdin string) int {
	scorecard.Main(strings.TrimRight(stdin, "\n"), false)
	return 0
}

// ── scrub ─────────────────────────────────────────────────────────────────

// ScrubAll strips conversation text older versions left in the plugin's own
// files. Idempotent; a file with nothing to strip is not rewritten.
func ScrubAll(folder string) {
	scrubJSON(filepath.Join(folder, "subagent_costs.json"), func(b any) { scrub.Delegates(b) })
	scrubJSON(filepath.Join(folder, "agent_health.json"), func(b any) { scrub.Health(b) })
}

func replaceFile(path, text string) {
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if os.WriteFile(tmp, []byte(text), 0o644) == nil {
		os.Rename(tmp, path)
	}
}

func scrubJSON(path string, strip func(any)) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	blob, err := ojson.Parse(raw)
	if err != nil {
		return
	}
	before := py.Dumps(blob)
	strip(blob)
	if after := py.Dumps(blob); after != before {
		replaceFile(path, after)
	}
}

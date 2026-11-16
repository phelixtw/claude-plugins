package scorecard

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"agentperform/internal/common"
)

func TestCountdownTiers(t *testing.T) {
	for _, c := range []struct {
		left float64
		want string
	}{{-5, "已重置"}, {0, "已重置"}, {59 * 60, "59m"}, {3*3600 + 5*60, "3h05m"}, {2*86400 + 3*3600, "2d3h"}} {
		if got := countdown(1000+c.left, 1000); got != c.want {
			t.Errorf("countdown(+%v) = %q, want %q", c.left, got, c.want)
		}
	}
}

func TestHTTPJSONFailures(t *testing.T) {
	status, body := 200, `{"a": 1}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	if httpJSON(srv.URL, time.Second) == nil {
		t.Error("200 + json must parse")
	}
	status = 500
	if httpJSON(srv.URL, time.Second) != nil {
		t.Error("non-2xx must be nil")
	}
	status, body = 200, "not json"
	if httpJSON(srv.URL, time.Second) != nil {
		t.Error("bad body must be nil")
	}
	if httpJSON("http://127.0.0.1:1", time.Second) != nil {
		t.Error("unreachable must be nil")
	}
}

func TestBoardEncodesSessionAndPath(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.RawQuery
		fmt.Fprint(w, `{"row": null}`)
	}))
	defer srv.Close()
	t.Setenv("AP_DASH_URL", srv.URL)
	if board("abcdefghijk", "/a b/c.jsonl") == nil {
		t.Fatal("board nil")
	}
	if got != "session=abcdefgh&path=%2Fa%20b%2Fc.jsonl" && got != "session=abcdefgh&path=/a%20b/c.jsonl" {
		t.Errorf("query = %q", got)
	}
}

func TestScorecardLineRejectsIncompleteRows(t *testing.T) {
	for name, raw := range map[string]string{
		"no cost":          `{"id": "s", "waste": 0}`,
		"bad delegate":     `{"id": "s", "cost": 1, "delegate_cost": "x", "waste": 0}`,
		"no id":            `{"cost": 1, "waste": 0}`,
		"no waste":         `{"id": "s", "cost": 1}`,
		"bad ctx_delta":    `{"id": "s", "cost": 1, "waste": 0, "ctx_delta": "x"}`,
		"bad ctx_usual":    `{"id": "s", "cost": 1, "waste": 0, "ctx_delta": 5, "ctx_usual": "x"}`,
		"non-numeric cost": `{"id": "s", "cost": "x", "waste": 0}`,
	} {
		if line, ok := scorecardLine(obj(t, raw)); ok {
			t.Errorf("%s accepted: %q", name, line)
		}
	}
	line, ok := scorecardLine(obj(t, `{"id": "s", "cost": 1, "waste": 0.25, "ctx_delta": 86000, "ctx_usual": -6000}`))
	if !ok || line != "s #— · 花費 $1.00 · 浪費 $0.25 · 本回合 context +86k（平常 -6k）" {
		t.Errorf("line = %q %v", line, ok)
	}
}

func TestHeadlessRowsAndCache(t *testing.T) {
	sid := "covrows1"
	cache := "/tmp/claude-scorecard-cache-" + sid + ".json"
	os.Remove(cache)
	t.Cleanup(func() { os.Remove(cache) })
	tr := filepath.Join(t.TempDir(), sid+".jsonl")
	os.WriteFile(tr, []byte(`{"type":"user","message":{"content":"hi"}}`+"\n"), 0o644)

	kindOf := func(path string) rowsKind {
		var k rowsKind
		capture(func() int { _, k = rows(sid, path); return 0 })
		return k
	}
	t.Setenv("AP_DASH_URL", "http://127.0.0.1:1")
	if kind := kindOf(tr); kind != noRow {
		t.Errorf("no cache = %v", kind)
	}
	os.WriteFile(cache, []byte("garbage"), 0o644)
	if kind := kindOf(tr); kind != noRow {
		t.Errorf("unparseable cache = %v", kind)
	}
	if kind := kindOf(filepath.Join(t.TempDir(), "none.jsonl")); kind != noRow {
		t.Errorf("no transcript = %v", kind)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"row": null}`) }))
	defer srv.Close()
	t.Setenv("AP_DASH_URL", srv.URL)
	out, _ := capture(func() int { _, k := rows(sid, tr); return int(k) })
	if out != "" {
		t.Errorf("empty row and no delegation print nothing: %q", out)
	}
}

func TestBirthAndTranscriptFor(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a")
	os.WriteFile(f, nil, 0o644)
	if at, ok := birth(f); !ok || common.Now()-at > 60 || at > common.Now()+1 {
		t.Errorf("birth = %v %v", at, ok)
	}
	if _, ok := birth(f + "-missing"); ok {
		t.Error("missing file has no birth")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	if transcriptFor("sid1") != "" {
		t.Error("no projects dir")
	}
	proj := filepath.Join(home, ".claude", "projects")
	os.MkdirAll(filepath.Join(proj, ".hidden"), 0o755)
	os.MkdirAll(filepath.Join(proj, "real"), 0o755)
	os.WriteFile(filepath.Join(proj, ".hidden", "sid1.jsonl"), nil, 0o644)
	if transcriptFor("sid1") != "" {
		t.Error("dot dirs are skipped")
	}
	want := filepath.Join(proj, "real", "sid1.jsonl")
	os.WriteFile(want, nil, 0o644)
	if got := transcriptFor("sid1"); got != want {
		t.Errorf("transcriptFor = %q", got)
	}
}

func TestMainQuietWindowPrintsNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sid := "covquiet1"
	mod := "/tmp/claude-ap-mod-" + sid
	os.WriteFile(mod, nil, 0o644)
	t.Cleanup(func() { os.Remove(mod) })
	out, code := capture(func() int { return Main(`{"session_id": "`+sid+`"}`, false) })
	if out != "" || code != 0 {
		t.Errorf("recent mod file silences the legacy line: %q %d", out, code)
	}
	old := time.Now().Add(-10 * time.Minute)
	os.Chtimes(mod, old, old)
	t.Setenv("AP_DASH_URL", "http://127.0.0.1:1")
	if out, _ := capture(func() int { return Main(`{"session_id": "`+sid+`"}`, false) }); !strings.Contains(out, "(0K)") {
		t.Errorf("a stale mod file must not silence it: %q", out)
	}
}

func TestMainStatusLineVariants(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AP_DASH_URL", "http://127.0.0.1:1")
	repo := t.TempDir()
	if err := exec.Command("git", "init", "-q", "-b", "feat", repo).Run(); err != nil {
		t.Skip("git unavailable")
	}
	resets := time.Now().Add(3*time.Hour + 10*time.Minute).Unix()
	in := fmt.Sprintf(`{"model": {"display_name": "Opus 4 (1M context)"}, "context_window": {"context_window_size": 1000000},
 "workspace": {"current_dir": %q}, "rate_limits": {"five_hour": {"used_percentage": 42.4, "resets_at": %d}, "seven_day": {}}}`, repo, resets)
	out, code := capture(func() int { return Main(in, false) })
	line := strings.Split(out, "\n")[0]
	base := filepath.Base(repo)
	for _, want := range []string{"Opus 4 (1M context) · " + base + "(feat) · --", "context 已用 --", "5h 已用 42%(3h09m)", "7d --"} {
		if !strings.Contains(line, want) {
			t.Errorf("status line lacks %q: %q", want, line)
		}
	}
	if code != 0 {
		t.Errorf("code %d", code)
	}
	// plain model name gets a size tag, and 1M windows say M
	out, _ = capture(func() int {
		return Main(`{"model": {"display_name": "Sonnet 4"}, "context_window": {"context_window_size": 1000000}}`, false)
	})
	if !strings.HasPrefix(out, "Sonnet 4(1M) · ") {
		t.Errorf("out %q", out)
	}
	// a raw model id is shortened to family + version
	out, _ = capture(func() int {
		return Main(`{"model": {"display_name": "claude-opus-5-5[1m]"}, "context_window": {"context_window_size": 1000000}}`, false)
	})
	if !strings.HasPrefix(out, "Opus 5.5(1M) · ") {
		t.Errorf("out %q", out)
	}
}

func TestMainModOnlyAlerts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("AP_EXP_CACHE_COUNTDOWN", "1")
	sid := "covmod01"
	cache := "/tmp/claude-scorecard-cache-" + sid + ".json"
	t.Cleanup(func() { os.Remove(cache) })
	dir := filepath.Join(home, ".claude", "projects", "proj")
	os.MkdirAll(dir, 0o755)
	tr := filepath.Join(dir, sid+".jsonl")
	os.WriteFile(tr, []byte(`{"type":"user","message":{"content":"hi"}}`+"\n"), 0o644)
	dash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"row": {"id": "covmod01", "cost": 1, "waste": 0, "ctx_delta": 90000, "ctx_usual": 1000}}`)
	}))
	defer dash.Close()
	t.Setenv("AP_DASH_URL", dash.URL)
	in := fmt.Sprintf(`{"session_id": %q, "prompt_cache": {"expires_at": %d, "ttl": "1h"}}`, sid, time.Now().Unix()+120)
	out, _ := capture(func() int { return Main(in, true) }) // transcript found through the session id
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if !strings.Contains(lines[0], "Cache 剩 ") || !strings.Contains(lines[0], "/1h") {
		t.Errorf("status line %q", lines[0])
	}
	if last := lines[len(lines)-1]; last != "alert:cache,burst" {
		t.Errorf("alerts = %q (all: %q)", last, out)
	}
}

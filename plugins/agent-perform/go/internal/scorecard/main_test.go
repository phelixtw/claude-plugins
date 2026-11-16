package scorecard

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func capture(fn func() int) (string, int) {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	code := fn()
	w.Close()
	os.Stdout = old
	return <-done, code
}

const hookIn = `{"session_id": "e2escsid1", "transcript_path": "%s", "model": {"display_name": "Opus 4"},
 "context_window": {"context_window_size": 200000}, "workspace": {"project_dir": "/nonexistent/proj"}}`

func TestMainPrintsStatusDelegationAndRow(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cache := "/tmp/claude-scorecard-cache-e2escsid.json"
	defer os.Remove(cache)
	dash := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"row": {"id": "e2escsid1", "turn_n": 3, "cost": 1.0, "delegate_cost": 0.5, "waste": 0}}`)
	}))
	defer dash.Close()
	t.Setenv("AP_DASH_URL", dash.URL)
	tr := filepath.Join(t.TempDir(), "e2escsid1.jsonl")
	os.WriteFile(tr, []byte(`{"type":"user","message":{"content":"hi"}}`+"\n"), 0o644)
	out, code := capture(func() int { return Main(fmt.Sprintf(hookIn, tr), false) })
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(lines) != 2 {
		t.Fatalf("code=%d out=%q", code, out)
	}
	if !strings.HasPrefix(lines[0], "Opus 4(200K) · proj · ") || !strings.Contains(lines[0], "context 已用 --") {
		t.Errorf("status line %q", lines[0])
	}
	if lines[1] != "e2escsid #3 · 花費 $1.50" {
		t.Errorf("row line %q", lines[1])
	}
	// dashboard down: the last row this session had is still shown
	t.Setenv("AP_DASH_URL", "http://127.0.0.1:1")
	out, _ = capture(func() int { return Main(fmt.Sprintf(hookIn, tr), false) })
	if !strings.Contains(out, "e2escsid #3 · 花費 $1.50") {
		t.Errorf("cached row expected: %q", out)
	}
	// an unusable cache is a failure, not a silent blank
	os.WriteFile(cache, []byte(`{"id": "x"}`), 0o644)
	if _, code := capture(func() int { return Main(fmt.Sprintf(hookIn, tr), false) }); code != 1 {
		t.Errorf("crash code = %d", code)
	}
}

func TestMainBadInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if out, code := capture(func() int { return Main("not json", false) }); out != "" || code != 0 {
		t.Errorf("garbage: %q %d", out, code)
	}
	if _, code := capture(func() int { return Main("[1]", false) }); code != 1 {
		t.Errorf("non-object: %d", code)
	}
	// no transcript: only the status line
	out, _ := capture(func() int { return Main(`{"session_id": "e2escsid2"}`, true) })
	if strings.Count(out, "\n") != 1 { // status only: no delegation, no row
		t.Errorf("out %q", out)
	}
}

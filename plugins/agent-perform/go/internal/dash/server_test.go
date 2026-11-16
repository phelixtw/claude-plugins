package dash

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestServer(t *testing.T) {
	home := t.TempDir()
	t.Cleanup(func() { settle(t) })
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	h := NewHandler(8787)
	req := func(host, target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", target, nil)
		r.Host = host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := req("127.0.0.1:8787", "/health"); w.Code != 200 || w.Body.String() != "ok" {
		t.Errorf("/health: %d %q", w.Code, w.Body.String())
	}
	w := req("localhost:8787", "/")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "近期沒有資料。") ||
		!strings.Contains(w.Body.String(), "<title>"+brand+"</title>") {
		t.Errorf("/: %d", w.Code)
	}
	if w := req("evil.example:8787", "/health"); w.Code != http.StatusForbidden {
		t.Errorf("foreign Host: got %d, want 403", w.Code)
	}
	if code, hd, _ := Render("/glossary", ""); code != 302 || hd["Location"] != "/how#terms" {
		t.Errorf("/glossary: %d %v", code, hd)
	}
	if code, _, _ := Render("/patterns/", ""); code != 404 {
		t.Errorf("/patterns/: %d", code)
	}
}

func TestPythonSemantics(t *testing.T) {
	q := parseQS("a=1&a=2&b=&c&d=%E4%BD%A0+%zz")
	if len(q["a"]) != 2 || q["b"] != nil || q["c"] != nil || first(q, "d") != "你 %zz" {
		t.Errorf("parseQS: %v", q)
	}
	if _, ok := strptimeDay("2026-1-5"); !ok {
		t.Error("strptimeDay rejects a non-padded date")
	}
	if _, ok := strptimeDay("2026-02-30"); ok {
		t.Error("strptimeDay accepts Feb 30")
	}
	if p, qs := splitURI("//x;p/y;z?k=v#f"); p != "/x;p/y" || qs != "k=v" {
		t.Errorf("splitURI: %q %q", p, qs)
	}
}

// A page refresh and /pipeline run at the same time; run with -race.
func TestPipelineAlongsidePages(t *testing.T) {
	testHome(t)
	now := float64(time.Now().Unix()) - 3600
	for _, id := range []string{"aaaa1111", "bbbb2222", "cccc3333"} {
		writeRows(t, filepath.Join(projects(), "-x", id+"-0000.jsonl"),
			userRec(now, "hi "+id), assistantRec(now+5, "m1", 20_000, 0, ""),
			userRec(now+60, "again"), assistantRec(now+65, "m2", 1_000, 20_000, ""))
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); Render("/", "") }()
		go func() { defer wg.Done(); Render("/pipeline", "session=aaaa1111&turn=2") }()
	}
	wg.Wait()
	if st, _, body := Render("/pipeline", "session=aaaa1111&turn=2"); st != 200 || !strings.Contains(string(body), "Context Window") {
		t.Errorf("pipeline %d", st)
	}
}

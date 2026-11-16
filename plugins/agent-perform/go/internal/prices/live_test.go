package prices

import (
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var opusRow = regexp.MustCompile(`(Claude Opus 5\.5\s*\|\s*)\$4 /`)

// pageWith builds a pricing page from the real fixture, with the given opus 5.5 input price.
func pageWith(t *testing.T, opusIn string) string {
	t.Helper()
	md, err := os.ReadFile("testdata/pricing_model_table.md")
	if err != nil {
		t.Fatal(err)
	}
	if opusIn == "" {
		return string(md)
	}
	out := opusRow.ReplaceAllString(string(md), "${1}"+opusIn+" /")
	if out == string(md) {
		t.Fatal("fixture row not found")
	}
	return out
}

// env points the fetcher at srv and the cache at a temp HOME, and restores global state afterwards.
func env(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldURL, oldFile, oldTO, oldSheet := pricingURL, cacheFile, fetchTimeout, current.Load()
	pricingURL, cacheFile = srv.URL, filepath.Join(home, ".claude", "agent-perform", "prices.json")
	liveAt.Store(0)
	fetchBad.Store(false)
	t.Cleanup(func() {
		pricingURL, cacheFile, fetchTimeout = oldURL, oldFile, oldTO
		current.Store(oldSheet)
		liveAt.Store(0)
		fetchBad.Store(false)
	})
	return cacheFile
}

func serve(status int, body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

func TestRefreshSuccessUsesLivePriceAndWritesCache(t *testing.T) {
	srv := serve(200, pageWith(t, "$$4.5"))
	defer srv.Close()
	file := env(t, srv)
	refresh()
	if got := Of("claude-opus-5-5")[0]; got != 4.5 {
		t.Errorf("live input price = %v, want 4.5", got)
	}
	if !strings.Contains(Source(), "Anthropic 官方") || !strings.Contains(Source(), time.Now().Format("2006-01-02")) {
		t.Errorf("source = %q", Source())
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("cache not written: %v", err)
	}
	// rows the page dropped keep the built-in rate
	if got := Of("claude-3-5-haiku-20241022"); got != (Rates{0.8, 4, 0.08, 1.6, 1}) {
		t.Errorf("builtin row lost: %v", got)
	}
}

func TestFailuresFallBackToBuiltin(t *testing.T) {
	cases := map[string]*httptest.Server{
		"500":         serve(500, "boom"),
		"404":         serve(404, "nope"),
		"malformed":   serve(200, "## Model pricing\n\n| Model | Foo |\n| - | - |\n"),
		"no price":    serve(200, opusRow.ReplaceAllString(pageWith(t, ""), "${1}free /")),
		"implausible": serve(200, "## Model pricing\n\n| Model | Base Input Tokens | 5m Cache Writes | 1h Cache Writes | Cache Hits & Refreshes | Output Tokens |\n| - | - | - | - | - | - |\n| Claude Opus 5.5 | $4 | $5 | $8 | $0.2 | $20 |\n"),
		"empty":       serve(200, ""),
	}
	for name, srv := range cases {
		t.Run(name, func(t *testing.T) {
			defer srv.Close()
			file := env(t, srv)
			refresh()
			if got := Of("claude-opus-5-5")[0]; got != 4 {
				t.Errorf("price = %v, want built-in 4", got)
			}
			if got := Source(); got != "價目：內建表（官方抓取失敗）" {
				t.Errorf("source = %q", got)
			}
			if _, err := os.Stat(file); err == nil {
				t.Error("cache must not be written on failure")
			}
		})
	}
}

func TestUnreachableAndTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { time.Sleep(300 * time.Millisecond) }))
	defer srv.Close()
	env(t, srv)
	fetchTimeout = 50 * time.Millisecond
	refresh()
	if Source() != "價目：內建表（官方抓取失敗）" || Of("claude-opus-5-5")[0] != 4 {
		t.Errorf("timeout: %q", Source())
	}
	pricingURL = "http://127.0.0.1:1" // refused
	refresh()
	if Source() != "價目：內建表（官方抓取失敗）" {
		t.Errorf("refused: %q", Source())
	}
}

// A failed refresh keeps the last good cached sheet; a later success clears the failure flag.
func TestFailureKeepsLastGoodCache(t *testing.T) {
	good := serve(200, pageWith(t, "$$4.5"))
	defer good.Close()
	file := env(t, good)
	refresh()
	current.Store(builtin) // simulate a fresh process
	liveAt.Store(0)
	bad := serve(500, "")
	defer bad.Close()
	pricingURL = bad.URL
	loadCache()
	refresh()
	if got := Of("claude-opus-5-5")[0]; got != 4.5 {
		t.Errorf("after failed refresh price = %v, want cached 4.5", got)
	}
	if !strings.Contains(Source(), "Anthropic 官方") {
		t.Errorf("source = %q", Source())
	}
	if _, err := os.Stat(file); err != nil {
		t.Error("cache removed")
	}
	pricingURL = good.URL
	refresh()
	if fetchBad.Load() {
		t.Error("success must clear the failure flag")
	}
}

func TestLoadCache(t *testing.T) {
	srv := serve(200, "")
	defer srv.Close()
	file := env(t, srv)
	loadCache() // no file
	if Source() != "價目：內建表" {
		t.Errorf("source = %q", Source())
	}
	os.MkdirAll(filepath.Dir(file), 0o755)
	for name, content := range map[string]string{
		"corrupt":     "{nope",
		"implausible": `{"fetched": 1700000000, "table": {"opus-5-5": [1,2,3,4,5]}}`,
	} {
		os.WriteFile(file, []byte(content), 0o644)
		loadCache()
		if Source() != "價目：內建表" || Of("claude-opus-5-5")[0] != 4 {
			t.Errorf("%s cache used: %q", name, Source())
		}
	}
}

// Start loads a fresh cache and never fetches; the cache's fetch date shows in Source.
func TestStartWithFreshCacheSkipsFetch(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	defer srv.Close()
	file := env(t, srv)
	src := serve(200, pageWith(t, "$$4.5"))
	defer src.Close()
	pricingURL = src.URL
	refresh()
	current.Store(builtin)
	liveAt.Store(0)
	pricingURL = srv.URL
	_ = file
	Start()
	time.Sleep(100 * time.Millisecond)
	if hits != 0 {
		t.Errorf("fetched %d times despite fresh cache", hits)
	}
	if Of("claude-opus-5-5")[0] != 4.5 {
		t.Error("cache not loaded")
	}
}

// Start with a stale (or no) cache refreshes in the background.
func TestStartRefreshesStaleCache(t *testing.T) {
	srv := serve(200, pageWith(t, "$$4.5"))
	defer srv.Close()
	env(t, srv)
	Start()
	for i := 0; i < 50 && Of("claude-opus-5-5")[0] != 4.5; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if Of("claude-opus-5-5")[0] != 4.5 {
		t.Errorf("background refresh did not land: %q", Source())
	}
}

// Price math: each token type is billed at its own rate, per million tokens.
func TestPriceMathPerTokenType(t *testing.T) {
	const m = 1_000_000
	cases := []struct {
		name         string
		i, o, cr, cw float64
		short        bool
		want         float64
	}{
		{"input", m, 0, 0, 0, false, 4},
		{"output", 0, m, 0, 0, false, 20},
		{"cache read", 0, 0, m, 0, false, 0.20},
		{"cache write 1h", 0, 0, 0, m, false, 8},
		{"cache write 5m", 0, 0, 0, m, true, 5},
		{"mixed", 10_000, 2_000, 30_000, 5_000, true, (10_000*4 + 2_000*20 + 30_000*0.2 + 5_000*5) / m},
		{"zero", 0, 0, 0, 0, false, 0},
	}
	for _, c := range cases {
		got := Price("claude-opus-5-5", c.i, c.o, c.cr, c.cw, c.short)
		if d := got - c.want; d > 1e-12 || d < -1e-12 {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	// long-prompt rates apply only above 100k prompt tokens (input + reads + writes)
	if got := Price("claude-haiku-5-5", 100_000, 0, 0, 0, false); got != 0.01 {
		t.Errorf("exactly 100k stays short: %v", got)
	}
	if got := Price("claude-haiku-5-5", 100_001, 0, 0, 0, false); got <= 0.05 {
		t.Errorf("over 100k uses long input rate 0.5: %v", got)
	}
	if got := Price("claude-haiku-5-5", 50_000, 0, 30_000, 20_001, true); math.Abs(got-0.039000625) > 1e-12 {
		t.Errorf("writes count toward the long threshold: %v", got)
	}
	if CacheRead("claude-sonnet-5-5") != 0.10 {
		t.Error("CacheRead")
	}
	// unknown model prices at the default family
	if Price("mystery", m, 0, 0, 0, false) != 2 {
		t.Error("default family")
	}
}

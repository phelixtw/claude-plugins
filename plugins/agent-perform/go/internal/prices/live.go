package prices

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	maxAge     = 24 * time.Hour
	retryAfter = time.Hour // a failed fetch is retried hourly, not on every page view
)

// pricingURL and fetchTimeout are vars so tests can point refresh at a local server.
var (
	pricingURL   = "https://platform.claude.com/docs/en/about-claude/pricing.md"
	fetchTimeout = 10 * time.Second
)

// cacheFile is where the last good fetch lives; var so tests can point it elsewhere.
var cacheFile = filepath.Join(os.Getenv("HOME"), ".claude", "agent-perform", "prices.json")

type cached struct {
	Fetched int64            `json:"fetched"`
	Table   map[string]Rates `json:"table"`
	Long    map[string]Rates `json:"long"`
}

var (
	liveAt   atomic.Int64 // unix seconds of the sheet in use; 0 = built-in
	fetchBad atomic.Bool  // the last fetch failed
)

// Source says where the dollar figures come from, for the dashboard.
func Source() string {
	switch at := liveAt.Load(); {
	case at > 0:
		return "價目：Anthropic 官方，" + time.Unix(at, 0).Format("2006-01-02") + " 更新"
	case fetchBad.Load():
		return "價目：內建表（官方抓取失敗）"
	}
	return "價目：內建表"
}

// Start loads the cache now, then refreshes in the background when it is over a day old. It never blocks.
func Start() {
	loadCache()
	go func() {
		for {
			if time.Since(time.Unix(liveAt.Load(), 0)) > maxAge {
				refresh()
			}
			time.Sleep(retryAfter)
		}
	}()
}

func loadCache() {
	raw, err := os.ReadFile(cacheFile)
	var c cached
	if err != nil || json.Unmarshal(raw, &c) != nil || !plausible(c.Table) {
		return
	}
	current.Store(merged(c.Table, c.Long))
	liveAt.Store(c.Fetched)
}

func refresh() {
	resp, err := (&http.Client{Timeout: fetchTimeout}).Get(pricingURL)
	var body []byte
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == 200 {
			body, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		} else {
			err = fmt.Errorf("status %d", resp.StatusCode)
		}
	}
	var table, long map[string]Rates
	if err == nil {
		if table, long, err = Parse(string(body)); err == nil && !plausible(table) {
			err = fmt.Errorf("implausible table")
		}
	}
	if err != nil {
		fetchBad.Store(true) // the last good cache, or the built-in table, stays in use
		return
	}
	now := time.Now()
	if raw, err := json.Marshal(cached{now.Unix(), table, long}); err == nil {
		_ = os.MkdirAll(filepath.Dir(cacheFile), 0o755)
		_ = os.WriteFile(cacheFile, raw, 0o644)
	}
	current.Store(merged(table, long))
	liveAt.Store(now.Unix())
	fetchBad.Store(false)
}

// merged lays fetched rows over the built-in ones, so family fallbacks and rows the page dropped survive.
func merged(table, long map[string]Rates) *sheet {
	t, l := map[string]Rates{}, map[string]Rates{}
	for k, v := range builtin.table {
		t[k] = v
	}
	for k, v := range builtin.long {
		l[k] = v
	}
	for k, v := range table {
		t[k] = v
	}
	for k, v := range long {
		l[k] = v
	}
	return newSheet(t, l)
}

// plausible: every model the dashboard depends on is present and the columns are in a sane order.
func plausible(table map[string]Rates) bool {
	for _, k := range []string{"fable-5-1", "opus-5-5", "sonnet-5-5", "haiku-5-5", "opus-4-6"} {
		v, ok := table[k]
		if !ok || v[0] <= 0 || v[2] <= 0 || v[2] >= v[0] || v[1] <= v[0] || v[4] < v[0] || v[3] < v[4] {
			return false
		}
	}
	return len(table) >= 8
}

var (
	money    = regexp.MustCompile(`\$([\d.]+)`)
	sup      = regexp.MustCompile(`<sup>.*?</sup>`)
	paren    = regexp.MustCompile(`\s*\(.*`)
	notAlnum = regexp.MustCompile(`[^a-z0-9]+`)
)

// Parse reads the "Model pricing" table of the pricing page (markdown).
// Rows for prompts over 100,000 tokens go to long.
func Parse(md string) (table, long map[string]Rates, err error) {
	table, long = map[string]Rates{}, map[string]Rates{}
	col := map[string]int{}
	in := false
	for _, ln := range strings.Split(md, "\n") {
		if strings.HasPrefix(ln, "## ") {
			in = strings.TrimSpace(ln[3:]) == "Model pricing"
			continue
		}
		if !in || !strings.HasPrefix(ln, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(ln), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if len(col) == 0 {
			for i, c := range cells {
				c = strings.ToLower(c)
				for name, want := range map[string]string{"in": "base input", "out": "output", "read": "cache hits", "w1h": "1h cache", "w5m": "5m cache"} {
					if strings.HasPrefix(c, want) {
						col[name] = i + 1 // 0 stays "not found"
					}
				}
			}
			if len(col) != 5 {
				return nil, nil, fmt.Errorf("pricing columns not found")
			}
			continue
		}
		if strings.HasPrefix(cells[0], ":-") || len(cells) < 6 {
			continue
		}
		var rt Rates
		for i, name := range []string{"in", "out", "read", "w1h", "w5m"} {
			m := money.FindStringSubmatch(sup.ReplaceAllString(cells[col[name]-1], ""))
			if m == nil {
				return nil, nil, fmt.Errorf("no price in %q", ln)
			}
			if rt[i], err = strconv.ParseFloat(m[1], 64); err != nil {
				return nil, nil, err
			}
		}
		name := strings.ToLower(cells[0])
		dst := table
		if strings.Contains(name, "over 100,000") {
			dst = long
		}
		name = strings.TrimPrefix(strings.TrimSpace(paren.ReplaceAllString(name, "")), "claude ")
		dst[strings.Trim(notAlnum.ReplaceAllString(name, "-"), "-")] = rt
	}
	return table, long, nil
}

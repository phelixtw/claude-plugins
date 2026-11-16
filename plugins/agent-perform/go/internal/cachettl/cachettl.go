// Package cachettl reads one session's prompt-cache expiry from its transcript.
//
// The cache clock restarts at the end of every API call, so the countdown runs
// from the last main-thread assistant message, not from the user's message. The
// TTL comes from the latest usage.cache_creation block (ephemeral_1h: 1 hour,
// ephemeral_5m: 5 minutes, unknown: 5m). The statusline input's
// prompt_cache.expires_at wins when present (Claude Code 2.1.251+,
// https://code.claude.com/docs/en/statusline#prompt-cache-fields).
//
// A 5m cache under the cache-keepalive flag is renewed by /v1/messages/cache_touch
// while the CLI is open, until activityWindowMinutes pass without activity; it
// then lapses 5 minutes after the last touch.
package cachettl

import (
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"agentperform/internal/common"
	"agentperform/internal/ojson"
)

const (
	TTL5m        = 300.0
	TTL1h        = 3600.0
	tail         = 4_000_000
	keepaliveFlg = "tengu_sequential_cloud"
)

type State struct {
	Left float64
	TTL  float64
	Kept bool
}

func ttlOf(usage any) (float64, bool) {
	if !ojson.Truthy(usage) {
		return 0, false
	}
	cc := ojson.GetOr(usage, "cache_creation")
	if cc == nil {
		return 0, false
	}
	n := func(k string) float64 {
		f, ok := ojson.Float(ojson.GetOr(cc, k))
		if !ok {
			return 0
		}
		return f
	}
	switch {
	case n("ephemeral_5m_input_tokens") > 0:
		return TTL5m, true
	case n("ephemeral_1h_input_tokens") > 0:
		return TTL1h, true
	}
	return 0, false
}

// Splitlines splits on every Unicode line break, not just \n (str.splitlines).
func Splitlines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch r {
		case '\n', '\r', '\x0b', '\x0c', '\x1c', '\x1d', '\x1e', 0x85, 0x2028, 0x2029:
			out = append(out, s[start:i])
			start = i + size
			if r == '\r' && i+1 < len(s) && s[i+1] == '\n' {
				size++
				start++
			}
		}
		i += size
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// lastCall is (end of the last main-thread API call, ttl) from the end of the transcript.
func lastCall(path string) (float64, bool, float64) {
	fh, err := os.Open(path)
	if err != nil {
		return 0, false, TTL5m
	}
	defer fh.Close()
	size, err := fh.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, false, TTL5m
	}
	if _, err := fh.Seek(max(size-tail, 0), io.SeekStart); err != nil {
		return 0, false, TTL5m
	}
	raw, err := io.ReadAll(fh)
	if err != nil {
		return 0, false, TTL5m
	}
	lines := Splitlines(common.Lossy(raw))
	var last, ttl float64
	haveLast, haveTTL := false, false
	for i := len(lines) - 1; i >= 0; i-- {
		e, err := ojson.Parse([]byte(lines[i]))
		if err != nil {
			continue
		}
		if t, _ := ojson.Str(ojson.Field(e, "type")); t != "assistant" || ojson.Truthy(ojson.Field(e, "isSidechain")) {
			continue
		}
		if !haveLast {
			if ts, ok := ojson.Str(ojson.Field(e, "timestamp")); ok {
				last, haveLast = common.IsoTS(ts)
			}
		}
		if !haveTTL {
			ttl, haveTTL = ttlOf(ojson.Field(ojson.GetOr(e, "message"), "usage"))
		}
		if haveLast && haveTTL {
			break
		}
	}
	if !haveTTL {
		ttl = TTL5m
	}
	return last, haveLast, ttl
}

func keepaliveWindow() (float64, bool) {
	raw, err := os.ReadFile(common.Join(common.Home(), ".claude.json"))
	if err != nil {
		return 0, false
	}
	blob, err := ojson.Parse(raw)
	if err != nil {
		return 0, false
	}
	flag := ojson.Field(ojson.GetOr(blob, "cachedGrowthBookFeatures"), keepaliveFlg)
	if ojson.Obj(flag) == nil || !ojson.Truthy(ojson.Field(flag, "enabled")) {
		return 0, false
	}
	mins := 60.0
	switch v := ojson.GetOr(flag, "activityWindowMinutes").(type) {
	case nil:
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		mins = f
	default:
		if f, ok := ojson.Float(v); ok {
			mins = math.Trunc(f)
		}
	}
	return mins * 60, true
}

func StateOf(path string, now float64) *State {
	last, ok, ttl := lastCall(path)
	if !ok {
		return nil
	}
	window, kept := 0.0, false
	if ttl == TTL5m {
		window, kept = keepaliveWindow()
	}
	return &State{Left: last + window + ttl - now, TTL: ttl, Kept: kept}
}

func StateFromInput(pc any, now float64) *State {
	if pc == nil {
		return nil
	}
	exp, ok := ojson.Float(ojson.Field(pc, "expires_at"))
	if !ok {
		return nil
	}
	s, _ := ojson.Str(ojson.Field(pc, "ttl"))
	var ttl float64
	switch s {
	case "5m":
		ttl = TTL5m
	case "1h":
		ttl = TTL1h
	default:
		return nil
	}
	return &State{Left: exp - now, TTL: ttl}
}

func clock(secs float64) string {
	t := int64(math.Abs(secs))
	if t >= 3600 {
		return fmt.Sprintf("%dh%02dm", t/3600, (t%3600)/60)
	}
	return fmt.Sprintf("%dm%02ds", t/60, t%60)
}

func Label(st *State) string {
	if st == nil {
		return "Cache --"
	}
	tag := "5m"
	if st.TTL == TTL1h {
		tag = "1h"
	} else if st.Kept {
		tag = "背景續期"
	}
	if st.Left <= 0 {
		return "Cache 已過期"
	}
	return fmt.Sprintf("Cache 剩 %s/%s", clock(st.Left), tag)
}

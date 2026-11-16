// Package scorecard prints the statusline / mod rows:
//  1. model / project(branch) / elapsed (cache countdown in the mod) / context used / quota
//  2. the subagent this turn launched (no row when it delegated nothing)
//  3. id #turn · cost · waste · context growth this turn, from :8787
//
// plus `alert:ctx,cache,burst` for the mod's marquee, and the live context size
// dumped to /tmp/claude-ctx-<session>.json.
package scorecard

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"agentperform/internal/cachettl"
	"agentperform/internal/common"
	"agentperform/internal/ojson"
)

const (
	ctxAlertPct    = 40.0
	cacheAlertSecs = 300.0
	burstTimes     = 5.0
	burstMinExtra  = 50_000.0
)

// Unicode \w, as Rust's regex has it
var windowTag = regexp.MustCompile(`\[[\pL\pM\p{Nd}\p{Nl}\p{Pc}\x{200C}\x{200D}]+\]$`)

// a raw model id passed as display_name: claude-opus-5-5 → Opus 5.5
var rawModelID = regexp.MustCompile(`^claude-([a-z]+)-(\d+(?:-\d+)*)$`)

func shortModel(name string) string {
	m := rawModelID.FindStringSubmatch(name)
	if m == nil {
		return name
	}
	return strings.ToUpper(m[1][:1]) + m[1][1:] + " " + strings.ReplaceAll(m[2], "-", ".")
}

func countdown(target, now float64) string {
	left := int64(target - now)
	if left <= 0 {
		return "已重置"
	}
	h, m := left/3600, (left%3600)/60
	switch {
	case h >= 24:
		return fmt.Sprintf("%dd%dh", h/24, h%24)
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func f0(v float64) string { return fmt.Sprintf("%.0f", v) }

func quota(limits any, key string, now float64) string {
	l := ojson.GetOr(limits, key)
	if l == nil {
		return "--"
	}
	used, ok := ojson.Float(ojson.Field(l, "used_percentage"))
	if !ok {
		return "--"
	}
	left := "已用 " + f0(used) + "%"
	if r, ok := ojson.Float(ojson.GetOr(l, "resets_at")); ok {
		return left + "(" + countdown(r, now) + ")"
	}
	return left
}

func alerts(ctxPct, cacheLeft, delta, usual *float64) []string {
	var out []string
	if ctxPct != nil && *ctxPct > ctxAlertPct {
		out = append(out, "ctx")
	}
	if cacheLeft != nil && 0 < *cacheLeft && *cacheLeft < cacheAlertSecs {
		out = append(out, "cache")
	}
	if delta != nil && usual != nil && *delta > burstTimes**usual && *delta-*usual >= burstMinExtra {
		out = append(out, "burst")
	}
	return out
}

// quote is urllib.parse.quote(s): everything but unreserved characters and '/' is %XX-encoded.
func quote(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || strings.IndexByte("_.-~/", c) >= 0 {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// httpJSON GETs a JSON body, nil on any failure or non-2xx.
func httpJSON(url string, timeout time.Duration) any {
	client := http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	v, err := ojson.Parse(body)
	if err != nil {
		return nil
	}
	return v
}

func first8(s string) string {
	r := []rune(s)
	if len(r) > 8 {
		r = r[:8]
	}
	return string(r)
}

func board(sid, path string) any {
	base := os.Getenv("AP_DASH_URL")
	if base == "" {
		base = "http://127.0.0.1:8787"
	}
	return httpJSON(fmt.Sprintf("%s/type?session=%s&path=%s", base, first8(sid), quote(path)), 4*time.Second)
}

func toks(v float64) string {
	switch {
	case v >= 1_000_000:
		return fmt.Sprintf("%.1fM", v/1_000_000)
	case v >= 1000:
		return fmt.Sprintf("%.0fk", v/1000)
	}
	return fmt.Sprint(int64(math.Trunc(v)))
}

func signed(v float64) string {
	sign := "+"
	if v < 0 {
		sign = "-"
	}
	return sign + toks(math.Abs(v))
}

// scorecardLine is row 3, or false when the row lacks a required field.
func scorecardLine(row any) (string, bool) {
	cost, ok := ojson.Float(ojson.Field(row, "cost"))
	if !ok {
		return "", false
	}
	if dc := ojson.GetOr(row, "delegate_cost"); dc != nil {
		d, ok := ojson.Float(dc)
		if !ok {
			return "", false
		}
		cost += d
	}
	id, ok := ojson.Str(ojson.Field(row, "id"))
	if !ok {
		return "", false
	}
	turn := "—"
	if t := ojson.GetOr(row, "turn_n"); t != nil {
		turn = ojson.PyStr(t)
	}
	parts := []string{first8(id) + " #" + turn, fmt.Sprintf("花費 $%.2f", cost)}
	waste, ok := ojson.Float(ojson.Field(row, "waste"))
	if !ok {
		return "", false
	}
	if waste > 0 {
		parts = append(parts, fmt.Sprintf("浪費 $%.2f", waste))
	}
	if d := ojson.Field(row, "ctx_delta"); d != nil {
		dv, ok := ojson.Float(d)
		if !ok {
			return "", false
		}
		s := "本回合 context " + signed(dv)
		if u := ojson.Field(row, "ctx_usual"); u != nil {
			uv, ok := ojson.Float(u)
			if !ok {
				return "", false
			}
			s += "（平常 " + signed(uv) + "）"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " · "), true
}

type call struct{ kind, model string }

// delegatedNow is [(subagent_type, model)] for the Agent calls since the last real prompt.
func delegatedNow(path string) []call {
	var calls []call
	for _, e := range common.ReadJSONL(path) {
		if common.IsPrompt(e) {
			calls = nil
			continue
		}
		if t, _ := ojson.Str(ojson.Field(e, "type")); t != "assistant" {
			continue
		}
		parts, _ := ojson.Field(ojson.GetOr(e, "message"), "content").([]any)
		for _, p := range parts {
			typ, _ := ojson.Str(ojson.Field(p, "type"))
			name, _ := ojson.Str(ojson.Field(p, "name"))
			if typ != "tool_use" || name != "Agent" {
				continue
			}
			inp := ojson.Field(p, "input")
			field := func(k, dflt string) string {
				if v := ojson.GetOr(inp, k); v != nil {
					return ojson.PyStr(v)
				}
				return dflt
			}
			calls = append(calls, call{field("subagent_type", "general-purpose"), field("model", "")})
		}
	}
	return calls
}

func isFile(p string) bool {
	if p == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// subagentLine is row 2: the subagent this turn launched, "" when none.
func subagentLine(path string) string {
	var calls []call
	if isFile(path) {
		calls = delegatedNow(path)
	}
	if len(calls) == 0 {
		return ""
	}
	out := "本回合已委派 " + calls[0].kind
	if calls[0].model != "" {
		out += "（" + calls[0].model + "）"
	}
	if len(calls) > 1 {
		out += fmt.Sprintf("等 %d 個", len(calls))
	}
	return out
}

type rowsKind int

const (
	gotRow rowsKind = iota
	noRow
	crash // the cached row is unusable too
)

func rows(sid, path string) (any, rowsKind) {
	if l := subagentLine(path); l != "" {
		common.Say(l)
	}
	if !isFile(path) {
		return nil, noRow
	}
	cache := fmt.Sprintf("/tmp/claude-scorecard-cache-%s.json", first8(sid))
	if got := board(sid, path); ojson.Obj(got) != nil {
		row, _ := ojson.Obj(got).Get("row")
		if !ojson.Truthy(row) {
			return row, gotRow
		}
		if line, ok := scorecardLine(row); ok {
			common.Say(line)
			os.WriteFile(cache, []byte(ojson.Compact(row)), 0o644)
			return row, gotRow
		}
	}
	// :8787 did not answer: keep showing whatever this session last had
	raw, err := os.ReadFile(cache)
	if err != nil {
		return nil, noRow
	}
	row, err := ojson.Parse(raw)
	if err != nil {
		return nil, noRow
	}
	line, ok := scorecardLine(row)
	if !ok {
		return nil, crash
	}
	common.Say(line)
	return row, gotRow
}

func basename(p string) string {
	return p[strings.LastIndexByte(p, '/')+1:]
}

func transcriptFor(sid string) string {
	dirs, err := os.ReadDir(common.Join(common.Home(), ".claude", "projects"))
	if err != nil {
		return ""
	}
	for _, d := range dirs {
		if strings.HasPrefix(d.Name(), ".") {
			continue
		}
		p := common.Join(common.Home(), ".claude", "projects", d.Name(), sid+".jsonl")
		if _, err := os.Lstat(p); err == nil {
			return p
		}
	}
	return ""
}

func Main(stdin string, modOnly bool) int {
	data, err := ojson.Parse([]byte(stdin))
	if err != nil {
		return 0
	}
	if ojson.Obj(data) == nil {
		return 1
	}
	now := common.Now()
	path, sid := "", ""
	if v := ojson.GetOr(data, "transcript_path"); v != nil {
		path = ojson.PyStr(v)
	}
	if v := ojson.GetOr(data, "session_id"); v != nil {
		sid = ojson.PyStr(v)
	}
	quiet := false
	if !modOnly && sid != "" {
		if st, err := os.Stat("/tmp/claude-ap-mod-" + sid); err == nil {
			quiet = now-common.Epoch(st.ModTime()) < 120
		}
	}
	if path == "" && sid != "" && modOnly {
		path = transcriptFor(sid)
	}

	// ── 1. 在跑什麼 ──
	empty := ojson.NewObject()
	or := func(v any) any {
		if v == nil {
			return empty
		}
		return v
	}
	modelV := or(ojson.GetOr(data, "model"))
	display := "?"
	if v, ok := ojson.Obj(modelV).Get("display_name"); ok {
		display = ojson.PyStr(v)
	}
	model := shortModel(windowTag.ReplaceAllString(display, ""))
	cw := or(ojson.GetOr(data, "context_window"))
	var sizeV any = int64(0)
	if v := ojson.GetOr(cw, "context_window_size"); v != nil {
		sizeV = v
	}
	size, _ := ojson.Float(sizeV)
	label := fmt.Sprintf("%dK", int64(math.Floor(size/1000)))
	if size >= 1_000_000 {
		label = fmt.Sprintf("%dM", int64(math.Floor(size/1_000_000)))
	}
	head := model + "(" + label + ")"
	if strings.Contains(strings.ToLower(model), "context") {
		head = model
	}
	ws := or(ojson.GetOr(data, "workspace"))
	root := ""
	if v := ojson.GetOr(ws, "project_dir"); v != nil {
		root = ojson.PyStr(v)
	} else if v := ojson.GetOr(ws, "current_dir"); v != nil {
		root = ojson.PyStr(v)
	}
	project := basename(root)
	branch := ""
	if root != "" {
		cmd := exec.Command("git", "-C", root, "symbolic-ref", "--short", "HEAD")
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
		out, _ := cmd.Output()
		branch = strings.TrimSpace(common.Lossy(out))
	}
	place := project
	if branch != "" {
		place = project + "(" + branch + ")"
	}
	elapsed := "--"
	var cacheLeft *float64
	file := isFile(path)
	if file {
		if born, ok := birth(path); ok {
			mins := int64(math.Floor((now - born) / 60))
			if mins >= 60 {
				elapsed = fmt.Sprintf("%dh%02dm", mins/60, mins%60)
			} else {
				elapsed = fmt.Sprintf("%dm", mins)
			}
		}
	}
	if modOnly && common.Experiment("cache_countdown") {
		st := cachettl.StateFromInput(ojson.GetOr(data, "prompt_cache"), now)
		if st == nil && file {
			st = cachettl.StateOf(path, now)
		}
		elapsed = cachettl.Label(st)
		if st != nil && (st.TTL == cachettl.TTL1h || st.Kept) {
			cacheLeft = &st.Left
		}
	}
	leftV := ojson.Field(cw, "remaining_percentage")
	left, haveLeft := ojson.Float(leftV)
	limits := or(ojson.GetOr(data, "rate_limits"))
	if !quiet {
		used := "--"
		if haveLeft {
			used = f0(100-left) + "%"
		}
		common.Say(fmt.Sprintf("%s · %s · %s · context 已用 %s · 5h %s · 7d %s",
			head, place, elapsed, used, quota(limits, "five_hour", now), quota(limits, "seven_day", now)))
	}

	// the live context size, for whoever needs it outside this process
	if haveLeft {
		var used int64
		if size != 0 {
			used = int64(size * (1 - left/100))
		}
		session := ""
		if v := ojson.GetOr(data, "session_id"); v != nil {
			session = ojson.PyStr(v)
		}
		live := ojson.Compact(ojson.NewObject().Set("at", now).Set("session", session).Set("context", used).
			Set("window", sizeV).Set("remaining_pct", leftV))
		if os.WriteFile("/tmp/claude-ctx-latest.json", []byte(live), 0o644) == nil && session != "" {
			os.WriteFile("/tmp/claude-ctx-"+session+".json", []byte(live), 0o644)
		}
	}

	if quiet {
		return 0
	}
	row, kind := rows(sid, path)
	if kind == crash {
		return 1
	}
	if modOnly {
		num := func(k string) *float64 {
			if f, ok := ojson.Float(ojson.Field(row, k)); ok {
				return &f
			}
			return nil
		}
		var ctxPct *float64
		if haveLeft {
			p := 100 - left
			ctxPct = &p
		}
		if flags := alerts(ctxPct, cacheLeft, num("ctx_delta"), num("ctx_usual")); len(flags) > 0 {
			common.Say("alert:" + strings.Join(flags, ","))
		}
	}
	return 0
}

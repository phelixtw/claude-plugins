package dash

import (
	"agentperform/internal/common"
	"bytes"
	"compress/gzip"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"agentperform/internal/ojson"
	"agentperform/internal/prices"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
)

// dashboard.py: the page shell, render, the /levers bundle, routing and the
// local HTTP service.

//go:embed assets/dashboard_page.html
var pageHTML string

//go:embed assets/echarts.min.js
var echartsJS []byte

const (
	brand       = "agent-perform"
	defaultDays = 90
	pageTTL     = 30 * time.Second
)

var (
	h1View = pyre.Compile(`<h1 class="view">(.*?)</h1>`, pyre.S)
	anyTag = pyre.Compile(`<[^>]+>`)
)

// shell wraps a view in the page. The tab title comes from the view's own h1.
func shell(body string, days int) string {
	head := ""
	if m := h1View.Search(body); m != nil {
		head = py.Strip(anyTag.Sub(m.Group(1), ""))
	}
	title := brand
	if head != "" {
		title = head + " · " + brand
	}
	out := strings.ReplaceAll(pageHTML, "__TITLE__", title)
	to := today()
	out = strings.ReplaceAll(out, "__RANGE__", "近 "+strconv.Itoa(days)+" 天 · "+to.AddDate(0, 0, -(days-1)).Format("01-02")+"～"+to.Format("01-02"))
	return strings.ReplaceAll(out, "__BODY__", body)
}

// pageKey is one cacheable page: render()'s arguments.
type pageKey struct {
	days           int
	day, sid, page string
	focus          string    // focus lens ("" = none)
	rng            [2]string // from, to; both empty is Python's None
	since          string
}

// backDays is (today - strptime(s)).days + plus; an unparsable s is
// Python's ValueError.
func backDays(s string, plus int) int {
	d, ok := strptimeDay(s)
	if !ok {
		panic(pyExc("ValueError"))
	}
	return daysBetween(d, today()) + plus
}

func render(k pageKey) string {
	days := k.days
	shell := func(body string, days int) string { return shell(withFocus(body, k.focus), days) }
	if k.rng[0] != "" {
		// a range reaching further back than the default window has to widen
		// the collection, or the picker would silently show nothing
		days = max(days, min(365, backDays(k.rng[0], 1)))
	}
	if k.since != "" {
		// the before side reaches up to 30 days behind `since`
		days = max(days, min(365, backDays(k.since, 31)))
	}
	if k.sid != "" && k.page == "" {
		if body, ok := sessionFast(k.sid, days, k.focus); ok {
			return shell(body, days)
		}
	}
	if k.page == "optimize" {
		return shell(OptimizeView(), days)
	}
	sessions := Collect(days)
	switch k.page {
	case "how":
		return shell(HowView(sessions), days)
	case "expensive":
		return shell(ExpensiveView(sessions, days), days)
	}
	if _, ok := topics[k.page]; ok && len(sessions) > 0 {
		since := k.since
		if since == "" {
			since = defaultSince()
		}
		return shell(TopicView(sessions, days, since, k.page, k.focus), days)
	}
	var body string
	switch {
	case len(sessions) == 0:
		body = empty("近期沒有資料。")
	case k.sid != "":
		body = sessionLens(sessions, k.sid, k.focus)
	case k.day != "":
		body = dayLens(sessions, k.day, k.focus)
	default:
		body = Overview(sessions, days, k.rng[0], k.rng[1])
	}
	saveCachesSoon() // fixed_tokens fills its cache while the views render
	return shell(body, days)
}

// bundleText is the AI-analysis bundle of one /levers section for the same
// window as the page ("" if unknown).
func bundleText(section string, q map[string][]string) string {
	if section != "scripts" && section != "repeats" {
		return ""
	}
	since := first(q, "since")
	days := defaultDays
	n, ok := days, true
	if v := q["days"]; len(v) > 0 {
		n, ok = pyInt(v[0])
	}
	if ok {
		days = max(1, min(365, n))
		if d, ok := strptimeDay(since); ok && since != "" {
			days = max(days, min(365, daysBetween(d, today())+31))
		}
	}
	sessions := Collect(days)
	if section == "scripts" {
		return ScriptsBundle(sessions, nil)
	}
	return RepeatsBundle(sessions, health())
}

// route is ("redirect", location) | ("gone", "") | ("page", name or "") for a GET path.
func route(path string) (kind, target string) {
	p := strings.TrimRight(path, "/")
	if p == "" {
		p = "/"
	}
	switch p {
	case "/glossary":
		return "redirect", "/how#terms"
	case "/skills":
		return "redirect", "/optimize"
	case "/talk": // the correction page of versions before 2.0
		return "redirect", "/"
	case "/levers", "/patterns", "/per-turn-path.html", "/offline-batch.html":
		return "gone", ""
	case "/how", "/optimize", "/expensive", "/context", "/tools", "/delegate", "/model", "/verify":
		return "page", p[1:]
	}
	return "page", ""
}

// ── what Python's stdlib did for the handler ─────────────────────────────

// pyExc is an exception name the /pipeline error page prints.
type pyExc string

func errName(r any) string {
	if e, ok := r.(pyExc); ok {
		return string(e)
	}
	return "Exception"
}

var (
	dayRe   = regexp.MustCompile(`^(\d{4})-(1[0-2]|0[1-9]|[1-9])-(3[01]|[12]\d|0[1-9]|[1-9]| [1-9])$`)
	intRe   = regexp.MustCompile(`^[+-]?\d+(?:_\d+)*$`)
	floatRe = regexp.MustCompile(`^[+-]?(?:\d+(?:_\d+)*(?:\.(?:\d+(?:_\d+)*)?)?|\.\d+(?:_\d+)*)(?:[eE][+-]?\d+(?:_\d+)*)?$`)
)

// strptimeDay is datetime.strptime(s, "%Y-%m-%d"): "2026-1-5" parses too.
func strptimeDay(s string) (time.Time, bool) {
	m := dayRe.FindStringSubmatch(s)
	if m == nil {
		return time.Time{}, false
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(strings.TrimSpace(m[3]))
	t := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.Local)
	return t, y >= 1 && t.Day() == d && int(t.Month()) == mo
}

// pyInt is int(s); a value past int64 saturates, as Python's unbounded int
// would still clamp under min/max.
func pyInt(s string) (int, bool) {
	s = py.Strip(s)
	if !intRe.MatchString(s) {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(s, "_", ""), 10, 64)
	if err != nil {
		if s[0] == '-' {
			return math.MinInt, true
		}
		return math.MaxInt, true
	}
	return int(n), true
}

// pyFloat is float(s).
func pyFloat(s string) (float64, bool) {
	s = py.Strip(s)
	if floatRe.MatchString(s) {
		f, err := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64)
		return f, err == nil || errors.Is(err, strconv.ErrRange)
	}
	body, sign := s, 1.0
	if s != "" && (s[0] == '+' || s[0] == '-') {
		body = s[1:]
		if s[0] == '-' {
			sign = -1
		}
	}
	switch strings.ToLower(body) {
	case "inf", "infinity":
		return math.Inf(int(sign)), true
	case "nan":
		return math.NaN(), true
	}
	return 0, false
}

// parseQS is urllib.parse.parse_qs: blank values dropped, '+' is a space,
// bad %-escapes kept.
func parseQS(qs string) map[string][]string {
	q := map[string][]string{}
	if qs == "" {
		return q
	}
	for _, nv := range strings.Split(qs, "&") {
		name, value, found := strings.Cut(nv, "=")
		if !found || value == "" {
			continue
		}
		name = unquote(strings.ReplaceAll(name, "+", " "))
		q[name] = append(q[name], unquote(strings.ReplaceAll(value, "+", " ")))
	}
	return q
}

// unquote is urllib.parse.unquote: each ASCII run is percent-decoded and read
// as UTF-8, a bad byte becoming U+FFFD.
func unquote(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var out strings.Builder
	var run []byte
	flush := func() {
		for len(run) > 0 {
			r, n := utf8.DecodeRune(run)
			out.WriteRune(r)
			run = run[n:]
		}
		run = nil
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c >= 0x80:
			flush()
			_, n := utf8.DecodeRuneInString(s[i:])
			out.WriteString(s[i : i+n])
			i += n
		case c == '%' && i+2 < len(s) && isHex(s[i+1]) && isHex(s[i+2]):
			b, _ := strconv.ParseUint(s[i+1:i+3], 16, 8)
			run = append(run, byte(b))
			i += 3
		default:
			run = append(run, c)
			i++
		}
	}
	flush()
	return out.String()
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func first(q map[string][]string, name string) string {
	if v := q[name]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// latin1 reads the request target the way http.server did: one char per byte.
func latin1(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		b.WriteRune(rune(s[i]))
	}
	return b.String()
}

// splitURI is urlparse(path)'s (path, query), after http.server's "//" fix.
func splitURI(uri string) (path, query string) {
	if strings.HasPrefix(uri, "//") {
		uri = "/" + strings.TrimLeft(uri, "/")
	}
	if i := strings.IndexByte(uri, '#'); i >= 0 {
		uri = uri[:i]
	}
	path, query, _ = strings.Cut(uri, "?")
	slash := strings.LastIndexByte(path, '/') + 1
	if i := strings.IndexByte(path[slash:], ';'); i >= 0 { // ;params are not part of .path
		path = path[:slash+i]
	}
	return path, query
}

func requestPathQuery(r *http.Request) (string, string) {
	if uri := r.RequestURI; strings.HasPrefix(uri, "/") {
		return splitURI(latin1(uri))
	}
	return r.URL.EscapedPath(), r.URL.RawQuery // absolute-form target
}

// realpath is os.path.realpath: symlinks resolved as far as the path exists.
func realpath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	rest := ""
	for cur := abs; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// ── one GET, as a pure function ──────────────────────────────────────────

// send is send_response + headers + body. A Content-Type Python did not set
// is suppressed so net/http does not sniff one.
func send(w http.ResponseWriter, status int, headers map[string]string, body []byte) {
	hd := w.Header()
	for k, v := range headers {
		hd.Set(k, v)
	}
	if _, ok := headers["Content-Type"]; !ok {
		hd["Content-Type"] = nil
	}
	w.WriteHeader(status)
	w.Write(body)
}

// replyBytes sets Content-Type, the extra headers, an optional gzip and Content-Length.
func replyBytes(status int, ctype string, body []byte, gz bool, level int, extra ...string) (int, map[string]string, []byte) {
	h := map[string]string{"Content-Type": ctype}
	for i := 0; i+1 < len(extra); i += 2 {
		h[extra[i]] = extra[i+1]
	}
	if gz {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, level)
		zw.Write(body)
		zw.Close()
		body = buf.Bytes()
		h["Content-Encoding"] = "gzip"
	}
	h["Content-Length"] = strconv.Itoa(len(body))
	return status, h, body
}

func replyHTML(html string, gz bool) (int, map[string]string, []byte) {
	return replyBytes(200, "text/html; charset=utf-8", []byte(html), gz, 3)
}

func replyJSON(payload *ojson.Object) (int, map[string]string, []byte) {
	return replyBytes(200, "application/json; charset=utf-8", []byte(py.Dumps(payload)), false, 0)
}

func bare(status int) (int, map[string]string, []byte) { return status, map[string]string{}, nil }

func deny(w http.ResponseWriter, status int) { send(w, status, nil, nil) }

// Render answers one GET the way dashboard.py's handler does, without the
// host check, the page cache or gzip: for a parity harness. query is the raw
// query string.
func Render(path, query string) (status int, headers map[string]string, body []byte) {
	return get(path, query, false, render)
}

// get is do_GET after the host check. pages renders (or fetches from the TTL
// cache) one overview-style page.
func get(path, query string, gz bool, pages func(pageKey) string) (int, map[string]string, []byte) {
	switch path {
	case "/vendor/echarts.min.js":
		return replyBytes(200, "application/javascript", echartsJS, gz, 6, "Cache-Control", "max-age=86400")
	case "/levers/bundle":
		q := parseQS(query)
		body := []byte(swr("b?"+query, func() string { return bundleText(first(q, "section"), q) }))
		status := 404
		if len(body) > 0 {
			status = 200
		}
		return replyBytes(status, "text/plain; charset=utf-8", body, false, 0)
	}
	kind, target := route(path)
	switch kind {
	case "redirect":
		return 302, map[string]string{"Location": target}, nil
	case "gone":
		return bare(404)
	}
	q := parseQS(query)
	switch {
	case strings.HasPrefix(path, "/type"): // the Mod's numbers for one conversation
		return typeReply(q)
	case strings.HasPrefix(path, "/health"):
		return 200, map[string]string{"X-AP-Root": common.PluginRoot()}, []byte("ok")
	}
	days := defaultDays
	if v := q["days"]; len(v) > 0 {
		if n, ok := pyInt(v[0]); ok {
			days = max(1, min(365, n))
		}
	}
	if strings.HasPrefix(path, "/pipeline") {
		return replyHTML(shell(swr("p?"+query, func() string { return pipelineBody(q) }), days), gz)
	}
	date := func(name string) string {
		v := first(q, name)
		if _, ok := strptimeDay(v); !ok {
			return ""
		}
		return v
	}
	focus := first(q, "focus")
	if _, ok := lenses[focus]; !ok {
		focus = topicLens[target]
	}
	k := pageKey{days: days, day: first(q, "day"), sid: first(q, "session"), page: target, focus: focus,
		rng: [2]string{date("from"), date("to")}, since: date("since")}
	if target == "" && q["day"] == nil && q["session"] == nil {
		if k.rng != ([2]string{}) {
			rememberRange(k.rng[0], k.rng[1])
		} else if f, t, ok := rememberedRange(); ok {
			k.rng = [2]string{f, t}
		}
	}
	return replyHTML(pages(k), gz)
}

// typeReply is /type: the per-turn verdict comes from the UserPromptSubmit
// hook, not from here.
func typeReply(q map[string][]string) (int, map[string]string, []byte) {
	sid, tp := first(q, "session"), first(q, "path")
	out := ojson.NewObject().Set("session", sid)
	// only transcripts are read; any other path is ignored
	if tp != "" && strings.HasPrefix(realpath(tp), realpath(projects())+"/") {
		if row := ScorecardRow(sid, tp); row.Len() > 0 {
			out.Set("row", row)
		}
	}
	return replyJSON(out)
}

// pipelineBody is the /pipeline view; an error page, never a dropped connection.
func pipelineBody(q map[string][]string) (out string) {
	defer func() {
		if r := recover(); r != nil {
			out = `<h1 class="view">Context 組成</h1>` +
				`<p>這個頁面產生失敗：` + Esc(errName(r)) + `。</p>`
		}
	}()
	num := func(name string) *float64 {
		if f, ok := pyFloat(first(q, name)); ok {
			return &f
		}
		return nil
	}
	turn, hasTurn := 0, false
	if t := num("turn"); t != nil && *t != 0 {
		switch {
		case math.IsNaN(*t):
			panic(pyExc("ValueError"))
		case math.IsInf(*t, 0):
			panic(pyExc("OverflowError"))
		}
		turn, hasTurn = int(max(-1<<40, min(1<<40, math.Trunc(*t)))), true
	}
	k := pipeKey{sid: first(q, "session"), turn: turn, hasTurn: hasTurn, agent: first(q, "agent"), focus: first(q, "focus"), tool: toolParam(first(q, "tool"))}
	if at := num("at"); at != nil {
		k.at, k.hasAt = *at, true
	}
	if _, ok := lenses[k.focus]; !ok {
		k.focus = ""
	}
	out = withFocus(pipelineCached(k), k.focus)
	saveCachesSoon() // the parse and subagent scans it filled
	return out
}

// pipeKey is one /pipeline request, whole: View and agentView read nothing else, so the page cache keys on every input.
type pipeKey struct {
	sid     string
	turn    int
	hasTurn bool
	at      float64
	hasAt   bool
	agent   string
	focus   string
	tool    string // a tool_use id from toolParam, or ""
}

var toolRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// toolParam is v when it looks like a tool_use id, else "": it is printed into an element id and a script.
func toolParam(v string) string {
	if toolRe.MatchString(v) {
		return v
	}
	return ""
}

type pipeHit struct{ sig, html string }

var (
	pipeMu    sync.Mutex
	pipeCache = map[pipeKey]pipeHit{}
)

// pipelineCached is View memoised on the session transcript's stats, so
// revisiting a turn or subagent page skips re-parsing until the transcript changes.
func pipelineCached(k pipeKey) string {
	sig, hasSig := "", false
	if k.sid != "" {
		if path := Transcript(k.sid); path != "" {
			if s, err := statSig(path); err == nil {
				sig, hasSig = s, true
			}
		}
	}
	pipeMu.Lock()
	hit, ok := pipeCache[k]
	pipeMu.Unlock()
	if ok && hasSig && hit.sig == sig {
		return hit.html
	}
	html := View(k, CtxProbeCats)
	if hasSig {
		pipeMu.Lock()
		if len(pipeCache) > 200 { // ponytail: crude bound, LRU if it ever matters
			clear(pipeCache)
		}
		pipeCache[k] = pipeHit{sig, html}
		pipeMu.Unlock()
	}
	return html
}

// ── the service ──────────────────────────────────────────────────────────

type cacheHit struct {
	at   time.Time
	html string
}

// handler is dashboard.serve()'s Handler plus the state its closure held.
// ponytail: the page cache lives in memory only. Python also pickled the
// parse caches (dashboard-cache.pickle) so a restart skipped unchanged
// transcripts; persist (gob) if a cold start ever gets slow.
type handler struct {
	port       int
	mu         sync.Mutex
	hits       map[pageKey]cacheHit
	order      []pageKey // insertion order, oldest first
	refreshing map[pageKey]bool
	released   bool // caches dropped by release, saved pages not yet loaded back
}

func newHandler(port int) *handler {
	return &handler{port: port, hits: map[pageKey]cacheHit{}, refreshing: map[pageKey]bool{}}
}

// NewHandler is the dashboard's http.Handler for a service on this port.
func NewHandler(port int) http.Handler { return newHandler(port) }

// fill renders one page into the cache; cache reads never wait on this.
func (h *handler) fill(k pageKey) cacheHit {
	busy.Add(1)
	defer busy.Add(-1)
	defer func() {
		h.mu.Lock()
		delete(h.refreshing, k)
		h.mu.Unlock()
	}()
	at := Now()
	hit := cacheHit{at, render(k)}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.hits[k]; !ok {
		h.order = append(h.order, k)
	}
	h.hits[k] = hit
	if len(h.hits) > 60 {
		delete(h.hits, h.order[0])
		h.order = h.order[1:]
	}
	return hit
}

func (h *handler) clear() {
	h.mu.Lock()
	h.hits, h.order = map[pageKey]cacheHit{}, nil
	h.mu.Unlock()
}

// page is the TTL cache: stale answers with what it has and re-renders behind it.
func (h *handler) page(k pageKey) string {
	h.mu.Lock()
	hit, ok := h.hits[k]
	h.mu.Unlock()
	if !ok {
		return h.fill(k).html
	}
	if Now().Sub(hit.at) > pageTTL {
		h.mu.Lock()
		start := !h.refreshing[k]
		h.refreshing[k] = true
		h.mu.Unlock()
		if start {
			go func() {
				defer logPanic("dashboard refresh")
				h.fill(k)
			}()
		}
	}
	return hit.html
}

func logPanic(what string) {
	if r := recover(); r != nil {
		fmt.Fprintln(os.Stderr, what+":", r)
	}
}

// hostOK is the DNS-rebinding guard: a page on another site can resolve its
// own name to 127.0.0.1 and read this board; only our own host names are served.
func (h *handler) hostOK(r *http.Request) bool {
	return r.Host == fmt.Sprintf("127.0.0.1:%d", h.port) || r.Host == fmt.Sprintf("localhost:%d", h.port)
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	busy.Add(1)
	defer busy.Add(-1)
	if p := r.URL.Path; serving.Load() && p != "/type" && p != "/health" && p != "/favicon.ico" { // the Mod's polling is not a viewer
		lastReq.Store(time.Now().UnixNano())
		h.setReleased(false)
		h.load()
	}
	switch r.Method {
	case http.MethodGet:
		h.doGET(w, r)
	case http.MethodPost:
		h.doPOST(w, r)
	default:
		unsupported(w, r.Method)
	}
}

func (h *handler) doGET(w http.ResponseWriter, r *http.Request) {
	if !h.hostOK(r) {
		deny(w, 403)
		return
	}
	path, query := requestPathQuery(r)
	gz := strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
	status, headers, body := get(path, query, gz, h.page)
	send(w, status, headers, body)
}

func (h *handler) doPOST(w http.ResponseWriter, r *http.Request) {
	if !h.hostOK(r) {
		deny(w, 403)
		return
	}
	path, _ := requestPathQuery(r)
	if !strings.HasPrefix(path, "/forget") && path != "/optimize/run" {
		deny(w, 404)
		return
	}
	// A page on any other site can POST here too, so the origin has to be
	// this board itself.
	if o := r.Header.Get("Origin"); o != fmt.Sprintf("http://127.0.0.1:%d", h.port) && o != fmt.Sprintf("http://localhost:%d", h.port) {
		deny(w, 403)
		return
	}
	if path == "/optimize/run" {
		startAudit(h.clear)
		h.clear()
		status, hd, body := replyJSON(ojson.NewObject().Set("ok", true).Set("running", true))
		send(w, status, hd, body)
		return
	}
	out := ojson.NewObject()
	res, err := h.forgetRequest(r)
	if errors.Is(err, errUncaught) {
		panic(http.ErrAbortHandler) // Python's traceback: no reply at all
	}
	if err != nil {
		out.Set("ok", false).Set("error", py.Head(err.Error(), 160))
	} else {
		out.Set("ok", true)
		for _, k := range res.Keys {
			out.Set(k, res.Vals[k])
		}
		h.clear()
	}
	status, hd, body := replyJSON(out)
	send(w, status, hd, body)
}

// forgetRequest runs the POST /forget body through forget().
func (h *handler) forgetRequest(r *http.Request) (*ojson.Object, error) {
	size := 0
	if cl := r.Header.Get("Content-Length"); cl != "" {
		n, ok := pyInt(cl)
		if !ok {
			return nil, fmt.Errorf("invalid literal for int() with base 10: %s", py.StrRepr(cl))
		}
		size = n
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, int64(max(size, 0))))
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	payload, err := ojson.Parse(raw)
	if err != nil {
		return nil, err // ponytail: Go's JSON error text, not Python's "Expecting value: line 1 column 1 (char 0)"
	}
	obj := ojson.Obj(payload)
	if obj == nil {
		return nil, errUncaught // payload.get on a list/str/number
	}
	return forget(ojson.Field(obj, "session"), false)
}

// unsupported is BaseHTTPRequestHandler's 501 for a method with no do_ handler.
func unsupported(w http.ResponseWriter, method string) {
	body := []byte(fmt.Sprintf(errorPage, py.StrRepr(method)))
	status, hd, body := replyBytes(501, "text/html;charset=utf-8", body, false, 0, "Connection", "close")
	send(w, status, hd, body)
}

const errorPage = `<!DOCTYPE HTML>
<html lang="en">
    <head>
        <meta charset="utf-8">
        <style type="text/css">
            :root {
                color-scheme: light dark;
            }
        </style>
        <title>Error response</title>
    </head>
    <body>
        <h1>Error response</h1>
        <p>Error code: 501</p>
        <p>Message: Unsupported method (%s).</p>
        <p>Error code explanation: 501 - Server does not support this operation.</p>
    </body>
</html>
`

// Serve is the long-running local service (`dashboard.py --serve <port>`).
func Serve(port int) error {
	h := newHandler(port)
	serving.Store(true)
	lastReq.Store(time.Now().UnixNano())
	debug.SetMemoryLimit(1 << 30) // backstop: a rebuild collects harder instead of growing
	prices.Start()
	go h.watch()
	startStatsTicker()
	ln, err := inheritedListener()
	if err != nil {
		return err
	}
	if ln == nil {
		if ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
			return err
		}
	}
	listener = ln
	return (&http.Server{Handler: h}).Serve(ln)
}

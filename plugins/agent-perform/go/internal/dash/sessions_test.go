package dash

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
)

// ── synthetic transcripts ──────────────────────────────────────────────────

func iso(ts float64) string {
	return time.Unix(0, int64(ts*1e9)).UTC().Format("2006-01-02T15:04:05.000Z")
}

func assistantRec(ts float64, mid string, cw, cr int64, ttlKey string) *ojson.Object {
	if ttlKey == "" {
		ttlKey = "ephemeral_1h_input_tokens"
	}
	usage := obj("input_tokens", 1, "output_tokens", 10, "cache_read_input_tokens", cr,
		"cache_creation_input_tokens", cw, "cache_creation", obj(ttlKey, cw))
	return obj("type", "assistant", "timestamp", iso(ts), "message", obj("role", "assistant", "id", mid,
		"model", "claude-opus-5-5", "content", []any{obj("type", "text", "text", "x")}, "usage", usage))
}

func userRec(ts float64, text string) *ojson.Object {
	return obj("type", "user", "timestamp", iso(ts), "message", obj("role", "user", "content", text))
}

func writeRows(t testing.TB, path string, rows ...any) string {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	var lines []string
	for _, r := range rows {
		lines = append(lines, py.Dumps(r))
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// testHome points HOME at an empty ~/.claude and resets the process-wide base cost.
func testHome(t testing.TB) string {
	t.Helper()
	h := t.TempDir()
	t.Cleanup(func() { settle(t) }) // runs before the TempDir removal
	os.MkdirAll(filepath.Join(h, ".claude", "projects"), 0o755)
	t.Setenv("HOME", h)
	resetGlobals := func() {
		baseVal = nil
		probeMu.Lock()
		probeCache, probeSeen = map[string]*ojson.Object{}, map[string]bool{}
		probeMu.Unlock()
	}
	resetGlobals()
	t.Cleanup(resetGlobals)
	return h
}

// settle waits for background work started by a request (cache saves, stale
// page refreshes) so nothing is still writing into the test's HOME when it is removed.
func settle(t testing.TB) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); busy.Load() != 0; runtime.Gosched() {
		if time.Now().After(end) {
			t.Error("background work still running")
			return
		}
	}
}

func setHealth(t *testing.T, sid string, rep *ojson.Object, loud float64) {
	t.Helper()
	ss := ojson.NewObject()
	if rep != nil {
		ss.Set(sid, rep)
	}
	writeRows(t, claudeFile("agent_health.json"), obj("sessions", ss, "loud", loud))
}

// leverSessions is one session whose second turn comes back after two hours and rewrites 150k.
func leverSessions(t *testing.T) []*Session {
	t.Helper()
	now := float64(time.Now().Unix()) - 30_000
	path := writeRows(t, filepath.Join(t.TempDir(), "s.jsonl"),
		userRec(now, "first topic about the billing module"),
		assistantRec(now+5, "m1", 20_000, 0, ""),
		userRec(now+7_200, "something unrelated entirely, new question"),
		assistantRec(now+7_205, "m2", 150_000, 0, ""))
	return []*Session{one(path, 0)}
}

func local(y, mo, d, h int) float64 {
	return float64(time.Date(y, time.Month(mo), d, h, 0, 0, 0, time.Local).Unix())
}

// delegatingSession: day 1 23:00 launches an agent that runs on day 2; an earlier
// prompt never got an API call, so subagents numbers the launch turn 3 while the
// dashboard numbers it 2.
func delegatingSession(t *testing.T) *Session {
	t.Helper()
	t0 := local(2026, 10, 1, 22)
	t1 := local(2026, 10, 1, 23)
	folder := t.TempDir()
	launch := assistantRec(t1+5, "m2", 1000, 0, "")
	ojson.Obj(ojson.Field(launch, "message")).Set("content", []any{obj("type", "tool_use", "id", "tu1", "name", "Agent",
		"input", obj("description", "dig", "subagent_type", "x"))})
	result := obj("type", "user", "timestamp", iso(t1+9), "toolUseResult", ojson.NewObject(), "message", obj("role", "user",
		"content", []any{obj("type", "tool_result", "tool_use_id", "tu1", "content", "agentId: abc123")}))
	parent := writeRows(t, filepath.Join(folder, "sess1234.jsonl"),
		userRec(t0, "first"), assistantRec(t0+5, "m1", 1000, 0, ""),
		userRec(t0+60, "no reply to this one"),
		userRec(t1, "second"), launch, result)
	day2 := local(2026, 10, 2, 0) + 30*60
	writeRows(t, filepath.Join(folder, "sess1234", "subagents", "agent-abc123.jsonl"),
		assistantRec(day2, "a0", 1000, 0, ""), assistantRec(day2+1, "a1", 1000, 0, ""))
	s := one(parent, 0)
	applyWindow([]*Session{s})
	attachDelegation([]*Session{s})
	return s
}

func allTurns(ss []*Session) []*Turn {
	var out []*Turn
	for _, s := range ss {
		out = append(out, s.Turns...)
	}
	return out
}

var leverCardRe = regexp.MustCompile(`(?s)<section class="card lever" id="lever-(\w+)">(.*?)</section>`)

// ── /levers ────────────────────────────────────────────────────────────────

// Every card has 事實, 做法〔出處〕, an anchor and a folded 細節.
func TestLeversCards(t *testing.T) {
	testHome(t)
	html := LeversView(leverSessions(t), 14, "")
	cards := leverCardRe.FindAllStringSubmatch(html, -1)
	nctx := 0
	for _, c := range cards {
		if c[1] == "ctx" {
			nctx++
		}
		if strings.Count(c[2], `<p class="lvl">事實：`) != 1 {
			t.Errorf("%s: fact line", c[1])
		}
		if len(regexp.MustCompile(`(?s)<p class="fix">做法：.*?<sup class="cite">〔`).FindAllString(c[2], -1)) != 1 {
			t.Errorf("%s: action with source", c[1])
		}
		mustHave(t, c[2], "<summary>細節</summary>")
	}
	if nctx != 1 || len(cards) < 4 {
		t.Errorf("cards %d ctx %d", len(cards), nctx)
	}
	if footer := strings.SplitN(html, "依據與門檻", 2)[1]; strings.Count(footer, "<li>") < len(cards) {
		t.Error("footer lists every card")
	}
	for _, gone := range []string{"要求講短", "20,574", "41%", "實測沒問題", "閒置後回到大 session", "顯著", "沒有跟著上升"} {
		mustNot(t, html, gone)
	}
	mustHave(t, html, "其他卡片的金額不計入浪費")
	if strings.Count(html, "依據與門檻") != 1 {
		t.Error("one refs section")
	}
	for _, key := range []string{"cache", "fixed"} {
		card := regexp.MustCompile(`(?s)<section class="card lever" id="lever-` + key + `">(.*?)</section>`).FindStringSubmatch(html)
		if card == nil {
			t.Fatalf("no %s card", key)
		}
		if strings.Count(card[1], `<p class="lvl">事實：`) != 1 {
			t.Errorf("%s fact", key)
		}
		mustHave(t, card[1], `<sup class="cite">〔`)
		mustHave(t, card[1], `<p class="ba"`)
	}
	mustHave(t, html, "Cache 過期重寫")
	ctxCard := regexp.MustCompile(`(?s)id="lever-ctx">(.*?)</section>`).FindStringSubmatch(html)
	mustNot(t, ctxCard[1], "200k")
}

func TestLeversContextCardUsesOverviewBase(t *testing.T) {
	testHome(t)
	ss := leverSessions(t)
	html := LeversView(ss, 14, "")
	baseVal = nil
	mustHave(t, html, money(waste(allTurns(ss), baseCost(ss), nil).ctx))
}

func TestLeversWasteIsTheOverviewDefinition(t *testing.T) {
	testHome(t)
	s := delegatingSession(t)
	s.Turns[0].CtxPeak = 500_000
	s.Turns[0].Usage.Cost = 9.0
	s.Turns[0].Window = 1_000_000
	want := money(waste(s.Turns, baseCost([]*Session{s}), nil).total)
	html := LeversView([]*Session{s}, 14, "")
	mustHave(t, html, "浪費 <b>"+want+"</b>")
	lede := regexp.MustCompile(`(?s)<p class="lede">.*?</p>`).FindString(html)
	mustNot(t, lede, "估省")
	if strings.Index(html, `id="lever-slow"`) > strings.Index(html, `id="lever-delegate"`) {
		t.Error("slow before delegate")
	}
}

// Cache 過期重寫 detection on the turn after two hours away.
func TestRewriteFlagsTheTurnAfterTwoHours(t *testing.T) {
	testHome(t)
	ts := leverSessions(t)[0].Turns
	t1, t2 := ts[0], ts[1]
	if t1.Rewrite != 0 || t1.Gap != nil {
		t.Errorf("opening turn: %v %v", t1.Rewrite, t1.Gap)
	}
	if t2.TTL != 3600 || t2.RewriteTok != 150_000 || t2.Rewrite <= 1 {
		t.Errorf("turn 2: ttl %d tok %d usd %v", t2.TTL, t2.RewriteTok, t2.Rewrite)
	}
}

// ── session, day and overview pages ───────────────────────────────────────

func TestSessionPageTags(t *testing.T) {
	testHome(t)
	ss := leverSessions(t)
	html := SessionView(ss, ss[0].ID)
	if strings.Count(html, `class="pb cache"`) != 1 {
		t.Fatal("one Cache 過期重寫 row")
	}
	pill := html[strings.Index(html, `class="pb cache"`):]
	pill = pill[:strings.Index(pill, "</span>")]
	mustHave(t, pill, "Cache 過期重寫")
	mustNot(t, strings.SplitN(pill, ">", 2)[1], "Cache hit")
	mustNot(t, html, `class="pb ctx"`)
	mustNot(t, html, "沒有浪費")
	mustHave(t, html, `id="waste"`)
	for _, w := range []string{"浪費偏高", "有一些浪費", "浪費很少", "其他觀察", `<div id="repeat">`, `<div id="corr">`} {
		mustNot(t, html, w)
	}
}

func TestWarmUsageLeavesTheOpeningTurnOut(t *testing.T) {
	s := &Session{Turns: []*Turn{{Usage: Usage{Cw: 50_000}}, {Usage: Usage{Cr: 95, Cw: 5}}}}
	u := warmUsage(s.Turns, []*Session{s})
	if r := hitRate(u); r == nil || *r < 0.9499 || *r > 0.9501 {
		t.Errorf("hit rate %v", r)
	}
}

func TestToolsCollapseToPlusN(t *testing.T) {
	testHome(t)
	s := delegatingSession(t)
	s.Turns[0].Tools = py.NewCounter()
	for i := 0; i < 5; i++ {
		s.Turns[0].Tools.Add("Tool"+strconv.Itoa(i), 1)
	}
	mustHave(t, sessionLens([]*Session{s}, s.ID, "repeat"), "+2</span>") // the 工具 column belongs to the repeat lens
}

var statRe = regexp.MustCompile(`class="stat( hot)?"`)

func TestFirstScreens(t *testing.T) {
	testHome(t)
	addCorrections(t) // an old install's labels add nothing
	ss := leverSessions(t)
	day := fromTimestamp(ss[0].Start).Format("2006-01-02")
	for _, html := range []string{DayView(ss, day), SessionView(ss, ss[0].ID)} {
		if n := len(statRe.FindAllString(html, -1)); n != 3 {
			t.Errorf("%d tiles", n)
		}
		mustHave(t, html, `<p class="lede">`)
	}
	html := Overview(ss, 14, "", "")
	mustHave(t, html, `<h1 class="view">總覽</h1>`)
	mustHave(t, html, "花了")
	mustNot(t, html, "先做這件事")
	mustNot(t, html, "規則檢查")
	if n := strings.Count(html, `<a class="find`); n > 3 {
		t.Errorf("overview findings %d, want at most 3", n)
	}
	mustNot(t, html, `<section class="card">`)            // the chart and the day list sit in a single box
	mustNot(t, html, `<button type="submit">`)            // picking a date applies it
	if n := len(statRe.FindAllString(html, -1)); n != 4 { // spend, savable, active hours, delegation
		t.Errorf("overview tiles %d", n)
	}
}

func visibleText(html string) string {
	html = regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>`).ReplaceAllString(html, "")
	return regexp.MustCompile(`title="[^"]*"`).ReplaceAllString(html, "")
}

// Wording: 對話, never 工作階段; no statistics jargon on screen.
func TestWording(t *testing.T) {
	testHome(t)
	ss := leverSessions(t)
	day := fromTimestamp(ss[0].Start).Format("2006-01-02")
	banned := regexp.MustCompile(`z=|p50|p90|標準差|中位`)
	for _, body := range []string{Overview(ss, 14, "", ""), DayView(ss, day), SessionView(ss, ss[0].ID),
		LeversView(ss, 14, ""), HowView(ss)} {
		mustNot(t, visibleText(shell(body, 14)), "工作階段")
		if m := banned.FindString(visibleText(body)); m != "" && !strings.Contains(body, "<h3>流程</h3>") {
			if strings.Contains(body, `class="hero`) || strings.Contains(body, "lever") {
				t.Errorf("jargon %q", m)
			}
		}
	}
	how := HowView(ss)
	mustHave(t, how, "一個 Claude Code session（一份 transcript）")
	mustHave(t, how, "主對話")
	mustHave(t, how, `class="gl col"`)
	for _, a := range []string{"flow", "hooks", "terms", "sources"} {
		mustHave(t, how, `id="`+a+`"`)
	}
	for _, m := range regexp.MustCompile(`id="([^"]*)"`).FindAllStringSubmatch(how, -1) {
		if strings.ContainsAny(m[1], " \t\n") {
			t.Errorf("anchor with whitespace %q", m[1])
		}
	}
}

func TestOptimizePage(t *testing.T) {
	testHome(t)
	html := OptimizeView()
	mustHave(t, html, `<h1 class="view">優化</h1>`)
	mustHave(t, html, `<button class="audit-run">`)
	mustNot(t, html, `<pre class="audit">`) // no report yet
}

func TestMonthRowCostIncludesDelegation(t *testing.T) {
	testHome(t)
	ss := leverSessions(t)
	at := ss[0].Turns[0].At
	ss[0].Dele = obj("n", 1, "cost", 500.0, "launches", []any{obj("at", at, "cost", 500.0, "turn", 1)})
	month := regexp.MustCompile(`(?s)<button class="mh">(.*?)</button>`).FindStringSubmatch(Overview(ss, 14, "", ""))
	if month == nil || !regexp.MustCompile(`\$5\d\d\.\d\d`).MatchString(month[1]) {
		t.Errorf("month row %v", month)
	}
}

// ── delegation on the dashboard's turn numbers ─────────────────────────────

func TestRepeatCallsUseTheSameNumbers(t *testing.T) {
	testHome(t)
	s := delegatingSession(t)
	setHealth(t, s.ID, obj("turns", 3, "flagged", []any{3}, "share", 1.0/3, "reasons", obj("3", obj("bash", ojson.NewObject()))), 0.15)
	html := SessionView([]*Session{s}, s.ID)
	mustHave(t, html, `href="#t2"`)
	mustNot(t, html, "{}")
	mustHave(t, html, "1 / 2 個回合判成重複呼叫（50.0%")
}

func TestRowsFollowTheBlock(t *testing.T) {
	testHome(t)
	s := delegatingSession(t)
	rep := obj("turns", 3, "flagged", []any{2}, "share", 0.5, "reasons", obj("2", obj("1", "ls")))
	setHealth(t, s.ID, rep, 3.0)
	html := SessionView([]*Session{s}, s.ID)
	mustNot(t, html, `class="warn"`)
	mustNot(t, html, "判成重複呼叫")
	setHealth(t, s.ID, rep, 0.15)
	mustHave(t, SessionView([]*Session{s}, s.ID), `class="warn"`)
}

func TestDayViewCountsOnlyLaunchesOfThatDay(t *testing.T) {
	testHome(t)
	s := delegatingSession(t)
	c := fnum(s.Dele, "cost")
	if c <= 0 {
		t.Fatalf("delegation cost %v", c)
	}
	day1 := DayView([]*Session{s}, "2026-10-01")
	mustHave(t, day1, "委派 $0.00")
	mustHave(t, DayView([]*Session{s}, "2026-10-02"), "委派 "+money(c))
	if days := delegationByDay([]*Session{s}); strings.Join(days.keys, ",") != "2026-10-02" {
		t.Errorf("days %v", days.keys)
	}
	if regexp.MustCompile(`<a class="stat" href="([^"]*)"`).MatchString(day1) {
		t.Error("tiles are not links")
	}
	mustHave(t, SessionView([]*Session{s}, s.ID), `id="waste"`) // the Mod's 浪費 button
}

func TestModCostEqualsSessionPageCost(t *testing.T) {
	testHome(t)
	s := delegatingSession(t)
	row := ScorecardRow(s.ID, s.Path)
	dc, want := fnum(row, "delegate_cost"), fnum(s.Dele, "cost")
	if dc-want > 1e-9 || want-dc > 1e-9 {
		t.Errorf("mod %v page %v", dc, want)
	}
	mustHave(t, SessionView([]*Session{s}, s.ID), money(s.Totals.Cost+dc))
}

func TestCostPerTurnIncludesDelegation(t *testing.T) {
	testHome(t)
	s := delegatingSession(t)
	main := 0.0
	for _, t := range s.Turns {
		main += t.Usage.Cost
	}
	if costPerTurn(s.Turns) <= main/float64(len(s.Turns)) {
		t.Error("delegation not in cost per turn")
	}
}

func TestContextWindow(t *testing.T) {
	h := testHome(t)
	now := float64(time.Now().Unix()) - 5000
	path := writeRows(t, filepath.Join(t.TempDir(), "w.jsonl"), userRec(now, "hi"), assistantRec(now+5, "m1", 100_000, 0, ""))
	s := one(path, 0)
	if ctxLimit(s) != 200_000 {
		t.Errorf("default %d", ctxLimit(s))
	}
	applyWindow([]*Session{s})
	if !ctxBig(s.Turns[0]) {
		t.Error("100k of 200k is big")
	}
	key := s.Cwd + "|" + ctxMainModel(s)
	writeRows(t, filepath.Join(h, ".claude", "ctx-cache.json"), obj(key, obj("limit", 1_000_000, "cats", obj("System prompt", 1))))
	probeCache, probeSeen = map[string]*ojson.Object{}, map[string]bool{}
	s = one(path, 0)
	applyWindow([]*Session{s})
	if ctxLimit(s) != 1_000_000 || s.Turns[0].Window != 1_000_000 || ctxBig(s.Turns[0]) {
		t.Errorf("probe limit: %d %d", ctxLimit(s), s.Turns[0].Window)
	}
	before := s.Turns[0].Window
	grown := *s.Turns[0]
	grown.CtxPeak = 250_000
	s.Turns = append(s.Turns, &grown)
	applyWindow([]*Session{s})
	if s.Turns[0].Window != before {
		t.Error("window flipped when the session grew")
	}
}

// ── before / after ─────────────────────────────────────────────────────────

func baTurns(n int, ageDays float64, bad int, cost float64) []*Turn {
	now := float64(Now().Unix())
	var out []*Turn
	for i := 0; i < n; i++ {
		out = append(out, &Turn{At: now - ageDays*86400, Usage: Usage{Cost: cost}, Killed: i < bad})
	}
	return out
}

func daysAgo(n int) string { return today().AddDate(0, 0, -n).Format("2006-01-02") }

func TestBeforeAfter(t *testing.T) {
	ts := append(append(baTurns(40, 3, 0, 1), baTurns(35, 0, 0, 1)...), baTurns(9, 20, 0, 1)...)
	pre, post, span := baWindows(ts, daysAgo(1))
	if len(pre) != 40 || len(post) != 35 || span != 2 {
		t.Errorf("windows %d %d %d", len(pre), len(post), span)
	}
	corr := shareClause("中止率", func(t *Turn) bool { return t.Killed }, nil)
	cost := moneyClause("每回合花費", costPerTurn)
	html := baRow(append(baTurns(40, 18, 10, 1), baTurns(40, 0, 20, 1)...), daysAgo(10), []clause{corr, cost})
	mustHave(t, html, `前 25.0% → 後 50.0%（差距<span title="z=+2.`)
	mustHave(t, html, "前 $1.00 → 後 $1.00（+0%）")
	mustHave(t, html, "回合數 40 / 40")
	for _, w := range []string{"顯著", "有效"} {
		mustNot(t, html, w)
	}
	html = baRow(append(baTurns(29, 18, 0, 1), baTurns(40, 0, 0, 1)...), daysAgo(10), []clause{corr})
	mustHave(t, html, "資料不足（前 29 / 後 40 回合）")
	mustNot(t, html, "→")

	html = baRow(append(baTurns(40, 5, 0, 1), baTurns(40, 0, 0, 1)...), daysAgo(2), []clause{cost})
	mustHave(t, html, "資料不足（前 3 天 / 後 3 天，各需 7 天）")
	mustNot(t, html, "%")
	html = baRow(append(baTurns(40, 13, 0, 1), baTurns(40, 0, 0, 1)...), daysAgo(6), []clause{cost})
	mustHave(t, html, "（+0%）")
	mustNot(t, html, "資料不足")
	if _, _, pct, _, _ := beforeAfter(append(baTurns(40, 5, 0, 1), baTurns(40, 0, 0, 1)...), daysAgo(2), costPerTurn); pct != nil {
		t.Error("under seven days")
	}
	b, a, pct, n1, n2 := beforeAfter(append(baTurns(30, 18, 0, 2), baTurns(30, 0, 0, 1)...), daysAgo(10), costPerTurn)
	if *b != 2 || *a != 1 || py.Round(*pct, 0) != -50 || n1 != 30 || n2 != 30 {
		t.Errorf("30 each side: %v %v %v %d %d", *b, *a, *pct, n1, n2)
	}
	if _, _, pct, _, _ := beforeAfter(append(baTurns(29, 18, 0, 2), baTurns(30, 0, 0, 1)...), daysAgo(10), costPerTurn); pct != nil {
		t.Error("29 turns")
	}
}

func TestDefaultSince(t *testing.T) {
	at := func(d string) {
		day, _ := time.ParseInLocation("2006-01-02", d, time.Local)
		t.Setenv("AP_NOW", strconv.FormatInt(day.Add(12*time.Hour).Unix(), 10))
	}
	at("2026-10-10")
	if got := defaultSince(); got != "2026-10-08" {
		t.Errorf("got %s", got)
	}
	at("2026-09-10") // before every mark: today
	if got := defaultSince(); got != "2026-09-10" {
		t.Errorf("got %s", got)
	}
	found := false
	for _, m := range marks {
		found = found || m == [2]string{"2026-10-08", "8 項實驗性調整開始"}
	}
	if !found {
		t.Error("mark missing")
	}
}

func TestCtxGrowth(t *testing.T) {
	mk := func(ns ...int64) []*Turn {
		var out []*Turn
		for _, n := range ns {
			out = append(out, &Turn{Usage: Usage{In: n}})
		}
		return out
	}
	d, u := ctxGrowth(mk(100, 150, 160))
	if d == nil || d.v != 10 || u != nil {
		t.Errorf("three turns: %v %v", d, u)
	}
	d, u = ctxGrowth(mk(100, 110, 130, 140, 190))
	if d == nil || d.v != 50 || u == nil || u.v != 10 {
		t.Errorf("five turns: %v %v", d, u)
	}
}

// ── copied turns, repeats re-derived from the transcript ───────────────────

func copiedSess(t *testing.T, dir, id string, ats []float64, costs []float64, prompts []string, loads []Load) *Session {
	p := filepath.Join(dir, id+".jsonl")
	os.WriteFile(p, nil, 0o644)
	time.Sleep(10 * time.Millisecond) // the original file is created first
	s := &Session{ID: id, Path: p, Loads: loads}
	for i, at := range ats {
		s.Turns = append(s.Turns, &Turn{At: at, Usage: Usage{Cost: costs[i]}, Prompt: prompts[i]})
		s.Totals.Cost += costs[i]
	}
	s.Start, s.End, s.Opening = ats[0], ats[len(ats)-1], prompts[0]
	return s
}

func TestDropCopiedTurns(t *testing.T) {
	dir := t.TempDir()
	a := copiedSess(t, dir, "a", []float64{100, 200}, []float64{1, 2}, []string{"first", "p"}, nil)
	b := copiedSess(t, dir, "b", []float64{100, 200, 300}, []float64{1, 2, 5}, []string{"first", "p", "new"},
		[]Load{{Name: "x", At: 150}, {Name: "y", At: 300}})
	out := dropCopiedTurns([]*Session{b, a})
	var a2, b2 *Session
	for _, s := range out {
		if s.ID == "a" {
			a2 = s
		} else {
			b2 = s
		}
	}
	if len(a2.Turns) != 2 || len(b2.Turns) != 1 || b2.Turns[0].At != 300 || b2.Totals.Cost != 5 ||
		b2.Start != 300 || b2.Opening != "new" || len(b2.Loads) != 1 || b2.Loads[0].Name != "y" {
		t.Errorf("later file keeps only its own turns: %+v", b2)
	}
	dir = t.TempDir()
	a = copiedSess(t, dir, "a", []float64{100}, []float64{1}, []string{"p"}, nil)
	b = copiedSess(t, dir, "b", []float64{100}, []float64{1}, []string{"p"}, nil)
	if out := dropCopiedTurns([]*Session{a, b}); len(out) != 1 || out[0].ID != "a" {
		t.Error("a pure copy disappears")
	}
	dir = t.TempDir()
	a = copiedSess(t, dir, "a", []float64{100}, []float64{1}, []string{"p"}, nil)
	b = copiedSess(t, dir, "b", []float64{200}, []float64{2}, []string{"p"}, nil)
	if len(dropCopiedTurns([]*Session{a, b})) != 2 {
		t.Error("unrelated sessions untouched")
	}
}

func TestRepeatDetailRederivedFromTranscript(t *testing.T) {
	testHome(t)
	tool := func(cmd string) *ojson.Object {
		return obj("type", "assistant", "timestamp", "2026-01-01T00:00:01Z", "message", obj("role", "assistant",
			"content", []any{obj("type", "tool_use", "name", "Bash", "input", obj("command", cmd))}))
	}
	user := func(m int, body string) *ojson.Object {
		return obj("type", "user", "timestamp", "2026-01-01T00:0"+strconv.Itoa(m)+":00Z", "message", obj("role", "user", "content", body))
	}
	path := writeRows(t, filepath.Join(t.TempDir(), "sess1234.jsonl"),
		user(0, "a"), tool("npm run build"), user(1, "b"), tool("npm run build"))
	rep := obj("turns", 2, "flagged", []any{2}, "share", 0.5, "reasons", obj("2", obj("1", "")))
	s := &Session{Path: path, Turns: []*Turn{{At: 1767225600}, {At: 1767225660}}}
	got := remapRepeats(s, rep)
	if r := py.Dumps(ojson.Field(got, "reasons")); r != `{"2": {"1": "npm run build"}}` {
		t.Errorf("reasons %s", r)
	}
}

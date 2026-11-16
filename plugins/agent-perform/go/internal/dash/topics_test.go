package dash

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

func cfgStart(at float64, model string, skills []string, files ...any) []any {
	return []any{
		obj("type", "attachment", "timestamp", iso(at), "attachment", obj("type", "skill_listing", "isInitial", true, "names", skills)),
		obj("type", "attachment", "timestamp", iso(at), "attachment", obj("type", "instructions", "files", files)),
		obj("type", "attachment", "timestamp", iso(at), "attachment", obj("type", "model", "identity", obj("modelId", model))),
		obj("type", "attachment", "timestamp", iso(at), "attachment", obj("type", "deferred_tools_delta", "addedNames", []any{"mcp__jira__search", "Read"})),
		userRec(at+1, "hello"),
	}
}

func TestConfigChanges(t *testing.T) {
	h := testHome(t)
	dir := filepath.Join(h, ".claude", "projects", "p")
	day := 86400.0
	now := float64(time.Now().Unix())
	file := func(c string) any { return obj("path", h+"/.claude/CLAUDE.md", "content", c) }
	writeRows(t, filepath.Join(dir, "a.jsonl"), cfgStart(now-10*day, "m1", []string{"a", "b"}, file("one"))...)
	writeRows(t, filepath.Join(dir, "b.jsonl"), cfgStart(now-5*day, "m1", []string{"a", "b"}, file("one\ntwo"))...)
	writeRows(t, filepath.Join(dir, "c.jsonl"), cfgStart(now-1*day, "m2", []string{"a", "c"}, file("one\ntwo"))...)
	writeRows(t, filepath.Join(dir, "noise.jsonl"), userRec(now, "no start attachments"))

	evs := scanConfigChanges()
	var got []string
	for _, e := range evs {
		switch e.kind {
		case "file":
			got = append(got, "file "+e.path+" +"+strconv.Itoa(e.add)+" -"+strconv.Itoa(e.del)+" "+strings.Join(e.sample, "|"))
		case "model":
			got = append(got, "model "+e.from+">"+e.to)
		default:
			got = append(got, e.kind+" "+e.name)
		}
	}
	want := "file ~/.claude/CLAUDE.md +1 -0 two;skill- b;model m1>m2;skill+ c" // a model switch is dated at the start of its day
	if strings.Join(got, ";") != want {
		t.Errorf("events\n got %s\nwant %s", strings.Join(got, ";"), want)
	}

	// a rescan reuses unchanged files and sees new ones
	cfgMu.Lock()
	cfgAt = time.Time{}
	cfgMu.Unlock()
	writeRows(t, filepath.Join(dir, "d.jsonl"), cfgStart(now, "m2", []string{"a", "c", "d"}, file("one\ntwo"))...)
	found := false
	for _, e := range configChanges() {
		found = found || (e.kind == "skill+" && e.name == "d")
	}
	if !found {
		t.Error("the rescan missed the skill added in the new transcript")
	}
	if len(cfgFiles) != 5 {
		t.Errorf("cached %d files", len(cfgFiles))
	}
}

func TestLineDiff(t *testing.T) {
	add, del, sample := lineDiff("a\nb\n\nc", "a\nc\nd\ne\nf")
	if add != 3 || del != 1 || strings.Join(sample, ",") != "d,e" {
		t.Errorf("diff %d %d %v", add, del, sample)
	}
}

// Every topic page renders the shared template: crumb, title, a headline sentence, no meta wording.
func TestTopicPages(t *testing.T) {
	testHome(t)
	ss := leverSessions(t)
	since := Now().AddDate(0, 0, -7).Format("2006-01-02")
	for key, def := range topics {
		html := TopicView(ss, 14, since, key, topicLens[key])
		mustNot(t, html, `class="crumb"`) // a top-level page has no crumb
		mustHave(t, html, `<h1 class="view">`+def.title+`</h1>`)
		for _, bad := range []string{"原平台", "改版樣本", "%!", "NaN", "{}"} {
			if strings.Contains(visibleText(html), bad) {
				t.Errorf("%s: %q on the page", key, bad)
			}
		}
		if key != "verify" {
			mustHave(t, html, `<p class="lede">`)
		}
	}
}

// The before/after table and its headline agree on the per-turn cost.
func TestVerifyTableMatchesHeadline(t *testing.T) {
	testHome(t)
	day := 86400.0
	now := float64(time.Now().Unix())
	var rows []any
	for i := 0; i < 40; i++ { // 40 turns on each side, spread over 10 days
		for _, at := range []float64{now - float64(20-i/4)*day, now - float64(10-i/4)*day + 3600} {
			rows = append(rows, userRec(at, "q"), assistantRec(at+5, "m"+strconv.Itoa(i)+strconv.Itoa(int(at)), int64(20_000+int(at)%7), 0, ""))
		}
	}
	path := writeRows(t, filepath.Join(t.TempDir(), "v.jsonl"), rows...)
	ss := []*Session{one(path, 0)}
	since := Now().AddDate(0, 0, -10).Format("2006-01-02")
	html := TopicView(ss, 30, since, "verify", "")
	for _, h := range []string{"指標", "改動前", "改動後", "超出隨機波動"} {
		mustHave(t, html, ">"+h+"<")
	}
	mustHave(t, html, "以 <b>"+since[5:]+"</b>")
	mustHave(t, html, "每回合花費")
}

func TestNiceTop(t *testing.T) {
	for v, want := range map[float64]float64{0: 1, 0.7: 1, 2.4: 2.5, 3.1: 4, 180: 200, 1001: 2000} {
		if got := niceTop(v); got != want {
			t.Errorf("niceTop(%v) = %v, want %v", v, got, want)
		}
	}
}

func TestTurnRanges(t *testing.T) {
	if got := turnRanges([]int{26, 31, 32, 33, 34, 40, 41}); got != "#26、#31～#34、#40、#41" {
		t.Errorf("ranges %q", got)
	}
}

// The same day shows the same spend on the overview row, the day page and the sum of its sessions.
func TestDaySpendAgreesAcrossPages(t *testing.T) {
	testHome(t)
	y := time.Now().AddDate(0, 0, -1)
	at := float64(time.Date(y.Year(), y.Month(), y.Day(), 10, 0, 0, 0, time.Local).Unix()) // both turns on one day
	path := writeRows(t, filepath.Join(t.TempDir(), "d.jsonl"),
		userRec(at, "first"), assistantRec(at+5, "m1", 20_000, 0, ""),
		userRec(at+7_200, "second"), assistantRec(at+7_205, "m2", 150_000, 0, ""))
	ss := []*Session{one(path, 0)}
	day := fromTimestamp(ss[0].Start).Format("2006-01-02")
	var total float64
	for _, x := range ss[0].Turns {
		total += x.Usage.Cost
	}
	want := money(total)
	mustHave(t, Overview(ss, 14, "", ""), want)
	mustHave(t, DayView(ss, day), want)
	mustHave(t, SessionView(ss, ss[0].ID), want)
}

// Each month is its own sortable table: the overview's day table must not be locked.
func TestDayTableStaysSortable(t *testing.T) {
	testHome(t)
	html := Overview(leverSessions(t), 14, "", "")
	i := strings.Index(html, "每日一覽")
	if i < 0 {
		t.Fatal("no day table")
	}
	mustHave(t, html[i:], `<button class="mh">`)
	mustNot(t, html[i:min(len(html), i+4000)], "data-fixed")
}

func TestSpoken(t *testing.T) {
	for s, want := range map[float64]string{45: "45 秒", 725: "12 分鐘", 8567: "2.4 小時", 7200: "2 小時", 36107: "10 小時"} {
		if got := spoken(s); got != want {
			t.Errorf("spoken(%v) = %q, want %q", s, got, want)
		}
	}
}

func TestBarSVGPop(t *testing.T) {
	bars := []nbar{{"10-01", []float64{1}, "tip", "/verify?since=2026-10-01#chart", false, true}}
	ax := func(v float64) string { return "" }
	with := barSVGPop(bars, []string{"<b>10-01</b>　每回合 $1 <a href=\"/?day=2026-10-01\">x</a>"}, []string{"red"}, ax, 0, 1)
	mustHave(t, with, `class="hit on"`)
	mustHave(t, with, `data-pop="&lt;b&gt;10-01&lt;/b&gt;　每回合`)
	mustHave(t, with, `/?day=2026-10-01`)
	if strings.Contains(with, `<a href=`) {
		t.Errorf("bars still wrapped in a link: %s", with)
	}
	mustHave(t, barSVG(bars, []string{"red"}, ax, 0, 1), `<a href=`)
}

func TestVerifyChartKeepsItsWindow(t *testing.T) {
	testHome(t)
	t.Setenv("AP_NOW", "1793203199")
	title := regexp.MustCompile(`每天的每回合花費（[^）]+）`)
	a := title.FindString(VerifyBody(nil, Now().AddDate(0, 0, -30).Format("2006-01-02"), nil))
	b := title.FindString(VerifyBody(nil, Now().AddDate(0, 0, -3).Format("2006-01-02"), nil))
	if a == "" || a != b {
		t.Errorf("chart window moved with the change date: %q vs %q", a, b)
	}
	mustHave(t, VerifyBody(nil, "2026-10-01", nil), `<div id="vbody">`)
}

// addCorrections writes the label file the correction judging of versions before 2.0 left behind.
func addCorrections(t testing.TB) {
	t.Helper()
	writeRows(t, claudeFile("auto_corrections.jsonl"), obj("key", "x:1", "correction", false, "conf", 0.9))
}

// An old install's correction labels never bring a correction view back.
func TestOldCorrectionLabelsAreIgnored(t *testing.T) {
	sid := richHome(t)
	addCorrections(t)
	day := time.Now().Add(-7200 * time.Second).Format("2006-01-02")
	for _, c := range [][2]string{{"/", ""}, {"/", "day=" + day}, {"/", "session=" + sid}, {"/tools", ""}, {"/context", ""},
		{"/delegate", ""}, {"/model", ""}, {"/verify", ""}, {"/expensive", ""}, {"/how", ""}, {"/pipeline", "session=" + sid + "&turn=1"}} {
		_, _, raw := Render(c[0], c[1])
		page := regexp.MustCompile(`(?s)<script.*?</script>|<style.*?</style>`).ReplaceAllString(string(raw), "")
		for _, bad := range []string{`href="/talk"`, "更正", "本機模型", "bge-m3", "judge", "派工"} {
			mustNot(t, page, bad)
		}
	}
	st, h, _ := Render("/talk", "")
	if st != 302 || h["Location"] != "/" {
		t.Errorf("/talk: %d %v", st, h)
	}
}

func TestTopicPageAdviceIsTheCardFold(t *testing.T) {
	richHome(t)
	for _, path := range []string{"/context", "/tools", "/delegate", "/verify"} {
		_, _, raw := Render(path, "")
		body := string(raw)
		mustNot(t, body, "依據與門檻")
		mustNot(t, body, "<summary>怎麼省</summary>")
		if path == "/verify" {
			continue
		}
		if path != "/tools" { // 其他 Tag puts 怎麼省 under each tag
			mustHave(t, body, `<h2 class="nh">怎麼省</h2>`)
		}
		cards := regexp.MustCompile(`(?s)<details class="nfold" id="lever-.*?</div></details>`).FindAllString(body, -1)
		if len(cards) == 0 && path == "/context" {
			t.Errorf("%s: no card folds", path)
		}
		for _, f := range cards {
			mustNot(t, f, "<summary>細節</summary>") // the card's details sit right inside its fold
		}
	}
	_, _, raw := Render("/context", "")
	body := string(raw)
	mustHave(t, body, `id="lever-ctx"><summary><b>context 太長</b>：換到無關的工作用`)
	mustHave(t, body, "門檻：150–300k")
}

func TestOtherTagsPageOneCardPerTag(t *testing.T) {
	richHome(t)
	_, _, raw := Render("/tools", "")
	body := string(raw)
	mustHave(t, body, `<h1 class="view">其他 Tag</h1>`)
	mustNot(t, body, "7 種工具上限")
	mustNot(t, body, "擋下前景輪詢：流程與次數")            // no page-level fold: the poll gate is the 輪詢等待 tag's fix
	mustNot(t, body, `<h2 class="nh">怎麼省</h2>`) // each tag carries its own 怎麼省
	mustNot(t, body, `id="lever-`)              // 怎麼省 is one plain line, not a fold repeating the table
	if n := strings.Count(body, `<div class="sec">怎麼省</div>`); n < 7 {
		t.Errorf("怎麼省 under %d tags, want every limit at least", n)
	}
	i := strings.Index(body, `id="pane-bash_timeout"`)
	if i < 0 || !strings.Contains(body[i:i+strings.Index(body[i:], `id="pane-bash_out"`)], "PreToolUse") {
		t.Error("the poll gate flow sits under 輪詢等待")
	}
	mustNot(t, body, "擋下前景輪詢：流程與次數") // the flow lives on 怎麼算; the tile links there
	mustHave(t, body, `/how#hooks`)
	if n := strings.Count(body, `class="tpane`); n != 10 { // repeat, scripts, the seven limits and 做太久被中止
		t.Errorf("panes %d, want 10", n)
	}
	for _, p := range regexp.MustCompile(`(?s)<div class="tpane[^"]*" id="pane-[^"]+">.*?(?:<div class="tpane|</div></div>)`).FindAllString(body, -1) {
		if !strings.Contains(p, "<svg") {
			t.Errorf("pane without its own chart: %.80s", p)
		}
	}
	if p := body[strings.Index(body, `id="pane-slow"`):]; !strings.Contains(p, "做太久被中止") || !strings.Contains(p, `<div class="sec">怎麼省</div>`) {
		t.Error("the 做太久被中止 pane carries its own 怎麼省")
	}
	i, j := strings.Index(body, `<div class="seg">`), strings.Index(body, "<svg")
	if i < 0 || j < i {
		t.Error("the tag filter sits above the chart")
	}
}

func TestTagHowToIsShortAndSourced(t *testing.T) {
	tags := regexp.MustCompile(`<code>.*?</code>|<[^>]+>`) // commands don't count toward the length
	for _, k := range append([]string{"repeat", "scripts", "slow"}, limitOrder...) {
		h, ok := tagHowTo[k]
		if !ok {
			t.Fatalf("%s: no 怎麼省 tutorial", k)
		}
		if len(h.steps) == 0 || len(h.steps) > 3 || h.before == "" || h.after == "" {
			t.Errorf("%s: want a before/after picture and 1-3 steps", k)
		}
		n := len([]rune(tags.ReplaceAllString(h.what, "")))
		for _, s := range h.steps {
			n += len([]rune(tags.ReplaceAllString(s.text, "")))
			if s.url != "" && !strings.HasPrefix(s.url, "https://code.claude.com/") && !strings.HasPrefix(s.url, "https://platform.claude.com/") {
				t.Errorf("%s: step source is not an official doc: %s", k, s.url)
			}
		}
		if n > 200 {
			t.Errorf("%s: %d characters, keep it short", k, n)
		}
		t.Logf("%s: %d", k, n)
	}
	html := tagHowTo["bash_timeout"].html("GATE")
	mustHave(t, html, `<b class="no">改前</b>`)
	mustHave(t, html, "GATE")
	mustNot(t, tagHowTo["read"].html("GATE"), "GATE")
}

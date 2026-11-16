package dash

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"agentperform/internal/ojson"
)

func obj(kv ...any) *ojson.Object {
	o := ojson.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.Set(kv[i].(string), kv[i+1])
	}
	return o
}

func mustHave(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("missing %q in %.300q", sub, s)
	}
}

func mustNot(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("unexpected %q in %.300q", sub, s)
	}
}

func fp(v float64) *float64 { return &v }

// toObj builds an ordered object from a map, keys sorted (order is irrelevant to these tests).
func toObj(m map[string]any) *ojson.Object {
	o := ojson.NewObject()
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		o.Set(k, m[k])
	}
	return o
}

// Throwaway scripts on the subagent page: what counts as one.
func TestAdhocScript(t *testing.T) {
	kind := func(name string, inp map[string]any, w *orderedSet) string {
		k, _, ok := adhocScript(name, toObj(inp), w)
		if !ok {
			return "<none>"
		}
		return k
	}
	w := newSet()
	for cmd, want := range map[string]string{
		"cd /tmp/x; python3 -I - <<'E'\nprint(1)\nE": "python3",
		"node -e 'console.log(1)'":                   "node",
		"git status":                                 "<none>",
	} {
		if got := kind("Bash", map[string]any{"command": cmd}, w); got != want {
			t.Errorf("%q: got %q want %q", cmd, got, want)
		}
	}
	w = newSet()
	if got := kind("Bash", map[string]any{"command": "cat > /tmp/a/gen.py <<'E'\nx=1\nE"}, w); got != "py" {
		t.Errorf("scratch write: %q", got)
	}
	for _, cmd := range []string{"cd /tmp/a; python3 -I gen.py", "sed -i '' 's/1/2/' gen.py"} {
		if kind("Bash", map[string]any{"command": cmd}, w) == "<none>" {
			t.Errorf("%q should count", cmd)
		}
	}
	if kind("Bash", map[string]any{"command": "cat > report.md <<'E'\nbuilt by gen.py\nE"}, w) != "<none>" {
		t.Error("a report mentioning the script is not a run")
	}
	w = newSet()
	if kind("Write", map[string]any{"file_path": "/tmp/a/check.py", "content": "x"}, w) != "py" {
		t.Error("scratch Write")
	}
	for _, p := range []string{"/Users/me/repo/app.py", "/tmp/a/notes.md"} {
		if kind("Write", map[string]any{"file_path": p, "content": "x"}, w) != "<none>" {
			t.Errorf("%s is not a scratch script", p)
		}
	}
}

// Coarse 'what does this throwaway script do' buckets, from the code text.
func TestScriptKind(t *testing.T) {
	for code, word := range map[string]string{
		"p='a.py'; s=open(p).read()\ns=s.replace('x','y')\nopen(p,'w').write(s)": "改檔案",
		"sed -i '' 's/a/b/' f.py":                                       "改檔案",
		"import unittest\nunittest.main()":                              "測試",
		"import cProfile\ncProfile.run('f()')":                          "計時",
		"import urllib.request\nurllib.request.urlopen('http://x')":     "HTTP",
		"for l in open('/u/.claude/projects/x/s.jsonl'): json.loads(l)": "transcript",
		"import json\nd=json.load(open('a.json'))\nprint(len(d))":       "JSON",
		"import re,html\nprint(re.sub(r'<[^>]+>','',s))":                "擷取",
		"from PIL import Image\nImage.open('a.png')":                    "圖片",
		"print(1+1)": "其他",
	} {
		mustHave(t, scriptKind(code), word)
	}
	var calls []ScriptRec
	takeScript(&calls, map[string]ScriptRec{}, "x", "python3",
		"import os, re\nfrom a import b\n\"\"\"doc\"\"\"\nd = json.load(open('hunks.json'))\nprint(d)", true)
	if got, _ := ojson.Str(ojson.Field(calls[0], "line")); got != "d = json.load(open('hunks.json'))" {
		t.Errorf("example line %q", got)
	}
}

func TestRepTag(t *testing.T) {
	lbl, tip, ok := repTag("重複呼叫", obj("3", "…/scripts/dashboard.py"))
	if !ok || lbl != "重複呼叫" {
		t.Errorf("single reason: %q", lbl)
	}
	mustHave(t, tip, "重讀沒改過的檔案、同一個網址或搜尋：…/scripts/dashboard.py")
	lbl, tip, _ = repTag("重複呼叫", obj("1", "python3 analyze.py", "3", "a.py", "4", "相似度 0.91"))
	if lbl != "重複呼叫 ×3" {
		t.Errorf("several reasons: %q", lbl)
	}
	mustHave(t, tip, "python3 analyze.py")
	mustHave(t, tip, "a.py")
	if _, _, ok := repTag("重複呼叫", ojson.NewObject()); ok {
		t.Error("no reasons gives no tag")
	}
}

// A turn row is red only for waste kinds the 主對話花費 block above lists.
func TestListedKinds(t *testing.T) {
	w := wasteT{ctx: 0.6, slow: 0.2, total: 0.8}
	if got := listedKinds(w, nil, 0.15); len(got) != 1 || !got["ctx"] {
		t.Errorf("got %v", got)
	}
	low := obj("turns", 20, "flagged", []any{3}, "share", 0.04)
	high := obj("turns", 20, "flagged", []any{3, 4}, "share", 0.10)
	if listedKinds(w, low, 0.15)["rep"] {
		t.Error("under loud/3 is not listed")
	}
	if !listedKinds(w, high, 0.15)["rep"] {
		t.Error("over loud/3 is listed")
	}
	if got := listedKinds(wasteT{ctx: 0.1, total: 0.1}, nil, 0.15); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// Advice for a repeated call, picked from its reason code and what repeated.
func TestRepeatAdvice(t *testing.T) {
	cases := []struct{ code, detail, word string }{
		{"1", "cd x && python3 -m pytest -q tests", "測試"},
		{"1", "cargo test -q", "測試"},
		{"1", "git status -s", "狀態"},
		{"1", "curl -s localhost:8787/health", "背景"},
		{"1", "npm run build", "建置"},
		{"1", "python3 gen.py --out a.html", "只輸出需要的部分"},
		{"2", "Skill:docs-writer", "skill"},
		{"2", `mcp__jira__get_issue {"key": "A-1"}`, "MCP"},
		{"3", "…/repo/scripts/dashboard.py", "Read 指定行數"},
		{"3", "https://x.test/a", "網址"},
		{"3", "def main|/repo", "搜尋"},
		{"4", "相似度 0.91", "一次改好"},
		{"1", "source ~/.zshrc", "shell 環境"},
		{"1", "open /tmp/a/story.html", "開視窗"},
		{"1", "dotnet test", "測試"},
		{"1", "./gradlew :app:installDebug", "建置"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		_, got := repeatType(c.code, c.detail)
		mustHave(t, got, c.word)
		seen[got] = true
	}
	if len(seen) < 10 {
		t.Errorf("forms collapse into %d texts", len(seen))
	}
	if _, got := repeatType("9", "x"); got != "" {
		t.Errorf("unknown code: %q", got)
	}
}

// Cache hit tags: coloured only when Cache 過期重寫 is 10% of the row's cost.
func TestHitTags(t *testing.T) {
	mustHave(t, hitTagCell(fp(0.95), 1.0, 10.0), "pb cache")
	mustHave(t, hitTagCell(fp(0.5), 0.9, 10.0), "pb rep")
	mustHave(t, hitTagCell(fp(0.5), 0, 10.0), "Cache hit 50.0%")
	mustNot(t, hitTagCell(nil, 0, 0), "Cache hit")
	mustHave(t, hitPill(fp(0.97), 0.0996, 1.0, true), "9.9%，未達 10%")
	mustHave(t, hitPill(fp(0.97), 0.1, 1.0, true), "10.0%，達 10%")
	cells := hitTagCell(nil, 0, 0) + wasteCell(wasteT{}, 1.0)
	mustNot(t, cells, "·</td>")
	if n := strings.Count(cells, "—"); n != 2 {
		t.Errorf("empty cells use one dash each, got %d", n)
	}
}

func TestCacheRewrite(t *testing.T) {
	const opus = "claude-opus-5-5"
	near := func(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }
	tok, usd := cacheRewrite(fp(7200), 3600, opus, 150_000, 0)
	if tok != 150_000 || !near(usd, 150_000*(8.0-0.2)/1e6) {
		t.Errorf("1h: %d %v", tok, usd)
	}
	if _, usd5 := cacheRewrite(fp(400), 300, opus, 150_000, 0); !near(usd5, 150_000*(5.0-0.2)/1e6) {
		t.Errorf("5m: %v", usd5)
	}
	for _, c := range []struct {
		gap    *float64
		cw, cr int64
	}{{fp(3000), 150_000, 0}, {nil, 150_000, 0}, {fp(7200), 4_000, 0}, {fp(7200), 9_000, 20_000}} {
		if tok, usd := cacheRewrite(c.gap, 3600, opus, c.cw, c.cr); tok != 0 || usd != 0 {
			t.Errorf("%+v: %d %v", c, tok, usd)
		}
	}
}

func TestSmallFormatters(t *testing.T) {
	if mcpLabel("plugin_agent-perform_agent-perform") != "agent-perform" {
		t.Error(mcpLabel("plugin_agent-perform_agent-perform"))
	}
	mustNot(t, modelLabel("<synthetic>"), "<synthetic>")
	if got := strings.Split(spanText(0, 1617*60), "（")[0]; got != "26 小時 57 分" {
		t.Errorf("span %q", got)
	}
	if promptLabel("<task-notification> <task-id>a</task-id>") != "（背景工作通知）" || promptLabel("fix it") != "fix it" {
		t.Error("prompt label")
	}
	out := zSpan(2.31, "後減前")
	mustHave(t, out, "超出隨機波動")
	mustHave(t, out, `title="z=+2.31`)
	mustHave(t, zSpan(-0.5, "後減前"), "未超出")
}

func TestOtherKind(t *testing.T) {
	if !strings.HasPrefix(otherKind("<task-notification>x"), "背景工作通知") {
		t.Error(otherKind("<task-notification>x"))
	}
	if otherKind("<command-name>/clear</command-name>") != "斜線指令 /clear" {
		t.Error(otherKind("<command-name>/clear</command-name>"))
	}
	if !strings.HasPrefix(otherKind("[Image: source: /a.png]"), "貼上的圖片") {
		t.Error(otherKind("[Image: source: /a.png]"))
	}
}

// The source bar sits under the table heading and above the table.
func TestCatSectionLayout(t *testing.T) {
	items := []*Item{{Cat: "tool_result", Src: "Read", N: 60, Pos: -1}, {Cat: "other_user", Src: "背景工作通知（x）", N: 40, Pos: -1}}
	html := catSection(items, 100, "對話訊息裡有什麼（依來源）", msgsNote(100), 0, false)
	h, bar, tbl := strings.Index(html, "對話訊息裡有什麼（依來源）"), strings.Index(html, `class="pp-stack"`), strings.Index(html, "<table")
	if !(h < bar && bar < tbl) || h < 0 {
		t.Errorf("order heading %d bar %d table %d", h, bar, tbl)
	}
	mustHave(t, html, `token"></i>`)
	mustHave(t, html, "估算")
}

func TestRouteAndPage(t *testing.T) {
	if k, tg := route("/glossary"); k != "redirect" || tg != "/how#terms" {
		t.Errorf("glossary: %s %s", k, tg)
	}
	for _, p := range []string{"/levers", "/patterns", "/per-turn-path.html", "/offline-batch.html"} {
		if k, _ := route(p); k != "gone" {
			t.Errorf("%s: %s", p, k)
		}
	}
	if k, tg := route("/how"); k != "page" || tg != "how" {
		t.Errorf("how: %s %s", k, tg)
	}
	nav := pageHTML[strings.Index(pageHTML, `<nav>`):]
	nav = nav[:strings.Index(nav, "<button")]
	var links []string
	for _, part := range strings.Split(nav, `href="`)[1:] {
		links = append(links, part[:strings.Index(part, `"`)])
	}
	if strings.Join(links, " ") != "/ /context /tools /delegate /model /verify /expensive /optimize /how" {
		t.Errorf("nav %v", links)
	}
	mustNot(t, pageHTML, "__CORR_NAV__")
	for _, rule := range []string{".pb.cache", ".pb.slow", ".pb.ctx", ".pb.rep", ".verdict.good", "p.fix::before",
		"td.prompt{white-space:normal;width:100%"} {
		mustHave(t, pageHTML, rule)
	}
}

func TestPctOptAxisNotRotated(t *testing.T) {
	opt := pctOpt([]string{"10-01", "10-02"}, map[int][]any{50: {1, 2}}, nil)
	rot := ojson.Field(ojson.Field(ojson.Field(opt, "xAxis"), "axisLabel"), "rotate")
	if fmt.Sprint(rot) != "0" {
		t.Errorf("rotate %v", rot)
	}
}

func TestQuotedToS(t *testing.T) {
	for in, want := range map[string]string{
		`a "x" b 'y' c`: "a S b S c", `"unclosed`: `"unclosed`, `'it"s' "q'q"`: "S S", `"" ''`: "S S", `x "a'b" 'c`: "x S 'c",
	} {
		if got := quotedToS(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}

func TestLeverHrefPointsAtTheTopicPage(t *testing.T) {
	for k, want := range map[string]string{
		"fixed":    "/context#lever-fixed",
		"delegate": "/delegate#lever-delegate", "slow": "/verify#lever-slow",
	} {
		if got := leverHref(k); got != want {
			t.Errorf("leverHref(%q) = %q, want %q", k, got, want)
		}
	}
}

func TestLifeOf(t *testing.T) {
	const m = "claude-opus-5-5" // read $0.20, 1h write $8, 5m write $5 per MTok
	call := func(pos int, short bool) *Call {
		c := &Call{Pos: pos, Model: m, Ctx: 4_000_000}
		if short {
			c.Cw5m = 1
		}
		return c
	}
	calls := []*Call{call(1, true), call(5, false), call(7, true), call(9, true)}
	for _, c := range []struct {
		name               string
		calls              []*Call
		rpos               int
		cuts               []int
		sent, short, cut   bool
		reads              int
		write, read, ctxPc float64
	}{
		{"1h write, reads to the end", calls, 3, nil, true, false, false, 2, 8, 0.4, 25},
		{"5m write", []*Call{call(5, true), call(7, false)}, 3, nil, true, true, false, 1, 5, 0.2, 25},
		{"reads stop at the next compaction", calls, 3, []int{2, 8}, true, false, true, 1, 8, 0.2, 25},
		{"no call after the result", calls, 10, nil, false, false, false, 0, 0, 0, 0},
		{"compacted before it was sent", calls, 3, []int{4}, false, false, true, 0, 0, 0, 0},
	} {
		l := lifeOf(1_000_000, c.rpos, c.calls, c.cuts)
		near := func(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }
		if l.sent != c.sent || l.short != c.short || l.compacted != c.cut || l.reads != c.reads ||
			!near(l.writeUSD, c.write) || !near(l.readUSD, c.read) || !near(l.ctxPct, c.ctxPc) {
			t.Errorf("%s: got %+v", c.name, l)
		}
		if !near(l.total(), c.write+c.read) {
			t.Errorf("%s: total %v", c.name, l.total())
		}
	}
}

func TestToolParam(t *testing.T) {
	for in, want := range map[string]string{"toolu_01AbC-x": "toolu_01AbC-x", "": "", "<b>": "", "a\"b": "", strings.Repeat("a", 129): ""} {
		if got := toolParam(in); got != want {
			t.Errorf("toolParam(%q) = %q", in, got)
		}
	}
}

func TestPickPastCaps(t *testing.T) {
	var groups []group
	for g := 0; g <= RepeatListN; g++ {
		var us []*Use
		for i := 0; i <= CallsPerGrp; i++ {
			us = append(us, &Use{Kind: fmt.Sprint("k", g), Tid: fmt.Sprintf("g%dc%d", g, i), Usd: float64(RepeatListN - g)})
		}
		groups = append(groups, group{fmt.Sprint("k", g), us})
	}
	last := fmt.Sprintf("g%dc%d", RepeatListN, CallsPerGrp)
	plain := toolsSection(groups, nil, "", false, nil)
	mustNot(t, plain, `id="tool-`+last+`"`)
	out := toolsSection(groups, nil, "", false, &toolPick{tid: last, hit: true})
	mustHave(t, out, `id="tool-`+last+`" data-hit open`)
	if n := strings.Count(out, fmt.Sprintf(`id="tool-g%dc`, RepeatListN)); n != CallsPerGrp+1 {
		t.Errorf("picked group drew %d calls, want the capped %d plus the pick", n, CallsPerGrp)
	}
	mustHave(t, out, "還在執行")
}

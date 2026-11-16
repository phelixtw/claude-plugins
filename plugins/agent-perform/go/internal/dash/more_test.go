package dash

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
)

// ── pipeline: hook items, fixed load, scripts, cache line ──────────────────

func hookRec(kind, event, name, text string) *ojson.Object {
	return obj("type", "attachment", "rendered", []any{obj("content", text)},
		"attachment", obj("type", kind, "hookEvent", event, "hookName", name))
}

// Every hook output lands in the Hook category; only SessionStart counts as fixed load.
func TestHookCategory(t *testing.T) {
	testHome(t)
	path := writeRows(t, filepath.Join(t.TempDir(), "h.jsonl"),
		obj("type", "user", "message", obj("role", "user", "content", "fix it")),
		hookRec("hook_success", "SessionStart", "SessionStart:startup", "skill body"),
		hookRec("hook_additional_context", "SessionStart", "SessionStart", "more"),
		hookRec("hook_additional_context", "UserPromptSubmit", "UserPromptSubmit", "per turn"),
		obj("type", "assistant", "message", obj("id", "m1", "role", "assistant", "content", []any{
			obj("type", "tool_use", "id", "t1", "name", "Agent", "input", obj("description", "d"))})),
		obj("type", "user", "toolUseResult", ojson.NewObject(), "message", obj("role", "user", "content", []any{
			obj("type", "tool_result", "tool_use_id", "t1", "is_error", true,
				"content", "PreToolUse:Agent hook error: [check.sh]: ask first")})),
		hookRec("hook_blocking_error", "Stop", "Stop", "keep going"),
		obj("type", "user", "isMeta", true, "message", obj("role", "user", "content", "Stop hook feedback: keep going")),
		obj("type", "assistant", "message", obj("id", "m2", "role", "assistant", "content", []any{obj("type", "text", "text", "ok")})))
	items, _, _, _ := itemsFor(path, nil, 1, false)
	var srcs []string
	fixed := map[string]bool{}
	for _, it := range items {
		if it.Cat == "hook" {
			srcs = append(srcs, it.Src)
			if isFixed(it) {
				fixed[it.Src] = true
			}
		}
	}
	sort.Strings(srcs)
	want := []string{"PreToolUse hook（擋下 Agent）", "SessionStart hook", "SessionStart hook", "Stop hook", "Stop hook", "UserPromptSubmit hook"}
	if strings.Join(srcs, "|") != strings.Join(want, "|") {
		t.Errorf("hook items %q", srcs)
	}
	if len(fixed) != 1 || !fixed["SessionStart hook"] {
		t.Errorf("fixed %v", fixed)
	}
}

// fixed_tokens: a single-turn transcript is counted once until the file changes.
func TestFixedTokensSingleTurnCache(t *testing.T) {
	testHome(t)
	path := writeRows(t, filepath.Join(t.TempDir(), "f.jsonl"), obj("type", "user", "message", obj("content", "hi")))
	a := FixedTokens(path, nil)
	sig := fixedSig[path]
	if FixedTokens(path, nil) != a || fixedSig[path] != sig {
		t.Error("second call is a cache hit")
	}
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("\n" + py.Dumps(obj("type", "user", "message", obj("content", "more"))))
	f.Close()
	FixedTokens(path, nil)
	if fixedSig[path] == sig {
		t.Error("a changed file is counted again")
	}
	if _, ok := fixedAll[path]; ok {
		t.Error("single-turn transcripts stay out of the permanent cache")
	}
}

func bashUse(id, cmd string) *ojson.Object {
	return obj("type", "tool_use", "id", id, "name", "Bash", "input", obj("command", cmd))
}

func entry(kind string, content any, sec int) *ojson.Object {
	return obj("type", kind, "timestamp", fmt.Sprintf("2026-01-01T00:00:%02dZ", sec),
		"message", obj("role", kind, "content", content, "id", fmt.Sprintf("m%d", sec),
			"model", "claude-sonnet-4-5", "usage", obj("input_tokens", 10, "output_tokens", 10)))
}

func TestScriptTag(t *testing.T) {
	html := scriptTagTurn(2, 3)
	mustHave(t, html, "臨時腳本 ×5")
	mustHave(t, html, "主對話 2、subagent 3")
	if scriptTagTurn(0, 0) != "" {
		t.Error("hidden at zero")
	}
	testHome(t)
	code := "python3 - <<EOF\nprint(1)\nEOF"
	path := writeRows(t, filepath.Join(t.TempDir(), "s.jsonl"),
		entry("user", "hi", 0),
		entry("assistant", []any{bashUse("a", code), bashUse("b", "ls")}, 1),
		entry("assistant", []any{bashUse("a", code)}, 2)) // streamed repeat of "a"
	if n := fnum(scanAgent(path), "scripts"); n != 1 {
		t.Errorf("subagent scan counts %v", n)
	}
	if n := one(path, 0).Turns[0].Scripts; n != 1 {
		t.Errorf("turn counts %d", n)
	}
}

func TestUsdText(t *testing.T) {
	for v, want := range map[float64]string{0: "不到 $0.01", 0.004: "不到 $0.01", 0.785: "$0.79", 3.474: "$3.47"} {
		if got := usdText(v); got != want {
			t.Errorf("usdText(%v) = %q, want %q", v, got, want)
		}
	}
	if usdApprox(0.03) != "約 $0.03" || usdApprox(0.001) != "不到 $0.01" {
		t.Error("usdApprox")
	}
}

func TestTurnSummary(t *testing.T) {
	testHome(t)
	now := float64(time.Now().Unix()) - 600
	a := assistantRec(now+5, "m1", 48_200, 51_800, "")
	ojson.Obj(ojson.Field(ojson.Field(a, "message"), "usage")).Set("input_tokens", 0)
	path := writeRows(t, filepath.Join(projects(), "-x", "cl.jsonl"), userRec(now, "go"), a)
	calls := []*tcCall{{usd: 2}, {usd: 1}}
	out := turnSummary(one(path, 0).Turns[0], calls, nil, nil, "本回合")
	for _, want := range []string{"Cache hit 51.8%", "cache 讀 51,800", "cache 寫 48,200", "$3.00", "2 次 API 呼叫", "最貴的 2 次，佔本回合 100%", "#1"} {
		mustHave(t, out, want)
	}
}

// ── the day page's script and repeat groups, session tags ──────────────────

const job = "import json\nd = json.load(open('%s'))\nprint(len(d), %d)\n# note %d\n"

func rec(code string) []ScriptRec {
	var calls []ScriptRec
	takeScript(&calls, map[string]ScriptRec{}, "x", "python3", code, true)
	return calls
}

func jobCode(path string, a, b int) string {
	return strings.NewReplacer("%s", path, "%d", "").Replace(strings.Replace(strings.Replace(job, "%d", py.F(float64(a), 0), 1), "%d", py.F(float64(b), 0), 1))
}

var dayAt = float64(time.Date(2026, 10, 17, 10, 0, 0, 0, time.Local).Unix())

// bareTurn has the empty collections one() always fills in.
func bareTurn() *Turn {
	return &Turn{Tools: py.NewCounter(), Limits: py.NewCounter(), SubLimits: py.NewCounter(), Skills: newSet(), MCP: newSet()}
}

func scriptTurn(n int, calls []ScriptRec) *Turn {
	t := bareTurn()
	t.At, t.ScriptCalls = dayAt+float64(n), calls
	return t
}

func dayScripts(ss []*Session, day string) [][]scriptRef {
	lo, _ := time.ParseInLocation("2006-01-02", day, time.Local)
	l := float64(lo.Unix())
	order, groups := scriptRecords(ss, func(_ *Session, at float64) bool { return l <= at && at < l+86400 })
	var out [][]scriptRef
	for _, fp := range order {
		if len(groups[fp]) > 1 {
			out = append(out, groups[fp])
		}
	}
	return out
}

func TestScriptGroups(t *testing.T) {
	a, b := rec(jobCode("/tmp/a.json", 1, 1)), rec(jobCode("/tmp/b/c.json", 99, 2))
	if py.Dumps(ojson.Field(a[0], "fp")) != py.Dumps(ojson.Field(b[0], "fp")) {
		t.Fatal("path and number differences share a fingerprint")
	}
	s := &Session{ID: "s1", Turns: []*Turn{scriptTurn(1, a), scriptTurn(2, b)},
		Dele: obj("launches", []any{obj("at", dayAt, "turn", 1, "script_calls", toAnys(rec(jobCode("/tmp/z", 5, 3))))})}
	groups := dayScripts([]*Session{s}, "2026-10-17")
	if len(groups) != 1 || len(groups[0]) != 3 {
		t.Errorf("groups %d", len(groups))
	}
	other := rec("import csv\nrows = list(csv.reader(open('/tmp/x.csv')))\nprint(len(rows))\n")
	s = &Session{ID: "s1", Turns: []*Turn{scriptTurn(1, rec(jobCode("/tmp/a.json", 1, 1))), scriptTurn(2, other)}, Dele: obj("launches", []any{})}
	if len(dayScripts([]*Session{s}, "2026-10-17")) != 0 {
		t.Error("different jobs are not a group")
	}
	if len(rec("python3 /tmp/x.py")) != 0 {
		t.Error("a bare script run is not fingerprinted")
	}
	keys := append([]string{}, a[0].Keys...)
	sort.Strings(keys)
	if strings.Join(keys, " ") != "code fp kind lang line out shape tok" {
		t.Errorf("record keys %v", keys)
	}
}

func toAnys(rs []ScriptRec) []any {
	out := make([]any, len(rs))
	for i, r := range rs {
		out[i] = r
	}
	return out
}

func TestSessionTags(t *testing.T) {
	var picked []*Turn
	for i := 0; i < 3; i++ {
		lim := py.NewCounter()
		lim.Add("read", 1)
		picked = append(picked, &Turn{At: dayAt + float64(i), Scripts: 2, DScripts: 1, Limits: lim, SubLimits: py.NewCounter()})
	}
	w := wasteT{flags: map[int][]flag{0: {{"slow", 1}}, 1: {{"slow", 1}}}}
	html := sessionTags(picked, w, 2, 10.0)
	if strings.Count(html, "pb scr") != 1 {
		t.Error("each kind once")
	}
	for _, want := range []string{"臨時腳本 ×9", "重複呼叫 ×2", "做太久被中止 ×2"} {
		mustHave(t, html, want)
	}
	mustNot(t, html, "Cache hit")
	mustNot(t, html, "context")
}

// ── /levers candidates, repeat types and the copy-out bundles ──────────────

const jira = "import requests\nr = requests.get(f'{BASE}/rest/api/3/issue/%s/comment', headers=H)\nprint(r.json())\n"

func jiraCode(key string) string { return strings.Replace(jira, "%s", key, 1) }

func TestScriptShape(t *testing.T) {
	a, b := scriptShape(jiraCode("ABC-1"), "py"), scriptShape(jiraCode("ABC-22"), "py")
	if a != "API|Jira|issue/*/comment" || a != b {
		t.Errorf("jira %q %q", a, b)
	}
	for code, want := range map[[2]string]string{
		{"curl -s https://slack.com/api/chat.postMessage -d x", "sh"}:                        "API|Slack|chat.postMessage",
		{"cd /tmp/x\nFOO=1\necho == head ==\nfor f in a b; do\n  git log -1 $f\ndone", "sh"}: "Shell|git log",
		{"import os, sys\nimport csv\nprint(1)", "py"}:                                       "Python|csv",
	} {
		if got := scriptShape(code[0], code[1]); got != want {
			t.Errorf("%q: %q", code[0], got)
		}
	}
}

func candSess(id, code string, start float64, loaded ...string) *Session {
	var loads []Load
	for _, n := range loaded {
		loads = append(loads, Load{Name: n})
	}
	return &Session{ID: id, Start: start, Loads: loads, Turns: []*Turn{scriptTurn(1, rec(code))}, Dele: obj("launches", []any{})}
}

func TestScriptCandidates(t *testing.T) {
	ss := []*Session{candSess("a", jiraCode("X-1"), 200, "jira-api"), candSess("b", jiraCode("X-2"), 200),
		candSess("c", jiraCode("X-3"), 50), candSess("d", jobCode("/tmp/a", 1, 1), 200)}
	order, groups := scriptRecords(ss, func(*Session, float64) bool { return true })
	rows, once := scriptCandidates(order, groups)
	if rows[0].sessions != 3 || len(rows[0].samples) != 3 {
		t.Errorf("top %+v", rows[0])
	}
	if once != 0.25 {
		t.Errorf("one-off share %v", once)
	}
}

func usageTurn(cost float64) *Turn {
	return &Turn{Usage: Usage{Cost: cost, In: 1, Out: 1, Cr: 1, Cw: 1}}
}

func TestRepeatTypes(t *testing.T) {
	s1 := &Session{ID: "s1", Turns: []*Turn{usageTurn(1), usageTurn(2)}}
	s2 := &Session{ID: "s2", Turns: []*Turn{usageTurn(4)}}
	ka, kb, kc := repKey{"1", "pytest tests/a.py -k 12"}, repKey{"1", "pytest tests/b.py -k 7"}, repKey{"3", "/Users/me/x.py"}
	groups := map[repKey][]scriptRef{ka: {{nil, s1, 1}, {nil, s2, 1}}, kb: {{nil, s1, 1}}, kc: {{nil, s1, 2}}}
	rows := repeatTypes([]repKey{ka, kb, kc}, groups)
	by := map[string]repRow{}
	for _, r := range rows {
		by[r.label] = r
	}
	test := by["同一組測試重跑"]
	if test.calls != 3 || test.sessions != 2 || test.turns != 2 || test.cost != 5.0 {
		t.Errorf("tests row %+v", test)
	}
	if test.ex[0][0] != "pytest P -k N" || test.ex[0][1] != 3 {
		t.Errorf("example %v", test.ex[0])
	}
	if by["沒改過的檔案重讀"].ex[0][0] != "/Users/me/x.py" {
		t.Error("file example")
	}
	if rows[0].label != "同一組測試重跑" {
		t.Error("most expensive first")
	}
	if len(test.occ["pytest tests/a.py -k 12"]) != 2 {
		t.Error("occurrences")
	}
}

func TestBundles(t *testing.T) {
	testHome(t)
	code := jiraCode("X-1") + "token = 'ghp_" + strings.Repeat("a", 30) + "'\n# me@example.co.uk\n"
	mk := func(id string) *Session {
		r := rec(code)[0]
		r.Set("code", code) // unredacted, as an old cache entry
		return &Session{ID: id, Start: 200, Turns: []*Turn{scriptTurn(1, []ScriptRec{r})}, Dele: obj("launches", []any{})}
	}
	text := ScriptsBundle([]*Session{mk("aaaaaaaa1"), mk("bbbbbbbb2")}, []skillInfo{{"jira-api", "Jira REST usage", 100}})
	for _, part := range []string{"## 請你做的事", "## 欄位與算法", `| API\|Jira\|issue/*/comment`, "對話 aaaaaaaa",
		"該對話載入過的 skill：無", "jira-api | 1970-01-01"} {
		mustHave(t, text, part)
	}
	for _, gone := range []string{"ghp_" + strings.Repeat("a", 30), "@example", "skill 使用情況"} {
		mustNot(t, text, gone)
	}
	s1 := &Session{ID: "s1abcdef", Turns: []*Turn{usageTurn(1)}}
	s2 := &Session{ID: "s2abcdef", Turns: []*Turn{usageTurn(1)}}
	rep := func() *ojson.Object {
		return obj("turns", 1, "flagged", []any{1}, "share", 1.0, "reasons", obj("1", obj("1", "pytest tests/a.py -k 12")))
	}
	hp := obj("sessions", obj("s1abcdef", rep(), "s2abcdef", rep()), "loud", 0.15)
	text = RepeatsBundle([]*Session{s1, s2}, hp)
	mustHave(t, text, "| 同一組測試重跑 | 2 | 2 |")
	mustHave(t, text, "s1abcdef")
}

// ── per-turn API call list ─────────────────────────────────────────────────

func tcUse(name string, kv ...any) *ojson.Object { return obj("name", name, "input", obj(kv...)) }

func TestTurnCallsClassifier(t *testing.T) {
	if got := tcSegments(`grep -E "a|b" f.py | wc -l`); strings.Join(got, "§") != `grep -E "a|b" f.py§wc -l` {
		t.Errorf("quoted pipe %q", got)
	}
	if got := tcSegments("ls 2>&1 | head"); strings.Join(got, "§") != "ls 2>&1§head" {
		t.Errorf("redirect %q", got)
	}
	for _, c := range []struct {
		use  any
		want string
	}{
		{tcUse("Bash", "command", "cd /x && grep foo bar.py"), "讀寫檔案"},
		{tcUse("Bash", "command", "python3 run.py"), "執行指令"},
		{tcUse("Bash", "command", "git status"), "執行指令"},
		{tcUse("Bash", "command", "curl -s https://a.com/x"), "查外部資料"},
		{tcUse("Bash", "command", "curl -s http://127.0.0.1:8787/?session=x"), "執行指令"},
		{tcUse("Bash", "command", "curl -s localhost:3000/api"), "執行指令"},
		{tcUse("Bash", "command", "FOO=1 cat x"), "讀寫檔案"},
		{tcUse("Read", "file_path", "/a/b.py"), "讀寫檔案"},
		{tcUse("Edit", "file_path", "/a/b.py"), "讀寫檔案"},
		{tcUse("WebFetch", "url", "https://a.com"), "查外部資料"},
		{tcUse("mcp__x__y"), "查外部資料"},
		{tcUse("Agent", "subagent_type", "Explore"), "派 subagent"},
		{tcUse("Skill", "skill", "s"), "載入 skill／工具"},
		{tcUse("AskUserQuestion"), "問使用者"},
		{nil, "文字（無工具）"},
	} {
		if got := tcCategory(c.use); got != c.want {
			t.Errorf("%s: %q want %q", py.Dumps(c.use), got, c.want)
		}
	}
	for _, c := range []struct {
		use  any
		want string
	}{
		{tcUse("Read", "file_path", "/a/b.py"), "b.py"},
		{tcUse("WebFetch", "url", "https://a.com/x"), "a.com"},
		{tcUse("Bash", "command", "curl https://h.io/p?q=1"), "h.io"},
		{tcUse("Agent", "subagent_type", "Explore"), "Explore"},
	} {
		if got := tcTarget(c.use); got != c.want {
			t.Errorf("target %q want %q", got, c.want)
		}
	}
	mustNot(t, tcTarget(tcUse("Bash", "command", "echo sk-abcdefghijklmnopqrstuv")), "sk-abcdefghijklmnopqrstuv")
}

func tcAsst(mid string, blocks ...any) *ojson.Object {
	return obj("type", "assistant", "message", obj("id", mid, "model", "claude-opus-5",
		"usage", obj("input_tokens", 1, "output_tokens", 2, "cache_read_input_tokens", 3), "content", blocks))
}

func tcTool(name string, kv ...any) *ojson.Object {
	return obj("type", "tool_use", "id", "t", "name", name, "input", obj(kv...))
}

func TestTurnCallsList(t *testing.T) {
	recs := []any{
		tcAsst("m1", tcTool("Read", "file_path", "/a/dash.py")),
		tcAsst("m1", tcTool("Grep", "pattern", "x")), // same id, streamed in two records
		obj("type", "user", "toolUseResult", ojson.NewObject()),
		tcAsst("m2", tcTool("Bash", "command", "ls"), tcTool("Bash", "command", "pwd")),
		tcAsst("m3", obj("type", "text", "text", "done")),
	}
	calls := tcCallsOf(recs)
	var cats []string
	for _, c := range calls {
		cats = append(cats, c.cat)
	}
	if strings.Join(cats, ",") != "讀寫檔案,讀寫檔案,文字（無工具）" || len(calls[0].uses) != 2 {
		t.Errorf("calls %v", cats)
	}
	html := tcRender(calls, "回合", "MID", "<i>ROW #1 </i><i>ROW #2 </i><i>ROW #3 </i>")
	mustHave(t, html, `<details class="tc-list" open><summary>逐次清單（`)
	mustHave(t, html, "3 次 API 呼叫 · ")
	mustHave(t, html, "MID")
	if !(strings.Index(html, "#1 ") < strings.Index(html, "#2 ") && strings.Index(html, "#2 ") < strings.Index(html, "#3 ")) {
		t.Error("order")
	}
	mustHave(t, tcRender(tcCallsOf([]any{tcAsst("m1", obj("type", "text", "text", "hi"))}), "回合", "", ""), "只有 1 次 API 呼叫，直接回覆")
}

// ── the tools table on /pipeline ───────────────────────────────────────────

func TestToolsSection(t *testing.T) {
	desc := "do x"
	res := "ok"
	agent := &Use{Kind: "Agent", Input: `{"prompt": "SECRET-PROMPT"}`, Desc: &desc, Tid: "t", Turn: 1, HasTurn: true, N: 5, Result: &res, HasRes: true}
	out := toolsSection([]group{{"Agent", []*Use{agent}}}, &mainCtx{sid: "s", turn: 1, agents: map[string][2]string{}}, "", true, nil)
	mustHave(t, out, "找不到這個 subagent 的 transcript")
	mustNot(t, out, "SECRET-PROMPT")
	bash := &Use{Kind: "Bash", Input: "echo one\necho two", Tid: "t", Turn: 1, HasTurn: true, N: 5}
	out = toolsSection([]group{{"Bash", []*Use{bash}}}, &mainCtx{sid: "s", turn: 1, agents: map[string][2]string{}}, "", true, nil)
	if strings.Count(out, "echo one") != 2 {
		t.Error("summary line + folded full command")
	}
	mustHave(t, out, "<details><summary>完整指令")
}

func TestCredentialsMasked(t *testing.T) {
	for _, text := range []string{"key K7Q2XW9PLM", "sk-abcdefghijklmnopqrstuv", "ghp_" + strings.Repeat("a", 25), "token=abc123",
		"Authorization: Bearer xyz123456789"} {
		got := Redact(text)
		for _, leak := range []string{"K7Q2XW9PLM", "abcdefghijklmnop", "xyz123", "abc123", strings.Repeat("a", 25)} {
			mustNot(t, got, leak)
		}
	}
	if Redact("README.md and token count") != "README.md and token count" {
		t.Error("plain text untouched")
	}
}

func TestCategoryNamesAgree(t *testing.T) {
	labels := []string{"系統提示", "系統工具", "延後載入的工具", "記憶檔"} // the /context category names
	for i, cat := range []string{"system", "tools", "deferred", "claudemd"} {
		if !strings.HasPrefix(cats[cat][0], labels[i]) {
			t.Errorf("%s: %q vs %q", cat, cats[cat][0], labels[i])
		}
	}
	found := false
	for _, b := range ctxRanges {
		found = found || b.lo == ctxRatio
	}
	if !found {
		t.Error("context ranges split at the 40% line")
	}
}

// ── delete button, model cache ─────────────────────────────────────────────

func TestForget(t *testing.T) {
	h := testHome(t)
	if _, err := forget("", false); err == nil {
		t.Error("needs an id")
	}
	path := filepath.Join(h, ".claude", "session_types.json")
	writeRows(t, path, obj("abcd1234", 1, "abcd1234:3", 2, "zzzz9999", 3))
	out, err := forget("abcd1234-rest", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := py.Dumps(ojson.Field(ojson.Field(out, "removed"), "session_types.json")); got != `["abcd1234", "abcd1234:3"]` {
		t.Errorf("removed %s", got)
	}
	raw, _ := os.ReadFile(path)
	if v, _ := ojson.Parse(raw); ojson.Obj(v).Len() != 3 {
		t.Error("dry run touched the file")
	}
}

func TestModelCacheLivesOutsidePluginDir(t *testing.T) {
	h := testHome(t)
	if !strings.HasPrefix(tmState(), filepath.Join(h, ".claude", "agent-perform")) {
		t.Errorf("state dir %s", tmState())
	}
	built := 0
	build := func(int) *ojson.Object { built++; return obj("n", 5) }
	if got := py.Dumps(onDisk("delegate_model.json", "shape", build, false)); got != `{"n": 5}` || built != 1 {
		t.Errorf("first build %s", got)
	}
	if got := py.Dumps(onDisk("delegate_model.json", "shape", build, false)); got != `{"n": 5}` || built != 1 {
		t.Errorf("read back without building: %s (%d builds)", got, built)
	}
}

func TestTurnSummaryWithSubagents(t *testing.T) {
	testHome(t)
	now := float64(time.Now().Unix()) - 600
	path := writeRows(t, filepath.Join(projects(), "-x", "cs.jsonl"), userRec(now, "go"), assistantRec(now+5, "m1", 100, 100, ""))
	sub := &Sub{Scan: ojson.NewObject(), Desc: "Design skill", start: now + 10}
	sub.Scan.Set("cost", 4.0).Set("secs", 600.0)
	out := turnSummary(one(path, 0).Turns[0], []*tcCall{{usd: 1}}, nil, []*Sub{sub}, "本回合")
	for _, want := range []string{"$5.00", "主對話 $1.00", "subagent $4.00 · 1 個", "Design skill", "委派", "最貴的 2 筆（呼叫或委派），佔本回合 100%", clockText(610)} {
		mustHave(t, out, want)
	}
}

func TestTurnSpanAndCostIncludeSubagents(t *testing.T) {
	tn := &Turn{At: 100, E2E: 165, DCost: 16.93, DEnd: endAfter(0, 110, 2159)}
	tn.Usage.Cost = 0.65
	if got := turnSpan(tn); got != 2169 {
		t.Fatalf("turnSpan = %v, want 2169", got)
	}
	if got := turnCost(tn); math.Abs(got-17.58) > 1e-9 {
		t.Fatalf("turnCost = %v, want 17.58", got)
	}
	if got := turnSpan(&Turn{At: 100, E2E: 165}); got != 165 {
		t.Fatalf("turnSpan without subagents = %v, want 165", got)
	}
}

func TestGrowthPane(t *testing.T) {
	if growthPane([]*Call{{Ctx: 10}}, nil) != "" {
		t.Error("one call draws no curve")
	}
	out := growthPane([]*Call{{Ctx: 10}, {Ctx: 20}}, []int{2})
	mustHave(t, out, "Context 成長曲線")
	mustHave(t, out, "灰色細虛線：壓縮")
	if got := inTurnCalls([]*Call{{InTurn: true}, {}}); len(got) != 1 {
		t.Error("inTurnCalls", len(got))
	}
}

func TestSortableLists(t *testing.T) {
	tc := tcCallsOf([]any{
		tcAsst("m1", tcTool("Read", "file_path", "/a/x.go")),
		tcAsst("m2", tcTool("Bash", "command", "ls"), tcTool("Bash", "command", "pwd")),
	})
	calls := []*Call{{Pos: 5, Ctx: 100, InTurn: true}, {Pos: 9, Ctx: 200, InTurn: true}}
	items := []*Item{{Pos: 1, N: 7, Cat: "tool_use", Src: "Read"}, {Pos: 6, N: 3, Cat: "tool_use", Src: "Bash"}}
	html := apiSection(items, calls, 1, true, tc)
	for _, w := range []string{`data-usd="`, `data-add="7"`, `data-add="3"`, `data-n="2"`, `data-s="n"`, `class="pp-big tc-row" data-k="讀寫檔案"`, "×2 並行"} {
		mustHave(t, html, w)
	}
	g := toolsSection([]group{{"Bash", []*Use{{Kind: "Bash", N: 4, Resend: 2, Usd: 0.5}}}}, nil, "", false, nil)
	for _, w := range []string{`data-usd="0.500000"`, `data-tok="4"`, `data-cnt="1"`, `data-s="cnt"`} {
		mustHave(t, g, w)
	}
}

// A background subagent's task-notification stays inside the turn that launched it.
func TestTaskNotificationKeepsTurn(t *testing.T) {
	testHome(t)
	launch := func(mid, id string) *ojson.Object {
		return obj("type", "assistant", "message", obj("id", mid, "role", "assistant", "content", []any{
			obj("type", "tool_use", "id", id, "name", "Agent", "input", obj("description", id))}))
	}
	path := writeRows(t, filepath.Join(t.TempDir(), "n.jsonl"),
		obj("type", "user", "message", obj("role", "user", "content", "scan it")),
		launch("m1", "t1"),
		obj("type", "user", "origin", obj("kind", "task-notification"), "message", obj("role", "user",
			"content", "<task-notification>\n<task-id>a1</task-id>\n<tool-use-id>t1</tool-use-id>\n</task-notification>")),
		launch("m2", "t2"))
	_, prompts, _, meta := itemsFor(path, nil, 1, false)
	if len(prompts) != 1 {
		t.Errorf("prompts %d, want 1", len(prompts))
	}
	if !meta.ToolIDs["t1"] || !meta.ToolIDs["t2"] {
		t.Errorf("tool ids %v, want t1 and t2", meta.ToolIDs)
	}
}

// The turn page's data tables put their header in <thead>, which is what the page's click-to-sort attaches to.
func TestPipelineTablesSortable(t *testing.T) {
	testHome(t)
	cat := catSection([]*Item{{Cat: "tool_result", Src: "Bash", N: 10}}, 10, "", "", 1, true)
	sub := subagentTable("s", []*Sub{{Scan: obj("secs", 67, "peak", 5), ID: "a1", Desc: "d"}})
	for name, html := range map[string]string{"category": cat, "subagent": sub} {
		if !strings.Contains(html, `<table class="pp-t"><thead><tr>`) || !strings.Contains(html, `</thead><tbody>`) {
			t.Errorf("%s table has no thead/tbody", name)
		}
	}
}

// The sort key of every number format the dashboard renders.
func TestSortValues(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	i, j := strings.Index(pageHTML, "function val(td)"), strings.Index(pageHTML, "// A sort you picked")
	if i < 0 || j < i {
		t.Fatal("sort parser not found in the page")
	}
	js := pageHTML[i:j] + `
function td(t,num){return {textContent:t,dataset:{},classList:{contains:function(c){return num&&c==="num";}}};}
var cases=[["1:07",false,67],["10:05",false,605],["18:48:12",false,67692],["\u00d710.5",false,10.5],
  ["39m15s\u21b3 sub 3m53s",true,2355],["26 / 54",true,26],["8 \u500d",true,8],["$2.01 +4%",true,2.01],
  ["$1,234",false,1234],["1.2M",false,1200000],["7c3e9b1a",true,"7c3e9b1a"],["58204613 \u63a5\u4e0b\u4f86",false,"58204613 \u63a5\u4e0b\u4f86"]];
cases.forEach(function(c){var v=val(td(c[0],c[1]));if(v!==c[2])console.log("val("+JSON.stringify(c[0])+") = "+v+", want "+c[2]);});
`
	out, err := exec.Command(node, "-e", js).CombinedOutput()
	if err != nil || len(out) > 0 {
		t.Errorf("%v\n%s", err, out)
	}
}

// A sort saved on the session page must not reorder /pipeline's tables, which also carry ?session=.
func TestSortKeyPerPage(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	i := strings.Index(pageHTML, "function pageKind(){")
	j := strings.Index(pageHTML[i:], "\n}\n")
	if i < 0 || j < 0 {
		t.Fatal("pageKind not found in the page")
	}
	js := pageHTML[i:i+j+3] + `
var cases=[["/","?session=83920175","session"],["/","?day=2026-10-29","day"],["/verify","","/verify"],
  ["/pipeline","?session=83920175&at=1","/pipeline"],["/pipeline","?session=83920175&agent=a1","/pipeline:agent"]];
cases.forEach(function(c){global.location={pathname:c[0],search:c[1]};var k=pageKind();if(k!==c[2])console.log(c[0]+c[1]+" -> "+k+", want "+c[2]);});
`
	out, err := exec.Command(node, "-e", js).CombinedOutput()
	if err != nil || len(out) > 0 {
		t.Errorf("%v\n%s", err, out)
	}
}

package dash

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentperform/internal/ojson"
)

// richHome writes recent sessions: tool calls, a Bash script, a skill load and
// an Agent launch with its subagent transcript. It returns the session id.
func richHome(t *testing.T) string {
	t.Helper()
	testHome(t)
	now := float64(time.Now().Unix()) - 7200
	asst := func(ts float64, mid string, cw, cr int64, blocks ...any) *ojson.Object {
		r := assistantRec(ts, mid, cw, cr, "")
		ojson.Obj(ojson.Field(r, "message")).Set("content", blocks)
		return r
	}
	res := func(ts float64, id, content string) *ojson.Object {
		return obj("type", "user", "timestamp", iso(ts), "toolUseResult", ojson.NewObject(), "message", obj("role", "user",
			"content", []any{obj("type", "tool_result", "tool_use_id", id, "content", content)}))
	}
	code := "python3 - <<EOF\nprint(1)\nEOF"
	rows := []any{
		userRec(now, "first prompt"),
		asst(now+5, "m1", 30_000, 0, obj("type", "tool_use", "id", "b1", "name", "Bash", "input", obj("command", code))),
		res(now+8, "b1", "1"),
		asst(now+9, "m1b", 500, 30_000, obj("type", "tool_use", "id", "sk1", "name", "Skill", "input", obj("skill", "good"))),
		res(now+9.5, "sk1", "loaded"),
		asst(now+10, "m2", 1_000, 30_000, obj("type", "tool_use", "id", "r1", "name", "Read", "input", obj("file_path", "/tmp/x.txt")),
			obj("type", "tool_use", "id", "ag1", "name", "Agent", "input", obj("description", "dig", "subagent_type", "x", "prompt", "do it"))),
		res(now+12, "r1", "file body"),
		res(now+20, "ag1", "agentId: abc123"),
		userRec(now+400, "second prompt"),
		asst(now+405, "m3", 2_000, 31_000, obj("type", "text", "text", "done")),
	}
	for _, id := range []string{"sess1234-0000", "sess5678-0000"} {
		writeRows(t, filepath.Join(projects(), "-x", id+".jsonl"), rows...)
	}
	subUse := func(ts float64, mid, id, name string, in *ojson.Object) *ojson.Object {
		r := assistantRec(ts, mid, 500, 5_000, "")
		ojson.Obj(ojson.Field(r, "message")).Set("content", []any{obj("type", "tool_use", "id", id, "name", name, "input", in)})
		return r
	}
	subRes := func(ts float64, id, content string, isErr bool) *ojson.Object {
		return obj("type", "user", "timestamp", iso(ts), "message", obj("role", "user",
			"content", []any{obj("type", "tool_result", "tool_use_id", id, "content", content, "is_error", isErr)}))
	}
	writeRows(t, filepath.Join(projects(), "-x", "sess1234-0000", "subagents", "agent-abc123.jsonl"),
		userRec(now+13, "do it"), assistantRec(now+14, "a0", 5_000, 0, ""),
		subUse(now+15, "a1", "s1", "Read", obj("file_path", "/tmp/x.txt")), subRes(now+15.5, "s1", "file body", false),
		subUse(now+16, "a2", "s2", "Bash", obj("command", code)), subRes(now+16.5, "s2", "boom", true),
		subUse(now+17, "a3", "s3", "Write", obj("file_path", "/tmp/y.txt", "content", "x")), subRes(now+17.5, "s3", "ok", false),
		obj("type", "system", "subtype", "compact_boundary", "timestamp", iso(now+18)),
		assistantRec(now+19, "a4", 800, 1_000, ""))
	writeRows(t, filepath.Join(projects(), "-x", "sess1234-0000", "subagents", "agent-abc123.meta.json"),
		obj("toolUseId", "ag1", "description", "dig", "agentType", "x"))
	return "sess1234"
}

func TestPageRoutes(t *testing.T) {
	sid := richHome(t)
	for _, c := range []struct{ path, query, want string }{
		{"/", "", "<h1"},
		{"/", "days=30", "<h1"},
		{"/", "session=" + sid, "second prompt"},
		{"/", "day=" + time.Now().Add(-7200*time.Second).Format("2006-01-02"), "sess1234"},
		{"/how", "", "Cache"},
		{"/optimize", "", "優化"},
		{"/expensive", "", "$"},
		{"/context", "", "<h1"},
		{"/tools", "", "Read"},
		{"/delegate", "", "<h1"},
		{"/model", "", "<h1"},
		{"/verify", "", "<h1"},
		{"/verify", "since=2020-01-01", "<h1"},
		{"/tools", "focus=cost", "Read"},
		{"/pipeline", "session=" + sid, "Context"},
		{"/pipeline", "session=" + sid + "&turn=1", "Context"},
		{"/pipeline", "session=" + sid + "&turn=2&agent=abc123", "Context"},
		{"/pipeline", "session=" + sid + "&turn=nan", "ValueError"},
		{"/pipeline", "session=" + sid + "&turn=inf", "OverflowError"},
		{"/levers/bundle", "section=scripts", ""},
	} {
		st, _, body := Render(c.path, c.query)
		if st != 200 && st != 404 {
			t.Errorf("%s?%s: status %d", c.path, c.query, st)
			continue
		}
		if st == 200 && !strings.Contains(string(body), c.want) {
			t.Errorf("%s?%s: missing %q", c.path, c.query, c.want)
		}
	}
}

func TestSubagentPage(t *testing.T) {
	sid := richHome(t)
	st, _, body := Render("/pipeline", "session="+sid+"&agent=abc123")
	if st != 200 || strings.Contains(string(body), "找不到這個 subagent") {
		t.Fatalf("subagent page: %d", st)
	}
	_, _, body = Render("/pipeline", "session="+sid+"&agent=nope")
	mustHave(t, string(body), "找不到這個 subagent 的 transcript")
	_, _, body = Render("/pipeline", "session=zzzz")
	mustHave(t, string(body), "找不到這個對話的 transcript")
}

// TestPipelineToolFocus covers the Mod's per-call links: /pipeline?session=…&tool=<id>[&agent=…].
func TestPipelineToolFocus(t *testing.T) {
	sid := richHome(t)
	now := float64(time.Now().Unix()) - 7200
	sub := filepath.Join(projects(), "-x", "sess1234-0000", "subagents")
	use := assistantRec(now+30, "d1", 500, 5_000, "ephemeral_5m_input_tokens")
	ojson.Obj(ojson.Field(use, "message")).Set("content", []any{obj("type", "tool_use", "id", "t1", "name", "Read", "input", obj("file_path", "/tmp/z.txt"))})
	writeRows(t, filepath.Join(sub, "agent-def456.jsonl"), userRec(now+29, "look"), use,
		obj("type", "user", "timestamp", iso(now+31), "toolUseResult", ojson.NewObject(), "message", obj("role", "user", "content", []any{obj("type", "tool_result", "tool_use_id", "t1", "content", "zzz")})),
		assistantRec(now+32, "d2", 100, 5_500, "ephemeral_5m_input_tokens"), assistantRec(now+33, "d3", 100, 5_600, ""))
	writeRows(t, filepath.Join(sub, "agent-def456.meta.json"), obj("toolUseId", "ag9", "description", "peek", "agentType", "x"))
	for _, c := range []struct {
		name, query string
		want, not   []string
	}{
		{"turn from the call", "&tool=r1",
			[]string{"第 1 個回合", `id="tool-r1" data-hit open`, `id="tool-b1"`, `id="ct-cnt" checked`, `getElementById("tool-r1")`, "累計花費（估）", "（1h）", "不計 Cache 過期後的重寫"},
			[]string{`id="ct-src" checked`, "找不到這次工具呼叫"}},
		{"explicit turn wins", "&turn=2&tool=r1", []string{"第 2 個回合", `id="tool-r1" data-hit open`}, nil},
		{"subagent call", "&agent=def456&tool=t1",
			[]string{"subagent：peek", `id="tool-t1" data-hit open`, `id="ct-cnt" checked`, "（5m）", "之後 1 次讀取"}, nil},
		{"subagent call compacted away", "&agent=abc123&tool=s1",
			[]string{"subagent：dig", `id="ct-src" checked`, "已經不在這個 subagent 的 context 裡", "之後 1 次讀取", "到下次壓縮為止"}, []string{"data-hit", "找不到這次工具呼叫"}},
		{"compacted before any call sent it", "&agent=abc123&tool=s3", []string{"送出前就被壓縮掉了"}, []string{"data-hit"}},
		{"unknown id", "&tool=nope", []string{"第 2 個回合", `id="ct-src" checked`, "找不到這次工具呼叫"}, []string{"data-hit"}},
		{"malformed id", "&tool=%3Cb%3E", []string{"第 2 個回合", `id="ct-src" checked`}, []string{"data-hit", "找不到這次工具呼叫", `id="tool-<`}},
		{"no tool", "", []string{`id="tool-r1"`, `id="ct-src" checked`}, []string{"data-hit", `getElementById("tool-`}},
	} {
		st, _, b := Render("/pipeline", "session="+sid+c.query)
		body := string(b)
		if st != 200 {
			t.Errorf("%s: status %d", c.name, st)
		}
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", c.name, w)
			}
		}
		for _, w := range c.not {
			if strings.Contains(body, w) {
				t.Errorf("%s: unexpected %q", c.name, w)
			}
		}
	}
}

func do(h http.Handler, method, target string, hdr map[string]string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Host = "127.0.0.1:8787"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHandlerRoutes(t *testing.T) {
	sid := richHome(t)
	h := NewHandler(8787)
	for _, c := range []struct {
		target string
		code   int
		loc    string
	}{{"/glossary", 302, "/how#terms"}, {"/skills", 302, "/optimize"}, {"/patterns", 404, ""}, {"/levers", 404, ""}, {"/health", 200, ""}} {
		w := do(h, "GET", c.target, nil, "")
		if w.Code != c.code || w.Header().Get("Location") != c.loc {
			t.Errorf("%s: %d %q", c.target, w.Code, w.Header().Get("Location"))
		}
	}
	w := do(h, "GET", "/vendor/echarts.min.js", map[string]string{"Accept-Encoding": "gzip"}, "")
	if w.Code != 200 || w.Header().Get("Content-Encoding") != "gzip" || w.Header().Get("Cache-Control") != "max-age=86400" {
		t.Errorf("echarts: %d %v", w.Code, w.Header())
	}
	// the second request is answered from the page cache
	first := do(h, "GET", "/how", nil, "").Body.String()
	if second := do(h, "GET", "/how", nil, "").Body.String(); first != second || !strings.Contains(first, "Cache") {
		t.Error("cached page differs")
	}
	if w := do(h, "GET", "/pipeline?session="+sid, nil, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Context") {
		t.Errorf("pipeline %d", w.Code)
	}
	if w := do(h, "PUT", "/", nil, ""); w.Code != 501 || !strings.Contains(w.Body.String(), "Unsupported method ('PUT')") {
		t.Errorf("PUT: %d", w.Code)
	}
}

func TestHandlerRange(t *testing.T) {
	richHome(t)
	day := time.Now().Format("2006-01-02")
	h := NewHandler(8787)
	do(h, "GET", "/?from="+day+"&to="+day, nil, "")
	if f, to, ok := rememberedRange(); !ok || f != day || to != day {
		t.Errorf("range not remembered: %v %v %v", f, to, ok)
	}
	if w := do(h, "GET", "/", nil, ""); w.Code != 200 {
		t.Errorf("remembered range page: %d", w.Code)
	}
}

func TestTypeReply(t *testing.T) {
	sid := richHome(t)
	h := NewHandler(8787)
	path := Transcript(sid)
	w := do(h, "GET", "/type?session="+sid+"&path="+path, nil, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"session": "`+sid+`"`) {
		t.Errorf("/type: %d %s", w.Code, w.Body)
	}
	// a path outside the projects folder is ignored
	w = do(h, "GET", "/type?session="+sid+"&path=/etc/hosts", nil, "")
	if strings.Contains(w.Body.String(), `"row"`) {
		t.Errorf("foreign path returned a row: %s", w.Body)
	}
}

func TestForgetPost(t *testing.T) {
	richHome(t)
	os.MkdirAll(filepath.Join(home(), ".Trash"), 0o755) // forget moves into an existing Trash
	h := NewHandler(8787)
	origin := map[string]string{"Origin": "http://127.0.0.1:8787"}
	if w := do(h, "POST", "/forget", map[string]string{"Origin": "http://evil.example"}, "{}"); w.Code != 403 {
		t.Errorf("foreign origin: %d", w.Code)
	}
	if w := do(h, "POST", "/other", origin, "{}"); w.Code != 404 {
		t.Errorf("other path: %d", w.Code)
	}
	w := do(h, "POST", "/forget", origin, "{}")
	if !strings.Contains(w.Body.String(), `"ok": false`) || !strings.Contains(w.Body.String(), "要給對話 id") {
		t.Errorf("no session: %s", w.Body)
	}
	if w := do(h, "POST", "/forget", origin, "not json"); !strings.Contains(w.Body.String(), `"ok": false`) {
		t.Errorf("bad json: %s", w.Body)
	}
	hdr := map[string]string{"Origin": "http://127.0.0.1:8787", "Content-Length": "x"}
	if w := do(h, "POST", "/forget", hdr, "{}"); !strings.Contains(w.Body.String(), "invalid literal for int()") {
		t.Errorf("bad length: %s", w.Body)
	}
	body := `{"session": "sess1234"}`
	w = do(h, "POST", "/forget", map[string]string{"Origin": "http://localhost:8787", "Content-Length": strconv.Itoa(len(body))}, body)
	if !strings.Contains(w.Body.String(), `"ok": true`) || !strings.Contains(w.Body.String(), `"session": "sess1234"`) {
		t.Fatalf("forget: %s", w.Body)
	}
	if got := Transcript("sess1234"); got != "" {
		t.Errorf("transcript still on the board: %s", got)
	}
	if _, err := os.Stat(filepath.Join(home(), ".Trash", "sess1234-0000.jsonl")); err != nil {
		t.Errorf("transcript not in Trash: %v", err)
	}
}

// weekHome writes one session per day for 8 days: a repeated API script, a
// repeated Read, a cache-expiry gap and a growing context, plus the correction
// labels an install from before 2.0 left behind.
func weekHome(t *testing.T) {
	t.Helper()
	testHome(t)
	base := float64(time.Now().Unix()) - 12_000
	var labels []any
	for d := 0; d < 8; d++ {
		t0 := base - float64(d)*86400
		tool := func(ts float64, mid, id, name string, in *ojson.Object, cw, cr int64) []any {
			a := assistantRec(ts, mid, cw, cr, "")
			ojson.Obj(ojson.Field(a, "message")).Set("content", []any{obj("type", "tool_use", "id", id, "name", name, "input", in)})
			r := obj("type", "user", "timestamp", iso(ts+1), "toolUseResult", ojson.NewObject(), "message", obj("role", "user",
				"content", []any{obj("type", "tool_result", "tool_use_id", id, "content", "result "+id)}))
			return []any{a, r}
		}
		code := jiraCode("ABC-" + strconv.Itoa(100+d))
		rows := []any{userRec(t0, "implement the feature number "+strconv.Itoa(d))}
		rows = append(rows, tool(t0+5, "m1", "b1", "Bash", obj("command", "python3 - <<EOF\n"+code+"EOF"), 40_000, 0)...)
		rows = append(rows, tool(t0+20, "m2", "r1", "Read", obj("file_path", "/tmp/same.txt"), 1_000, 40_000)...)
		rows = append(rows, tool(t0+30, "m3", "r2", "Read", obj("file_path", "/tmp/same.txt"), 1_000, 41_000)...)
		rows = append(rows, userRec(t0+60, "不對，改成另一種做法"), assistantRec(t0+65, "m4", 2_000, 42_000, ""))
		rows = append(rows, userRec(t0+7200, "continue after a long break"), assistantRec(t0+7205, "m5", 90_000, 0, ""))
		rows = append(rows, userRec(t0+9000, "a slow thing"), assistantRec(t0+9005, "m6", 1_000, 90_000, ""),
			userRec(t0+9200, "[Request interrupted by user]"))
		sid := "week" + strconv.Itoa(d) + "abc"
		writeRows(t, filepath.Join(projects(), "-x", sid+"-0000.jsonl"), rows...)
		for _, j := range []struct {
			at   float64
			corr bool
		}{{t0, false}, {t0 + 60, true}, {t0 + 7200, false}} {
			labels = append(labels, obj("key", sid+":"+strconv.Itoa(int(j.at)), "correction", j.corr, "conf", 0.9))
		}
	}
	writeRows(t, claudeFile("auto_corrections.jsonl"), labels...)
}

func TestWeekPages(t *testing.T) {
	weekHome(t)
	since := time.Now().AddDate(0, 0, -3).Format("2006-01-02")
	for _, page := range []string{"/", "/context", "/tools", "/delegate", "/model", "/verify", "/expensive", "/optimize", "/how"} {
		for _, q := range []string{"days=14", "days=14&since=" + since, "days=14&focus=repeat", "days=14&focus=cachebreak", "days=14&focus=killed", "days=14&focus=fixed", "days=14&focus=delegate"} {
			st, _, body := Render(page, q)
			if st != 200 || !strings.Contains(string(body), "<h1") {
				t.Errorf("%s?%s: %d", page, q, st)
			}
		}
	}
	for _, b := range []string{"scripts", "repeats"} {
		st, _, body := Render("/levers/bundle", "section="+b+"&days=14&since="+time.Now().AddDate(0, 0, -3).Format("2006-01-02"))
		if st != 200 || len(body) == 0 {
			t.Errorf("bundle %s: %d", b, st)
		}
	}
	_, _, body := Render("/levers/bundle", "section=scripts&days=14")
	mustHave(t, string(body), "Jira")
	_, _, body = Render("/", "days=14")
	mustNot(t, string(body), "更正")
	if st, _, _ := Render("/levers/bundle", "section=nope"); st != 404 {
		t.Errorf("unknown bundle section: %d", st)
	}
}

func TestWeekSessionPages(t *testing.T) {
	weekHome(t)
	day := time.Now().Add(-12_000 * time.Second).Format("2006-01-02")
	for _, c := range []struct{ path, query, want string }{
		{"/", "session=week0abc", "week0a"},
		{"/", "session=week0abc&focus=repeat", "week0a"},
		{"/", "session=week0abc&focus=killed", "week0a"},
		{"/", "session=nosuchid", "找不到"},
		{"/", "day=" + day, "week0a"},
		{"/", "day=2020-01-01", "<h1"},
		{"/pipeline", "session=week0abc&turn=1", "Context"},
		{"/pipeline", "session=week0abc&turn=2", "Context"},
		{"/pipeline", "session=week0abc&turn=3&focus=cachebreak", "Context"},
		{"/pipeline", "session=week0abc&turn=4&focus=killed", "Context"},
		{"/pipeline", "session=week0abc&turn=99", "Context"},
		{"/pipeline", "session=week0abc&turn=-1", "Context"},
		{"/pipeline", "session=week0abc&at=" + strconv.Itoa(int(time.Now().Unix())-12_000), "Context"},
		{"/pipeline", "", "Context"},
	} {
		st, _, body := Render(c.path, c.query)
		if st != 200 || !strings.Contains(string(body), c.want) {
			t.Errorf("%s?%s: %d, missing %q", c.path, c.query, st, c.want)
		}
	}
}

func TestTurnContext(t *testing.T) {
	weekHome(t)
	path := Transcript("week0abc")
	if turnContext(filepath.Join(t.TempDir(), "gone.jsonl"), 1, CtxProbeCats, ctx1M) != nil {
		t.Error("a missing transcript has no context breakdown")
	}
	got := turnContext(path, 2, CtxProbeCats, ctx1M)
	if got == nil || len(got.Cats) == 0 {
		t.Fatalf("turnContext = %+v", got)
	}
	if again := turnContext(path, 2, CtxProbeCats, ctx1M); again != got {
		t.Error("second call missed the cache")
	}
}

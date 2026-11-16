package dash

import (
	"crypto/md5"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"agentperform/internal/common"
	"agentperform/internal/ojson"
	"agentperform/internal/prices"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
	"agentperform/internal/redact"
	"agentperform/internal/scrub"
	"agentperform/internal/transcript"
)

// subagents.py: pair each delegation with the subagent transcript it launched.

const (
	scriptCallsMax = 50 // records kept per turn or subagent, so cached values stay small
	fpMin          = 40 // shorter normalised code is a bare "python3 /tmp/x.py" run, not a job
	scriptKindElse = "其他"
)

var (
	scriptKinds = []struct {
		label string
		re    *pyre.Re
	}{
		{"讀檔改內容再寫回（改檔案）", pyre.Compile(`sed -i|(read\(|read_text).*(\.replace\(|re\.sub).*(write\(|write_text)`, pyre.S)},
		{"跑測試或驗證", pyre.Compile(`unittest|pytest|selftest|\bassert\b`)},
		{"計時或效能分析", pyre.Compile(`perf_counter|cProfile|timeit|pstats`)},
		{"呼叫 HTTP / API", pyre.Compile(`urllib|requests\.|http\.client|\bcurl\b|fetch\(`)},
		{"讀 transcript / 對話紀錄", pyre.Compile(`\.jsonl|/projects/|transcript`)},
		{"讀 JSON 做統計", pyre.Compile(`json\.loads?\(|JSON\.parse`)},
		{"擷取 HTML / 文字", pyre.Compile(`html\.unescape|BeautifulSoup|re\.(sub|findall|search|finditer)\(`)},
		{"圖片 / 截圖", pyre.Compile(`\bPIL\b|Image\.|screencap`)},
	}
	boringLine = pyre.Compile(`^(import |from \S+ import |cd \S+\s*$|cd \S+\s*(&&|;)\s*$|#!|"""|'''|EOF$|set -|source ~)`)
	fpPath     = pyre.Compile(`(?<![\w.])/[\w.\-~/]+`)
	fpID       = pyre.Compile(`\b[0-9a-fA-F]{8,}\b`)
	fpNum      = pyre.Compile(`\b\d+(?:\.\d+)?\b`)
	agentIDHex = pyre.Compile(`agentId:\s*([a-f0-9]+)`)
)

func scriptKind(code string) string {
	for _, k := range scriptKinds {
		if k.re.Has(code) {
			return k.label
		}
	}
	return scriptKindElse
}

func meaningful(code string, dropHeredoc bool) []string {
	var lines []string
	for _, l := range py.Splitlines(code) {
		s := py.Strip(l)
		if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "//") {
			continue
		}
		if dropHeredoc && strings.Contains(l, "<<") {
			continue
		}
		lines = append(lines, s)
	}
	return lines
}

// scriptFP is (fingerprint, normalised length) of a script.
// quotedToS is re.sub(r'"[^"]*"|\'[^\']*\”, "S", t): each closed quoted run becomes S.
func quotedToS(t string) string {
	var b strings.Builder
	for i := 0; i < len(t); {
		if q := t[i]; q == '"' || q == '\'' {
			if j := strings.IndexByte(t[i+1:], q); j >= 0 {
				b.WriteByte('S')
				i += j + 2
				continue
			}
		}
		b.WriteByte(t[i])
		i++
	}
	return b.String()
}

func scriptFP(code string) (string, int) {
	t := py.CollapseSpace(strings.Join(meaningful(code, false), " "))
	t = quotedToS(t)
	t = fpPath.Sub(t, "P")
	t = fpID.Sub(t, "I")
	t = fpNum.Sub(t, "N")
	return fmt.Sprintf("%x", md5.Sum([]byte(t)))[:12], py.Len(t)
}

// ScriptRec is one throwaway-script record, as an ordered object (fp, kind,
// shape, lang, line, code, tok, out) so it serialises like the Python dict.
type ScriptRec = *ojson.Object

// takeScript records one adhoc_script result; byID lets takeResult add the
// call's output size later.
func takeScript(calls *[]ScriptRec, byID map[string]ScriptRec, callID string, lang, code string, ok bool) {
	if !ok || len(*calls) >= scriptCallsMax {
		return
	}
	fp, n := scriptFP(code)
	if n < fpMin {
		return
	}
	lines := meaningful(code, true)
	line := ""
	if len(lines) > 0 {
		line = lines[0]
	}
	for _, l := range lines {
		if boringLine.MatchStart(l) == nil {
			line = l
			break
		}
	}
	rec := ojson.NewObject().Set("fp", fp).Set("kind", scriptKind(code)).Set("shape", scriptShape(code, lang)).
		Set("lang", lang).Set("line", py.Head(redact.Redact(line), 80)).Set("code", py.Head(redact.Redact(code), 1500)).
		Set("tok", int64(py.Len(code)/4)).Set("out", int64(0))
	*calls = append(*calls, rec)
	byID[callID] = rec
}

// takeResult adds a tool_result's size to the script call it answers.
func takeResult(byID map[string]ScriptRec, part any) {
	rec := byID[idStr(ojson.Field(part, "tool_use_id"))]
	if rec == nil {
		return
	}
	body := ojson.Field(part, "content")
	text, isStr := body.(string)
	if !isStr {
		text = py.Dumps(body)
	}
	out := num(ojson.Field(rec, "out"))
	rec.Set("out", out+int64(py.Len(text)/4))
}

func round4(v float64) float64 { return py.Round(v, 4) }

// scanAgent is the cost and shape of one subagent run.
func scanAgent(path string) *ojson.Object {
	var cost, miss float64
	var tin, tout, tcr, tcw int64
	calls := 0
	tools := py.NewCounter()
	models := newFloatCounter()
	seen := map[string]bool{}
	var first, last *float64
	var peak int64
	var firstCtx *int64
	written := newSet()
	scripts := map[string]bool{}
	scriptN := 0
	var scriptCalls []ScriptRec
	byID := map[string]ScriptRec{}
	skills := newSet() // skills it called; counted in skill usage, its tokens stay unattributed
	for e := range transcript.Entries(path) {
		msg := transcript.Msg(e)
		usage := ojson.Field(msg, "usage")
		if role, _ := ojson.Str(ojson.Field(msg, "role")); ojson.Truthy(usage) && role == "assistant" {
			mid := ojson.GetOr(msg, "id")
			if mid == nil {
				mid = ojson.Field(e, "uuid")
			}
			if !ojson.Truthy(mid) || !seen[py.Repr(mid)] {
				if ojson.Truthy(mid) {
					seen[py.Repr(mid)] = true
				}
				i, o := getNum(usage, "input_tokens"), getNum(usage, "output_tokens")
				cr, cw := getNum(usage, "cache_read_input_tokens"), getNum(usage, "cache_creation_input_tokens")
				model := ""
				if v := ojson.Field(msg, "model"); v != nil {
					model = py.Str(v)
				}
				step := prices.Price(model, float64(i), float64(o), float64(cr), float64(cw), false)
				key := model
				if key == "" {
					key = "?"
				}
				models.add(key, step)
				if stamp := whenLoose(e); stamp != nil && *stamp != 0 {
					if first == nil || *stamp < *first {
						v := *stamp
						first = &v
					}
					if last == nil || *stamp > *last {
						v := *stamp
						last = &v
					}
				}
				cost += step
				miss += prices.Price(model, float64(i), 0, 0, float64(cw), false) - prices.Price(model, 0, 0, float64(i+cw), 0, false)
				tin, tout, tcr, tcw = tin+i, tout+o, tcr+cr, tcw+cw
				peak = max(peak, i+cr+cw)
				if firstCtx == nil {
					v := i + cr + cw
					firstCtx = &v
				}
			}
		}
		for _, part := range transcript.Parts(msg) {
			if ojson.Obj(part) == nil {
				continue
			}
			switch typ(part) {
			case "tool_result":
				takeResult(byID, part)
			case "tool_use":
				name := getStr(part, "name", "?")
				tools.Add(name, 1)
				calls++
				if name == "Skill" {
					if sk := getStr(ojson.GetOr(part, "input"), "skill", ""); sk != "" {
						skills.add(sk)
					}
				}
				id := idStr(ojson.Field(part, "id"))
				if _, done := scripts[id]; !done {
					inp := ojson.GetOr(part, "input")
					if inp == nil {
						inp = ojson.NewObject()
					}
					lang, code, ok := adhocScript(name, inp, written)
					scripts[id] = ok
					if ok {
						scriptN++
					}
					takeScript(&scriptCalls, byID, id, lang, code, ok)
				}
			}
		}
	}
	tok := ojson.NewObject().Set("in", tin).Set("out", tout).Set("cr", tcr).Set("cw", tcw)
	recs := []any{}
	for _, r := range scriptCalls {
		recs = append(recs, r)
	}
	out := ojson.NewObject().Set("cost", round4(cost)).Set("miss", round4(miss)).Set("calls", int64(calls)).
		Set("scripts", int64(scriptN)).Set("script_calls", recs).Set("tok", tok).Set("peak", peak)
	fc := int64(0)
	if firstCtx != nil {
		fc = *firstCtx
	}
	out.Set("first_ctx", fc)
	if names := skills.items(); len(names) > 0 {
		vs := make([]any, len(names))
		for i, n := range names {
			vs[i] = n
		}
		out.Set("skills_called", vs)
	}
	if first != nil {
		out.Set("start", *first)
	} else {
		out.Set("start", nil)
	}
	ms := ojson.NewObject()
	for _, k := range models.keys {
		ms.Set(k, round4(models.n[k]))
	}
	out.Set("models", ms)
	if len(models.keys) > 0 {
		out.Set("model", models.mostCommon())
	} else {
		out.Set("model", nil)
	}
	if first != nil && last != nil {
		out.Set("secs", py.Round(*last-*first, 1))
	} else {
		out.Set("secs", nil)
	}
	var top []any
	for _, k := range tools.MostCommon(4) {
		top = append(top, k)
	}
	if top == nil {
		top = []any{}
	}
	return out.Set("tools", top)
}

// whenLoose is subagents.when(): None on a non-string timestamp too.
func whenLoose(e any) *float64 {
	ts, ok := ojson.Field(e, "timestamp").(string)
	if !ok && ojson.Has(e, "timestamp") {
		return nil
	}
	if v, ok := common.IsoTS(ts); ok {
		return &v
	}
	return nil
}

type floatCounter struct {
	keys []string
	n    map[string]float64
}

func newFloatCounter() *floatCounter { return &floatCounter{n: map[string]float64{}} }

func (c *floatCounter) clone() *floatCounter {
	return &floatCounter{keys: slices.Clone(c.keys), n: maps.Clone(c.n)}
}

func (c *floatCounter) add(k string, v float64) {
	if _, ok := c.n[k]; !ok {
		c.keys = append(c.keys, k)
	}
	c.n[k] += v
}

// mostCommon is Counter.most_common(1)[0][0]: the largest, first on ties.
func (c *floatCounter) mostCommon() string {
	best := c.keys[0]
	for _, k := range c.keys[1:] {
		if c.n[k] > c.n[best] {
			best = k
		}
	}
	return best
}

// scanParent is the delegations in one main transcript, in turn order.
func scanParent(path string, agents map[string]*ojson.Object) []*ojson.Object {
	var launches []*ojson.Object
	pending := ojson.NewObject()
	turn := 0
	for e := range transcript.Entries(path) {
		if ojson.Truthy(ojson.Field(e, "isSidechain")) {
			continue
		}
		if transcript.IsPrompt(e) {
			turn++
		}
		for _, part := range transcript.Parts(transcript.Msg(e)) {
			if ojson.Obj(part) == nil {
				continue
			}
			name, _ := ojson.Field(part, "name").(string)
			switch {
			case typ(part) == "tool_use" && (name == "Task" || name == "Agent"):
				inp := ojson.GetOr(part, "input")
				if inp == nil {
					inp = ojson.NewObject()
				}
				kind := ojson.GetOr(inp, "subagent_type")
				if kind == nil {
					kind = "claude"
				}
				desc := ""
				if v := ojson.GetOr(inp, "description"); v != nil {
					desc = py.Str(v)
				}
				pending.Set(py.Repr(ojson.Field(part, "id")), ojson.NewObject().Set("turn", int64(max(turn, 1))).
					Set("tid", ojson.Field(part, "id")).Set("what", py.Head(redact.Redact(desc), 60)).
					Set("kind", kind).Set("bg", ojson.Truthy(ojson.Field(inp, "run_in_background"))))
			case typ(part) == "tool_result" && ojson.Has(pending, py.Repr(ojson.Field(part, "tool_use_id"))):
				body := ojson.Field(part, "content")
				text, isStr := body.(string)
				if !isStr {
					text = py.Dumps(body)
				}
				key := py.Repr(ojson.Field(part, "tool_use_id"))
				row := ojson.Obj(pending.Vals[key])
				scrub.RemoveKey(pending, key)
				if m := agentIDHex.Search(text); m != nil {
					if a := agents[m.Group(1)]; a != nil {
						for _, k := range a.Keys {
							row.Set(k, a.Vals[k])
						}
						if t := getStr(a, "agent_type", ""); t != "" && getStr(row, "kind", "") == "claude" { // launched without subagent_type
							row.Set("kind", t)
						}
					}
				}
				launches = append(launches, row)
			}
		}
	}
	for _, k := range pending.Keys { // launched, no result seen
		launches = append(launches, ojson.Obj(pending.Vals[k]))
	}
	return launches
}

type agentHit struct {
	sig  string
	scan *ojson.Object
}

var (
	agentMu    sync.Mutex
	agentCache = map[string]agentHit{} // ponytail: one entry per subagent transcript ever read, never pruned
)

// scanAgentCached is scanAgent kept until the file's size or mtime changes; callers must not modify the result.
func scanAgentCached(path string) *ojson.Object {
	loadCaches()
	st, err := os.Stat(path)
	if err != nil {
		return scanAgent(path)
	}
	sig := fmt.Sprintf("%d:%d", st.ModTime().UnixNano(), st.Size())
	agentMu.Lock()
	hit, ok := agentCache[path]
	agentMu.Unlock()
	if ok && hit.sig == sig {
		return hit.scan
	}
	scan := scanAgent(path)
	agentMu.Lock()
	agentCache[path] = agentHit{sig, scan}
	agentMu.Unlock()
	parseDirty.Store(true)
	return scan
}

// scanFolder scans every subagent transcript in folder; only the ones that are new or changed are read.
func scanFolder(folder string) map[string]*ojson.Object {
	agents := map[string]*ojson.Object{}
	if st, err := os.Stat(folder); err != nil || !st.IsDir() {
		return agents
	}
	fh, err := os.Open(folder)
	if err != nil {
		return agents
	}
	names, _ := fh.Readdirnames(-1)
	fh.Close()
	for _, a := range names {
		if strings.HasPrefix(a, "agent-") && strings.HasSuffix(a, ".jsonl") {
			id := a[len("agent-") : len(a)-len(".jsonl")]
			scan := copyObj(scanAgentCached(filepath.Join(folder, a)))
			if raw, err := os.ReadFile(filepath.Join(folder, "agent-"+id+".meta.json")); err == nil {
				if meta, err := ojson.Parse(raw); err == nil {
					if t := getStr(meta, "agentType", ""); t != "" {
						scan.Set("agent_type", t)
					}
				}
			}
			agents[id] = scan
		}
	}
	return agents
}

func summarise(launches []*ojson.Object) *ojson.Object {
	var tin, tout, tcr, tcw int64
	var cost, miss float64
	matched := 0
	for _, l := range launches {
		t := ojson.GetOr(l, "tok")
		tin += getNum(t, "in")
		tout += getNum(t, "out")
		tcr += getNum(t, "cr")
		tcw += getNum(t, "cw")
		c, _ := py.Number(ojson.Field(l, "cost"))
		cost += c
		m, _ := py.Number(ojson.Field(l, "miss"))
		miss += m
		if ojson.Has(l, "cost") {
			matched++
		}
	}
	sorted := append([]*ojson.Object{}, launches...)
	sort.SliceStable(sorted, func(i, j int) bool { return num(ojson.Field(sorted[i], "turn")) < num(ojson.Field(sorted[j], "turn")) })
	ls := []any{}
	for _, l := range sorted {
		ls = append(ls, l)
	}
	return ojson.NewObject().Set("n", int64(len(launches))).Set("cost", round4(cost)).Set("miss", round4(miss)).
		Set("tok", ojson.NewObject().Set("in", tin).Set("out", tout).Set("cr", tcr).Set("cw", tcw)).
		Set("matched", int64(matched)).Set("launches", ls)
}

// ForSession pairs and prices one session's delegations, right now (nil when none).
func ForSession(tpath string) *ojson.Object {
	folder := filepath.Join(filepath.Dir(tpath), strings.TrimSuffix(filepath.Base(tpath), ".jsonl"), "subagents")
	launches := scanParent(tpath, scanFolder(folder))
	if len(launches) == 0 {
		return nil
	}
	return summarise(launches)
}

// SubagentsMain rebuilds ~/.claude/subagent_costs.json (subagents.py main()).
func SubagentsMain() {
	out := ojson.NewObject()
	walkDir(projects(), func(dir string, files []string) {
		var names []string
		for _, f := range files {
			if strings.HasSuffix(f, ".jsonl") {
				names = append(names, f)
			}
		}
		if len(names) == 0 || inSubagents(dir) {
			return
		}
		for _, name := range names {
			folder := filepath.Join(dir, strings.TrimSuffix(name, ".jsonl"), "subagents")
			launches := scanParent(filepath.Join(dir, name), scanFolder(folder))
			if len(launches) > 0 {
				out.Set(py.Head(name, 8), summarise(launches))
			}
		}
	})
	os.WriteFile(filepath.Join(home(), ".claude", "subagent_costs.json"), []byte(py.Dumps(scrub.Delegates(out))), 0o644)
}

func inSubagents(dir string) bool {
	for _, p := range strings.Split(dir, string(os.PathSeparator)) {
		if p == "subagents" {
			return true
		}
	}
	return false
}

// walkDir is os.walk(root) top-down in directory order: fn(dir, files).
func walkDir(root string, fn func(dir string, files []string)) {
	fh, err := os.Open(root)
	if err != nil {
		return
	}
	names, _ := fh.Readdirnames(-1)
	fh.Close()
	var dirs, files []string
	for _, n := range names {
		p := filepath.Join(root, n)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			dirs = append(dirs, n)
		} else {
			files = append(files, n)
		}
	}
	fn(root, files)
	for _, d := range dirs {
		p := filepath.Join(root, d)
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink == 0 {
			walkDir(p, fn)
		}
	}
}

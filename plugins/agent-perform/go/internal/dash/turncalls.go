package dash

import (
	"sort"
	"strconv"
	"strings"

	"agentperform/internal/ojson"
	"agentperform/internal/prices"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
)

// turn_calls.py: per-turn API-call breakdown for /pipeline.

const tcHead = 60

var (
	tcReadCmds  = setOf("grep", "rg", "ls", "find", "fd", "tree", "wc", "head", "tail", "cat", "sed", "awk", "stat", "file", "du", "jq")
	tcWebCmds   = setOf("curl", "wget")
	tcLocal     = pyre.Compile(`\b(localhost|127\.0\.0\.1|0\.0\.0\.0)\b`)
	tcSkipped   = pyre.Compile(`(cd|source|export|set|unset)\b`)
	tcAssign    = pyre.Compile(`[A-Za-z_]\w*=`)
	tcHost      = pyre.Compile(`https?://([^/\s'"?:]+)`)
	tcOrder     = []string{"讀寫檔案", "執行指令", "查外部資料", "文字（無工具）", "載入 skill／工具", "問使用者", "派 subagent"}
	tcColors    = map[string]string{"讀寫檔案": "#58a6ff", "執行指令": "#f0883e", "查外部資料": "#3fb950", "文字（無工具）": "#8b949e", "載入 skill／工具": "#d29922", "問使用者": "#bc8cff", "派 subagent": "#f778ba"}
	tcFileTools = setOf("Read", "Edit", "Write", "NotebookEdit", "Grep", "Glob")
	tcByTool    = map[string]string{"Skill": "載入 skill／工具", "ToolSearch": "載入 skill／工具", "WebSearch": "查外部資料",
		"WebFetch": "查外部資料", "AskUserQuestion": "問使用者", "Agent": "派 subagent", "SendMessage": "派 subagent"}
)

const tcHover = "onmouseenter=\"tcPick(this.closest('.tc'),this.dataset.k)\" onmouseleave=\"tcPick(this.closest('.tc'),this.closest('.tc').dataset.pin||'')\" onclick=\"var t=this.closest('.tc');t.dataset.pin=t.dataset.pin==this.dataset.k?'':this.dataset.k;t.querySelector('.tc-list').open=true\""
const tcScript = "<script>function tcPick(t,k){t.querySelectorAll(\".tc-row\").forEach(function(r){r.style.opacity=!k||r.dataset.k==k?1:.25})}</script>"

// splitOps splits a shell line on | ; & outside quotes; 2>&1 and &> stay in one piece.
func splitOps(line string) []string {
	rs := []rune(line)
	var out []string
	cur := ""
	var q rune
	for i := 0; i < len(rs); {
		ch := rs[i]
		if ch == '\\' && i+1 < len(rs) {
			cur += string(rs[i : i+2])
			i += 2
			continue
		}
		switch {
		case q != 0:
			if ch == q {
				q = 0
			}
		case ch == '\'' || ch == '"':
			q = ch
		case ch == '&' && (strings.HasSuffix(cur, ">") || (i+1 < len(rs) && rs[i+1] == '>')):
		case ch == '|' || ch == ';' || ch == '&':
			out = append(out, cur)
			cur = ""
			i++
			continue
		}
		cur += string(ch)
		i++
	}
	return append(out, cur)
}

func tcSegments(cmd string) []string {
	var segs []string
	for _, line := range strings.Split(cmd, "\n") {
		for _, s := range splitOps(line) {
			if s = py.Strip(s); s != "" && tcSkipped.MatchStart(s) == nil {
				segs = append(segs, s)
			}
		}
	}
	return segs
}

func headWord(seg string) string {
	words, err := py.ShlexSplit(seg)
	if err != nil {
		words = py.Split(seg)
	}
	var kept []string
	for _, w := range words {
		if tcAssign.MatchStart(w) == nil {
			kept = append(kept, w)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return basename(kept[0])
}

type tcCall struct {
	uses  []any
	usage any
	model string
	cat   string
	usd   float64
}

func useName(use any) string { return py.Str(orNil(ojson.Field(use, "name"), "")) }

func useInput(use any) any {
	if v := ojson.GetOr(use, "input"); v != nil {
		return v
	}
	return ojson.NewObject()
}

func tcCategory(use any) string {
	if use == nil {
		return "文字（無工具）"
	}
	name, inp := useName(use), useInput(use)
	switch {
	case name == "Bash":
		segs := tcSegments(py.Str(orNil(ojson.Field(inp, "command"), "")))
		first := ""
		if len(segs) > 0 {
			first = segs[0]
		}
		word := headWord(first)
		if tcWebCmds[word] {
			// a local service (e.g. this dashboard) is not outside data
			if tcLocal.Has(first) {
				return "執行指令"
			}
			return "查外部資料"
		}
		if tcReadCmds[word] {
			return "讀寫檔案"
		}
		return "執行指令"
	case tcFileTools[name]:
		return "讀寫檔案"
	case strings.HasPrefix(name, "mcp__"):
		return "查外部資料"
	}
	if c, ok := tcByTool[name]; ok {
		return c
	}
	return "執行指令"
}

// netloc is urllib.parse.urlparse(url).netloc.
func netloc(url string) string {
	if i := strings.Index(url, ":"); i > 0 {
		scheme := url[:i]
		ok := true
		for k, r := range scheme {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || (k > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'))) {
				ok = false
				break
			}
		}
		if ok {
			url = url[i+1:]
		}
	}
	if !strings.HasPrefix(url, "//") {
		return ""
	}
	rest := url[2:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func tcTarget(use any) string {
	name, inp := useName(use), useInput(use)
	get := func(k string) string { return py.Str(orNil(ojson.Field(inp, k), "")) }
	switch name {
	case "Bash":
		cmd := firstLine(get("command"), tcHead)
		if tcCategory(use) == "查外部資料" {
			if m := tcHost.Search(cmd); m != nil {
				return m.Group(1)
			}
		}
		return cmd
	case "Read", "Edit", "Write", "NotebookEdit":
		p := ojson.GetOr(inp, "file_path")
		if p == nil {
			p = ojson.GetOr(inp, "notebook_path")
		}
		return basename(py.Str(orNil(p, "")))
	case "Grep", "Glob":
		return py.Head(Redact(get("pattern")), tcHead)
	case "WebFetch":
		return netloc(get("url"))
	case "WebSearch":
		return py.Head(Redact(get("query")), tcHead)
	case "Skill":
		return get("skill")
	case "Agent", "SendMessage":
		v := ojson.GetOr(inp, "subagent_type")
		if v == nil {
			v = ojson.GetOr(inp, "to")
		}
		return py.Str(orNil(v, ""))
	case "AskUserQuestion":
		qs, _ := ojson.GetOr(inp, "questions").([]any)
		var q any = ojson.NewObject()
		if len(qs) > 0 {
			q = qs[0]
		}
		return py.Str(orNil(ojson.Field(q, "header"), ""))
	}
	return ""
}

func tcCallsOf(recs []any) []*tcCall {
	var calls []*tcCall
	at := map[string]int{}
	for _, r := range recs {
		msg := msgOf(r)
		if typ(r) != "assistant" || !ojson.Truthy(ojson.Field(msg, "usage")) {
			continue
		}
		mid := ojson.GetOr(msg, "id")
		if mid == nil {
			mid = ojson.Field(r, "uuid")
		}
		key := py.Repr(mid)
		if _, ok := at[key]; !ok {
			at[key] = len(calls)
			calls = append(calls, &tcCall{})
		}
		c := calls[at[key]]
		c.usage, c.model = ojson.Field(msg, "usage"), getStr(msg, "model", "")
		for _, b := range transcript_parts(r) {
			if ojson.Obj(b) != nil && typ(b) == "tool_use" {
				c.uses = append(c.uses, b)
			}
		}
	}
	for _, c := range calls {
		var first any
		if len(c.uses) > 0 {
			first = c.uses[0]
		}
		c.cat = tcCategory(first)
		i, cr, cw := getNum(c.usage, "input_tokens"), getNum(c.usage, "cache_read_input_tokens"), getNum(c.usage, "cache_creation_input_tokens")
		c.usd = prices.Price(c.model, float64(i), float64(getNum(c.usage, "output_tokens")), float64(cr), float64(cw), false)
	}
	return calls
}

// useLabel is 「Write · index.html」.
func useLabel(use any) string { return strings.TrimRight(useName(use)+" · "+tcTarget(use), " ·") }

// tcRowParts are the per-call pieces both the merged API list and the tooltip use: label, parallel note, $ and the raw input.
func tcRowParts(c *tcCall) (tag, label, par, cost, full string) {
	i, cr, cw := getNum(c.usage, "input_tokens"), getNum(c.usage, "cache_read_input_tokens"), getNum(c.usage, "cache_creation_input_tokens")
	cost = ` <span class="dim tip" title="送入 ` + py.Comma(i+cr+cw) + ` token">· $` + py.F(c.usd, 3) + `</span>`
	tag = `<span class="pp-tag">` + Esc(c.cat) + `</span>`
	if len(c.uses) == 0 {
		return
	}
	use := c.uses[0]
	if len(c.uses) > 1 {
		par = " · ×" + strconv.Itoa(len(c.uses)) + " 並行"
	}
	label = Esc(useLabel(use))
	raw := py.Str(ojson.Field(use, "input"))
	if s, ok := ojson.Field(useInput(use), "command").(string); ok && useName(use) == "Bash" {
		raw = s
	}
	full = `<pre>` + shown(raw) + `</pre>`
	return
}

// srtScript reorders the rows of a .srt list by the data-<key> of the clicked button; n sorts ascending.
const srtScript = "<script>function srtPick(b){var w=b.closest('.srtw'),l=w.querySelector('.srt'),k=b.dataset.s,a=k=='n'?1:-1;" +
	"Array.prototype.slice.call(l.children).filter(function(e){return e.dataset[k]!==undefined}).sort(function(x,y){return a*(x.dataset[k]-y.dataset[k])}).forEach(function(e){l.appendChild(e)});" +
	"w.querySelectorAll('button').forEach(function(x){x.style.fontWeight=x==b?'600':'400'})}</script>"

// srtWrap is a sort button row (first option is the default order of rows) above the rows.
func srtWrap(opts [][2]string, rows string) string {
	var b strings.Builder
	b.WriteString(srtScript + `<div class="srtw"><div class="dim" style="font-size:13px;margin:0 0 var(--s2)">排序：`)
	for i, o := range opts {
		w := "400"
		if i == 0 {
			w = "600"
		}
		b.WriteString(`<button type="button" data-s="` + o[0] + `" onclick="srtPick(this)" style="font-weight:` + w + `;margin-right:6px">` + o[1] + `</button>`)
	}
	return b.String() + `</div><div class="srt">` + rows + `</div></div>`
}

// tcRender is the API calls block inside the 依 API tab; mid (the growth chart) and list (the per-call rows) follow the bars. scope is 回合 or subagent.
func tcRender(calls []*tcCall, scope, mid, list string) string {
	if len(calls) == 0 {
		return ""
	}
	n := len(calls)
	var total float64
	costs := map[string]float64{}
	counts := map[string]int{}
	top := 0.0
	any := false
	for _, c := range calls {
		total += c.usd
		costs[c.cat] += c.usd
		counts[c.cat]++
		top = max(top, costs[c.cat])
		any = any || len(c.uses) > 0
	}
	title := `<div class="sec">` + strconv.Itoa(n) + ` 次 API 呼叫 · ` + usdText(total) + `</div>`
	if !any {
		return title + `<p class="lede">這個` + scope + `只有 1 次 API 呼叫，直接回覆。</p>` + mid + list
	}
	var bars strings.Builder
	keys := append([]string(nil), tcOrder...)
	sort.SliceStable(keys, func(a, b int) bool { return costs[keys[a]] > costs[keys[b]] })
	for _, k := range keys {
		if counts[k] > 0 {
			bars.WriteString(`<div class="tc-bar" data-k="` + k + `" ` + tcHover + ` style="--c:` + tcColors[k] + `"><span title="` + strconv.Itoa(counts[k]) + ` 次">` + k + `</span>` +
				`<i style="width:` + py.F(100*costs[k]/top, 0) + `%"></i><b>` + usdText(costs[k]) + `</b></div>`)
		}
	}
	return title + tcScript + `<div class="tc"><div class="tc-bars">` + bars.String() + `</div>` + mid +
		`<details class="tc-list" open><summary>逐次清單（預設依花費排，點開看每一次在做什麼）</summary>` + list + `</details></div>`
}

// turnCalls is the turn's API calls in order.
func turnCalls(path string, meta *Meta) []*tcCall {
	recs := readRecs(path, false)
	a0, a1 := meta.Span[0], min(meta.Span[1], len(recs))
	if a0 > a1 {
		a0 = a1
	}
	return tcCallsOf(recs[a0:a1])
}

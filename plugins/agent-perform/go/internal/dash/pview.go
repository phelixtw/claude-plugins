package dash

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentperform/internal/ojson"
	"agentperform/internal/prices"
	"agentperform/internal/py"
	"agentperform/internal/refs"
	"agentperform/internal/toollimits"
)

const (
	sysPromptTokens = 3_700
	compactBuffer   = 3_000
	probeNear       = 12 * 3600
)

var (
	deferredKeys = []string{"MCP tools (deferred)", "System tools (deferred)"}
	ctxLabels    = []string{"系統提示", "系統工具", "延後載入的工具", "記憶檔", "Skills", "對話訊息", "壓縮緩衝", "剩餘空間"}
	fixedCats    = []string{"listing", "hook", "claudemd"}
	bigKeys      = []string{"ctx_eng", "cc_costs", "ctx_mgmt", "agentdiet"}
	returnKeys   = []string{"ctx_sum"}
	coldKeys     = []string{"subdoc"}
	resendKeys   = []string{"agentdiet"}
)

// Probe is the /context readout for (cwd, model), or nil.
type Probe func(cwd, model string) *ojson.Object

func near(probe Probe, meta *Meta) Probe {
	if probe == nil {
		return nil
	}
	return func(cwd, model string) *ojson.Object {
		got := probe(cwd, model)
		if got == nil {
			return nil
		}
		at, _ := py.Number(ojson.Field(got, "at"))
		if math.Abs(at-or0(meta.At)) <= probeNear {
			return got
		}
		return nil
	}
}

type calib struct {
	real   *Real
	realN  int64
	toolsN int64
	est    int64
	scale  float64
	total  int64
}

// calibrate scales the local token estimates to the API's figure for the last
// call and adds the probe's tool definitions.
func calibrate(items *[]*Item, meta *Meta, probe Probe) calib {
	probe = near(probe, meta)
	for _, it := range *items {
		it.N = int64(countTokens(it.Text) + it.NImg*imageTokens)
	}
	real := meta.Real
	var realN int64
	if real != nil {
		realN = real.In + real.Cr + real.Cw
	}
	var cats any
	if probe != nil && real != nil {
		cats = ojson.Field(probe(meta.Cwd, real.Model), "cats")
	}
	sysTools := getNum(cats, "System tools")
	var deferredN int64
	for _, k := range deferredKeys {
		deferredN += getNum(cats, k)
	}
	toolsN := sysTools + deferredN
	var est int64
	for _, it := range *items {
		est += it.N
	}
	if est == 0 {
		est = 1
	}
	scale := 1.0
	if realN > toolsN {
		scale = float64(realN-toolsN) / float64(est)
	}
	for _, it := range *items {
		it.N = int64(math.RoundToEven(float64(it.N) * scale))
	}
	for _, x := range []struct {
		cat string
		n   int64
		src string
	}{{"tools", sysTools, "系統工具（/context 讀數）"}, {"deferred", deferredN, "延後載入的工具（/context 讀數）"}} {
		if x.n != 0 {
			*items = append(*items, &Item{Cat: x.cat, Pos: -1, Src: x.src, N: x.n})
		}
	}
	var total int64
	for _, it := range *items {
		total += it.N
	}
	if total == 0 {
		total = 1
	}
	return calib{real, realN, toolsN, est, scale, total}
}

// TurnCtx is one turn's /context buckets.
type TurnCtx struct {
	Turn  int
	Total int64
	Cats  [][3]any // label, tokens (int64), source
}

var (
	ctxMu    sync.Mutex
	ctxCache = map[string]*TurnCtx{}
)

// turnContext is the /context buckets for one turn of a session, cached on
// the transcript's stats.
func turnContext(path string, turn int, probe Probe, limit int64) *TurnCtx {
	st, err := os.Stat(path)
	if err != nil {
		return nil // transcript gone: nothing to break down
	}
	key := fmt.Sprintf("%s|%d|%d|%d|%d", path, st.Size(), st.ModTime().UnixNano(), turn, limit)
	ctxMu.Lock()
	if hit, ok := ctxCache[key]; ok {
		ctxMu.Unlock()
		return hit
	}
	ctxMu.Unlock()
	got := computeTurnContext(path, turn, probe, limit)
	ctxMu.Lock()
	if len(ctxCache) > 2000 { // ponytail: crude bound, LRU if it ever matters
		ctxCache = map[string]*TurnCtx{}
	}
	ctxCache[key] = got
	ctxMu.Unlock()
	return got
}

func computeTurnContext(path string, turn int, probe Probe, limit int64) *TurnCtx {
	items, _, turn, meta := itemsFor(path, nil, turn, false)
	if !meta.OK() {
		return nil
	}
	c := calibrate(&items, meta, probe)
	pick := near(probe, meta)
	var cats *ojson.Object
	if pick != nil && c.real != nil {
		cats = ojson.Obj(ojson.Field(pick(meta.Cwd, c.real.Model), "cats"))
	}
	by := map[string]int64{}
	for _, it := range items {
		by[it.Cat] += it.N
	}
	got := func(key string, est int64) (int64, string) {
		if cats.Len() > 0 && ojson.Has(cats, key) {
			return getNum(cats, key), "實測"
		}
		return est, "估"
	}
	var deferred int64
	if cats.Len() > 0 {
		for _, k := range deferredKeys {
			deferred += getNum(cats, k)
		}
	}
	sysEst := by["system"]
	if sysEst == 0 {
		sysEst = sysPromptTokens
	}
	sysp, syspSrc := got("System prompt", sysEst)
	tools, toolsSrc := got("System tools", 0)
	mem, memSrc := got("Memory files", by["claudemd"])
	buffer, bufferSrc := got("Compact buffer", compactBuffer)
	skills := by["skill"]
	used := sysp + tools + deferred + mem
	msgs := min(max(0, c.total-used-skills), max(0, limit-used-buffer-skills))
	free := max(0, limit-used-buffer-skills-msgs)
	srcMsgs := "估"
	if cats.Len() > 0 {
		srcMsgs = "實測"
	}
	vals := [][2]any{{sysp, syspSrc}, {tools, toolsSrc}, {deferred, srcMsgs}, {mem, memSrc},
		{skills, "估"}, {msgs, srcMsgs}, {buffer, bufferSrc}, {free, srcMsgs}}
	out := &TurnCtx{Turn: turn, Total: c.total}
	for i, l := range ctxLabels {
		out.Cats = append(out.Cats, [3]any{l, vals[i][0], vals[i][1]})
	}
	return out
}

func msgsNote(total int64) string {
	return `<p class="lede">逐段用 tokenizer 估算再校正，合計 ` + py.Comma(total) +
		` token，另外含系統提示、工具、記憶檔；估算值與 /context 實測或 API 總量不會剛好相等。</p>`
}

const estNote = "逐段用 tokenizer 估算再校正，另含系統提示、工具、記憶檔；和 /context 實測或 API 總量不會剛好相等。"

// ctxHead is the Context Window heading of the turn page: size, share of the window, biggest source, growth.
func ctxHead(path string, turn int, items []*Item, calls []*Call, c calib) string {
	model := ""
	if c.real != nil {
		model = c.real.Model
	}
	win := int64(1_000_000)
	if got := oneCached(path, 0); got != nil && turn <= len(got.Turns) && got.Turns[turn-1].Window != 0 {
		win = got.Turns[turn-1].Window
	}
	type key struct{ cat, src string }
	sums := map[key]int64{}
	var big key
	for _, it := range items {
		k := key{it.Cat, it.Src}
		sums[k] += it.N
		if sums[k] > sums[big] {
			big = k
		}
	}
	// what the 依 API tab adds up: items between the last call before the turn and the turn's last call
	prev, end := -1, -1
	for _, cl := range calls {
		if cl.InTurn {
			end = cl.Pos
		}
	}
	for _, cl := range calls {
		if cl.InTurn {
			break
		}
		prev = cl.Pos
	}
	var listed int64
	for _, it := range items {
		if it.Pos >= 0 && prev <= it.Pos && it.Pos < end {
			listed += it.N
		}
	}
	title := `<div class="sec" title="` + ctxTitleTip + `">Context Window · 現在 <span class="tip" title="` + estNote + `&#10;之後每次呼叫重讀一次` +
		usdApprox(float64(c.total)*prices.CacheRead(model)/1e6) + `">` + py.Comma(c.total) + ` token · 佔了 ` +
		py.F(float64(c.total)/float64(win)*100, 0) + `%</span></div>`
	lede := ""
	if sums[big] > 0 && c.total > 0 {
		src, cat := Redact(big.src), strings.SplitN(cats[big.cat][0], "（", 2)[0]
		if !strings.HasPrefix(src, cat) {
			src += "（" + cat + "）"
		}
		lede = `最大的單一來源是 ` + Esc(src) + ` ` + py.Comma(sums[big]) +
			`（` + py.F(float64(sums[big])/float64(c.total)*100, 0) + `%）`
	}
	if g := growthSpan(path, turn, model, listed); g != "" {
		if lede != "" {
			lede += " · "
		}
		lede += g
	}
	if lede != "" {
		lede = `<p class="lede">` + lede + `</p>`
	}
	return title + lede
}

func isFixed(it *Item) bool {
	for _, c := range fixedCats {
		if it.Cat == c {
			return it.Cat != "hook" || strings.HasPrefix(it.Src, "SessionStart")
		}
	}
	return false
}

var (
	fixedMu  sync.Mutex
	fixedAll = map[string]int64{}    // transcript path -> first-turn fixed-load tokens
	fixedOne = map[string][2]int64{} // single-turn transcripts: path -> (sig hash, tokens)
	fixedSig = map[string][2]int64{}
)

// FixedTokens is the tokens in the fixed categories on a session's first turn.
func FixedTokens(path string, probe Probe) int64 {
	fixedMu.Lock()
	if n, ok := fixedAll[path]; ok {
		fixedMu.Unlock()
		return n
	}
	var sig *[2]int64
	if st, err := os.Stat(path); err == nil {
		sig = &[2]int64{st.ModTime().UnixNano(), st.Size()}
		if old, ok := fixedSig[path]; ok && old == *sig {
			n := fixedOne[path][0]
			fixedMu.Unlock()
			return n
		}
	}
	fixedMu.Unlock()
	items, _, _, meta := itemsFor(path, nil, 1, false)
	if meta.OK() {
		calibrate(&items, meta, probe)
	}
	var n int64
	for _, it := range items {
		if isFixed(it) {
			n += it.N
		}
	}
	fixedMu.Lock()
	if len(meta.PromptsPos) > 1 {
		fixedAll[path] = n
	} else if sig != nil {
		fixedSig[path] = *sig
		fixedOne[path] = [2]int64{n, 0}
	}
	fixedMu.Unlock()
	parseDirty.Store(true)
	return n
}

func age(itemTurn int, hasTurn bool, turn int, hasView bool) string {
	if !hasView {
		return ""
	}
	if hasTurn && itemTurn == turn {
		return "now"
	}
	return "old"
}

func groupAge(kinds map[string]bool) string {
	switch {
	case kinds["now"] && kinds["old"]:
		return "mixed"
	case kinds["now"]:
		return "now"
	case kinds["old"]:
		return "old"
	}
	return ""
}

// ageSplit spells out a group whose segments came partly from this turn and partly from earlier ones.
func ageSplit(now, all int) string {
	return "（本回合 " + strconv.Itoa(now) + "、先前 " + strconv.Itoa(all-now) + "）"
}

func scriptTag(kind string) string {
	return strings.Replace(scriptTagHTML, "pp-script", strings.TrimRight("pp-script "+kind, " "), 1)
}

func ageLegend(hasView bool) string {
	if !hasView {
		return ""
	}
	return `<div class="pp-legend" title="這個頁面列出 context 裡的所有內容。本回合產生的用藍底藍框並標「本回合」，` +
		`先前回合留下的（這個回合只是帶著走）用淡色虛線框">` +
		`<span><i class="pp-sw now"></i>本回合產生</span><span><i class="pp-sw old"></i>先前回合留下</span><span>兩者都有的，括號裡寫各幾段</span></div>`
}

type pane struct{ id, label, html string }

const ctxTitleTip = `context 裡的每一段內容。依來源：按類別分組；依次數：同一種工具呼叫歸成一組；依 API：這個回合每次 API 呼叫新加進來的內容`

// contentTabs checks the pane whose id is open, or the first when open is "".
func contentTabs(panes []pane, head, open string) string {
	var b strings.Builder
	b.WriteString(`<section class="pp-ctx">` + head + `<div class="pp-tabs">`)
	if !slices.ContainsFunc(panes, func(p pane) bool { return p.id == open }) {
		open = panes[0].id
	}
	for _, p := range panes {
		checked := ""
		if p.id == open {
			checked = " checked"
		}
		b.WriteString(`<input type="radio" name="ctab" id="ct-` + p.id + `"` + checked + `><label for="ct-` + p.id + `">` + p.label + `</label>`)
	}
	for _, p := range panes {
		b.WriteString(`<div class="pane ` + p.id + `">` + p.html + `</div>`)
	}
	return b.String() + `</div></section>`
}

func pct2(n, total int64) string { return py.F(float64(n)/float64(total)*100, 2) }

// catSection is the stacked bar and the by-category table.
func catSection(items []*Item, total int64, title, note string, turn int, hasView bool) string {
	byCat := map[string][]*Item{}
	var order []string
	for _, it := range items {
		if _, ok := byCat[it.Cat]; !ok {
			order = append(order, it.Cat)
		}
		byCat[it.Cat] = append(byCat[it.Cat], it)
	}
	sum := func(c string) int64 {
		var n int64
		for _, it := range byCat[c] {
			n += it.N
		}
		return n
	}
	sort.SliceStable(order, func(i, j int) bool { return sum(order[i]) > sum(order[j]) })
	var bar strings.Builder
	for _, c := range order {
		bar.WriteString(`<i style="width:` + pct2(sum(c), total) + `%;background:` + cats[c][1] + `" title="` +
			Esc(cats[c][0]) + ` · ` + py.Comma(sum(c)) + ` token"></i>`)
	}
	subRows := func(c string) string {
		type grp struct {
			tok                int64
			segs, scripts, now int
			scriptNow          int
			ages, scriptAges   map[string]bool
			texts              []string
		}
		groups := map[string]*grp{}
		var keys []string
		for _, it := range byCat[c] {
			g := groups[it.Src]
			if g == nil {
				g = &grp{ages: map[string]bool{}, scriptAges: map[string]bool{}}
				groups[it.Src] = g
				keys = append(keys, it.Src)
			}
			a := age(it.Turn, it.HasTurn, turn, hasView)
			g.tok += it.N
			g.segs++
			if it.Script != "" {
				g.scripts++
				g.scriptAges[a] = true
				if a == "now" {
					g.scriptNow++
				}
			}
			if a == "now" {
				g.now++
			}
			g.ages[a] = true
			if it.Text != "" {
				g.texts = append(g.texts, Redact(it.Text))
			}
		}
		sort.SliceStable(keys, func(i, j int) bool { return groups[keys[i]].tok > groups[keys[j]].tok })
		if len(keys) > 8 {
			keys = keys[:8]
		}
		var b strings.Builder
		for _, k := range keys {
			v := groups[k]
			b.WriteString(`<li class="` + groupAge(v.ages) + `"`)
			if len(v.texts) > 0 {
				b.WriteString(` data-copy="` + Esc(strings.Join(v.texts, "\n\n")) + `" title="點一下複製內容"`)
			}
			b.WriteString(`><b>` + py.Comma(v.tok) + `</b> · ` + Esc(tilde(k)))
			if v.segs > 1 {
				b.WriteString(" ×" + strconv.Itoa(v.segs))
			}
			if hasView && 0 < v.now && v.now < v.segs {
				b.WriteString(ageSplit(v.now, v.segs))
			}
			if v.scripts > 0 {
				b.WriteString(" " + scriptTag(groupAge(v.scriptAges)) + "×" + strconv.Itoa(v.scripts))
				if hasView && 0 < v.scriptNow && v.scriptNow < v.scripts {
					b.WriteString(ageSplit(v.scriptNow, v.scripts))
				} else if groupAge(v.scriptAges) == "old" && groupAge(v.ages) != "old" {
					b.WriteString("（都是先前回合）")
				}
			}
			b.WriteString(`</li>`)
		}
		return b.String()
	}
	var rows strings.Builder
	for _, c := range order {
		rows.WriteString(`<tr><td><span class="pp-dot" style="background:` + cats[c][1] + `"></span>` + Esc(cats[c][0]))
		if (c == "listing" || c == "claudemd") && c != "hook" {
			rows.WriteString(` <a href="` + leverHref("fixed") + `">改善方法 →</a>`)
		}
		rows.WriteString(`</td><td class="num">` + py.Comma(sum(c)) + `</td><td class="num">` +
			py.F(float64(sum(c))/float64(total)*100, 1) + `%</td><td class="num">` + strconv.Itoa(len(byCat[c])) +
			`</td><td><ul class="pp-ul">` + subRows(c) + `</ul></td></tr>`)
	}
	head := ""
	if title != "" {
		head = `<div class="sec">` + title + `</div>`
	}
	return head + note + `<div class="pp-stack">` + bar.String() + `</div>` +
		`<table class="pp-t"><thead><tr><th>類別</th><th>token（估）</th><th>佔比</th><th>段數</th><th>最大的幾個來源</th></tr></thead><tbody>` +
		rows.String() + `</tbody></table>` + copyJS
}

// markScripts sets Script (language) on every tool_use that writes or runs a
// throwaway script, and on its result.
func markScripts(items []*Item) {
	written := newSet()
	lang := map[string]string{}
	for _, it := range items {
		switch it.Cat {
		case "tool_use":
			if l, _, ok := adhocScript(it.Tool, it.Inp, written); ok {
				it.Script, lang[it.Tid] = l, l
			}
		case "tool_result":
			if l, ok := lang[it.Tid]; ok && it.HasTid {
				it.Script = l
			}
		}
	}
}

// markLimits sets Limit / LimitHit on the rows where a tool limit hit.
func markLimits(items []*Item, perTurn bool) {
	groups := map[int][]*Item{}
	var order []int
	for _, it := range items {
		k := 0
		if perTurn {
			k = it.Turn
			if !it.HasTurn {
				k = -1
			}
		}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], it)
	}
	for _, k := range order {
		rows := groups[k]
		var events []toollimits.Event
		for _, it := range rows {
			switch it.Cat {
			case "prompt":
				events = append(events, toollimits.Event{Kind: "prompt", Text: it.Text})
			case "tool_use":
				events = append(events, toollimits.Event{Kind: "use", ID: it.Tid, Name: it.Tool, Inp: it.Inp})
			case "tool_result":
				events = append(events, toollimits.Event{Kind: "result", ID: it.Tid, Text: it.Text})
			}
		}
		mark, hit := map[string][]string{}, map[string][]string{}
		for _, h := range toollimits.Scan(events) {
			key := h.Role + "\x00" + h.ID
			mark[key] = append(mark[key], h.Key)
			if h.Counted {
				hit[key] = append(hit[key], h.Key)
			}
		}
		for _, it := range rows {
			if it.Cat != "tool_use" && it.Cat != "tool_result" {
				continue
			}
			role := "result"
			if it.Cat == "tool_use" {
				role = "use"
			}
			key := role + "\x00" + it.Tid
			if len(mark[key]) > 0 {
				it.Limit = mark[key]
			}
			if len(hit[key]) > 0 {
				it.LimitHit = hit[key]
			}
		}
	}
}

func limitTag(keys []string) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, k := range keys {
		if seen[k] {
			continue
		}
		seen[k] = true
		b.WriteString(` <span class="pp-tag pp-limit" title="` + Esc(toollimits.Limits[k][1]) + `">` + Esc(toollimits.Limits[k][0]) + `</span>`)
	}
	return b.String()
}

func has(xs []string, k string) bool {
	for _, x := range xs {
		if x == k {
			return true
		}
	}
	return false
}

func limitsTotal(items []*Item) string {
	hits := map[string]int{}
	var order []string
	uses := map[string]map[string]bool{}
	for _, it := range items {
		for _, k := range it.LimitHit {
			if _, ok := hits[k]; !ok {
				order = append(order, k)
			}
			hits[k]++
		}
		if it.Cat == "tool_use" {
			for _, k := range it.Limit {
				if uses[k] == nil {
					uses[k] = map[string]bool{}
				}
				uses[k][it.Tid] = true
			}
		}
	}
	var b strings.Builder
	for _, k := range order {
		var n, cum int64
		for _, it := range items {
			if has(it.Limit, k) || (it.Cat == "tool_result" && it.HasTid && uses[k][it.Tid]) {
				n += it.N
				cum += it.N * it.Resend
			}
		}
		b.WriteString(`<p class="lede" title="多出的呼叫與結果＝撞到限制那一段加上回頭補拿的呼叫與結果；` +
			`重送累計＝每段 token × 之後被帶進的 API 呼叫次數；次數與 token 只算 context 裡留著的（含先前回合，不含 subagent）">` +
			limitTag([]string{k}) + ` ` + strconv.Itoa(hits[k]) + ` 次，多出 ` + py.Comma(n) + ` token，之後重送累計 <b>` + py.Comma(cum) + `</b> token</p>`)
	}
	return b.String()
}

func scriptsTotal(items []*Item) string {
	var calls int
	var code, out, cum int64
	for _, it := range items {
		if it.Script == "" {
			continue
		}
		if it.Cat == "tool_use" {
			calls++
			code += it.N
		} else {
			out += it.N
		}
		cum += it.N * it.Resend
	}
	return `<p class="lede" title="臨時寫的腳本：inline 直譯器程式碼（python3 - &lt;&lt;EOF、node -e），或寫到 /tmp 的腳本檔與之後執行它。` +
		`次數與 token 只算 context 裡留著的（含先前回合，不含 subagent）：程式碼 ` + py.Comma(code) + ` token、執行結果 ` + py.Comma(out) + ` token。` +
		`重送累計＝每段 token × 之後被帶進的 API 呼叫次數">` + scriptTagHTML + ` ` + strconv.Itoa(calls) + ` 次，共 ` + py.Comma(code+out) +
		` token，之後重送累計 <b>` + py.Comma(cum) + `</b> token</p>`
}

// apiSection is one row per API call of the turn: $, tool and target from tc (when its calls line up one to one with the profile's), and what the call added.
func apiSection(items []*Item, calls []*Call, turn int, hasView bool, tc []*tcCall) string {
	var mine []*Call
	for _, c := range calls {
		if c.InTurn {
			mine = append(mine, c)
		}
	}
	if len(mine) == 0 {
		return `<p class="lede dim">這個回合沒有 API 呼叫。</p>`
	}
	prev := -1
	for _, c := range calls {
		if c.Pos < mine[0].Pos && c.Pos > prev {
			prev = c.Pos
		}
	}
	paired := len(tc) == len(mine) // ponytail: matched by position, no per-call id shared by both sources
	type row struct {
		usd  float64
		html string
	}
	var rows []row
	var usds []float64
	if paired {
		for _, c := range tc {
			usds = append(usds, c.usd)
		}
		sort.Float64s(usds)
	}
	for n, c := range mine {
		var nw []*Item
		for _, it := range items {
			if it.Pos >= 0 && prev <= it.Pos && it.Pos < c.Pos {
				nw = append(nw, it)
			}
		}
		sort.SliceStable(nw, func(i, j int) bool { return nw[i].N > nw[j].N })
		prev = c.Pos
		var lines strings.Builder
		var tot int64
		scr := 0
		var allLimits []string
		for k, it := range nw {
			tot += it.N
			if it.Script != "" {
				scr++
			}
			allLimits = append(allLimits, it.Limit...)
			if k >= CallsPerGrp {
				continue
			}
			a := age(it.Turn, it.HasTurn, turn, hasView)
			lines.WriteString(`<li class="` + a + `"><b>` + py.Comma(it.N) + `</b> · ` + Esc(strings.SplitN(cats[it.Cat][0], "（", 2)[0]) +
				` · ` + Esc(Redact(it.Src)))
			if it.Script != "" {
				lines.WriteString(" " + scriptTag(a))
			}
			lines.WriteString(limitTag(it.Limit) + `</li>`)
		}
		more := ""
		if len(nw) > CallsPerGrp {
			more = `<li class="dim">另有 ` + strconv.Itoa(len(nw)-CallsPerGrp) + ` 段</li>`
		}
		var b strings.Builder
		tag, label, par, cost, full, col, usd := "", "", "", "", "", "#58a6ff", 0.0
		k := ""
		if paired {
			tag, label, par, cost, full = tcRowParts(tc[n])
			col, usd, k = tcColors[tc[n].cat], tc[n].usd, ` data-k="`+Esc(tc[n].cat)+`"`
			if usd >= usds[max(len(usds)-3, 0)] {
				cost = strings.Replace(strings.Replace(cost, "$", "<b>$", 1), "</span>", "</b></span>", 1)
			}
		}
		b.WriteString(`<details class="pp-big tc-row"` + k + ` data-usd="` + py.F(usd, 6) + `" data-add="` + strconv.FormatInt(tot, 10) + `" data-n="` + strconv.Itoa(n+1) +
			`" style="--c:` + col + `"><summary>#` + strconv.Itoa(n+1) + ` ` + tag + label + par + cost + ` · 新加進 <b>` + py.Comma(tot) +
			`</b> token（` + strconv.Itoa(len(nw)) + ` 段）`)
		if scr > 0 {
			b.WriteString(" " + scriptTagHTML + "×" + strconv.Itoa(scr))
		}
		sent := `<span class="dim">· 這次送入 ` + py.Comma(c.Ctx) + ` token</span>`
		if hasView {
			sent = `<span class="dim tip" title="這次送入 ` + py.Comma(c.Ctx) + ` token">·</span>`
		}
		b.WriteString(limitTag(allLimits) + ` ` + sent + `</summary>` + full +
			`<ul class="pp-ul">` + lines.String() + more + `</ul></details>`)
		rows = append(rows, row{usd, b.String()})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].usd > rows[j].usd })
	var out strings.Builder
	for _, r := range rows {
		out.WriteString(r.html)
	}
	return srtWrap([][2]string{{"usd", "花費 (大→小)"}, {"add", "新加進 token (大→小)"}, {"n", "呼叫順序 (#1→#N)"}}, out.String())
}

func numbersItems(c calib, agent bool) []refs.Item {
	tools := "工具定義（tools[]）"
	reason := "沒有 /context 探針資料，已含在校正係數裡。"
	if c.toolsN != 0 {
		tools = "工具定義（tools[]）" + py.Comma(c.toolsN) + " token"
		reason = "transcript 不含 tools[]，只有 /context 輸出有這個分類，所以數字來自 /context 探針。"
	}
	items := []refs.Item{
		{Element: "各段 token 數（本機估算合計 " + py.Comma(c.est) + "，校正係數 ×" + py.F(c.scale, 2) + "）",
			Reason: "本機 tokenizer 是 OpenAI o200k，與 Claude 的計法不同，所以先估算，再按該次 API 呼叫實際回報的 token 數用單一全域係數等比校正。"},
		{Element: tools, Reason: reason},
	}
	if agent {
		items = append(items, refs.Item{Element: "task prompt、回傳結論的 token 數",
			Reason: "本機 tokenizer 估算，沒有校正：校正係數是用主對話這個回合最後一次 API 呼叫算的，task prompt 和回傳結論不在那次呼叫裡。"})
	}
	return items
}

func mainItems(c calib) []refs.Item {
	n := strconv.Itoa(BigN)
	return append([]refs.Item{
		{Element: "最大的 " + n + " 段", Keys: bigKeys},
		{Element: "最大的幾段 = " + n, Reason: refs.Cap},
		{Element: "呼叫最多次的工具列前幾列 = " + strconv.Itoa(RepeatListN), Reason: refs.Cap},
		{Element: "每種工具點開後列前幾次呼叫 = " + strconv.Itoa(CallsPerGrp), Reason: refs.Cap},
		{Element: "「之後被重送」與累計 token", Reason: "之後被重送＝該段之後到這個回合結束的主對話 API 呼叫數（從最後一次壓縮起算）；累計 token＝該段 token × 次數，" +
			"沒有扣掉cache 讀較低的計價。用呼叫次數乘 token 當上界，簡單可驗證。"},
	}, numbersItems(c, false)...)
}

func dur(secs float64) string {
	t := int64(secs)
	h, rest := t/3600, t%3600
	m, s := rest/60, rest%60
	if h != 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func fromTimestamp(ts float64) time.Time {
	sec := math.Floor(ts)
	us := math.RoundToEven((ts - sec) * 1e6)
	return time.Unix(int64(sec), int64(us)*1000).Local()
}

var _ = filepath.Join

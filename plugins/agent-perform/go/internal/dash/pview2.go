package dash

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"agentperform/internal/ojson"
	"agentperform/internal/prices"
	"agentperform/internal/py"
	"agentperform/internal/refs"
	"agentperform/internal/transcript"
)

// mainCtx is tools_section's main argument on the main-turn page.
type mainCtx struct {
	sid    string
	turn   int
	agents map[string][2]string // launch tool_use id -> (agent id, description)
	no     func(int) int        // the session page's number for a pipeline turn (0 = unknown)
}

// label is the turn number shown to the user; the link keeps the pipeline's own index.
func (m *mainCtx) label(u *Use) string {
	if u.HasTurn && m.no != nil {
		if n := m.no(u.Turn); n > 0 {
			return strconv.Itoa(n)
		}
	}
	return turnStr(u)
}

// toolsSection is the 依次數 tab. A picked call is drawn even past RepeatListN and CallsPerGrp, opened, outlined,
// carrying its whole-life cost, and scrolled into view on load.
func toolsSection(groups []group, main *mainCtx, empty string, head bool, pick *toolPick) string {
	if len(groups) == 0 {
		return `<p class="lede dim">` + empty + `</p>`
	}
	viewTurn, hasView := 0, false
	if main != nil {
		viewTurn, hasView = main.turn, true
	}
	call := func(u *Use) string {
		errS := ""
		if u.Error {
			errS = ` · <b>回傳錯誤</b>`
		}
		origin, headS := "", ""
		if main != nil {
			whenS := "—"
			if u.Ts != nil && *u.Ts != 0 {
				whenS = fromTimestamp(*u.Ts).Format("01-02 15:04:05")
			}
			origin = ` · <a href="/pipeline?session=` + Esc(main.sid) + `&turn=` + turnStr(u) + `">第 ` + main.label(u) + ` 個回合</a> · ` + whenS
			if a, ok := main.agents[u.Tid]; ok {
				name := a[1]
				if name == "" {
					if u.Desc != nil && *u.Desc != "" {
						name = *u.Desc
					} else {
						name = a[0]
					}
				}
				headS = `<p>Subagent：<a href="/pipeline?session=` + Esc(main.sid) + `&agent=` + Esc(a[0]) + `">` + Esc(name) + `</a></p>`
			} else if u.Desc != nil {
				headS = `<p>Subagent：` + Esc(*u.Desc) + `（找不到這個 subagent 的 transcript）</p>`
			}
		}
		isAgent := u.Desc != nil
		cmd := u.Input
		if isAgent {
			cmd = *u.Desc
		}
		top := firstLine(cmd, firstLineN)
		full := ""
		if !isAgent && !(py.Strip(cmd) == top && py.Len(cmd) <= firstLineN) {
			full = `<details><summary>完整指令（` + py.Comma(int64(py.Len(cmd))) + ` 字）</summary><pre>` + shown(cmd) + `</pre></details>`
		}
		resHTML := `<p class="dim">（沒有回傳）</p>`
		if u.Result != nil && *u.Result != "" {
			resHTML = `<details><summary>結果（` + py.Comma(int64(py.Len(*u.Result))) + ` 字）</summary><pre>` + shown(*u.Result) + `</pre></details>`
		}
		a := age(u.Turn, u.HasTurn, viewTurn, hasView)
		cost := `<b>` + py.Comma(u.N) + `</b> token（估） · 之後被重送 ` + strconv.FormatInt(u.Resend, 10) + ` 次 · 累計 ` + py.Comma(u.N*u.Resend) + ` token`
		if main != nil || u.Usd > 0 {
			cost = `<span class="tip" title="` + py.Comma(u.N) + ` token（估），之後被重送 ` + strconv.FormatInt(u.Resend, 10) +
				` 次，累計 ` + py.Comma(u.N*u.Resend) + ` token"><b>` + usdText(u.Usd) + `</b></span>`
		}
		cls, attrs, life := a, "", ""
		if u.Tid != "" {
			attrs = ` id="tool-` + Esc(u.Tid) + `"`
		}
		if pick.on(u) {
			cls, attrs = a+" hl", attrs+" data-hit open"
			life = `<p class="dim">還在執行，還沒有結果。</p>`
			if pick.life != nil {
				life = pick.life.html()
			}
		}
		s := `<details class="pp-big ` + cls + `"` + attrs + ` style="--c:#d29922"><summary>` + cost + errS + origin
		if u.Script != "" {
			s += " " + scriptTag(a)
		}
		return s + limitTag(u.Limit) + `<br><span class="mono dim">` + Esc(top) + `</span></summary>` + life + headS + full + resHTML + `</details>`
	}
	grp := func(kind string, us []*Use) string {
		var n, cum int64
		var usd float64
		scr := 0
		now := len(us)
		if main != nil {
			now = 0
		}
		ages, scriptAges := map[string]bool{}, map[string]bool{}
		var lims []string
		for _, u := range us {
			n += u.N
			cum += u.N * u.Resend
			usd += u.Usd
			a := age(u.Turn, u.HasTurn, viewTurn, hasView)
			if u.Script != "" {
				scr++
				scriptAges[a] = true
			}
			if main != nil && u.HasTurn && u.Turn == main.turn {
				now++
			}
			ages[a] = true
			lims = append(lims, u.Limit...)
		}
		here := ""
		if main != nil && 0 < now && now < len(us) {
			here = ageSplit(now, len(us))
		}
		tag := ""
		if scr > 0 {
			tag = scriptTag(groupAge(scriptAges)) + "×" + strconv.Itoa(scr) + " "
		}
		open := ""
		if slices.ContainsFunc(us, pick.on) {
			open = " open"
		}
		var b strings.Builder
		b.WriteString(`<details class="pp-big ` + groupAge(ages) + `"` + open + ` data-usd="` + py.F(usd, 6) + `" data-tok="` + strconv.FormatInt(n, 10) + `" data-cnt="` + strconv.Itoa(len(us)) +
			`" style="--c:#f0883e"><summary><b>×` + strconv.Itoa(len(us)) + here + `</b>` +
			`<span class="pp-tag">` + Esc(kind) + `</span>` + tag + limitTag(lims))
		if main != nil || usd > 0 {
			b.WriteString(`<span class="tip" title="結果共 ` + py.Comma(n) + ` token（估），之後重送累計 ` + py.Comma(cum) + ` token">重送` + usdApprox(usd) +
				`</span> · ` + py.Comma(n) + ` token</summary>`)
		} else {
			b.WriteString(`結果共 ` + py.Comma(n) + ` token（估） · 之後重送累計 ` + py.Comma(cum) + ` token</summary>`)
		}
		for i, u := range us {
			if i < CallsPerGrp || pick.on(u) {
				b.WriteString(call(u))
			}
		}
		return b.String() + `</details>`
	}
	title := ""
	if head {
		title = `<div class="sec2" title="每一列都能點開，看每一次的指令和結果">呼叫最多次的工具</div>`
	}
	opts := [][2]string{{"usd", "重送花費 (大→小)"}, {"tok", "token (大→小)"}, {"cnt", "次數 (多→少)"}}
	sort.SliceStable(groups, func(i, j int) bool { return sumUsd(groups[i].uses) > sumUsd(groups[j].uses) })
	var b strings.Builder
	for i, g := range groups {
		if i < RepeatListN || slices.ContainsFunc(g.uses, pick.on) {
			b.WriteString(grp(g.kind, g.uses))
		}
	}
	focus := ""
	if pick != nil && pick.hit { // tid passed toolParam, so it is safe inside the script
		focus = `<script>addEventListener("load",function(){var e=document.getElementById("tool-` + pick.tid + `");` +
			`if(e&&!location.hash)e.scrollIntoView({block:"center"})})</script>`
	}
	return title + srtWrap(opts, b.String()) + focus
}

func sumUsd(us []*Use) float64 {
	var x float64
	for _, u := range us {
		x += u.Usd
	}
	return x
}

func turnStr(u *Use) string {
	if !u.HasTurn {
		return "None"
	}
	return strconv.Itoa(u.Turn)
}

// subagentTable is the 這個回合委派的 subagent table.
func subagentTable(sid string, rows []*Sub) string {
	if len(rows) == 0 {
		return ""
	}
	launches := map[string]any{}
	for _, a := range rows {
		launches[a.ID] = a.Launch
	}
	back := handbacks(Transcript(sid), launches)
	var b strings.Builder
	for _, a := range rows {
		secs := int64(a.num("secs"))
		m, sec := secs/60, secs%60
		tok := ojson.Field(a.Scan, "tok")
		tin, tcr, tcw, tout := getNum(tok, "in"), getNum(tok, "cr"), getNum(tok, "cw"), getNum(tok, "out")
		sent := tin + tcr + tcw
		peak := int64(a.num("peak"))
		hb := back[a.ID]
		hbS := "—"
		if hb != nil {
			hbS = py.Comma(int64(countTokens(hb.text)))
		}
		ratio := "—"
		if peak != 0 {
			ratio = "×" + py.F(float64(sent)/float64(peak), 1)
		}
		model := ""
		if v := ojson.Field(a.Scan, "model"); v != nil {
			model = py.Str(v)
		}
		b.WriteString(`<tr><td><a href="/pipeline?session=` + Esc(py.Head(sid, 8)) + `&agent=` + Esc(a.ID) + `">` + Esc(a.Desc) + `</a>` +
			`<br><span class="dim">` + Esc(a.Type) + ` · ` + Esc(model) + `</span></td>` +
			`<td class="num">` + strconv.FormatInt(int64(a.num("calls")), 10) + `</td><td class="num">` + py.Comma(tin+tout+tcr+tcw) + `</td>` +
			`<td class="num">` + py.Comma(peak) + `</td>` +
			`<td class="num">` + fmt.Sprintf("%d:%02d", m, sec) + `</td><td class="num">$` + py.F(a.num("cost"), 2) + `</td>` +
			`<td class="num">` + hbS + `</td><td class="num">` + ratio + `</td></tr>`)
	}
	return `<div class="sec">這個回合委派的 subagent（點名稱看該 subagent 的 context 組成）</div>` +
		`<table class="pp-t"><thead><tr><th>Subagent</th><th>工具呼叫</th><th>總 token</th>` +
		`<th>最大 context</th><th>耗時</th><th>花費（API 牌價）</th>` +
		`<th>回傳結論 token</th><th>重讀倍數</th></tr></thead><tbody>` + b.String() + `</tbody></table>`
}

// recentSessions is the 30 newest transcripts with their first prompt.
func recentSessions(n int) [][2]string {
	type f struct {
		path string
		mt   int64
	}
	var files []f
	for _, d := range listdir(projects()) {
		for _, name := range listdir(filepath.Join(projects(), d)) {
			if strings.HasSuffix(name, ".jsonl") {
				p := filepath.Join(projects(), d, name)
				if st, err := statOK(p); err == nil {
					files = append(files, f{p, st})
				}
			}
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].mt > files[j].mt })
	if len(files) > n {
		files = files[:n]
	}
	var out [][2]string
	for _, x := range files {
		out = append(out, [2]string{py.Head(filepath.Base(x.path), 8), firstPrompt(x.path)})
	}
	return out
}

func firstPrompt(path string) string {
	for r := range transcript.Entries(path) {
		if !ojson.Truthy(ojson.Field(r, "isSidechain")) && isPromptT(r) {
			return py.Head(promptText(r), 40)
		}
	}
	return ""
}

// withSession puts the open session at the top of the picker when it is older than the newest ones listed.
func withSession(list [][2]string, sid, path string) [][2]string {
	for _, s := range list {
		if s[0] == py.Head(sid, 8) {
			return list
		}
	}
	return append([][2]string{{py.Head(sid, 8), firstPrompt(path)}}, list...)
}

// View is pipeline.view(): the context composition of one turn, or a subagent's page.
func View(k pipeKey, probe Probe) string {
	sid, turn, hasTurn, focus := k.sid, k.turn, k.hasTurn, k.focus
	var at *float64
	if k.hasAt {
		at = &k.at
	}
	sessions := recentSessions(30)
	if sid == "" {
		for _, s := range sessions {
			if s[1] != "" {
				sid = s[0]
				break
			}
		}
		if sid == "" && len(sessions) > 0 {
			sid = sessions[0][0]
		}
	}
	path := ""
	if sid != "" {
		path = Transcript(sid)
	}
	if path == "" {
		return `<h1 class="view">Context 組成</h1><p>找不到這個對話的 transcript。</p>`
	}
	if k.agent != "" {
		k.sid = sid
		return agentView(k, path, probe)
	}
	defer holdRecs(path)()
	if k.tool != "" && !hasTurn && at == nil { // the Mod's link names only the call: open the turn that made it
		if n := launchTurn(path, k.tool); n > 0 {
			turn, hasTurn = n, true
		}
	}
	if !hasTurn && at == nil {
		turn = 1_000_000_000 // no turn asked for: land on the session's last one
	}
	items, prompts, turn, meta := itemsFor(path, at, turn, false)
	if !meta.OK() {
		return `<h1 class="view">Context 組成</h1><p>這個對話沒有回合。</p>`
	}
	c := calibrate(&items, meta, probe)
	calls, _ := mainProfile(path, meta)
	var poss []int
	for _, cl := range calls {
		poss = append(poss, cl.Pos)
	}
	rates := make([]float64, len(calls)+1) // rates[i] = cache-read $/MTok of calls[i:] summed
	for i := len(calls) - 1; i >= 0; i-- {
		rates[i] = rates[i+1] + prices.CacheRead(calls[i].Model)
	}
	for _, it := range items {
		p := -1
		if it.Pos >= 0 {
			p = it.Pos
		}
		x := max(p, meta.Start-1)
		first := sort.SearchInts(poss, x+1)
		it.Resend, it.HasRes, it.RateSum = int64(len(poss)-first), true, rates[first]
	}
	markScripts(items)
	markLimits(items, true)
	subs := subagentsOf(path, meta.ToolIDs)
	agents := map[string][2]string{}
	for _, a := range subs {
		agents[idStr(a.Launch)] = [2]string{a.ID, a.Desc}
	}
	uses := mainToolUses(items)
	tool := pickTool(k.tool, uses, func() []any { return readRecs(path, false) })
	toolsHTML := toolsSection(toolGroups(uses, nil), &mainCtx{py.Head(sid, 8), turn, agents, memoTurnNo(path, meta)},
		"這個回合的 context 裡沒有工具呼叫。", false, tool)

	var pick strings.Builder
	pick.WriteString(`<form class="pp-pick" method="get" action="/pipeline">` +
		`<label>對話 <select name="session" onchange="this.form.turn.value=1;this.form.submit()">`)
	for _, s := range withSession(sessions, sid, path) {
		sel := ""
		if s[0] == py.Head(sid, 8) {
			sel = " selected"
		}
		pick.WriteString(`<option value="` + s[0] + `"` + sel + `>` + s[0] + ` · ` + Esc(orStr(cleanOpening(s[1]), "（指令輸出）")) + `</option>`)
	}
	pick.WriteString(`</select></label><label>第幾個回合 <select name="turn" onchange="this.form.submit()">`)
	for _, p := range prompts {
		sel := ""
		if p.N == turn {
			sel = " selected"
		}
		lab := strconv.Itoa(p.N)
		if n := sessionTurnNo(path, meta, p.N); n > 0 {
			lab = strconv.Itoa(n)
		}
		pick.WriteString(`<option value="` + strconv.Itoa(p.N) + `"` + sel + `>#` + lab + ` ` + Esc(p.Text) + `</option>`)
	}
	pick.WriteString(`</select></label></form>`)

	note := ""
	if c.real == nil {
		note = `<p class="lede">這個回合沒有 API 回應（可能被中止），數字是本機估算。</p>`
	}
	if meta.Compacts != 0 {
		note += `<p class="lede dim">這個對話已壓縮 ` + strconv.Itoa(meta.Compacts) + ` 次，只算最後一次壓縮之後的內容。</p>`
	}
	tc := turnCalls(path, meta)
	got := oneCached(path, 0)
	var t *Turn
	no := turn
	if got != nil && meta.At != nil { // the dashboard's turn numbers skip prompts without an API call; match by time
		for j, x := range got.Turns {
			if x.At == *meta.At {
				t, no = x, j+1
			}
		}
	}
	if t == nil && got != nil && 0 < turn && turn <= len(got.Turns) {
		t = got.Turns[turn-1]
	}
	s8 := Esc(py.Head(sid, 8))
	// tiles carry the same numbers as this turn's row on the session page
	head, fs := "", []nfind(nil)
	spend0 := 0.0
	if t != nil {
		for _, x := range subs {
			spend0 += x.num("cost")
		}
		spend0 += t.Usage.Cost
	}
	var fkeys []string // lens key of each finding
	if t != nil {
		span := turnSpan(t)
		sc, scalls := 0.0, 0
		for _, x := range subs {
			sc += x.num("cost")
			scalls += int(x.num("calls"))
		}
		win := t.Window
		if win == 0 {
			win = 1_000_000
		}
		delta, usual := ctxGrowthAt(path, turn)
		growthV, growthSub := "—", ""
		if delta != nil {
			growthV = signed(delta.v)
		}
		if usual != nil {
			growthSub = "平常 " + signed(usual.v)
		}
		head = `<div class="hero">` + hotTile(stats([]stat{
			{"花費", money(t.Usage.Cost + sc), "主對話 " + money(t.Usage.Cost) + " · subagent " + money(sc), "", "照 Anthropic API 牌價換算；訂閱方案不是這樣計費"},
			{"API 呼叫", strconv.Itoa(t.Calls) + " 次", "另有 " + strconv.Itoa(len(subs)) + " 個 subagent、" + strconv.Itoa(scalls) + " 次工具呼叫", "", ""},
			{"耗時", secs(&span), "主對話 " + secs(&t.E2E) + "，算到最後一個 subagent 結束", "", ""},
			{"本回合加進 context", growthV, growthSub, "", "本回合最大一次 API 呼叫的 context 比上一個回合多了多少（API 回報）。最大 context 是 " + toks(pi(t.CtxPeak)) + "，視窗的 " + pct0(float64(ctxOf(t)), float64(win)) + "。"},
		}), 0) + `</div>`
		var tr int64 // tool results sit in context and are re-read on every call
		type grp struct {
			src  string
			tok  int64
			segs int
		}
		groups := map[string]*grp{}
		var gOrder []*grp
		for _, it := range items {
			if it.Cat != "tool_result" {
				continue
			}
			tr += it.N
			g := groups[it.Src]
			if g == nil {
				g = &grp{src: it.Src}
				groups[it.Src] = g
				gOrder = append(gOrder, g)
			}
			g.tok += it.N
			g.segs++
		}
		sort.SliceStable(gOrder, func(i, j int) bool { return gOrder[i].tok > gOrder[j].tok })
		if tr > 0 && c.total > 0 {
			var parts []string
			for _, g := range gOrder[:min(2, len(gOrder))] {
				name := g.src // "Bash · cat" reads as "cat"
				if i := strings.LastIndex(name, " · "); i >= 0 {
					name = name[i+len(" · "):]
				}
				parts = append(parts, Esc(name)+" "+strconv.Itoa(g.segs)+" 次共 "+toks(pi(g.tok)))
			}
			fs = append(fs, nfind{"hot", pct0(float64(tr), float64(c.total)), "context 有 " + pct0(float64(tr), float64(c.total)) + " 是工具結果：" + strings.Join(parts, "、") + "，之後每次呼叫都要重讀", "#ctxw", "看來源 ↓"})
			fkeys = append(fkeys, "context")
		}
		if delta != nil && usual != nil && usual.v > 0 && delta.v/usual.v >= 2 {
			big, bigSrc, bigCat := "", "", ""
			var bigTok int64
			bySrc := map[string]int64{}
			nowN, allN := map[string]int{}, map[string]int{} // segments of a source this turn and in the whole context
			for _, it := range items {
				k := it.Src + " · " + strings.SplitN(cats[it.Cat][0], "（", 2)[0]
				allN[k]++
				if it.HasTurn && it.Turn == turn {
					nowN[k]++
					bySrc[k] += it.N
					if bySrc[k] > bigTok {
						big, bigSrc, bigCat, bigTok = k, it.Src, strings.SplitN(cats[it.Cat][0], "（", 2)[0], bySrc[k]
					}
				}
			}
			txt := "這個回合加進 context " + toks(pi(int64(delta.v))) + "，是平常的 " + py.F(delta.v/usual.v, 0) + " 倍"
			if big != "" {
				txt += "；最多的是本回合 " + Esc(bigSrc) + " " + strconv.Itoa(nowN[big]) + " 次（" + Esc(bigCat) + " " + toks(pi(bigTok)) + "；整個 context 累計 " + strconv.Itoa(allN[big]) + " 次）"
			}
			fs = append(fs, nfind{"", "×" + py.F(delta.v/usual.v, 0), txt, "#ctxw", "看來源 ↓"})
			fkeys = append(fkeys, "growth")
		}
		if len(subs) > 0 {
			ret := returnToks(sid, subs)
			tail := "，探索過程沒有留在主對話"
			if len(ret) > 0 {
				lo, hi := ret[0], ret[0]
				for _, v := range ret {
					lo, hi = min(lo, v), max(hi, v)
				}
				tail = "，各帶回約 " + py.Comma(lo) + "–" + py.Comma(hi) + " token 的結論" + tail
			}
			fs = append(fs, nfind{"", money(sc), strconv.Itoa(len(subs)) + " 個 subagent 合計花了 " + money(sc) + "，佔這個回合 " + pct0(sc, t.Usage.Cost+sc) + tail, "#subs", "看 subagent →"})
			fkeys = append(fkeys, "delegate")
		}
		if ctxBig(t) && len(fs) < 3 {
			fs = append(fs, nfind{"", pct0(float64(ctxOf(t)), float64(win)), "最大 context " + toks(pi(t.CtxPeak)) + "，超過視窗 40%；之後每次呼叫都要重讀這麼多", "#ctxw", "看組成 ↓"})
			fkeys = append(fkeys, "context")
		}
		if c := t.Usage.Cost + sc; c > 0 && t.Rewrite/c >= 0.10 && len(fs) < 3 {
			fs = append(fs, nfind{"", pct0(t.Rewrite, c), "開始時 Cache 已過期，重寫多花 " + money(t.Rewrite) + "，佔這個回合 " + pct0(t.Rewrite, c), "#ctxw", "看組成 ↓"})
			fkeys = append(fkeys, "cachebreak")
		}
	}
	if t != nil { // fill up to three findings from the turn's own Tag facts
		type cand struct {
			ok  bool
			key string
			f   nfind
		}
		for _, c := range []cand{
			{killedSlow(t), "killed", nfind{"", "中止", "這個回合等太久，被你中止；花費照算", "#ctxw", "看組成 ↓"}},
			{t.Scripts+t.DScripts > 0, "repeat", nfind{"", strconv.Itoa(t.Scripts+t.DScripts) + " 次", "臨時腳本 " + strconv.Itoa(t.Scripts+t.DScripts) + " 次（主對話 " + strconv.Itoa(t.Scripts) + "、subagent " + strconv.Itoa(t.DScripts) + "）", "#ctxw", "看組成 ↓"}},
			{true, "", nfind{"", strconv.Itoa(t.Calls) + " 次", "API 呼叫 " + strconv.Itoa(t.Calls) + " 次；Cache hit " + hitStr(hitRate(t.Usage)), "#ctxw", "看組成 ↓"}},
		} {
			if c.ok && len(fs) < 3 {
				fs = append(fs, c.f)
				fkeys = append(fkeys, c.key)
			}
		}
	}
	lensNote := ""
	if d, ok := activeLens(focus); ok {
		for i := range fs {
			fs[i].tone = ""
			if fkeys[i] == focus {
				fs[i].tone = "hot"
			}
		}
		sort.SliceStable(fs, func(i, j int) bool { return fs[i].tone == "hot" && fs[j].tone != "hot" })
		lensNote = `<div class="lbar">聚焦：<b>` + Esc(d.name) + `</b></div>`
	}
	scopeNote, lede := "", ""
	if meta.Compacts != 0 {
		scopeNote = `<p class="lede dim">這個對話壓縮過 ` + strconv.Itoa(meta.Compacts) + ` 次；下面的 Context Window 只算最後一次壓縮之後。</p>`
	}
	if t != nil {
		win := t.Window
		if win == 0 {
			win = 1_000_000
		}
		lede = `<p class="lede">「` + Esc(py.Head(orStr(cleanOpening(t.Prompt), "（指令輸出）"), 24)) + `」這一句花了 <b>` + money(spend0) + `</b>；送出時 context 已經佔視窗 ` + pct0(float64(c.total), float64(win)) + `（` + toks(pi(c.total)) + `）`
		if tr := toolResultTok(items); tr > 0 && c.total > 0 {
			lede += `，其中 ` + pct0(float64(tr), float64(c.total)) + ` 是工具讀回來的內容`
		}
		lede += `。</p>`
	}
	after := ""
	if meta.Compacts != 0 {
		after = "（壓縮後）"
	}
	cap := func(txt string) string { return `<p class="dim">範圍：` + txt + after + `</p>` }
	savings := ""
	if t != nil {
		savings = savingsSection(repeatLine(path, turn), func(it *Item) bool { return it.HasTurn && it.Turn == turn }, t, items, tc, "本回合")
	}
	subFold := ""
	if len(subs) > 0 {
		subFold = foldID("subs", "這個回合委派的 subagent（類型·模型、總 token、最大 context、重讀倍數）", subagentTable(sid, subs))
	}
	problems := ""
	if savings != "" {
		problems = fold("這個回合撞到的問題（臨時腳本與工具上限，點編號看那次呼叫）", savings)
	}
	return pipelineCSS + crumbs(sessionCrumbs(path, s8), "第 "+strconv.Itoa(no)+" 回合") +
		`<h1 class="view">第 ` + strconv.Itoa(no) + ` 個回合</h1>` + lede + head + scopeNote + lensNote + finds(fs) +
		`<h2 class="nh" id="ctxw">Context Window</h2>` + tool.note("這個回合", "可能已被壓縮，或屬於 subagent") + pick.String() +
		contentTabs([]pane{{"src", "依來源", cap("現在 context 裡每一類內容的大小") + catSection(items, c.total, "", "", turn, true)},
			{"cnt", "依次數", cap("這個回合 context 裡的工具呼叫") + toolsHTML},
			{"api", "依 API", cap("這個回合每一次 API 呼叫") + tcRender(tc, "回合", growthPane(inTurnCalls(calls), nil), apiSection(items, calls, turn, true, tc))}},
			ctxHead(path, turn, items, calls, c)+ageLegend(true), tool.tab()) +
		subFold + problems +
		turnChart(addedByTurn(items), meta, turn, py.Head(sid, 8), toks(pi(c.total))) +
		fold("依據與門檻（顯示上限、重送上界、tokenizer 校正）", note+refs.Section(mainItems(c)))
}

// sessionCrumbs is 總覽 ／ day ／ session, the path above a turn or subagent page.
func sessionCrumbs(path, s8 string) string {
	out := `<a href="/">總覽</a>`
	if got := oneCached(path, 0); got != nil && len(got.Turns) > 0 {
		d := day(got.Turns[0].At)
		out += ` ／ <a href="/?day=` + d + `">` + d[5:] + `</a>`
	}
	return out + ` ／ <a href="/?session=` + s8 + `">` + s8 + `</a>`
}

// toolResultTok is the tokens of tool results in context.
func toolResultTok(items []*Item) int64 {
	var n int64
	for _, it := range items {
		if it.Cat == "tool_result" {
			n += it.N
		}
	}
	return n
}

// ctxGrowthAt is how much the context grew in turn n and the usual growth, as in the Context Window header.
func ctxGrowthAt(path string, n int) (*pnum, *pnum) {
	got := oneCached(path, 0)
	if got == nil {
		return nil, nil
	}
	turns := got.Turns
	if n < len(turns) {
		turns = turns[:n]
	}
	return ctxGrowth(turns)
}

// returnToks is the size of each subagent's returned conclusion, in tokens.
func returnToks(sid string, rows []*Sub) []int64 {
	launches := map[string]any{}
	for _, a := range rows {
		launches[a.ID] = a.Launch
	}
	back := handbacks(Transcript(sid), launches)
	var out []int64
	for _, a := range rows {
		if hb := back[a.ID]; hb != nil {
			out = append(out, int64(countTokens(hb.text)))
		}
	}
	return out
}

func statOK(p string) (int64, error) {
	st, err := statFile(p)
	if err != nil {
		return 0, err
	}
	return st, nil
}

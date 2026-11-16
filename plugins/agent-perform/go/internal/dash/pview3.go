package dash

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"agentperform/internal/ojson"
	"agentperform/internal/prices"
	"agentperform/internal/py"
	"agentperform/internal/refs"
	"agentperform/internal/transcript"
)

func statFile(p string) (int64, error) {
	st, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return st.ModTime().UnixNano(), nil
}

func isPromptT(r any) bool { return transcript.IsPrompt(r) }

// agentProf is agent_profile()'s result.
type agentProf struct {
	calls     []*Call
	toolCalls int
	prompt    string
	firstCtx  int64
	peak      int64
	sent      int64
	reads     *py.Counter
	compacts  []int
	tools     []group
}

// agentProfile is one pass over a subagent transcript.
func agentProfile(path string, scale float64) agentProf {
	var calls []*Call
	order := map[string]int{}
	var compacts []int
	uses := map[string]*Use{}
	var useOrder []string
	written := newSet()
	reads := py.NewCounter()
	prompt := ""
	toolCalls := 0
	i := -1
	for r := range transcript.Entries(path) {
		i++
		msg := msgOf(r)
		switch {
		case getStr(r, "subtype", "") == "compact_boundary":
			compacts = append(compacts, i)
		case typ(r) == "assistant":
			u := ojson.Field(msg, "usage")
			mid := ojson.GetOr(msg, "id")
			if mid == nil {
				mid = ojson.Field(r, "uuid")
			}
			if ojson.Truthy(u) {
				c := usageCall(u, getStr(msg, "model", ""))
				key := py.Repr(mid)
				if k, ok := order[key]; ok {
					c.Pos = calls[k].Pos
					calls[k] = c
				} else {
					order[key] = len(calls)
					c.Pos = i
					calls = append(calls, c)
				}
			}
			for _, p := range transcript.Parts(msg) {
				if ojson.Obj(p) == nil || typ(p) != "tool_use" {
					continue
				}
				toolCalls++
				name := getStr(p, "name", "?")
				inp := ojson.GetOr(p, "input")
				if inp == nil {
					inp = ojson.NewObject()
				}
				for _, x := range readPaths(name, inp) {
					reads.Add(x, 1)
				}
				lang, code, ok := adhocScript(name, inp, written)
				id := idStr(ojson.Field(p, "id"))
				if _, seen := uses[id]; !seen {
					useOrder = append(useOrder, id)
					if name == "Bash" {
						cmd := py.Str(orNil(ojson.GetOr(inp, "command"), ""))
						uses[id] = &Use{Kind: bashKind(cmd), Input: cmd, Pos: i, Tid: id}
					} else {
						uses[id] = &Use{Kind: name, Pos: i, Input: py.Dumps(inp), Tid: id}
					}
				}
				if ok {
					uses[id].Lang = lang
					uses[id].CodeN = int64(math.RoundToEven(float64(countTokens(code)) * scale))
				}
			}
		case typ(r) == "user":
			c := ojson.Field(msg, "content")
			if prompt == "" && !ojson.Truthy(ojson.Field(r, "isMeta")) {
				prompt = textOf(c)
			}
			list, _ := c.([]any)
			for _, x := range list {
				if ojson.Obj(x) == nil || typ(x) != "tool_result" {
					continue
				}
				body := ojson.Field(x, "content")
				text, isStr := body.(string)
				if !isStr {
					text = textOf(body)
				}
				n := countTokens(text)
				if u, ok := uses[idStr(ojson.Field(x, "tool_use_id"))]; ok {
					t := text
					u.Result, u.N, u.RPos, u.HasRPos = &t, int64(math.RoundToEven(float64(n)*scale)), i, true
					u.Error = ojson.Truthy(ojson.Field(x, "is_error"))
				}
			}
		}
	}
	var first, peak, sent int64
	if len(calls) > 0 {
		first = calls[0].Ctx
	}
	for _, c := range calls {
		peak = max(peak, c.Ctx)
		sent += c.Ctx
	}
	cset := map[int]bool{}
	for _, p := range compacts {
		after := false
		before := 0
		for _, c := range calls {
			if c.Pos > p {
				after = true
			}
			if c.Pos < p {
				before++
			}
		}
		if after {
			cset[1+before] = true
		}
	}
	var cs []int
	for k := range cset {
		cs = append(cs, k)
	}
	sort.Ints(cs)
	var us []*Use
	for _, id := range useOrder {
		us = append(us, uses[id])
	}
	return agentProf{calls: calls, toolCalls: toolCalls, prompt: prompt, firstCtx: first, peak: peak, sent: sent,
		reads: reads, compacts: cs, tools: toolGroups(us, calls)}
}

// frame is the axes, grid and compaction marks shared by the charts.
func frame(n int, top int64, compacts []int, label string) ([]string, func(int) float64, func(float64) float64) {
	const W, H, L, R, T, B = 900, 240, 56, 14, 12, 28
	if top == 0 {
		top = 1
	}
	step := int64(math.Pow(10, float64(len(strconv.FormatInt(top, 10))-1)))
	ymax := float64(ceilDiv(top, step) * step)
	x := func(i int) float64 {
		if n > 1 {
			return L + float64(W-L-R)*(float64(i)/float64(n-1))
		}
		return L + float64(W-L-R)*0.5
	}
	y := func(v float64) float64 { return T + float64(H-T-B)*(1-v/ymax) }
	out := []string{fmt.Sprintf(`<svg class="pp-svg" viewBox="0 0 %d %d" role="img" aria-label="%s">`, W, H, label)}
	for k := 0; k < 5; k++ {
		v := ymax * float64(k) / 4
		out = append(out, fmt.Sprintf(`<line class="g" x1="%d" x2="%d" y1="%s" y2="%s"/>`, L, W-R, py.F(y(v), 1), py.F(y(v), 1))+
			fmt.Sprintf(`<text x="%d" y="%s" text-anchor="end">%sk</text>`, L-6, py.F(y(v)+4, 1), py.CommaF(v/1000, 0)))
	}
	idx := map[int]bool{0: true, n / 2: true, n - 1: true}
	var keys []int
	for k := range idx {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, i := range keys {
		anchor := "middle"
		if i == n-1 && n > 1 {
			anchor = "end"
		}
		out = append(out, fmt.Sprintf(`<text x="%s" y="%d" text-anchor="%s">第 %d 次</text>`, py.F(x(i), 1), H-8, anchor, i+1))
	}
	for _, c := range compacts {
		out = append(out, fmt.Sprintf(`<line class="m" x1="%s" x2="%s" y1="%d" y2="%d"/>`, py.F(x(c-1), 1), py.F(x(c-1), 1), T, H-B)+
			fmt.Sprintf(`<text x="%s" y="%d">壓縮</text>`, py.F(x(c-1)+3, 1), T+10))
	}
	return out, x, y
}

func growthSVG(calls []*Call, compacts []int) string {
	var top int64
	for _, c := range calls {
		top = max(top, c.Ctx)
	}
	out, x, y := frame(len(calls), top, compacts, "Context 成長曲線")
	var pts []string
	for i, c := range calls {
		pts = append(pts, py.F(x(i), 1)+","+py.F(y(float64(c.Ctx)), 1))
	}
	out = append(out, `<polyline class="a" points="`+strings.Join(pts, " ")+`"/>`)
	for i, c := range calls {
		out = append(out, fmt.Sprintf(`<circle class="a" cx="%s" cy="%s" r="2"><title>第 %d 次呼叫：%s token</title></circle>`,
			py.F(x(i), 1), py.F(y(float64(c.Ctx)), 1), i+1, py.Comma(c.Ctx)))
	}
	return strings.Join(out, "") + `</svg>`
}

// growthPane is the context-growth chart at the top of the 依 API tab; a single call has no curve to draw.
func growthPane(calls []*Call, compacts []int) string {
	if len(calls) < 2 {
		return ""
	}
	legend := `<div class="pp-legend"><span><svg width="34" height="10"><line x1="0" y1="5" x2="34" y2="5" stroke="#58a6ff" stroke-width="2.5"/></svg>` +
		`實際 context（每次 API 呼叫送出的 token）</span>`
	if len(compacts) > 0 {
		legend += ` <span class="dim">灰色細虛線：壓縮</span>`
	}
	return legend + `</div>` + growthSVG(calls, compacts)
}

// inTurnCalls keeps the calls that belong to the viewed turn.
func inTurnCalls(calls []*Call) []*Call {
	var out []*Call
	for _, c := range calls {
		if c.InTurn {
			out = append(out, c)
		}
	}
	return out
}

func addedByTurn(items []*Item) map[int]int64 {
	out := map[int]int64{}
	for _, it := range items {
		if it.HasTurn && it.Turn != 0 {
			out[it.Turn] += it.N
		}
	}
	return out
}

// turnChart is the growing stack, one column per turn from the first one still
// in context to the viewed turn.
func turnChart(added map[int]int64, meta *Meta, turn int, sid, size string) string {
	const W, H, L, R, T, B = 900, 220, 56, 14, 22, 28
	first := meta.StartTurn
	nTurns := turn - first + 1
	var total int64
	for j := first; j <= turn; j++ {
		total += added[j]
	}
	t1 := total
	if t1 == 0 {
		t1 = 1
	}
	step := 4 * int64(math.Pow(10, float64(max(len(strconv.FormatInt(t1, 10))-2, 0))))
	ymax := ceilDiv(t1, step) * step
	slot := float64(W-L-R) / float64(nTurns)
	y := func(v float64) float64 { return T + float64(H-T-B)*(1-v/float64(ymax)) }
	out := []string{fmt.Sprintf(`<svg class="pp-svg" viewBox="0 0 %d %d" role="img" aria-label="各回合留在 context 的內容">`, W, H)}
	for k := 0; k < 5; k++ {
		v := float64(ymax) * float64(k) / 4
		out = append(out, fmt.Sprintf(`<line class="g" x1="%d" x2="%d" y1="%s" y2="%s"/>`, L, W-R, py.F(y(v), 1), py.F(y(v), 1))+
			fmt.Sprintf(`<text x="%d" y="%s" text-anchor="end">%sk</text>`, L-6, py.F(y(v)+4, 1), py.CommaF(v/1000, 0)))
	}
	var cum int64
	for k := first; k <= turn; k++ {
		x0 := L + float64(k-first)*slot
		cum += added[k]
		at := "None"
		if k-1 >= 0 && k-1 < len(meta.PromptAt) && meta.PromptAt[k-1] != nil {
			at = py.FloatRepr(*meta.PromptAt[k-1])
		}
		out = append(out, `<a href="/pipeline?session=`+Esc(sid)+`&amp;at=`+at+`">`+
			fmt.Sprintf(`<rect x="%s" y="%d" width="%s" height="%d" fill="transparent">`, py.F(x0, 1), T, py.F(slot, 1), H-T-B)+
			`<title>到 #`+strconv.Itoa(k)+` 累積 `+py.Comma(cum)+` token</title></rect>`)
		var base int64
		for j := first; j <= k; j++ {
			v := added[j]
			base += v
			if v != 0 {
				fill := ""
				cls := "bl"
				if j == turn {
					cls = "bw"
				} else {
					fill = ` style="fill:` + layerColors[j%len(layerColors)] + `"`
				}
				out = append(out, `<rect class="`+cls+`"`+fill+` x="`+py.F(x0+slot*.1, 1)+`" y="`+py.F(y(float64(base)), 1)+`" `+
					`width="`+py.F(slot*.8, 1)+`" height="`+py.F(y(float64(base-v))-y(float64(base)), 1)+`">`+
					`<title>#`+strconv.Itoa(j)+` 加進 `+py.Comma(v)+` token</title></rect>`)
			}
		}
		out = append(out, `</a>`)
	}
	out = append(out, fmt.Sprintf(`<text x="%d" y="%s" text-anchor="end">共 %s token</text>`, W-R, py.F(y(float64(total))-6, 1), py.Comma(total)))
	every := int(ceilDiv(int64(nTurns), 10))
	for k := first; k <= turn; k++ {
		// the current turn is always labelled; drop a periodic label that would crowd it
		if k == turn || ((k-first)%every == 0 && turn-k > every/2) {
			out = append(out, fmt.Sprintf(`<text x="%s" y="%d" text-anchor="middle">#%d</text>`, py.F(L+(float64(k-first)+.5)*slot, 1), H-8, k))
		}
	}
	note := ""
	if meta.Compacts != 0 {
		note = `<p class="lede dim">這個對話已壓縮，從第 ` + strconv.Itoa(first) + ` 個回合起算。</p>`
	}
	return `<h2 class="nh" title="每一欄＝到那個回合為止 context 裡的內容，一層是一個回合加進來、到現在還留在裡面的部分（token 為估）。` +
		`點一欄開那個回合的頁面。紫色是目前這個回合">各回合留在 context 的內容（共 ` + size + `）</h2>` +
		`<div class="card nchart">` + note + strings.Join(out, "") + `</svg></div>`
}

func ceilDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a > 0) == (b > 0) {
		q++
	}
	return q
}

// agentView is the page for one subagent.
func agentView(k pipeKey, parent string, probe Probe) string {
	sid, agent := k.sid, k.agent
	var sub *Sub
	for _, a := range subagentsOf(parent, nil) {
		if a.ID == agent {
			sub = a
			break
		}
	}
	path := filepath.Join(strings.TrimSuffix(parent, ".jsonl"), "subagents", "agent-"+agent+".jsonl")
	if sub == nil || !isFileP(path) {
		return `<h1 class="view">Context 組成</h1><p>找不到這個 subagent 的 transcript。</p>`
	}
	items, _, _, meta := itemsFor(path, nil, 1_000_000, true)
	if !meta.OK() {
		return `<h1 class="view">Context 組成</h1><p>這個 subagent 沒有回合。</p>`
	}
	c := calibrate(&items, meta, probe)
	prof := agentProfile(path, c.scale)
	calls := prof.calls
	for _, it := range items {
		var n int64
		var rate float64
		for _, cl := range calls {
			if it.Pos < 0 || cl.Pos > it.Pos {
				n++
				rate += prices.CacheRead(cl.Model)
			}
		}
		it.Resend, it.HasRes, it.RateSum = n, true, rate
	}
	hb := handbacks(parent, map[string]any{agent: sub.Launch})[agent]
	before := parentReads(parent, sub.Launch)
	var seenBefore []string
	for _, p := range prof.reads.Keys {
		if before[p] {
			seenBefore = append(seenBefore, p)
		}
	}
	sort.Strings(seenBefore) // ponytail: Python sorts a set; ties in the read count fall back to path order here
	sort.SliceStable(seenBefore, func(i, j int) bool { return prof.reads.N[seenBefore[i]] > prof.reads.N[seenBefore[j]] })
	task := prof.prompt
	taskN := int64(countTokens(task))
	h := home()
	sec1 := `<div class="sec">主對話：委派內容與回傳結論</div><table class="pp-t">` +
		`<tr><th>委派內容</th><td>task prompt <b>` + py.Comma(taskN) + `</b> token` +
		`<details class="pp-big" style="--c:#bc8cff"><summary>點開看全文</summary>` +
		`<pre>` + shown(task) + `</pre></details></td></tr>` +
		`<tr><th>重複讀取的檔案` + refs.Cite(coldKeys) + `</th><td>`
	if len(seenBefore) > 0 {
		sec1 += `主對話委派前已讀過、subagent 又讀一次的檔案，共 ` + strconv.Itoa(len(seenBefore)) + ` 個：<ul class="pp-ul">`
		for i, p := range seenBefore {
			if i == RepeatListN {
				break
			}
			sec1 += `<li>` + Esc(strings.ReplaceAll(p, h, "~")) + ` · subagent 讀了 ×` + strconv.Itoa(prof.reads.N[p]) + `</li>`
		}
		sec1 += `</ul>`
	} else {
		sec1 += `沒有（比對主對話委派前的 Read，與 subagent 的 Read、cat、sed）。`
	}
	sec1 += `</td></tr><tr><th>回傳結論` + refs.Cite(returnKeys) + `</th><td>`
	if hb != nil {
		hn := int64(countTokens(hb.text))
		sec1 += `回傳結論 <b>` + py.Comma(hn) + `</b> token` +
			`<details class="pp-big" style="--c:#bc8cff"><summary>點開看全文</summary>` +
			`<pre>` + shown(hb.text) + `</pre></details>`
		sec1 += `</td></tr><tr><th>回傳結論之後被重讀的量</th><td>` +
			`回傳結論 ` + py.Comma(hn) + ` token × 主對話之後的 ` + py.Comma(int64(hb.after)) + ` 次 API 呼叫` +
			` = <b>` + py.Comma(hn*int64(hb.after)) + `</b> token`
	} else {
		sec1 += `主對話 transcript 裡找不到回傳結論。` + `</td></tr><tr><th>回傳結論之後被重讀的量</th><td>` + `—`
	}
	sec1 += `</td></tr></table>`

	promptN := min(taskN, prof.firstCtx)
	ratio := "—"
	if prof.peak != 0 {
		ratio = "×" + py.F(float64(prof.sent)/float64(prof.peak), 1)
	}
	growth := `<p class="lede">第一次 API 呼叫` + refs.Cite(coldKeys) + `送進 <b>` + py.Comma(prof.firstCtx) + `</b> token，其中 task prompt ` +
		py.Comma(promptN) + `，其餘 ` + py.Comma(prof.firstCtx-promptN) + `（system、tools、CLAUDE.md、skills）。最大 context ` + py.Comma(prof.peak) +
		`，累計送出 ` + py.Comma(prof.sent) + ` token（最大的 ` + ratio + `）。</p>`

	markScripts(items)
	markLimits(items, false)
	var inTurn []*Call
	for _, cl := range calls {
		cp := *cl
		cp.InTurn = true
		inTurn = append(inTurn, &cp)
	}
	uses := mainToolUses(items)
	pick := pickTool(k.tool, uses, func() []any { return readRecs(path, true) })
	toolsHTML := toolsSection(toolGroups(uses, nil), nil, "這個 subagent 沒有呼叫工具。", false, pick)
	model := ""
	if v := ojson.Field(sub.Scan, "model"); v != nil {
		model = py.Str(v)
	}
	// ponytail: the subagent's own transcript has no turn row, so the card gets a Turn built from its calls
	card := &Turn{Model: model, CtxPeak: prof.peak, E2E: sub.num("secs")}
	for _, cl := range calls {
		card.Usage.In += cl.In
		card.Usage.Cr += cl.Cr
		card.Usage.Cw += cl.Cw
	}
	tc := tcCallsOf(readRecs(path, true))
	const scope = "這個 subagent"
	all := func(*Item) bool { return true }
	ctx := contentTabs([]pane{{"src", "依來源", catSection(items, c.total, "", msgsNote(c.total), 0, false)},
		{"cnt", "依次數", toolsHTML},
		{"api", "依 API", tcRender(tc, "subagent", growth+growthPane(inTurn, prof.compacts), apiSection(items, inTurn, 0, false, tc))}},
		ctxHead("", 0, items, inTurn, c), pick.tab())

	sec5 := refs.Section(append([]refs.Item{
		{Element: "回傳結論", Keys: returnKeys},
		{Element: "重複讀取的檔案、第一次 API 呼叫", Keys: coldKeys},
		{Element: "「最大的 " + strconv.Itoa(BigN) + " 段」的重送次數", Keys: resendKeys},
		{Element: "「最大的幾段」列幾段 = " + strconv.Itoa(BigN), Reason: refs.Cap},
		{Element: "重複讀取的檔案、依次數各列前幾列 = " + strconv.Itoa(RepeatListN), Reason: refs.Cap},
		{Element: "每種工具點開後列前幾次呼叫 = " + strconv.Itoa(CallsPerGrp), Reason: refs.Cap},
	}, numbersItems(c, true)...))

	turnCrumb := ""
	launchNo, launchN := 0, 0
	if ojson.Truthy(sub.Launch) {
		if n := launchTurn(parent, sub.Launch); n != 0 {
			shown := n
			if _, _, _, pm := itemsFor(parent, nil, n, false); pm != nil {
				if k := sessionTurnNo(parent, pm, n); k > 0 {
					shown = k
				}
			}
			launchNo, launchN = shown, n
			turnCrumb = ` ／ <a href="/pipeline?session=` + Esc(py.Head(sid, 8)) + `&turn=` + strconv.Itoa(n) + `">第 ` + strconv.Itoa(shown) + ` 回合</a>`
		}
	}
	s8 := Esc(py.Head(sid, 8))
	return pipelineCSS + crumbs(sessionCrumbs(parent, s8)+turnCrumb, "subagent") +
		`<h1 class="view">subagent：` + Esc(sub.Desc) + `</h1>` +
		agentLede(sub, model, hb, tc) + agentHead(sub, tc, agentFacts{prof.firstCtx, prof.peak, prof.sent, hbTok(hb), taskN, seenBefore, parentTurn(parent, sub.Launch), py.Head(parent, 8), launchNo, launchN}) +
		`<h2 class="nh" id="ctxw">Context Window</h2>` + pick.note("這個 subagent ", "可能已被壓縮") + ctx +
		fold("委派內容與回傳結論（點開看全文）", sec1) +
		fold("最貴的幾次呼叫", turnSummary(card, tc, c.real, nil, scope)) +
		fold("這個 subagent 撞到的問題（臨時腳本與工具上限，點編號看那次呼叫）", savingsSection("", all, card, items, tc, scope)) +
		fold("依據與門檻（顯示上限、重送上界、tokenizer 校正）", sec5)
}

// agentFacts are the numbers around one subagent that its headline tiles and findings quote.
type agentFacts struct {
	firstCtx, peak, sent int64
	back, task           int64
	reread               []string
	parent               *Turn
	sid                  string
	turnNo, turnN        int
}

func hbTok(hb *handback) int64 {
	if hb == nil {
		return -1
	}
	return int64(countTokens(hb.text))
}

// parentTurn is the main-conversation turn that launched the subagent, or nil.
func parentTurn(parent string, launch any) *Turn {
	n := launchTurn(parent, launch)
	if n == 0 {
		return nil
	}
	got := oneCached(parent, 0)
	if got == nil {
		return nil
	}
	if _, _, _, pm := itemsFor(parent, nil, n, false); pm != nil && pm.At != nil {
		for _, x := range got.Turns {
			if x.At == *pm.At {
				return x
			}
		}
	}
	return nil
}

// agentLede says in one sentence who did what for how much and what came back.
func agentLede(sub *Sub, model string, hb *handback, tc []*tcCall) string {
	secsV := sub.num("secs")
	kind := sub.Type
	if _, short, ok := strings.Cut(kind, ":"); ok {
		kind = short
	}
	who := Esc(kind)
	if m := shortModel(model); m != "" {
		who += "（" + Esc(m) + "）"
	}
	who = strings.TrimSpace(who)
	back := ""
	if n := hbTok(hb); n >= 0 {
		back = "，帶回主對話的結論是 " + py.Comma(n) + " token"
	}
	return `<p class="lede" title="` + Esc(sub.Type+" · "+model) + `">` + who + `做這件事花了 <b>` + money(sub.num("cost")) + `</b>、` + clockText(secsV) + back + `。</p>`
}

// agentHead: the subagent's tiles and findings, from the same scan the old summary used.
func agentHead(sub *Sub, tc []*tcCall, f agentFacts) string {
	total := 0.0
	for _, c := range tc {
		total += c.usd
	}
	secsV := sub.num("secs")
	costSub := "API 牌價換算"
	if f.parent != nil {
		if pc := turnCost(f.parent); pc > 0 {
			costSub = "佔第 " + strconv.Itoa(f.turnNo) + " 回合 " + pct0(sub.num("cost"), pc)
		}
	}
	backV, backSub := "—", "委派內容 "+py.Comma(f.task)+" token"
	if f.back >= 0 {
		backV = py.Comma(f.back)
	}
	tiles := hotTile(stats([]stat{
		{"花費", money(sub.num("cost")), costSub, "", "照 Anthropic API 牌價換算；訂閱方案不是這樣計費"},
		{"API 呼叫", strconv.Itoa(len(tc)) + " 次", "context 從 " + toks(pi(f.firstCtx)) + " 長到 " + toks(pi(f.peak)), "", strconv.Itoa(int(sub.num("calls"))) + " 次工具呼叫"},
		{"耗時", fmt.Sprintf("%d:%02d", int64(secsV)/60, int64(secsV)%60), "", "", secs(&secsV)}, // same form as the turn page's subagent table
		{"回傳結論 token", backV, backSub, "", ""},
	}), 0)
	var fs []nfind
	if len(f.reread) > 0 {
		names := make([]string, 0, 3)
		for _, p := range f.reread[:min(3, len(f.reread))] {
			names = append(names, Esc(filepath.Base(p)))
		}
		fs = append(fs, nfind{"hot", strconv.Itoa(len(f.reread)) + " 個", "主對話已經讀過的 " + strconv.Itoa(len(f.reread)) + " 個檔案（" + strings.Join(names, "、") + "），subagent 又讀了一次", "#ctxw", "看呼叫 ↓"})
	}
	if len(tc) > 0 && total > 0 {
		fs = append(fs, nfind{"", pct0(tc[0].usd, total), "第 1 次呼叫就花了 " + money(tc[0].usd) + "，佔這個 subagent 的 " + pct0(tc[0].usd, total) + "；一開場就送進 " + toks(pi(f.firstCtx)) + "（system、工具說明、CLAUDE.md、skill 清單）", "#ctxw", "看圖 ↓"})
		mx, at := 0.0, 0
		for i, c := range tc {
			if c.usd > mx {
				mx, at = c.usd, i
			}
		}
		if at != 0 {
			fs = append(fs, nfind{"", money(mx), "最貴的一次是第 " + strconv.Itoa(at+1) + " 次呼叫（" + money(mx) + "）", "#ctxw", "看依 API ↓"})
		}
	}
	if f.back > 0 && f.sent > 0 && len(fs) < 3 {
		fs = append(fs, nfind{"ok", "1/" + py.Comma(f.sent/f.back), "回傳的 " + py.Comma(f.back) + " token，只有它 " + strconv.Itoa(len(tc)) + " 次呼叫累計送出的 " + toks(pi(f.sent)) + " token 的 1/" + py.Comma(f.sent/f.back) + "；探索過程沒有進到主對話", "/pipeline?session=" + f.sid + "&turn=" + strconv.Itoa(f.turnN), "回第 " + strconv.Itoa(f.turnNo) + " 回合 →"})
	}
	var bars []nbar
	for i, c := range tc {
		bars = append(bars, nbar{"第" + strconv.Itoa(i+1) + "次", []float64{c.usd}, "第 " + strconv.Itoa(i+1) + " 次呼叫 " + money(c.usd), "", false, false})
	}
	chart := ""
	if len(bars) > 0 {
		chart = chartCard("每次 API 呼叫花多少", legend([2]string{"var(--accent)", "每次呼叫的花費"}),
			barSVG(bars, []string{"var(--accent)"}, moneyAxis, 0, max(1, len(bars)/6)))
	}
	return `<div class="hero">` + tiles + `</div>` + finds(fs) + chart
}

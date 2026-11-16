package dash

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"agentperform/internal/ojson"
	"agentperform/internal/prices"
	"agentperform/internal/py"
	"agentperform/internal/refs"
)

var ctxRanges = []struct {
	name   string
	lo, hi float64
}{{"不到視窗 20%", 0.0, 0.2}, {"視窗 20–40%", 0.2, ctxRatio}, {"視窗 40–60%", ctxRatio, 0.6}, {"超過視窗 60%", 0.6, 1e6}}

var delegateActionKeys = []string{"cc_delegate"}

func leverCard(key, title, badge, headline, action string, citeKeys []string, mod, details, tone, ba string) string {
	em := ""
	if badge != "" {
		em = `<em class="` + tone + `">` + Esc(badge) + `</em>`
	}
	s := `<section class="card lever" id="lever-` + key + `">` +
		`<div class="lvh"><b>` + Esc(title) + `</b>` + em + `</div>` +
		`<p class="lvl">事實：` + headline + `</p>` +
		`<p class="fix">做法：` + action + refs.Cite(citeKeys) + `</p>`
	if mod != "" {
		s += `<p class="mod">Mod：` + mod + `</p>`
	}
	if details == "" {
		details = "<p>這段期間沒有符合的回合可以展開。</p>"
	}
	s += ba + noteToggle(details, "細節") + `</section>`
	leverParts[key] = leverPart{title, badge, headline, action + refs.Cite(citeKeys), mod, details, ba, s}
	return s
}

// leverParts keeps every card's pieces so the topic pages can lay them out (redesign sample).
var leverParts = map[string]leverPart{}

type leverPart struct{ title, badge, headline, action, mod, details, ba, card string }

func sinceBar(since string) string {
	t := today()
	var quick [][2]string
	for _, m := range marks {
		quick = append(quick, [2]string{m[0], m[0] + " " + m[1]})
	}
	quick = append(quick, [2]string{dstr(t.AddDate(0, 0, -14)), "14 天前"})
	var links strings.Builder
	for _, q := range quick {
		on := ""
		if q[0] == since {
			on = " on"
		}
		links.WriteString(`<a class="rq` + on + `" href="/verify?since=` + q[0] + `">` + Esc(q[1]) + `</a>`)
	}
	return `<form class="rng" method="get" action="/verify" onchange="this.requestSubmit()">` +
		`<label>改動日<input type="date" name="since" value="` + Esc(since) + `"></label>` +
		`<span class="rqs">` + links.String() + `</span></form>`
}

func topSessions(picked []*Turn, noun string) string {
	count := py.NewCounter()
	for _, t := range picked {
		count.Add(t.Sid, 1)
	}
	if count.Len() == 0 {
		return ""
	}
	var links []string
	for _, sid := range count.MostCommon(3) {
		links = append(links, `<a href="/?session=`+Esc(sid)+`">`+Esc(sid)+`</a>（`+strconv.Itoa(count.N[sid])+` `+noun+`）`)
	}
	return "<p>集中在這幾個對話：" + strings.Join(links, "、") + "。</p>"
}

func bundleButton(section, text string) string {
	n := py.Len(text)
	return `<button class="ghost" data-bundle="` + section + `">複製給 AI 分析</button> ` +
		`<span class="dim">約 ` + py.Comma(int64(n)) + ` 字元、` + py.Comma(int64(n/3)) + ` token</span>`
}

// opusShare is the Opus share of the main-conversation spend over these turns.
func opusShare(ts []*Turn) float64 {
	tot, op := 0.0, 0.0
	for _, t := range ts {
		for _, k := range t.Models.keys {
			tot += t.Models.n[k]
		}
	}
	for _, t := range ts {
		for _, k := range t.Models.keys {
			if strings.Contains(k, "opus") {
				op += t.Models.n[k]
			}
		}
	}
	if tot == 0 {
		return 0
	}
	return op / tot
}

func skillCandidatesCard(sessions []*Session) string {
	order, groups := scriptRecords(sessions, func(*Session, float64) bool { return true })
	rows, once := scriptCandidates(order, groups)
	lastScripts = scriptsSummary{}
	if len(rows) == 0 {
		return ""
	}
	ids := map[string]bool{}
	single := 0
	for _, r := range rows {
		for _, i := range r.ids {
			ids[i] = true
		}
		if r.sessions == 1 {
			single++
		}
	}
	lastScripts = scriptsSummary{rows, len(ids), single, once}
	var trs []string
	for i, r := range rows {
		if i == 15 {
			break
		}
		var links []string
		for k, id := range r.ids {
			if k == 3 {
				break
			}
			links = append(links, `<a href="/?session=`+Esc(id)+`">`+Esc(py.Head(id, 6))+`</a>`)
		}
		trs = append(trs, `<tr><td class="mono" title="`+Esc(r.line)+`">`+Esc(r.shape)+`</td><td class="num">`+strconv.Itoa(r.sessions)+`</td>`+
			`<td class="num">`+strconv.Itoa(r.calls)+`</td><td class="num">`+toks(pi(r.tok))+`</td>`+
			`<td class="num">`+money(r.cost)+`</td><td>`+strings.Join(links, " ")+`</td></tr>`)
	}
	head := []string{"腳本類型（去掉參數後）", "對話數", "次數", "token", "估計花費", "對話"}
	return leverCard("scripts", "重複出現的臨時腳本", "未估算",
		strconv.Itoa(len(rows))+` 種腳本分布在 `+strconv.Itoa(len(ids))+` 個對話；其中 `+strconv.Itoa(single)+` 種只出現在單一對話，`+
			`這些呼叫佔全部的 <b>`+py.F(once*100, 0)+`%</b>`,
		bundleNote+" "+bundleButton("scripts", ScriptsBundle(sessions, nil)),
		nil, "", table(head, trs, "t2", "", nil)+scriptsCardDim, "", "")
}

func repeatCard(sessions []*Session, hp *ojson.Object) string {
	order, groups := repeatRecords(sessions, hp, func(*Session, float64) bool { return true })
	rows := repeatTypes(order, groups)
	lastRepeat = rows
	if len(rows) == 0 {
		return ""
	}
	var trs []string
	calls := 0
	for _, r := range rows {
		calls += r.calls
		var ex []string
		for _, e := range r.ex {
			ex = append(ex, Esc(py.Head(e[0].(string), 70))+" ×"+strconv.Itoa(e[1].(int)))
		}
		trs = append(trs, `<tr><td>`+Esc(r.label)+`</td><td class="num">`+strconv.Itoa(r.calls)+`</td><td class="num">`+strconv.Itoa(r.sessions)+`</td>`+
			`<td class="num">`+strconv.Itoa(r.turns)+`</td><td class="num">`+money(r.cost)+`</td><td class="num">`+toks(pi(r.tok))+`</td>`+
			`<td class="mono">`+strings.Join(ex, "<br>")+`</td></tr>`)
	}
	top := rows[0]
	return leverCard("repeat", "重複呼叫", "未估算",
		py.Comma(int64(calls))+` 次重複，分 `+strconv.Itoa(len(rows))+` 種；涉及回合花費最多的是「`+Esc(top.label)+`」`+
			`（`+strconv.Itoa(top.sessions)+` 個對話、`+money(top.cost)+`）`,
		bundleNote+" "+bundleButton("repeats", RepeatsBundle(sessions, hp)),
		nil, "", table([]string{"種類", "次數", "對話數", "回合", "涉及回合花費", "涉及回合 token", "例子（去掉路徑與數字）"}, trs, "t2", "", nil)+
			repeatCardDim, "", "")
}

// the repeat and script rows of the last LeversView, for the tools page tables and numbers.
var (
	lastRepeat  []repRow
	lastScripts scriptsSummary
)

type scriptsSummary struct {
	rows          []candRow
	sessions, one int
	oneShare      float64
}

type lever struct {
	usd  float64
	est  bool
	html string
}

// LeversView is the /levers page.
func LeversView(sessions []*Session, days int, since string) string {
	if since == "" {
		since = defaultSince()
	}
	for _, x := range sessions {
		fixed := FixedTokens(x.Path, CtxProbeCats)
		rate := prices.CacheRead(ctxMainModel(x))
		for _, t := range x.Turns {
			t.Sid, t.Fixed, t.FixedUSD = x.ID, fixed, float64(fixed)*rate/1e6
		}
	}
	var turns []*Turn
	for _, x := range sessions {
		turns = append(turns, x.Turns...)
	}
	var levers []lever

	// -- context length
	var rangeRows []string
	per := map[string]float64{}
	for _, b := range ctxRanges {
		var v []float64
		for _, t := range turns {
			w := t.Window
			if w == 0 {
				w = 1_000_000
			}
			if c := ctxOf(t); c != 0 && b.lo <= float64(c)/float64(w) && float64(c)/float64(w) < b.hi {
				v = append(v, t.Usage.Cost)
			}
		}
		cell := "—"
		if len(v) > 0 {
			s := 0.0
			for _, x := range v {
				s += x
			}
			per[b.name] = s / float64(len(v))
			cell = money(per[b.name])
		}
		rangeRows = append(rangeRows, `<tr><td>`+Esc(b.name)+`</td><td class="num">`+cell+`</td><td class="num dim">`+strconv.Itoa(len(v))+`</td></tr>`)
	}
	base := baseCost(sessions)
	topB := per[ctxRanges[len(ctxRanges)-1].name]
	mult := 0.0
	if base != 0 {
		mult = topB / base
	}
	if mult != 0 {
		rangeRows[len(rangeRows)-1] = strings.ReplaceAll(rangeRows[len(rangeRows)-1], "</tr>", `<td class="num" style="color:var(--hot)">`+py.F(mult, 0)+` 倍</td></tr>`)
	}
	for i := 0; i < len(rangeRows)-1; i++ {
		rangeRows[i] = strings.ReplaceAll(rangeRows[i], "</tr>", "<td></td></tr>")
	}
	var big []*Turn
	for _, t := range turns {
		if ctxBig(t) {
			big = append(big, t)
		}
	}
	ctxUSD := waste(turns, base, nil).ctx
	levers = append(levers, lever{ctxUSD, true, leverCard("ctx", "context 太長", "浪費 "+money(ctxUSD),
		`<b>`+strconv.Itoa(len(big))+` 個回合</b>（`+py.F(float64(len(big))/float64(len(turns))*100, 0)+`%）的 context 超過視窗 40%，`+
			`比<span title="一般回合＝context 在 150–300k 的回合，每回合花費的中位數；`+
			`150–300k 取自 Thariq 所說 context rot 起點之前的區間">一般回合</span>`+
			`多花 <b>`+money(ctxUSD)+`</b>`,
		"換到無關的工作用 <code>/clear</code>，舊 context 之後每則訊息都要重讀。",
		[]string{"cc_clear", "thariq_rot", "ctx_eng"}, "條件列的 <code>/clear</code>、「context 已用 N%」",
		table([]string{"context 佔視窗", "每回合花費", "回合數", ""}, rangeRows, "t2", "", nil)+topSessions(big, "個回合"),
		"hotv", baRow(turns, since, []clause{
			shareClause("超過視窗 40% 的回合佔比", ctxBig, nil),
			moneyClause("這些回合的每回合花費", meanCost(ctxBig))}))})

	// -- delegation
	m := tmCached()
	hasM := ojson.Obj(m).Len() > 0
	deleUSD := 0.0
	if hasM {
		deleUSD = max(0.0, fnum(m, "per")*fnum(m, "n"))
	}
	badge, head := "", "還沒有可推估的委派紀錄"
	if hasM {
		badge = "估省 " + money(deleUSD)
		head = `委派 <b>` + numOf(ojson.Field(m, "n")).str() + ` 次</b>，每次淨省 ` + money(fnum(m, "per")) + `（一般值），合計約 <b>` + money(deleUSD) + `</b>`
	}
	levers = append(levers, lever{deleUSD, hasM, leverCard("delegate", "委派", badge, head,
		"主對話 context 已經很大、之後還有很多回合的工作，委派給 subagent；幾步就能做完的，主對話自己做。",
		delegateActionKeys, "「已委派」", costTable(sessions)+delegateDetail,
		"hotv", baRow(turns, since, []clause{shareClause("委派的回合佔比", func(t *Turn) bool { return t.Dele > 0 }, nil)}))})

	// -- model
	type mb struct {
		n    int
		cost float64
		ctx  []int64
	}
	byModel := map[string]*mb{}
	var mOrder []string
	for _, t := range turns {
		if t.Models == nil {
			continue
		}
		for _, mdl := range t.Models.keys {
			b := byModel[mdl]
			if b == nil {
				b = &mb{}
				byModel[mdl] = b
				mOrder = append(mOrder, mdl)
			}
			b.n++
			b.cost += t.Usage.Cost / float64(len(t.Models.keys))
			b.ctx = append(b.ctx, ctxOf(t))
		}
	}
	totM := 0.0
	for _, k := range mOrder {
		totM += byModel[k].cost
	}
	if totM == 0 {
		totM = 1
	}
	sorted := append([]string{}, mOrder...)
	sort.SliceStable(sorted, func(i, j int) bool { return byModel[sorted[i]].cost > byModel[sorted[j]].cost })
	var mRows strings.Builder
	for _, k := range sorted {
		b := byModel[k]
		c := append([]int64{}, b.ctx...)
		sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
		mRows.WriteString(`<tr><td class="mono">` + Esc(modelLabel(k)) + `</td>` +
			`<td class="num">` + py.Comma(int64(b.n)) + `</td>` +
			`<td class="num">` + money(b.cost) + `</td>` +
			`<td class="num dim">` + py.F(b.cost/totM*100, 1) + `%</td>` +
			`<td class="num dim">` + toks(pi(c[len(c)/2])) + `</td>` +
			`</tr>`)
	}
	mixed := 0
	for _, x := range sessions {
		ks := map[string]bool{}
		for _, t := range x.Turns {
			if t.Models != nil {
				for _, k := range t.Models.keys {
					ks[k] = true
				}
			}
		}
		if len(ks) > 1 {
			mixed++
		}
	}
	topM := ""
	for _, k := range mOrder {
		if topM == "" || byModel[k].cost > byModel[topM].cost {
			topM = k
		}
	}
	mHead := "沒有模型資料"
	if topM != "" {
		mHead = `花費最高的模型是 <code>` + Esc(topM) + `</code>，` + money(byModel[topM].cost) +
			`（` + py.F(byModel[topM].cost/totM*100, 0) + `%）· ` + strconv.Itoa(mixed) + `/` + strconv.Itoa(len(sessions)) + ` 個對話混用多個模型`
	}
	levers = append(levers, lever{0, false, leverCard("model", "模型選擇", "未估算", mHead,
		"Sonnet 能處理多數寫程式工作且比 Opus 便宜，Opus 留給複雜架構決策與多步推理；簡單工作用 /effort 調低。",
		[]string{"cc_model", "cc_effort"}, "模型×effort 選單",
		table([]string{"模型", "回合", "花費", "佔比", "context 一般"}, []string{mRows.String()}, "t2", "", nil)+modelDetail,
		"warnv", baRow(turns, since, []clause{
			{"Opus 佔花費", opusShare, func(v float64) string { return py.Pct(v, 1) }, nil},
			moneyClause("每回合花費", costPerTurn)}))})

	// -- killed for taking too long
	var slowT []*Turn
	slowSess := map[string]bool{}
	slowCost := 0.0
	for _, x := range sessions {
		for _, t := range x.Turns {
			if killedSlow(t) {
				slowSess[x.ID] = true
			}
		}
	}
	for _, t := range turns {
		if killedSlow(t) {
			slowT = append(slowT, t)
			slowCost += t.Usage.Cost
		}
	}
	var slowHead, slowDet, sBadge, sTone string
	if len(slowT) > 0 {
		var waits []float64
		worst := slowT[0]
		for _, t := range slowT {
			waits = append(waits, t.E2E)
			if t.Usage.Cost > worst.Usage.Cost {
				worst = t
			}
		}
		sort.Float64s(waits)
		mid, top := waits[len(waits)/2], waits[len(waits)-1]
		slowHead = `<b>` + strconv.Itoa(len(slowT)) + ` 個回合</b>做太久被你中止，花費共 <b>` + money(slowCost) + `</b>`
		slowDet = `<p>佔全部回合 ` + py.F(float64(len(slowT))/float64(len(turns))*100, 1) + `%，分布在 ` + strconv.Itoa(len(slowSess)) + ` 個對話；` +
			`一般等了 ` + secs(&mid) + `、最久 ` + secs(&top) + `。` +
			`花費最高的回合 ` + money(worst.Usage.Cost) + `，` +
			`你打的字開頭是「` + Esc(py.Head(worst.Prompt, 24)) + `」。</p>` +
			`<p>認定條件：回合裡出現「Request interrupted by user」，而且從你送出到中止超過 ` +
			strconv.Itoa(slowKillSec) + ` 秒。不到 1 分鐘的中止不列入，多半是你改變主意；` +
			`超過 ` + strconv.Itoa(slowKillMax/60) + ` 分鐘的也不列入，多半是對話開著沒關。</p>`
		sBadge, sTone = "浪費 "+money(slowCost), "hotv"
	} else {
		slowHead = `近 ` + strconv.Itoa(days) + ` 天沒有等超過 ` + strconv.Itoa(slowKillSec) + ` 秒被你中止的回合`
		sTone = "okv"
	}
	levers = append(levers, lever{slowCost, true, leverCard("slow", "做太久被中止", sBadge, slowHead,
		"具體的要求只讀最少的檔案，模糊的要求會觸發大範圍掃描；方向不對就按 Escape、用 /rewind。",
		[]string{"cc_specific", "cc_complex"}, "", slowDet+topSessions(slowT, "個回合"), sTone,
		baRow(turns, since, []clause{
			shareClause("被中止的回合佔比", killedSlow, nil),
			moneyClause("被中止回合的花費（每回合平均）", func(ts []*Turn) float64 {
				s := 0.0
				for _, t := range ts {
					if killedSlow(t) {
						s += t.Usage.Cost
					}
				}
				return s / float64(len(ts))
			})}))})

	// -- cache expiry
	var rw []*Turn
	var rwTok int64
	for _, t := range turns {
		if t.Rewrite > 0 {
			rw = append(rw, t)
			rwTok += t.RewriteTok
		}
	}
	rwUSD := rewriteUSD(turns)
	allCost := 0.0
	for _, t := range turns {
		allCost += t.Usage.Cost
	}
	if allCost == 0 {
		allCost = 1
	}
	n1h, n5m := 0, 0
	for _, t := range rw {
		if t.TTL == ttl1h {
			n1h++
		}
		if t.TTL == ttl5m {
			n5m++
		}
	}
	rBadge, rTone := "", "okv"
	rHead := `近 ` + strconv.Itoa(days) + ` 天沒有 Cache 過期後重寫的回合`
	if len(rw) > 0 {
		rBadge, rTone = "重寫 "+money(rwUSD), "hotv"
		rHead = `<b>` + strconv.Itoa(len(rw)) + ` 個回合</b>開始時 Cache 已過期，第一次 API 呼叫重寫了 ` + toks(pi(rwTok)) + ` token，` +
			`比照 cache 讀價多花 <b>` + money(rwUSD) + `</b>（佔花費 ` + py.F(rwUSD/allCost*100, 1) + `%）`
	}
	levers = append(levers, lever{rwUSD, true, leverCard("cache", "Cache 過期重寫", rBadge, rHead,
		"長時間離開前先收尾；換到無關的工作用 <code>/clear</code>，它不花錢，<code>/compact</code> 本身是大請求。",
		[]string{"cc_cache_miss", "cc_compact"}, "Cache 倒數",
		`<p>判定：回合開始距上一個 API 呼叫超過該對話的 Cache 存活時間（1 小時或 5 分鐘），`+
			`而且第一次 API 呼叫的 cache 寫大於 cache 讀、至少 `+py.Comma(rewriteMin)+` token。`+
			`金額＝那次 cache 寫 token ×（cache 寫價 − cache 讀價）。</p>`+
			`<p>其中存活時間 1 小時的 `+strconv.Itoa(n1h)+` 個，`+
			`5 分鐘的 `+strconv.Itoa(n5m)+` 個。</p>`+topSessions(rw, "個回合"),
		rTone, baRow(turns, since, []clause{
			shareClause("Cache 過期重寫的回合佔比", func(t *Turn) bool { return t.Rewrite > 0 }, nil),
			moneyClause("每回合重寫金額", func(ts []*Turn) float64 { return rewriteUSD(ts) / float64(len(ts)) })}))})

	// -- fixed load
	bySess := map[string]int64{}
	var bsOrder []string
	fixedUSD := 0.0
	for _, t := range turns {
		if t.Fixed != 0 {
			if _, ok := bySess[t.Sid]; !ok {
				bsOrder = append(bsOrder, t.Sid)
			}
			bySess[t.Sid] = t.Fixed
		}
		fixedUSD += t.FixedUSD
	}
	usual := pi(0)
	if len(bsOrder) > 0 {
		var vals []int64
		for _, k := range bsOrder {
			vals = append(vals, bySess[k])
		}
		usual = median(vals)
	}
	biggest := ""
	for _, k := range bsOrder {
		if biggest == "" || bySess[k] > bySess[biggest] {
			biggest = k
		}
	}
	fBadge, fHead := "", "這段期間沒有回合"
	if len(turns) > 0 {
		fBadge = "每回合重讀 " + money(fixedUSD)
		fHead = `每個對話第 1 回合一般就載入 <b><span title="各對話第 1 回合裡 Skill / subagent / MCP 清單、` +
			`SessionStart hook 輸出、CLAUDE.md / rules / memory 的 token 合計，取 ` + strconv.Itoa(len(bsOrder)) + ` 個對話的中位數">` +
			toks(pi(int64(math.RoundToEven(usual.v)))) + ` token</span></b>，之後每回合都重讀；` + py.Comma(int64(len(turns))) + ` 個回合照 cache 讀價約 <b>` + money(fixedUSD) + `</b>`
	}
	fDet := fixedDetailA
	if biggest != "" {
		fDet += ` 最大的是對話 <a href="/pipeline?session=` + Esc(biggest) + `&turn=1">` + Esc(biggest) + `</a>` +
			`（` + toks(pi(bySess[biggest])) + ` token）。`
	}
	fDet += `</p>`
	levers = append(levers, lever{fixedUSD, true, leverCard("fixed", "每回合固定載入", fBadge, fHead,
		"用 <code>/mcp</code> 關掉沒在用的 server；CLAUDE.md 開場就載入，專用流程移到 skill 按需載入，控制在 200 行內。",
		[]string{"cc_mcp", "cc_claude_md", "ctx_eng"}, "", fDet,
		"warnv", baRow(turns, since, []clause{
			{"固定載入 token（對話一般）", func(ts []*Turn) float64 {
				m := map[string]int64{}
				var o []string
				for _, t := range ts {
					if t.Fixed != 0 {
						if _, ok := m[t.Sid]; !ok {
							o = append(o, t.Sid)
						}
						m[t.Sid] = t.Fixed
					}
				}
				if len(o) == 0 {
					return 0
				}
				var vals []int64
				for _, k := range o {
					vals = append(vals, m[k])
				}
				return median(vals).v
			}, func(v float64) string { return toks(pi(int64(math.RoundToEven(v)))) }, nil},
			moneyClause("每回合固定載入金額", func(ts []*Turn) float64 {
				s := 0.0
				for _, t := range ts {
					s += t.FixedUSD
				}
				return s / float64(len(ts))
			})}))})

	levers = append(levers, lever{0, false, repeatCard(sessions, health())})
	levers = append(levers, lever{0, false, skillCandidatesCard(sessions)})
	var kept []lever
	for _, l := range levers {
		if l.html != "" {
			kept = append(kept, l)
		}
	}
	isWaste := func(l lever) bool {
		return strings.Contains(l.html, `id="lever-ctx"`) || strings.Contains(l.html, `id="lever-slow"`)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if isWaste(a) != isWaste(b) {
			return isWaste(a)
		}
		if a.usd != b.usd {
			return a.usd > b.usd
		}
		return a.est && !b.est
	})
	wAll := waste(turns, base, nil)
	lede := ""
	if len(kept) > 0 {
		lede = `<p class="lede">近 ` + strconv.Itoa(days) + ` 天 ` + strconv.Itoa(len(sessions)) + ` 個對話 · ` +
			`浪費 <b>` + money(wAll.total) + `</b>` +
			`（context 太長 ` + money(wAll.ctx) + `，做太久被中止 ` + money(wAll.slow) + `，Cache 過期重寫 ` + money(wAll.cache) + `），` +
			`其他卡片的金額不計入浪費</p>`
	}
	b4, af, pct, n1, n2 := beforeAfter(turns, since, costPerTurn)
	preT, postT, _ := baWindows(turns, since)
	why := daysShort(preT, postT, since)
	if why == "" {
		why = "前 " + strconv.Itoa(n1) + " / 後 " + strconv.Itoa(n2) + " 回合"
	}
	if b4 != nil {
		lede += `<p class="lede">以 ` + since + ` 為界，每回合花費（含委派）` + money(*b4) + ` → ` + money(*af)
		if pct != nil {
			lede += `（` + py.SignedF(*pct, 0) + `%）`
		}
		lede += ` · 回合數 ` + py.Comma(int64(n1)) + ` / ` + py.Comma(int64(n2)) + `</p>`
	} else {
		lede += `<p class="lede">以 ` + since + ` 為界，資料不足（` + why + `）</p>`
	}
	var body strings.Builder
	for _, l := range kept {
		body.WriteString(l.html)
	}
	return `<div class="crumb"><a href="/">← 總覽</a></div>` +
		`<h1 class="view">改善與驗證 · ` + strconv.Itoa(len(kept)) + ` 項</h1>` + lede +
		sinceBar(since) + `<div class="sec">依金額排序</div>` + body.String() +
		`<div class="sec">` + refs.Title + `</div>` + refs.Section(leverRefItems())
}

// leverRefs is each card's sources and, where the card uses a home-made filter, that threshold.
func leverRefs() map[string]refs.Item {
	return map[string]refs.Item{
		"ctx":      {Element: "「context 太長」卡片", Keys: []string{"cc_clear", "thariq_rot", "ctx_eng"}, Reason: "150–300k 基準取 Thariq 所說 context rot 起點之前的區間。"},
		"delegate": {Element: "「委派」卡片", Keys: delegateActionKeys},
		"model":    {Element: "「模型選擇」卡片", Keys: []string{"cc_model", "cc_effort"}},
		"slow":     {Element: "「做太久被中止」卡片", Keys: []string{"cc_specific", "cc_complex"}, Reason: strconv.Itoa(slowKillSec) + " 秒與 " + strconv.Itoa(slowKillMax/60) + " 分鐘是自訂篩選。"},
		"cache":    {Element: "「Cache 過期重寫」卡片", Keys: []string{"cc_cache_miss", "cc_compact"}, Reason: "第一次 API 呼叫至少寫入 " + py.Comma(rewriteMin) + " token 才計入，是自訂篩選。"},
		"fixed":    {Element: "「每回合固定載入」卡片", Keys: []string{"cc_mcp", "cc_claude_md", "ctx_eng"}},
		"verify":   {Element: "「改動前後」", Keys: []string{"cc_baseline"}, Reason: "前後各取同樣天數（最多 30 天），任一邊少於 " + strconv.Itoa(minTurns) + " 個回合就不比較。"},
	}
}

func leverRefItems() []refs.Item {
	m := leverRefs()
	var out []refs.Item
	for _, k := range []string{"ctx", "delegate", "model", "slow", "cache", "fixed", "verify"} {
		out = append(out, m[k])
	}
	return out
}

// ScriptsBundle is the plain-text bundle of the repeated-scripts section.
func ScriptsBundle(sessions []*Session, skills []skillInfo) string {
	if skills == nil {
		skills = installedSkills()
	}
	order, groups := scriptRecords(sessions, func(*Session, float64) bool { return true })
	all, once := scriptCandidates(order, groups)
	var rows []candRow
	for _, r := range all {
		if r.sessions >= bundleMinSessions {
			rows = append(rows, r)
		}
	}
	if len(rows) > bundleMaxRows {
		rows = rows[:bundleMaxRows]
	}
	out := []string{
		"# 重複出現的臨時腳本：資料與原始樣本",
		"",
		"## 請你做的事",
		"下面是我在 Claude Code 對話裡，AI 為了某件事臨時寫的腳本（ad-hoc script），依「去掉參數後的樣子」分組的統計，" +
			"附上每組的原始樣本與我已安裝的 skill 清單。請你：",
		"1. 判斷每一組腳本在做什麼；",
		"2. 判斷哪些值得抽成 skill、哪些改用現成工具就好、哪些不用處理；",
		"3. 已有名稱或描述相近的 skill、卻沒被載入的，說明可能的原因；",
		"4. 給具體建議（要做什麼、為什麼），並指出你的判斷依據是哪一筆資料。",
		"以下只有事實，沒有結論。",
		"",
		"## 欄位與算法",
		"- 範圍：Claude Code 儀表板目前時間範圍內的所有對話，含主對話與 subagent 寫的 Bash／腳本呼叫。",
		"- 分組鍵 shape：把腳本去掉參數、路徑、ID、字串後的樣子。呼叫 Jira／Confluence／Slack／GitLab／GitHub／" +
			"Elasticsearch／BigQuery 等 API 的，記成 `API|服務|端點`（端點中的 `*` 代表 ID）；" +
			"其他記成 `語言|匯入的模組或第一個指令`。",
		"- sessions：出現這組腳本的對話數；calls：呼叫次數。",
		"- tokens：寫出腳本（約字元數 / 4）加上腳本輸出（約字元數 / 4）。",
		"- cost：寫出腳本按 output 單價、腳本輸出第一次進 context 按 input 單價估算，不含之後每回合重讀。",
		"- 每筆樣本附「該對話載入過的 skill」；最後附上已安裝 skill 清單與 SKILL.md 建立時間，請自行對照有沒有該用卻沒用的 skill。",
		"- 只列至少出現在 " + strconv.Itoa(bundleMinSessions) + " 個對話的組，最多 " + strconv.Itoa(bundleMaxRows) + " 組；依 sessions × tokens 排序。" +
			"只出現在單一對話的呼叫佔全部的 " + py.F(once*100, 0) + "%。",
		"- 樣本：表格前 " + strconv.Itoa(sampleGroups) + " 組各附最多 3 筆（優先不同對話），每筆最多 1,500 字元，已遮蔽 token、金鑰、密碼。",
		"",
		"## 表格",
		"| shape | sessions | calls | tokens | cost(USD) |",
		"|---|---|---|---|---|",
	}
	for _, r := range rows {
		out = append(out, "| "+bCell(r.shape)+" | "+strconv.Itoa(r.sessions)+" | "+strconv.Itoa(r.calls)+" | "+strconv.FormatInt(r.tok, 10)+" | "+py.F(r.cost, 3)+" |")
	}
	out = append(out, "", "## 原始樣本")
	for i, r := range rows {
		if i == sampleGroups {
			break
		}
		out = append(out, "\n### "+strconv.Itoa(i+1)+". "+r.shape)
		for _, x := range r.samples {
			loaded := strings.Join(loadedSkills(x.s), "、")
			if loaded == "" {
				loaded = "無"
			}
			out = append(out, "\n對話 "+py.Head(x.s.ID, 8)+" · "+bAt(x.s, x.turn)+" · "+py.Str(orNil(ojson.Field(x.rec, "lang"), ""))+" · 該對話載入過的 skill："+loaded)
			code := ojson.GetOr(x.rec, "code")
			if code == nil {
				code = orNil(ojson.Field(x.rec, "line"), "")
			}
			out = append(out, bFence(py.Str(code)))
		}
	}
	return strings.Join(append(out, "", skillsBlock(skills)), "\n") + "\n"
}

// RepeatsBundle is the plain-text bundle of the repeated-calls section.
func RepeatsBundle(sessions []*Session, hp *ojson.Object) string {
	order, groups := repeatRecords(sessions, hp, func(*Session, float64) bool { return true })
	var rows []repRow
	for _, r := range repeatTypes(order, groups) {
		if r.sessions >= bundleMinSessions {
			rows = append(rows, r)
		}
	}
	if len(rows) > bundleMaxRows {
		rows = rows[:bundleMaxRows]
	}
	out := []string{
		"# 重複呼叫：資料與原始樣本",
		"",
		"## 請你做的事",
		"下面是我在 Claude Code 對話裡，AI 在同一個對話中重複做的呼叫（同樣的指令、工具、檔案、網址），分類統計加上原始樣本。請你：",
		"1. 判斷每一種重複發生的原因；",
		"2. 判斷哪些是正常的、哪些是浪費；",
		"3. 對浪費的給具體改法（改提示、改流程、加 skill 或 hook 等），並指出判斷依據是哪一筆資料。",
		"以下只有事實，沒有結論。",
		"",
		"## 欄位與算法",
		"- 範圍：Claude Code 儀表板目前時間範圍內的所有對話。",
		"- 判定規則：跟同一個對話的前 3 個回合比，同一個指令、skill／MCP 工具、檔案或網址又出現，或寫入內容高度相似，記為一次重複。" +
			"「同一個 API 反覆呼叫」「輪詢」歸在「反覆確認服務或呼叫同一個 API」；錯誤後重試沒有資料可判斷，不列。",
		"- type：依重複的內容比對指令字樣分出的種類（只是歸類，不是判斷好壞）。",
		"- calls：重複的呼叫次數；sessions：涉及的對話數；turns：涉及的回合數。",
		"- cost、tokens：涉及回合的整個回合花費與 token（不是重複那一次呼叫本身），所以是上限。",
		"- 只列至少出現在 " + strconv.Itoa(bundleMinSessions) + " 個對話的種類；依 cost 排序。",
		"- 樣本：每種最多 3 組重複內容（出現最多的），每組列最多 3 次出現的對話與時間；內容已去除敏感資訊。",
		"",
		"## 表格",
		"| type | calls | sessions | turns | cost(USD) | tokens |",
		"|---|---|---|---|---|---|",
	}
	for _, r := range rows {
		out = append(out, "| "+r.label+" | "+strconv.Itoa(r.calls)+" | "+strconv.Itoa(r.sessions)+" | "+strconv.Itoa(r.turns)+" | "+py.F(r.cost, 2)+" | "+strconv.FormatInt(r.tok, 10)+" |")
	}
	out = append(out, "", "## 原始樣本")
	for _, r := range rows {
		out = append(out, "\n### "+r.label)
		occ := append([]string{}, r.occOrder...)
		sort.SliceStable(occ, func(i, j int) bool { return len(r.occ[occ[i]]) > len(r.occ[occ[j]]) })
		for k, detail := range occ {
			if k == 3 {
				break
			}
			g := r.occ[detail]
			out = append(out, "\n重複內容（"+strconv.Itoa(len(g))+" 次）：")
			body := "（內容相似，沒有單一字串）"
			if detail != "" {
				body = py.Head(detail, 1500)
			}
			out = append(out, bFence(body))
			var where []string
			for i, x := range g {
				if i == 3 {
					break
				}
				where = append(where, py.Head(x.s.ID, 8)+" "+bAt(x.s, x.turn)+"（第 "+strconv.Itoa(x.turn)+" 回合）")
			}
			out = append(out, "出現於："+strings.Join(where, "；"))
		}
	}
	return strings.Join(out, "\n") + "\n"
}

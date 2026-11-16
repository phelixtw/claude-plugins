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
	"agentperform/internal/toollimits"
)

type daySlot struct {
	u        Usage
	turns    int
	sessions map[string]bool
	tl       []*Turn
	bySess   map[string][]*Turn
	bySessK  []string
}

// Overview is the / page.
func Overview(sessions []*Session, days int, rngFrom, rngTo string) string {
	perDay := map[string]*daySlot{}
	var dayOrder []string
	for _, s := range sessions {
		for _, t := range s.Turns {
			key := day(t.At)
			slot := perDay[key]
			if slot == nil {
				slot = &daySlot{sessions: map[string]bool{}, bySess: map[string][]*Turn{}}
				perDay[key] = slot
				dayOrder = append(dayOrder, key)
			}
			slot.u.add(t.Usage)
			slot.turns++
			slot.sessions[s.ID] = true
			slot.tl = append(slot.tl, t)
			if _, ok := slot.bySess[s.ID]; !ok {
				slot.bySessK = append(slot.bySessK, s.ID)
			}
			slot.bySess[s.ID] = append(slot.bySess[s.ID], t)
		}
	}
	var total Usage
	for _, k := range dayOrder {
		total.add(perDay[k].u)
	}
	byDayDele := delegationByDay(sessions)
	dayDeles := newDayDeleMap()
	for _, d := range byDayDele.keys {
		if perDay[d] == nil {
			continue
		}
		v := &dayDele{}
		rows := byDayDele.m[d]
		for _, sid := range rows.keys {
			v.cost += rows.m[sid].cost
			v.n += rows.m[sid].n
		}
		dayDeles.set(d, v)
	}
	dCost := 0.0
	for _, d := range dayDeles.keys {
		dCost += dayDeles.m[d].cost
	}
	var allT []*Turn
	for _, x := range sessions {
		allT = append(allT, x.Turns...)
	}
	base := baseCost(sessions)
	wAll := waste(allT, base, nil)
	allCost := total.Cost + dCost

	keys := append([]string{}, dayOrder...)
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	rowsByMon := map[string][]string{}
	for _, key := range keys {
		slot := perDay[key]
		u, n := slot.u, slot.turns
		rowsByMon[key[:7]] = append(rowsByMon[key[:7]], `<tr onclick="location='/?day=`+key+`'">`+
			`<td class="mono">`+Esc(key)+`</td>`+
			costCell(u.Cost, dayDeles.get(key))+
			`<td class="num">`+strconv.Itoa(n)+`</td>`+
			hitTagCell(hitRate(warmUsage(slot.tl, sessions)), rewriteUSD(slot.tl), u.Cost)+
			wasteCell(waste(slot.tl, base, nil), u.Cost)+`</tr>`)
	}
	dayHead := []string{"日期", "花費", "回合數", "Tag", "可以省的"}
	monthHead := func(mkey string, mdays []string) string {
		cost := 0.0
		segs := 0
		for _, d := range mdays {
			dc := 0.0
			if dd := dayDeles.get(d); dd != nil {
				dc = dd.cost
			}
			cost += perDay[d].u.Cost + dc
			segs += len(perDay[d].sessions)
		}
		return `<b>` + Esc(mkey) + `</b><span class="dim">` + strconv.Itoa(len(mdays)) + ` 天 · ` +
			money(cost) + ` · ` + strconv.Itoa(segs) + ` 個對話</span>`
	}
	monSet := map[string]bool{}
	for _, k := range dayOrder {
		monSet[k[:7]] = true
	}
	var months []string
	for m := range monSet {
		months = append(months, m)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(months)))
	var blocks [][3]string // one sortable table per month, latest open
	for _, m := range months {
		var mdays []string
		for _, d := range dayOrder {
			if d[:7] == m {
				mdays = append(mdays, d)
			}
		}
		blocks = append(blocks, [3]string{m, monthHead(m, mdays), table(dayHead, rowsByMon[m], "click", "", nil)})
	}
	dayTable := monthAccordion(blocks)

	allKeys := append([]string{}, dayOrder...)
	sort.Strings(allKeys)
	loKey := dstr(Now())
	if len(allKeys) > 0 {
		loKey = allKeys[0]
	}
	hiKey := loKey
	if len(allKeys) > 0 {
		hiKey = allKeys[len(allKeys)-1]
	}
	dFrom, dTo := loKey, hiKey
	if rngFrom != "" {
		dFrom = maxStr(rngFrom, loKey)
	}
	if rngTo != "" {
		dTo = minStr(rngTo, hiKey)
	}
	var keysIn []string
	for _, d := range allKeys {
		if dFrom <= d && d <= dTo {
			keysIn = append(keysIn, d)
		}
	}
	if len(keysIn) == 0 {
		keysIn = allKeys
	}
	byMonth := len(keysIn) > 45
	var markOrder []string
	byLabel := map[string][]string{}
	for _, m := range marks {
		lbl := m[0][5:]
		if byMonth {
			lbl = m[0][:7]
		}
		if _, ok := byLabel[lbl]; !ok {
			markOrder = append(markOrder, lbl)
		}
		byLabel[lbl] = append(byLabel[lbl], m[1])
	}
	var mk [][2]string
	for _, l := range markOrder {
		mk = append(mk, [2]string{l, strings.Join(byLabel[l], "；")})
	}
	keysSet := map[string]bool{}
	for _, k := range keysIn {
		keysSet[k] = true
	}
	cpt, tok := map[string][]float64{}, map[string][]float64{}
	tokInt := map[string][]int64{}
	for _, x := range sessions {
		for _, t := range x.Turns {
			d := day(t.At)
			if keysSet[d] {
				m := d
				if byMonth {
					m = d[:7]
				}
				cpt[m] = append(cpt[m], t.Usage.Cost)
				tok[m] = append(tok[m], float64(ctxOf(t)))
				tokInt[m] = append(tokInt[m], ctxOf(t))
			}
		}
	}
	pctOptOf := func(src map[string][]float64, ints bool) *ojson.Object {
		var ms []string
		for m := range src {
			ms = append(ms, m)
		}
		sort.Strings(ms)
		var labels []string
		for _, m := range ms {
			if byMonth {
				labels = append(labels, m)
			} else {
				labels = append(labels, m[5:])
			}
		}
		series := map[int][]any{}
		for _, q := range []int{50, 90} {
			var vals []any
			for _, m := range ms {
				v := pctAt(src[m], float64(q))
				if ints {
					vals = append(vals, int64(*v))
				} else {
					vals = append(vals, *v)
				}
			}
			series[q] = vals
		}
		return pctOpt(labels, series, mk)
	}
	metricCombo := comboChart([]comboEntry{
		{"花費/回合（一般／偏高）", "money", pctOptOf(cpt, false)},
		{"最大 context/回合（一般／偏高）", "tok", pctOptOf(tok, true)},
	})
	mainTok, subTok := tokenSplit(sessions)
	active, wall := activeSplit(sessions)
	launches := 0
	for _, x := range sessions {
		launches += len(windowLaunches(x))
	}
	spare := func(v float64) string { return `<span title="` + money(v) + `">` + money0(v) + `</span>` }
	nSessions := py.Comma(int64(len(sessions)))
	ks := subagentKinds(sessions)
	tileRows := []stat{
		{"花費", money0(allCost), nSessions + " 個對話", "", "精確值 " + money(allCost) + "（主對話 " + money(total.Cost) + "、委派 " + money(dCost) + "）；總 token " + toks(pi(mainTok+subTok))},
		{"可以省的", money0(wAll.total), "佔花費 " + pct0(wAll.total, allCost) + mostlyContext(wAll.total, wAll.ctx+rewriteUSD(allT)), "", "精確值 " + money(wAll.total) + "：context 太長 " + money(wAll.ctx) + "＋做太久被中止 " + money(wAll.slow) + "＋Cache 過期重寫 " + money(wAll.cache) + "；是上限估計"},
		{"實際作業", py.Comma(int64(math.Round(active/3600))) + " 小時", "掛著 " + py.Comma(int64(math.Round(wall/3600))) + " 小時", "", activeStatTip},
		{"委派", money0(dCost), py.Comma(int64(launches)) + " 次，佔 " + pct0(dCost, allCost), "/delegate", "精確值 " + money(dCost) + "；token " + toks(pi(subTok))},
	}
	tiles := hotTile(stats(tileRows), 1)
	ctxNow, ctxPrev := ctxTrend(allT, base)
	ctxAll := wAll.ctx + rewriteUSD(allT)
	var fs []nfind
	if ctxPrev > 0 {
		fs = append(fs, nfind{"hot", py.SignedF((ctxNow/ctxPrev-1)*100, 0) + "%", "<b>Context</b>：近 30 天因 context 多花的錢，比前 30 天" + map[bool]string{true: "多 ", false: "少 "}[ctxNow >= ctxPrev] + py.F(math.Abs(ctxNow/ctxPrev-1)*100, 0) + "%；" + strconv.Itoa(days) + " 天共 " + money0(ctxAll) + "，佔可以省的 " + pct0(ctxAll, wAll.total), "/context", "去 Context →"})
	}
	if k := costliestKind(ks); k != nil {
		fs = append(fs, nfind{"", money0(k.cost), "<b>委派</b>：" + Esc(k.kind) + " " + py.Comma(int64(k.n)) + " 次共花 " + money0(k.cost) + "，平均每次 " + money(k.cost/float64(max(k.n, 1))) + "，是最貴的 subagent 類型", "/delegate", "去委派 →"})
	}
	lede := `<p class="lede">近 ` + strconv.Itoa(days) + ` 天花了 ` + spare(allCost) + `，其中 <b>` + spare(wAll.total) + `（` + pct0(wAll.total, allCost) + `）</b>是可以省的。</p>`
	return `<h1 class="view">總覽</h1>` + lede + `<div class="hero">` + tiles + `</div>` +
		findsUnder("這期最值得看的 3 件事", fs) +
		chartCard("每回合花費與 context 趨勢", "", rangeBar(dFrom, dTo)+metricCombo) +
		`<h2 class="nh" id="list">每日一覽</h2><div class="nlist">` + dayTable + `</div>`
}

// fixedLoad is what every conversation loads before the first prompt: the median size, what re-reading it
// costs over all turns (tokens x cache-read price, once per turn, same as the Context page), and how many conversations have one.
func fixedLoad(sessions []*Session) (med string, usd float64, n int) {
	var fixed []int64
	for _, x := range sessions {
		if f := FixedTokens(x.Path, CtxProbeCats); f != 0 {
			fixed = append(fixed, f)
			for range x.Turns {
				usd += float64(f) * prices.CacheRead(ctxMainModel(x)) / 1e6
			}
		}
	}
	med = "0"
	if len(fixed) > 0 {
		med = toks(median(fixed))
	}
	return med, usd, len(fixed)
}

// mostlyContext says where the savable money sits when more than half of it is context.
func mostlyContext(total, ctx float64) string {
	if total > 0 && ctx*2 > total {
		return "，大多在 Context"
	}
	return ""
}

// costliestKind is the subagent type that cost the most.
func costliestKind(ks []*kindStat) *kindStat {
	var best *kindStat
	for _, k := range ks {
		if best == nil || k.cost > best.cost {
			best = k
		}
	}
	return best
}

func maxStr(a, b string) string {
	if a > b {
		return a
	}
	return b
}

func minStr(a, b string) string {
	if a < b {
		return a
	}
	return b
}

// DayView is the /?day= page.
func DayView(sessions []*Session, d string) string { return dayLens(sessions, d, "") }

// dayLens is DayView with the focus lens applied ("" = none).
func dayLens(sessions []*Session, d, focus string) string {
	lo, hi := daySpan(d)
	hp := health()
	hps := ojson.Field(hp, "sessions")
	dele := delegationByDay(sessions).m[d]
	base := baseCost(sessions)
	type rowT struct {
		w    float64
		html string
		hit  bool
	}
	lensN := 0
	type metaT struct {
		id    string
		cost  float64
		turns []*Turn
		w     wasteT
	}
	var rows []rowT
	var metas []metaT
	var total Usage
	byStart := append([]*Session{}, sessions...)
	sort.SliceStable(byStart, func(i, j int) bool { return byStart[i].Start > byStart[j].Start })
	for _, s := range byStart {
		var picked []*Turn
		for _, t := range s.Turns {
			if lo <= t.At && t.At < hi {
				picked = append(picked, t)
			}
		}
		if len(picked) == 0 {
			continue
		}
		var u Usage
		for _, t := range picked {
			u.add(t.Usage)
		}
		total.add(u)
		recap := ""
		for i := len(s.Recaps) - 1; i >= 0; i-- {
			if s.Recaps[i].At < hi {
				recap = s.Recaps[i].Text
				break
			}
		}
		topic := s.Opening
		if isCmdOutput(topic) {
			topic = "（指令輸出）"
		}
		if topic == "（背景工作通知）" {
			topic = "（第一句是背景通知）"
		}
		if topic == "" {
			topic = "—"
		}
		if recap != "" {
			topic = "/recap：" + recap
		}
		repRaw := ojson.Obj(ojson.Field(hps, s.ID))
		w := waste(picked, base, repRaw)
		rep := remapRepeats(s, repRaw)
		inDay := map[*Turn]bool{}
		for _, t := range picked {
			inDay[t] = true
		}
		repeated := 0
		fl, _ := ojson.Field(rep, "flagged").([]any)
		for _, n := range fl {
			if i := int(num(n)); i >= 1 && i <= len(s.Turns) && inDay[s.Turns[i-1]] {
				repeated++
			}
		}
		flagAttr := ""
		if w.total > 0 {
			flagAttr = ` class="warn"`
		}
		metas = append(metas, metaT{s.ID, u.Cost, picked, w})
		tags := sessionTags(picked, w, repeated, u.Cost)
		if tags == "" {
			tags = "—"
		}
		topicShort := py.Head(topic, 120)
		if py.Len(topic) > 120 {
			topicShort += "…"
		}
		dd := dele.get(s.ID)
		hits := 0
		for t := range lensTurns(focus, s, base) {
			if lo <= t.At && t.At < hi {
				hits++
			}
		}
		lensN += hits
		rows = append(rows, rowT{w.total, `<tr id="s` + s.ID + `"` + flagAttr + ` onclick="location='/?session=` + s.ID + `'">` +
			`<td class="mono">` + Esc(s.ID) + `</td>` +
			`<td class="ts">` + fromTimestamp(picked[0].At).Format("15:04") + `</td>` +
			`<td class="dim">` + Esc(s.Proj) + `</td>` +
			`<td class="mdlc">` + modelTag(sessionModels(picked)) + `</td>` +
			costCell(u.Cost, dd) +
			`<td class="num">` + strconv.Itoa(len(picked)) + `</td>` +
			`<td class="prob">` + tags + `</td>` +
			wasteCell(w, u.Cost) +
			`<td class="memo"><span class="topic" title="` + Esc(topic) + `">` + Esc(orStr(cleanOpening(topicShort), "—")) + `</span></td>` +
			`<td class="num"><button class="forget" data-sid="` + Esc(s.ID) + `" title="刪除這筆工作紀錄">×</button></td></tr>`, hits > 0})
	}
	var dayTurns []*Turn
	for _, m := range metas {
		dayTurns = append(dayTurns, m.turns...)
	}
	big := 0
	for _, t := range dayTurns {
		if ctxBig(t) {
			big++
		}
	}
	dDay := 0.0
	if dele != nil {
		for _, id := range dele.keys {
			dDay += dele.m[id].cost
		}
	}
	wDay := waste(dayTurns, base, nil)
	var worst *metaT
	for i := range metas {
		if worst == nil || metas[i].w.total > worst.w.total {
			worst = &metas[i]
		}
	}
	spendDay := total.Cost + dDay
	d5 := d[5:]
	lede := `<p class="lede">` + d5 + ` 花了 ` + money(spendDay)
	if wDay.total > 0 {
		lede += `，其中 <b>` + money(wDay.total) + `</b> 可以省`
		if name, v := wasteMain(wDay); v > 0 {
			lede += `；可以省的有 ` + pct0(v, wDay.total) + ` 來自 ` + name + `（` + money(v) + `）`
		}
	}
	lede += `。</p>`
	wTip := ""
	if total.Cost != 0 {
		wTip = `可以省的佔主對話花費的 ` + py.F(wDay.total/total.Cost*100, 0) + `%：context 太長 ` + money(wDay.ctx) + `，做太久被中止 ` + money(wDay.slow) + `，Cache 過期重寫 ` + money(wDay.cache) + `。`
	}
	tileRows := []stat{
		{"花費", money(spendDay), `主對話 ` + money(total.Cost) + ` · 委派 ` + money(dDay), "", "委派只算這天發生的；跨天的對話，其他天的委派算在那一天。"},
		{"可以省的", money(wDay.total), `佔這天 ` + pct0(wDay.total, spendDay), "", wTip},
		{"context 超過 40%", strconv.Itoa(big) + ` 個`, strconv.Itoa(len(dayTurns)) + ` 個回合裡`, "", "context 超過視窗 40%＝送出去的最大一次 API 呼叫，超過模型視窗大小的 40%（" + pct0(float64(big), float64(len(dayTurns))) + "）。"},
	}
	hero := hotTile(stats(tileRows), 1)
	daySet := map[string]bool{}
	for _, x := range sessions {
		for _, t := range x.Turns {
			daySet[day(t.At)] = true
		}
	}
	var allDays []string
	for k := range daySet {
		allDays = append(allDays, k)
	}
	sort.Strings(allDays)
	var navParts []string
	prev, next := "", ""
	for i := len(allDays) - 1; i >= 0; i-- {
		if allDays[i] < d {
			prev = allDays[i]
			break
		}
	}
	for _, x := range allDays {
		if x > d {
			next = x
			break
		}
	}
	if prev != "" {
		navParts = append(navParts, `<a href="/?day=`+prev+`">← 前一天 `+prev+`</a>`)
	}
	if next != "" {
		navParts = append(navParts, `<a href="/?day=`+next+`">後一天 → `+next+`</a>`)
	}
	navParts = append(navParts, `<a href="/context">改善方法 →</a>`)
	body := empty("這天沒有資料。")
	if len(rows) > 0 {
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].w > rows[j].w })
		var lr []lrow
		for _, r := range rows {
			lr = append(lr, lrow{r.html, r.hit})
		}
		head := []string{"id", "當天第一回合", "專案", "模型", "花費", "回合數", "Tag", "可以省的"}
		body = lensBar(lensN, "回合", "/?day="+d, focus) + lensTable(append(head, "開場句", "刪除"), lr, "click", "", focus)
	}
	// page template: findings, a chart of each session's spend, the table
	type sc struct {
		id         string
		main, dele float64
		w          float64
	}
	var per []sc
	wSum := 0.0
	for _, m := range metas {
		dl := 0.0
		if dd := dele.get(m.id); dd != nil {
			dl = dd.cost
		}
		per = append(per, sc{m.id, m.cost, dl, m.w.total})
		wSum += m.w.total
	}
	var fs []nfind
	if worst != nil && wSum > 0 {
		fs = append(fs, nfind{"hot", pct0(worst.w.total, wSum), "這天可以省的錢有 " + pct0(worst.w.total, wSum) + " 在同一個對話 <b>" + Esc(worst.id) + "</b>（" + money(worst.w.total) + "），它最大的 context 長到 " + toks(pi(maxCtx(worst.turns))), "/?session=" + worst.id, "去這個對話 →"})
	}
	sort.SliceStable(per, func(i, j int) bool { return per[i].dele > per[j].dele })
	if len(per) > 0 && per[0].dele > 0 && dDay > 0 {
		fs = append(fs, nfind{"", money(per[0].dele), "<b>" + Esc(per[0].id) + "</b> 的委派就花了 " + money(per[0].dele) + "，佔這天委派花費的 " + pct0(per[0].dele, dDay) + "；沒有列入「可以省的」項目，花費集中在 subagent", "#s" + per[0].id, "看對話 ↓"})
	}
	sort.SliceStable(per, func(i, j int) bool { return per[i].main+per[i].dele > per[j].main+per[j].dele })
	if len(per) > 1 && total.Cost+dDay > 0 {
		two := per[0].main + per[0].dele + per[1].main + per[1].dele
		fs = append(fs, nfind{"", pct0(two, total.Cost+dDay), strconv.Itoa(len(per)) + " 個對話裡，最貴的 2 個（" + Esc(per[0].id) + "、" + Esc(per[1].id) + "）佔這天花費的 " + pct0(two, total.Cost+dDay), "#chart", "看圖 ↓"})
	}
	var bars []nbar
	for _, p := range per {
		bars = append(bars, nbar{p.id, []float64{p.main, p.dele}, p.id + " 主對話 " + money(p.main) + " · 委派 " + money(p.dele), "/?session=" + p.id, false, false})
	}
	chart := chartCard("這天每個對話花多少", legend([2]string{"var(--accent)", "主對話"}, [2]string{"var(--mid)", "委派"}),
		barSVG(bars, []string{"var(--accent)", "var(--mid)"}, moneyAxis, 0, 1))
	return crumbs(`<a href="/">總覽</a>`, d5) +
		`<h1 class="view">` + d5 + ` 這一天</h1>` + lede +
		`<p class="dim">` + strings.Join(navParts, " · ") + `</p>` +
		`<div class="hero">` + hero + `</div>` + finds(fs) + chart +
		listCard("這天的對話", body) +
		fold("怎麼省", `<ul><li>context 超過視窗 40% 就該 <code>/clear</code> 或開新對話；這天 `+strconv.Itoa(big)+` 個這樣的回合就多花了 `+money(wDay.ctx)+`。<div class="src">Claude Code 文件：Manage context proactively</div></li></ul>`) +
		fold("說明", `<p>花費含這天發生的委派；跨天的對話，其他天的委派算在那一天。可以省的＝context 太長、做太久被中止、Cache 過期重寫三項多花的錢加總。</p>`)
}

type diagItem struct {
	amt            float64
	title, lever   string
	keys           []string
	jump, why, fix string
}

// diagnose is the page's answer to "was this any good, and if not, what do I change".
func diagnose(turns []*Turn, w wasteT, base float64, dRow *ojson.Object, rep *ojson.Object, loud float64) string {
	cost := 0.0
	for _, t := range turns {
		cost += t.Usage.Cost
	}
	var items []diagItem
	var bigIdx []int
	ctxN := 0
	for i, t := range turns {
		if ctxBig(t) {
			bigIdx = append(bigIdx, i+1)
		}
		if ctxOf(t) != 0 {
			ctxN++
		}
	}
	listed := listedKinds(w, rep, loud)
	jumps := func(ns []int, n int) string {
		var b strings.Builder
		for i, x := range ns {
			if i == n {
				break
			}
			b.WriteString(`<a class="jump" href="#t` + strconv.Itoa(x) + `">#` + strconv.Itoa(x) + `</a>`)
		}
		return b.String()
	}
	if listed["ctx"] && len(bigIdx) > 0 {
		jump := jumps(bigIdx, 8)
		if len(bigIdx) > 8 {
			jump += `<span class="dim">另有 ` + strconv.Itoa(len(bigIdx)-8) + ` 個</span>`
		}
		items = append(items, diagItem{w.ctx, "context 太長", "ctx", []string{"cc_clear"}, jump,
			strconv.Itoa(len(bigIdx)) + ` / ` + strconv.Itoa(ctxN) + ` 個回合的 context 超過視窗 40%。` +
				`以 150–300k 回合的一般花費 ` + money(base) + `／回合計算，` +
				`這些回合多花了 ` + money(w.ctx) + `。`,
			`換到無關的工作就 /clear，舊 context 之後每則訊息都要重讀。`})
	}
	var killed []int
	for i, t := range turns {
		if killedSlow(t) {
			killed = append(killed, i+1)
		}
	}
	if listed["slow"] && len(killed) > 0 {
		var waited []float64
		for _, n := range killed {
			waited = append(waited, turns[n-1].E2E)
		}
		sort.Float64s(waited)
		mid, top := waited[len(waited)/2], waited[len(waited)-1]
		items = append(items, diagItem{w.slow, "做太久被中止", "slow", []string{"cc_specific", "cc_complex"}, jumps(killed, 6),
			strconv.Itoa(len(killed)) + ` / ` + strconv.Itoa(len(turns)) + ` 個回合等太久被你中止，` +
				`一般等了 ` + secs(&mid) + `、` +
				`最久 ` + secs(&top) + `，花費共 ` + money(w.slow) + `。` +
				`（等超過 ` + strconv.Itoa(slowKillMax/60) + ` 分鐘的不列入，` +
				`多半是對話開著沒關。）`,
			`具體的要求只讀最少的檔案；方向不對就按 Escape、用 /rewind。`})
	}
	if listed["rep"] {
		fl, _ := ojson.Field(rep, "flagged").([]any)
		var ns []int
		for _, x := range fl {
			ns = append(ns, int(num(x)))
		}
		share, _ := py.Number(ojson.Field(rep, "share"))
		items = append(items, diagItem{0, "重複呼叫", "", []string{"agentdiet"}, jumps(ns, 6),
			strconv.Itoa(len(fl)) + ` / ` + numOf(ojson.Field(rep, "turns")).str() + ` 個回合判成重複呼叫` +
				`（` + py.F(share*100, 1) + `%，跟前 3 個回合比對）。` +
				`重跑的內容會留在 context 裡，之後每次 API 呼叫都要再讀一次。`, ""})
	}
	m := tmCached()
	per := fnum(m, "per")
	if ojson.Obj(m).Len() > 0 && per >= deleMin && dRow.Len() == 0 && cost >= 5 {
		items = append(items, diagItem{0, "沒有委派", "delegate", []string{"cc_delegate"}, "",
			`token 模型推估每次委派淨省 ` + money(per) +
				`（` + numOf(ojson.Field(m, "n")).str() + ` 次委派的一般值，所有對話合計、不分工作種類），` +
				`這個對話沒有委派。`,
			`探索過程不需要留在主對話 context 的工作可以委派。` +
				`這個推估是上限：token 模型假設主對話自己做也要跑一樣多次 API 呼叫。`})
	}
	if ojson.Obj(m).Len() > 0 && per <= 0 && dRow.Len() > 0 && fnum(dRow, "cost") >= 5 {
		items = append(items, diagItem{0, "委派淨虧", "delegate", []string{"cc_delegate"}, "",
			`token 模型推估每次委派淨虧 ` + money(-per) + `（一般值），` +
				`這個對話的委派花了 ` + money(fnum(dRow, "cost")) + `。`,
			`subagent 的 API 呼叫次數越多，subagent 自身花費越高，` +
				`會超過主對話省下的重讀費。`})
	}
	share := 0.0
	if cost != 0 {
		share = w.total / cost
	}
	if len(items) == 0 {
		return `<span id="waste"></span>` // anchor only: nothing found is not shown
	}
	head := `<div class="vhead"><span>主對話花費 ` + money(cost) + ` 裡 ` + money(w.total) +
		`（` + py.F(share*100, 0) + `%）屬於下列項目</span></div>`
	sort.SliceStable(items, func(i, j int) bool { return items[i].amt > items[j].amt })
	var body strings.Builder
	for _, it := range items {
		body.WriteString(`<div class="vitem"><div class="vt"><b>` + Esc(it.title) + `</b>`)
		if it.amt > 0 {
			body.WriteString(`<em>` + money(it.amt) + `</em>`)
		}
		body.WriteString(it.jump + `</div><p>` + it.why + `</p>`)
		if it.fix != "" {
			body.WriteString(`<p class="fix">` + it.fix + refs.Cite(it.keys) + `</p>`)
		} else {
			body.WriteString(refs.Cite(it.keys))
		}
		if it.lever != "" {
			body.WriteString(`<p><a href="` + leverHref(it.lever) + `">這類問題的改善方法 →</a></p>`)
		}
		body.WriteString(`</div>`)
	}
	note := diagnoseNoteA + money(base) + diagnoseNoteB
	return `<div class="verdict" id="waste">` + head + body.String() + noteToggle(note, "「浪費」怎麼算") + `</div>`
}

var _ = toollimits.Limits

// wasteMain names the biggest part of the savable money and how much it is.
func wasteMain(w wasteT) (string, float64) {
	name, v := "context 太長", w.ctx
	if w.cache > v {
		name, v = "Cache 過期重寫", w.cache
	}
	if w.slow > v {
		name, v = "做太久被中止", w.slow
	}
	return name, v
}

// maxCtx is the largest context any of these turns sent.
func maxCtx(turns []*Turn) int64 {
	var m int64
	for _, t := range turns {
		m = max(m, ctxOf(t))
	}
	return m
}

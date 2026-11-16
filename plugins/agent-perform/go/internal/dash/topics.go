package dash

// The five topic pages (Context, tools, delegation, model, before/after) that replace the levers page.
// Cards are rendered by LeversView exactly as before; their parts are laid out here.

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"agentperform/internal/py"
	"agentperform/internal/toollimits"
)

type topicDef struct {
	title string
	cards []string
}

var topics = map[string]topicDef{
	"context":  {"Context", []string{"ctx", "cache", "fixed"}},
	"tools":    {"其他 Tag", []string{"repeat", "scripts"}},
	"delegate": {"委派", []string{"delegate"}},
	"model":    {"模型", []string{"model"}},
	"verify":   {"改動前後", []string{"ctx", "slow", "delegate", "cache", "fixed", "model"}},
}

// leverHref is the topic page that shows lever card k; the first page in
// nav order wins, and before/after holds the rest.
func leverHref(k string) string {
	for _, page := range []string{"context", "tools", "delegate", "model"} {
		for _, c := range topics[page].cards {
			if c == k {
				return "/" + page + "#lever-" + k
			}
		}
	}
	return "/verify#lever-" + k
}

func last30() []string {
	t := Now()
	var out []string
	for i := 29; i >= 0; i-- {
		out = append(out, t.AddDate(0, 0, -i).Format("2006-01-02"))
	}
	return out
}

func allTurnsOf(ss []*Session) []*Turn {
	var out []*Turn
	for _, s := range ss {
		out = append(out, s.Turns...)
	}
	return out
}

// topicParts is what differs between topic pages; TopicView lays every page out in the same order.
type topicParts struct {
	lede  string
	tiles []stat
	hot   []int // tiles that get the hot tone
	fs    []nfind
	chart string
	title string   // heading of the one table
	table string   // its card body
	cards []string // lever cards: a 怎麼省 bullet and a fold each
	extra []string // more folds, after the card folds
}

func TopicView(sessions []*Session, days int, since, key, focus string) string {
	def := topics[key]
	LeversView(sessions, days, since) // fills leverParts with the same numbers the old page shows
	turns := allTurnsOf(sessions)
	base := baseCost(sessions)
	if key == "verify" {
		return `<h1 class="view">` + Esc(def.title) + `</h1>` + VerifyBody(turns, since, def.cards)
	}
	mainSpend, deleSpend := spend(sessions)
	total := mainSpend + deleSpend
	w := waste(turns, base, nil)
	var pg topicParts
	switch key {
	case "context":
		pg = contextPage(sessions, turns, base, total, w, days)
	case "tools":
		pg = toolsPage(sessions, turns, days)
	case "delegate":
		pg = delegatePage(sessions, turns, since, total, days)
	case "model":
		pg = modelPage(sessions, turns, since, days)
	}
	var folds strings.Builder
	for _, f := range pg.extra {
		folds.WriteString(f)
	}
	if len(pg.cards) > 0 {
		folds.WriteString(`<h2 class="nh">怎麼省</h2>`)
	}
	for _, k := range pg.cards {
		if p, ok := leverParts[k]; ok {
			folds.WriteString(cardFold(k, p))
		}
	}
	hero := ""
	if len(pg.tiles) > 0 {
		hero = `<div class="hero">` + hotTile(stats(pg.tiles), pg.hot...) + `</div>`
	}
	return `<h1 class="view">` + Esc(def.title) + `</h1>` + pg.lede + hero + finds(pg.fs) + pg.chart +
		listCard(pg.title, pg.table) + folds.String()
}

// cardFold is one 怎麼省 row: the advice as the summary; the card's facts, numbers and details inside.
func cardFold(k string, p leverPart) string {
	advice, body := p.action, `<p class="lvl">事實：`+p.headline+`</p>`
	if p.mod != "" {
		body += `<p class="mod">Mod：` + p.mod + `</p>`
	}
	if r := leverRefs()[k].Reason; r != "" {
		body += `<p class="mod">門檻：` + Esc(r) + `</p>`
	}
	return foldID("lever-"+k, `<b>`+Esc(p.title)+`</b>：`+advice, body+p.ba+p.details)
}

// howFold is the 怎麼省 fold: a few bullets, the doc each comes from under it.
func howFold(bullets [][2]string) string {
	if len(bullets) == 0 {
		return ""
	}
	var b strings.Builder
	for _, x := range bullets {
		b.WriteString(`<li>` + x[0] + `<div class="src">Claude Code 文件：` + x[1] + `</div></li>`)
	}
	return fold("怎麼省", `<ul>`+b.String()+`</ul>`)
}

// spend is the main-conversation cost and the delegation cost over these sessions (delegations only on days that have turns).
func spend(sessions []*Session) (mainCost, deleCost float64) {
	has := map[string]bool{}
	for _, s := range sessions {
		for _, t := range s.Turns {
			has[day(t.At)] = true
			mainCost += t.Usage.Cost
		}
	}
	by := delegationByDay(sessions)
	for _, d := range by.keys {
		if !has[d] {
			continue
		}
		rows := by.m[d]
		for _, sid := range rows.keys {
			deleCost += rows.m[sid].cost
		}
	}
	return mainCost, deleCost
}

// convRow is one conversation row: id chip, opening, project, then the page's own cells.
func convRow(s *Session, cells ...string) string {
	open := py.Head(cleanOpening(s.Opening), 28)
	if open != cleanOpening(s.Opening) {
		open += "…"
	}
	return `<tr id="s` + s.ID + `" onclick="location='/?session=` + s.ID + `'"><td><span class="chip">` + Esc(s.ID) + `</span> ` + Esc(open) + `</td>` +
		`<td class="dim">` + Esc(s.Proj) + `</td>` + strings.Join(cells, "") + `</tr>`
}

// moneyAxis labels a chart axis: whole dollars.
func moneyAxis(v float64) string {
	if v == 0 {
		return "$0"
	}
	return money0(v)
}

func numCell(v string) string { return `<td class="num">` + v + `</td>` }

// barCell is a money cell with a bar as wide as v is of max (60px at most).
func barCell(txt string, v, max float64) string {
	w := 0
	if max > 0 {
		w = int(math.Round(60 * v / max))
	}
	return `<td class="num">` + txt + `<span class="hbar" style="width:` + strconv.Itoa(w) + `px"></span></td>`
}

// convTable shows the first 10 rows and the rest under 「看全部 N 個對話 →」.
func convTable(head []string, rows []string) string {
	const top = 10
	out := table(head, rows[:min(top, len(rows))], "click", "", nil)
	if len(rows) > top {
		out += `<details class="nfold more"><summary>看全部 ` + py.Comma(int64(len(rows))) + ` 個對話 →</summary>` + table(head, rows[top:], "click", "", nil) + `</details>`
	}
	return out
}

// bySess ranks sessions by v, biggest first, dropping nothing.
func bySess[T any](sessions []*Session, v func(*Session) T, key func(T) float64) ([]*Session, map[*Session]T) {
	m := map[*Session]T{}
	out := append([]*Session{}, sessions...)
	for _, s := range out {
		m[s] = v(s)
	}
	sort.SliceStable(out, func(i, j int) bool { return key(m[out[i]]) > key(m[out[j]]) })
	return out, m
}

func contextPage(sessions []*Session, turns []*Turn, base, total float64, w wasteT, days int) topicParts {
	rw := rewriteUSD(turns)
	cw := w.ctx + rw
	nBig, nRw := 0, 0
	for _, t := range turns {
		if ctxBig(t) {
			nBig++
		}
		if t.Rewrite != 0 {
			nRw++
		}
	}
	fMed, fUSD, _ := fixedLoad(sessions)
	pg := topicParts{cards: []string{"ctx", "cache", "fixed"}, hot: []int{0, 1}}
	pg.lede = `<p class="lede">近 ` + strconv.Itoa(days) + ` 天，context 讓你多花了 <b>` + money0(cw) + `</b>，佔總花費 ` + money0(total) + ` 的 ` + pct0(cw, total) + `。</p>`
	pg.tiles = []stat{
		{"context 太長", money0(w.ctx), py.Comma(int64(nBig)) + " 個回合超過視窗 40%", "", "精確值 " + money(w.ctx)},
		{"Cache 過期重寫", money0(rw), py.Comma(int64(nRw)) + " 個回合開始時 Cache 已過期", "", "精確值 " + money(rw)},
		{"每回合固定載入", fMed, "token；每回合重讀，共 " + money0(fUSD), "", "精確值 " + money(fUSD)},
		{"佔總花費", pct0(cw, total), "總覽的「可以省的」有 " + pct0(min(cw, w.total), w.total) + " 在這頁", "", ""},
	}
	last := last30()
	byDay := map[string][]*Turn{}
	for _, t := range turns {
		byDay[day(t.At)] = append(byDay[day(t.At)], t)
	}
	var bars []nbar
	dayW := map[string]float64{}
	for _, d := range last {
		wc, wr := waste(byDay[d], base, nil).ctx, rewriteUSD(byDay[d])
		dayW[d] = wc + wr
		bars = append(bars, nbar{d[5:], []float64{wc, wr}, d + " context 太長 " + money(wc) + " · Cache 過期重寫 " + money(wr), "/?day=" + d, false, false})
	}
	type sv struct {
		ctx, rw, v float64
		big        int
	}
	ranked, per := bySess(sessions, func(s *Session) sv {
		c, r := waste(s.Turns, base, nil).ctx, rewriteUSD(s.Turns)
		n := 0
		for _, t := range s.Turns {
			if ctxBig(t) {
				n++
			}
		}
		return sv{c, r, c + r, n}
	}, func(x sv) float64 { return x.v })
	top5 := 0.0
	for i := 0; i < 5 && i < len(ranked); i++ {
		top5 += per[ranked[i]].v
	}
	sorted := append([]string{}, last...)
	sort.SliceStable(sorted, func(i, j int) bool { return dayW[sorted[i]] > dayW[sorted[j]] })
	peak, sum30 := 0.0, 0.0
	for i, d := range sorted {
		if i < 3 {
			peak += dayW[d]
		}
		sum30 += dayW[d]
	}
	a30, p30 := ctxTrend(turns, base)
	if p30 > 0 {
		pg.fs = append(pg.fs, nfind{"hot", py.SignedF((a30/p30-1)*100, 0) + "%", "近 30 天比前 30 天" + map[bool]string{true: "多", false: "少"}[a30 >= p30] + "花 " + money0(math.Abs(a30-p30)) + "（" + money0(a30) + " 對 " + money0(p30) + "）", "#chart", "看每日 ↓"})
	}
	if len(ranked) > 0 && cw > 0 {
		t := ranked[0]
		pg.fs = append(pg.fs, nfind{"", pct0(top5, cw), "集中在 5 個對話，最多的是 <b>" + Esc(t.ID) + "</b>（" + money0(per[t].v) + "，" + strconv.Itoa(len(t.Turns)) + " 個回合裡 " + strconv.Itoa(per[t].big) + " 個超過 40%）", "#s" + t.ID, "看對話 ↓"})
	}
	if sum30 > 0 {
		pg.fs = append(pg.fs, nfind{"", money0(peak), sorted[0][5:] + "、" + sorted[1][5:] + "、" + sorted[2][5:] + " 三天就佔了近 30 天的 " + pct0(peak, sum30), "#chart", "看那幾天 ↓"})
	}
	pg.chart = chartCard("每天多花多少（近 30 天）", legend([2]string{"var(--hot)", "context 太長"}, [2]string{"var(--mid)", "Cache 過期重寫"}),
		barSVG(bars, []string{"var(--hot)", "var(--mid)"}, moneyAxis, 0, 3))
	var rows []string
	maxV := 0.0
	if len(ranked) > 0 {
		maxV = per[ranked[0]].v
	}
	for _, s := range ranked {
		x := per[s]
		rows = append(rows, convRow(s, barCell(money0(x.v), x.v, maxV), numCell(money0(x.ctx)), numCell(money0(x.rw)), numCell(strconv.Itoa(x.big)+" / "+strconv.Itoa(len(s.Turns)))))
	}
	pg.title = "哪些對話最貴"
	pg.table = convTable([]string{"對話", "專案", "多花", "context 太長", "Cache 過期", "超過 40% 的回合"}, rows)
	return pg
}

func toolsPage(sessions []*Session, turns []*Turn, days int) topicParts {
	lim := map[string]int{}
	limTurns, limAll := 0, 0
	for _, t := range turns {
		n := 0
		for _, c := range []*py.Counter{t.Limits, t.SubLimits} {
			if c == nil {
				continue
			}
			for _, k := range c.Keys {
				lim[k] += c.N[k]
				n += c.N[k]
			}
		}
		limAll += n
		if n > 0 {
			limTurns++
		}
	}
	reps, repCalls := lastRepeat, 0
	for _, r := range reps {
		repCalls += r.calls
	}
	sc := lastScripts
	blocked := pollGateBlocks()
	pg := topicParts{hot: []int{0}}
	pg.lede = `<p class="lede">近 ` + strconv.Itoa(days) + ` 天有 <b>` + py.Comma(int64(repCalls)) + ` 次</b>重複呼叫、` + py.Comma(int64(limAll)) + ` 次撞到上限，另外寫了 ` + py.Comma(int64(len(sc.rows))) + ` 種臨時腳本。這些多跑的趟數換算不出準確金額，所以沒有算進總覽的「可以省的」。</p>`
	pg.tiles = []stat{
		{"重複呼叫", py.Comma(int64(repCalls)) + " 次", "分 " + strconv.Itoa(len(reps)) + " 種", "", ""},
		{"撞到工具上限", py.Comma(int64(limAll)) + " 次", py.Comma(int64(limTurns)) + " 個回合", "", ""},
		{"臨時腳本", py.Comma(int64(len(sc.rows))) + " 種", "分布在 " + py.Comma(int64(sc.sessions)) + " 個對話", "", ""},
		{"前景輪詢被擋下", py.Comma(int64(blocked)) + " 次", "每次都被要求改用背景執行", "/how#hooks", ""},
	}
	if len(reps) > 0 {
		top := reps[0]
		pg.fs = append(pg.fs, nfind{"hot", money0(top.cost), "最多的一種重複是「" + Esc(top.label) + "」：" + py.Comma(int64(top.calls)) + " 次、" + strconv.Itoa(top.sessions) + " 個對話，涉及的回合花了 " + money0(top.cost), "#list", "看種類 ↓"})
	}
	if len(sc.rows) > 0 {
		pg.fs = append(pg.fs, nfind{"", pct0(float64(sc.one), float64(len(sc.rows))), py.Comma(int64(len(sc.rows))) + " 種臨時腳本裡，" + py.Comma(int64(sc.one)) + " 種只在一個對話出現過；這種「寫完就丟」的呼叫佔全部的 " + py.F(sc.oneShare*100, 0) + "%", "#list", "看每日 ↓"})
	}
	topK, topV := "", 0
	for k, v := range lim {
		if v > topV || (v == topV && k < topK) {
			topK, topV = k, v
		}
	}
	if topV > 0 {
		what, _, _ := strings.Cut(toollimits.Limits[topK][1], "；")
		what, _, _ = strings.Cut(what, "，")
		pg.fs = append(pg.fs, nfind{"", strconv.Itoa(topV) + " 次", "最常撞到的上限是「" + toollimits.Limits[topK][0] + "」：" + what + "（" + strconv.Itoa(topV) + " 次），佔撞上限次數的 " + pct0(float64(topV), float64(limAll)), "#list", "看上限 ↓"})
	}
	// one pane per tag: the seg on top switches the daily chart and the table under it together
	count := func(n func(*Turn) int) map[string]int {
		m := map[string]int{}
		for _, t := range turns {
			m[day(t.At)] += n(t)
		}
		return m
	}
	chart := func(name string, byDay map[string]int) string {
		var bars []nbar
		for _, d := range last30() {
			n := byDay[d]
			bars = append(bars, nbar{d[5:], []float64{float64(n)}, d + " " + name + " " + strconv.Itoa(n) + " 次", "/?day=" + d, false, false})
		}
		return `<div class="nchart">` + legend([2]string{"var(--mid)", "每天的" + name + "次數（近 30 天）"}) +
			barSVG(bars, []string{"var(--mid)"}, func(v float64) string { return py.F(v, 0) + " 次" }, 0, 3) + `</div>`
	}
	repDay := map[string]int{}
	for _, r := range reps {
		for _, refs := range r.occ {
			for _, ref := range refs {
				if ref.turn > 0 && ref.turn <= len(ref.s.Turns) {
					repDay[day(ref.s.Turns[ref.turn-1].At)]++
				}
			}
		}
	}
	// a card's own table (all kinds, with examples) is the tag's table; its 做法 is the copy button under it
	card := func(k string) string {
		p, ok := leverParts[k]
		if !ok {
			return `<p class="dim">這段期間沒有資料。</p>`
		}
		_, b, _ := strings.Cut(p.action, "<button")
		if b != "" {
			b = `<div class="copyrow"><button` + b + `</div>`
		}
		return p.details + b + tagHowTo[k].html("") // 怎麼省 sits under each tag's table, so it switches with the tag
	}
	type pane struct{ id, name, body string }
	panes := []pane{
		{"rep", "重複呼叫", chart("重複呼叫", repDay) + card("repeat")},
		{"scr", "臨時腳本", chart("臨時腳本呼叫", count(func(t *Turn) int { return t.Scripts })) + card("scripts")},
	}
	hits := func(t *Turn, k string) int {
		n := 0
		for _, c := range []*py.Counter{t.Limits, t.SubLimits} {
			if c != nil {
				n += c.N[k]
			}
		}
		return n
	}
	for _, k := range limitOrder {
		l := toollimits.Limits[k]
		what, _, _ := strings.Cut(l[1], "；")
		type hit struct{ n, turns int }
		ss, by := bySess(sessions, func(s *Session) hit {
			var h hit
			for _, t := range s.Turns {
				if n := hits(t, k); n > 0 {
					h.n += n
					h.turns++
				}
			}
			return h
		}, func(h hit) float64 { return float64(h.n) })
		var rows []string
		for _, s := range ss {
			if h := by[s]; h.n > 0 {
				rows = append(rows, convRow(s, numCell(strconv.Itoa(h.n)), numCell(strconv.Itoa(h.turns))))
			}
		}
		body := chart(l[0], count(func(t *Turn) int { return hits(t, k) })) +
			`<p class="lede dim">` + Esc(what) + `</p>`
		if len(rows) > 0 {
			body += convTable([]string{"對話", "專案", "次數", "回合"}, rows)
		}
		gate := ""
		if k == "bash_timeout" { // the poll gate is part of this tag's fix
			gate = pollGateCard(blocked)
		}
		body += tagHowTo[k].html(gate)
		panes = append(panes, pane{k, l[0], body})
	}
	type slowHit struct {
		n    int
		cost float64
	}
	ss, by := bySess(sessions, func(s *Session) slowHit {
		var h slowHit
		for _, t := range s.Turns {
			if killedSlow(t) {
				h.n++
				h.cost += t.Usage.Cost
			}
		}
		return h
	}, func(h slowHit) float64 { return float64(h.n) })
	var slowRows []string
	for _, s := range ss {
		if h := by[s]; h.n > 0 {
			slowRows = append(slowRows, convRow(s, numCell(strconv.Itoa(h.n)), numCell(money0(h.cost))))
		}
	}
	slowBody := chart("做太久被中止", count(func(t *Turn) int {
		if killedSlow(t) {
			return 1
		}
		return 0
	})) + `<p class="lede dim">回合等超過 ` + strconv.Itoa(slowKillSec) + ` 秒、不到 ` + strconv.Itoa(slowKillMax/60) + ` 分鐘就被你中止，花費照算。</p>`
	if len(slowRows) > 0 {
		slowBody += convTable([]string{"對話", "專案", "次數", "花費"}, slowRows)
	}
	panes = append(panes, pane{"slow", "做太久被中止", slowBody + tagHowTo["slow"].html("")})
	var seg, bodies strings.Builder
	for i, p := range panes {
		on := ""
		if i == 0 {
			on = " on"
		}
		seg.WriteString(`<a class="` + strings.TrimSpace(on) + `" data-p="pane-` + p.id + `" onclick="tab(this)">` + Esc(p.name) + `</a>`)
		bodies.WriteString(`<div class="tpane` + on + `" id="pane-` + p.id + `">` + p.body + `</div>`)
	}
	pg.title = "依 Tag 看"
	pg.table = `<div class="seg">` + seg.String() + `</div>` + bodies.String()
	return pg
}

func delegatePage(sessions []*Session, turns []*Turn, since string, total float64, days int) topicParts {
	_, dele := spend(sessions)
	launches := 0
	for _, x := range sessions {
		launches += len(windowLaunches(x))
	}
	m := tmCached()
	per, n := fnum(m, "per"), fnum(m, "n")
	saved := max(0.0, per*n)
	hasDele := func(t *Turn) bool { return t.Dele > 0 }
	share := shareClause("委派的回合佔比", hasDele, nil)
	opus := clause{"Opus 佔花費", opusShare, nil, nil}
	since5 := since
	if len(since) >= 10 {
		since5 = since[5:]
	}
	pre, post, _ := baWindows(turns, since)
	haveBA := len(pre) >= minTurns && len(post) >= minTurns && daysShort(pre, post, since) == ""
	pg := topicParts{cards: []string{"delegate"}, hot: []int{3}}
	pg.lede = `<p class="lede">近 ` + strconv.Itoa(days) + ` 天委派了 ` + py.Comma(int64(launches)) + ` 次，subagent 實際花了 ` + money0(dele) + `；估計比全部在主對話自己做，省下約 <b>` + money0(saved) + `</b>。</p>`
	delegTurns, nAll := share.counts(turns)
	shareV, shareSub := pct1s(delegTurns, nAll), "全部回合裡有委派的"
	opusV, opusSub := py.Pct(opusShare(turns), 1), "主對話花費裡 Opus 的比例"
	if haveBA {
		shareV, shareSub = py.Pct(share.metric(post), 1), since5+" 前是 "+py.Pct(share.metric(pre), 1)
		opusV, opusSub = py.Pct(opus.metric(post), 1), since5+" 前是 "+py.Pct(opus.metric(pre), 1)
	}
	opusLabel := "Opus 佔主對話花費"
	if haveBA {
		opusLabel = since5 + " 後 Opus 佔主對話花費"
	}
	pg.tiles = []stat{
		{"委派花費", money0(dele), py.Comma(int64(launches)) + " 次，佔總花費 " + pct0(dele, total), "", "精確值 " + money(dele)},
		{"估計省下", money0(saved), "每次一般省 " + money(per), "", "精確值 " + money(saved)},
		{"有委派的回合", shareV, shareSub, "", ""},
		{opusLabel, opusV, opusSub, "", ""},
	}
	ks := subagentKinds(sessions)
	if top := costliestKind(ks); top != nil && top.n > 0 {
		avg := top.cost / float64(top.n)
		txt := "<b>" + Esc(top.kind) + "</b> 平均每次花 " + money(avg)
		var ref *kindStat
		for _, k := range ks {
			if k != top && k.n > 0 && (ref == nil || k.n > ref.n) {
				ref = k
			}
		}
		if ref != nil && ref.cost > 0 {
			_, short, ok := strings.Cut(ref.kind, ":")
			if !ok {
				short = ref.kind
			}
			txt += "，是 " + Esc(short) + "（" + money(ref.cost/float64(ref.n)) + "）的 " + py.F(avg/(ref.cost/float64(ref.n)), 1) + " 倍"
		}
		txt += "；" + py.Comma(int64(top.n)) + " 次共 " + money0(top.cost) + "，是最貴的一類"
		pg.fs = append(pg.fs, nfind{"hot", money(avg), txt, "#list", "看類型 ↓"})
	}
	if haveBA {
		a, b := share.metric(pre), share.metric(post)
		k1, m1 := share.counts(post)
		k2, m2 := share.counts(pre)
		if a > 0 {
			verdict := "差距未超出隨機波動"
			if z := twoPropZ(k1, m1, k2, m2); z != nil && math.Abs(*z) >= 1.96 {
				verdict = "差距超出隨機波動"
			}
			pg.fs = append(pg.fs, nfind{"", "×" + py.F(b/a, 1), since5 + " 之後，有委派的回合從 " + py.Pct(a, 1) + " 升到 " + py.Pct(b, 1) + "，" + verdict, "/verify", "去改動前後 →"})
		}
		pg.fs = append(pg.fs, nfind{"", py.Pct(opus.metric(post), 1), since5 + " 之後 Opus 佔主對話花費 " + py.Pct(opus.metric(post), 1) + "（之前 " + py.Pct(opus.metric(pre), 1) + "）；各模型的回合數、花費與 context 在「模型」頁", "/model", "去模型 →"})
	}
	byDay := map[string][2]int{}
	for _, t := range turns {
		x := byDay[day(t.At)]
		x[1]++
		if t.Dele > 0 {
			x[0]++
		}
		byDay[day(t.At)] = x
	}
	var bars []nbar
	for _, d := range last30() {
		x := byDay[d]
		r := 0.0
		if x[1] > 0 {
			r = float64(x[0]) / float64(x[1]) * 100
		}
		bars = append(bars, nbar{d[5:], []float64{r}, d + " 有委派的回合 " + strconv.Itoa(x[0]) + " / " + strconv.Itoa(x[1]), "/?day=" + d, false, false})
	}
	pg.chart = chartCard("每天有委派的回合佔比（近 30 天）", legend([2]string{"var(--accent)", "有委派的回合"}),
		barSVG(bars, []string{"var(--accent)"}, func(v float64) string { return py.F(v, 0) + "%" }, 0, 3))
	pg.title = subagentCard
	pg.table = kindTable(sessions)
	return pg
}

func modelPage(sessions []*Session, turns []*Turn, since string, days int) topicParts {
	type mb struct {
		n    int
		cost float64
		ctx  []int64
	}
	by := map[string]*mb{}
	var order []string
	all := 0.0
	for _, t := range turns {
		if t.Models == nil {
			continue
		}
		for _, k := range t.Models.keys {
			b := by[k]
			if b == nil {
				b = &mb{}
				by[k] = b
				order = append(order, k)
			}
			v := t.Usage.Cost / float64(len(t.Models.keys)) // same split as the model table
			b.n++
			b.cost += v
			all += v
			b.ctx = append(b.ctx, ctxOf(t))
		}
	}
	if len(order) == 0 {
		return topicParts{title: "依模型", table: empty("沒有模型資料")}
	}
	sort.SliceStable(order, func(i, j int) bool { return by[order[i]].cost > by[order[j]].cost })
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
	med := func(k string) int64 {
		c := append([]int64{}, by[k].ctx...)
		sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
		return c[len(c)/2]
	}
	pg := topicParts{}
	top := order[0]
	pg.lede = `<p class="lede">近 ` + strconv.Itoa(days) + ` 天主對話花費最高的模型是 <b>` + Esc(top) + `</b>，` + money(by[top].cost) + `（` + py.F(by[top].cost/all*100, 1) + `%）；` +
		strconv.Itoa(len(sessions)) + ` 個對話裡有 ` + strconv.Itoa(mixed) + ` 個混用多個模型。</p>`
	for i := 0; i < 3 && i < len(order); i++ {
		k := order[i]
		pg.tiles = append(pg.tiles, stat{k, py.F(by[k].cost/all*100, 1) + "%", py.Comma(int64(by[k].n)) + " 個回合，" + money(by[k].cost), "", ""})
	}
	pg.tiles = append(pg.tiles, stat{"混用模型的對話", strconv.Itoa(mixed) + " 個", strconv.Itoa(len(sessions)) + " 個對話裡", "", ""})
	since5 := since
	if len(since) >= 10 {
		since5 = since[5:]
	}
	pre, post, _ := baWindows(turns, since)
	if len(pre) >= minTurns && len(post) >= minTurns && daysShort(pre, post, since) == "" {
		pg.fs = append(pg.fs, nfind{"hot", py.Pct(opusShare(post), 1), since5 + " 之後，Opus 佔主對話花費 " + py.Pct(opusShare(post), 1) + "（之前 " + py.Pct(opusShare(pre), 1) + "）", "/verify", "去改動前後 →"})
	}
	var cheap, dear string // cheapest and dearest per turn among models with enough turns
	for _, k := range order {
		if by[k].n < 30 || k == "<synthetic>" {
			continue
		}
		avg := by[k].cost / float64(by[k].n)
		if cheap == "" || avg < by[cheap].cost/float64(by[cheap].n) {
			cheap = k
		}
		if dear == "" || avg > by[dear].cost/float64(by[dear].n) {
			dear = k
		}
	}
	if cheap != "" && cheap != dear && med(cheap) < med(dear) {
		pg.fs = append(pg.fs, nfind{"", toks(pi(med(cheap))), modelLabel(cheap) + " 回合的 context 一般是 " + toks(pi(med(cheap))) + "，" + modelLabel(dear) + " 是 " + toks(pi(med(dear))) + "；每回合花費的差距有一部分來自 context 大小", "#list", "看模型表 ↓"})
	}
	if sys := by["<synthetic>"]; sys != nil && all > 0 {
		pg.fs = append(pg.fs, nfind{"", strconv.Itoa(sys.n) + " 個", strconv.Itoa(sys.n) + " 個回合是系統產生、沒有模型名稱，花了 " + money(sys.cost) + "（" + py.F(sys.cost/all*100, 1) + "%）", "#list", "看模型表 ↓"})
	}
	byDay := map[string][2]float64{}
	for _, t := range turns {
		if t.Models == nil {
			continue
		}
		x := byDay[day(t.At)]
		for _, m := range t.Models.keys {
			v := t.Usage.Cost / float64(len(t.Models.keys))
			x[1] += v
			if strings.Contains(m, "opus") {
				x[0] += v
			}
		}
		byDay[day(t.At)] = x
	}
	var bars []nbar
	for _, d := range last30() {
		x := byDay[d]
		bars = append(bars, nbar{d[5:], []float64{x[0], x[1] - x[0]}, d + " Opus " + money(x[0]) + " · 其他 " + money(x[1]-x[0]), "/?day=" + d, false, false})
	}
	pg.chart = chartCard("每天主對話花在哪個模型（近 30 天）", legend([2]string{"var(--accent)", "Opus"}, [2]string{"var(--ok)", "其他模型"}),
		barSVG(bars, []string{"var(--accent)", "var(--ok)"}, moneyAxis, 0, 3))
	pg.title = "依模型"
	pg.table = leverParts["model"].card
	return pg
}

// ctxTrend is the money context cost in the last 30 days and in the 30 days before.
func ctxTrend(turns []*Turn, base float64) (a30, p30 float64) {
	cut := Now().AddDate(0, 0, -30)
	cut2 := Now().AddDate(0, 0, -60)
	for _, t := range turns {
		ww := waste([]*Turn{t}, base, nil).ctx + rewriteUSD([]*Turn{t})
		at := fromTimestamp(t.At)
		if at.After(cut) {
			a30 += ww
		} else if at.After(cut2) {
			p30 += ww
		}
	}
	return a30, p30
}

func firstClause(s string) string {
	s = anyTag.Sub(s, "")
	for _, sep := range []string{"，", "；", "（"} {
		if i := strings.Index(s, sep); i > 0 {
			s = s[:i]
		}
	}
	return s
}

func pct1s(a, b int) string {
	if b == 0 {
		return "—"
	}
	return py.F(float64(a)/float64(b)*100, 1) + "%"
}

var _ = time.Now

var reNum = regexp.MustCompile(`[0-9][0-9,.]*\s*(次|種|個|%)?`)

func firstNumber(s string) string { return reNum.FindString(anyTag.Sub(s, "")) }

// limitOrder is the order the seven tool limits are listed in.
var limitOrder = []string{"bash_timeout", "bash_out", "edit", "fetch", "read", "glob", "search"}

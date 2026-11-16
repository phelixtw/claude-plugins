package dash

// The before/after page. Rows are parsed from the cards' own before/after sentence (leverParts[].ba),
// so the table, the tiles and the findings all carry the numbers the cards show.

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"agentperform/internal/py"
)

type baRowT struct {
	card, label, before, after, delta string
	z                                 float64
	hasZ, over                        bool
}

var homeRe = regexp.MustCompile(`^/Users/[^/]+`)

var (
	baPartRe  = regexp.MustCompile(`^(.+?) 前 (\S+) → 後 ([^\s（]+)(.*)$`)
	baZRe     = regexp.MustCompile(`z=([+-][0-9.]+)`)
	baDeltaRe = regexp.MustCompile(`（([+-][0-9]+%)）`)
	baBodyRe  = regexp.MustCompile(`(?s)^<p class="ba"[^>]*>.*?天）：(.*)</p>$`)
)

// parseBA turns one card's before/after sentence into table rows; a sentence without numbers gives none.
func parseBA(card, ba string) []baRowT {
	m := baBodyRe.FindStringSubmatch(ba)
	if m == nil {
		return nil
	}
	var rows []baRowT
	for _, part := range strings.Split(m[1], " · ") {
		pm := baPartRe.FindStringSubmatch(part)
		if pm == nil {
			continue
		}
		r := baRowT{card: card, label: pm[1], before: pm[2], after: pm[3]}
		if zm := baZRe.FindStringSubmatch(pm[4]); zm != nil {
			r.z, _ = strconv.ParseFloat(zm[1], 64)
			r.hasZ, r.over = true, !strings.Contains(pm[4], "未超出")
		} else if dm := baDeltaRe.FindStringSubmatch(pm[4]); dm != nil {
			r.delta = dm[1]
		}
		rows = append(rows, r)
	}
	return rows
}

func pctVal(s string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	return v, err == nil && strings.HasSuffix(s, "%")
}

// topicOf is the topic page a card lives on.
func topicOf(card string) (string, string) {
	switch card {
	case "做太久被中止":
		return "/tools#pane-slow", "去其他 Tag →"
	case "委派":
		return "/delegate", "去委派 →"
	case "模型選擇":
		return "/model", "去模型 →"
	}
	return "/context", "去 Context →"
}

// baFind is one "你可能沒注意到" entry for a tested row: the change in points (or a multiple), and what it means.
func baFind(r baRowT, tone string) nfind {
	a, ok1 := pctVal(r.before)
	b, ok2 := pctVal(r.after)
	fig, verb := "", "升到"
	if ok1 && ok2 {
		if b < a {
			verb = "降到"
		}
		if a > 0 && (b/a >= 2 || b/a <= 0.5) {
			fig = "×" + py.F(b/a, 1)
		} else {
			fig = py.SignedF(b-a, 1) + " 點"
			fig = strings.Replace(fig, "-", "−", 1)
		}
	}
	tail := "，差距還在隨機波動範圍內"
	if r.over {
		tail = "，超出隨機波動"
	}
	href, goTxt := topicOf(r.card)
	label := strings.Replace(r.label, "的回合佔比", "的回合", 1)
	if label == "委派的回合" {
		label = "有委派的回合"
	}
	return nfind{tone, fig, label + "從 " + r.before + " " + verb + " " + r.after + tail, href, goTxt}
}

func cfgFold(from, to string) string {
	byDay := map[string][]cfgEvent{}
	for _, e := range configChanges() {
		if e.day >= from && e.day <= to {
			byDay[e.day] = append(byDay[e.day], e)
		}
	}
	var days []string
	for d := range byDay {
		days = append(days, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	kinds := [][3]string{{"skill+", "裝了", "skill"}, {"skill-", "移除", "skill"}, {"mcp+", "裝了", "MCP"}, {"mcp-", "移除", "MCP"}}
	var b strings.Builder
	for _, d := range days {
		ev := byDay[d]
		var det []string
		for _, e := range ev {
			if e.kind == "file" {
				s := `<div><b>` + Esc(homeRe.ReplaceAllString(e.path, "~")) + `</b>　+` + strconv.Itoa(e.add) + `／−` + strconv.Itoa(e.del)
				if len(e.sample) > 0 {
					for i := range e.sample {
						e.sample[i] = "+ " + Esc(e.sample[i])
					}
					s += `<pre>` + strings.Join(e.sample, "\n") + `</pre>`
				}
				det = append(det, s+`</div>`)
			}
		}
		for _, k := range kinds {
			var names []string
			for _, e := range ev {
				if e.kind == k[0] {
					names = append(names, Esc(e.name))
				}
			}
			if len(names) > 0 {
				det = append(det, `<div><b>`+k[1]+` `+k[2]+`</b>：`+strings.Join(names, "、")+`</div>`)
			}
		}
		for _, e := range ev {
			if e.kind == "model" {
				det = append(det, `<div><b>模型</b>：`+Esc(strings.TrimPrefix(e.from, "claude-"))+` → `+Esc(strings.TrimPrefix(e.to, "claude-"))+`</div>`)
			}
		}
		b.WriteString(`<div class="chg" id="chg-` + d[5:] + `"><div class="chd">` + d[5:] + `</div><div class="chb">` + strings.Join(det, "") + `</div></div>`)
	}
	if len(days) == 0 {
		b.WriteString(`<p class="dim">這段期間沒偵測到設定變更。</p>`)
	}
	b.WriteString(`<p class="dim" style="margin:8px 0 0;font-size:12px">日期是改動後第一個對話開始的時間；只看得到 transcript 還保留的範圍（cleanupPeriodDays，預設 30 天）。</p>`)
	return fold("自動偵測到的設定變更（近 30 天，"+strconv.Itoa(len(days))+" 天）", b.String())
}

// chgSummary is one day's config changes as a sentence, e.g. "CLAUDE.md 改了 2 次、裝了 2 個 skill".
func chgSummary(ev []cfgEvent) string {
	count := map[string]int{}
	for _, e := range ev {
		count[e.kind]++
	}
	var parts []string
	if n := count["file"]; n > 0 {
		parts = append(parts, "設定檔改了 "+strconv.Itoa(n)+" 次")
	}
	for _, k := range [][3]string{{"skill+", "裝了", "skill"}, {"skill-", "移除", "skill"}, {"mcp+", "裝了", "MCP"}, {"mcp-", "移除", "MCP"}} {
		if n := count[k[0]]; n > 0 {
			parts = append(parts, k[1]+" "+strconv.Itoa(n)+" 個 "+k[2])
		}
	}
	if count["model"] > 0 {
		parts = append(parts, "換模型")
	}
	if len(parts) == 0 {
		return "沒偵測到設定變更"
	}
	return strings.Join(parts, "、")
}

// markDays lists the default change dates as MM-DD.
func markDays() string {
	var d []string
	for _, m := range marks {
		d = append(d, m[0][5:])
	}
	return strings.Join(d, "、")
}

// chartDays is how many days the 改動前後 chart shows.
const chartDays = 42

// VerifyBody is the whole 改動前後 page body below the h1.
func VerifyBody(turns []*Turn, since string, cards []string) string {
	b4, af, pct, n1, n2 := beforeAfter(turns, since, costPerTurn)
	var rows []baRowT
	for _, k := range cards {
		if p, ok := leverParts[k]; ok {
			rows = append(rows, parseBA(p.title, p.ba)...)
		}
	}
	label := "你選的日期"
	for _, m := range marks {
		if m[0] == since {
			label = m[1]
		}
	}
	pre, post, span := baWindows(turns, since)
	sinceT, _ := parseDay(since)
	sd := strconv.Itoa(span)

	hero, lede, fs := "", `<p class="lede">這個改動日前後的資料不夠比較（`+orStr(daysShort(pre, post, since), "前後各需 "+strconv.Itoa(minTurns)+" 個回合")+`），換一個改動日試試。</p>`, ""
	if b4 != nil {
		p := ""
		if pct != nil {
			p = py.SignedF(*pct, 0) + "%"
		}
		lede = `<p class="lede">以 <b>` + since[5:] + `</b>（` + Esc(label) + `）為界，每回合花費從 ` + money(*b4) + ` 變成 <b>` + money(*af) + `（` + p + `）</b>；前 ` + sd + ` 天 ` + py.Comma(int64(n1)) + ` 個回合，後 ` + sd + ` 天 ` + py.Comma(int64(n2)) + ` 個。</p>`
		tested, over := 0, 0
		var hot, cold []baRowT
		for _, r := range rows {
			if r.hasZ {
				tested++
				if r.over {
					over++
					hot = append(hot, r)
				} else {
					cold = append(cold, r)
				}
			}
		}
		az := func(r baRowT) float64 { return math.Abs(r.z) }
		sort.SliceStable(hot, func(i, j int) bool { return az(hot[i]) > az(hot[j]) })
		sort.SliceStable(cold, func(i, j int) bool { return az(cold[i]) > az(cold[j]) })
		var fl []nfind
		for i, r := range hot[:min(2, len(hot))] {
			tone := ""
			if i == 0 {
				tone = "hot"
			}
			fl = append(fl, baFind(r, tone))
		}
		if len(cold) > 0 {
			fl = append(fl, baFind(cold[0], ""))
		}
		fs = finds(fl)
		hero = `<div class="hero">` + stats([]stat{
			{"每回合花費", p, money(*b4) + " → " + money(*af), "", ""},
			{"超出隨機波動", strconv.Itoa(over) + " 項", strconv.Itoa(tested) + " 項指標裡", "", ""},
			{"回合數", py.Comma(int64(n1)) + " → " + py.Comma(int64(n2)), "前後各 " + sd + " 天", "", ""},
			{"改動日", since[5:], Esc(label), "", ""},
		}) + `</div>`
	}

	// chart: span days before the change date through today
	byDay := map[string][]*Turn{}
	for _, t := range turns {
		byDay[day(t.At)] = append(byDay[day(t.At)], t)
	}
	chg := map[string]bool{}
	chgEv := map[string][]cfgEvent{}
	for _, e := range configChanges() {
		chg[e.day] = true
		chgEv[e.day] = append(chgEv[e.day], e)
	}
	var bars []nbar
	var pops []string
	first, lastD := "", ""
	// A fixed window, so picking another day in the chart keeps the same days on screen.
	start := Now().AddDate(0, 0, -chartDays+1)
	if sinceT.Before(start) {
		start = sinceT.AddDate(0, 0, -minDays)
	}
	for d := start; !d.After(Now()); d = d.AddDate(0, 0, 1) {
		ds := d.Format("2006-01-02")
		if first == "" {
			first = ds
		}
		lastD = ds
		c, n := 0.0, len(byDay[ds])
		for _, t := range byDay[ds] {
			c += turnCost(t)
		}
		v := 0.0
		if n > 0 {
			v = c / float64(n)
		}
		bars = append(bars, nbar{ds[5:], []float64{v}, ds + " 每回合 " + money(v) + " · " + strconv.Itoa(n) + " 個回合", "/verify?since=" + ds + "#chart", chg[ds], ds == since})
		pop := `<b>` + ds[5:] + `</b>　每回合 ` + money(v) + ` · ` + strconv.Itoa(n) + ` 個回合<br>` + chgSummary(chgEv[ds]) +
			`<br><span class="dim">已設成改動日，前後重新比較</span><br><a href="/?day=` + ds + `">看當天明細 →</a>`
		if chg[ds] {
			pop += `　<a href="#chg-` + ds[5:] + `">設定變更細節 ↓</a>`
		}
		pops = append(pops, pop)
	}
	leg := `<div class="nleg"><span><i style="background:var(--accent)"></i>每回合花費（含委派）</span><span>虛線＝改動日</span><span><i style="background:var(--mid);height:3px;margin-bottom:3px"></i>偵測到設定變更</span></div>`
	chart := chartCard("每天的每回合花費（"+first[5:]+" 到 "+lastD[5:]+"）", leg, barSVGPop(bars, pops, []string{"var(--accent)"}, moneyAxis, 0, 3))

	var trs []string
	for _, r := range rows {
		verdict := `<span class="dim">—</span>`
		if r.hasZ {
			verdict = "否"
			if r.over {
				verdict = `<b style="color:var(--hot-fg)">是</b>`
			}
			verdict = `<span title="z=` + py.SignedF(r.z, 2) + `（後減前）">` + verdict + `</span>`
		}
		after := Esc(r.after)
		if r.delta != "" {
			after += ` <span class="dim">` + Esc(r.delta) + `</span>`
		}
		trs = append(trs, `<tr><td>`+Esc(r.label)+`<br><span class="dim">`+Esc(r.card)+`</span></td><td class="num">`+Esc(r.before)+`</td><td class="num">`+after+`</td><td>`+verdict+`</td></tr>`)
	}
	list := ""
	if len(trs) > 0 {
		list = listCard("每一項改了多少", table([]string{"指標", "改動前", "改動後", "超出隨機波動"}, trs, "", "", nil))
	}

	method := `<ul><li>前後兩段的比例差距，用兩個比例的 z 值判斷（後減前，正值＝後面變多）：|z| 達 1.96 以上，才算超出隨機波動；沒超出的變化，可能只是剛好那幾天的工作不同。` +
		`每項指標前後各至少 ` + strconv.Itoa(minTurns) + ` 個回合、` + strconv.Itoa(minDays) + ` 天才比較，前後取同樣天數。<div class="src">統計方法：兩比例差的 z 檢定</div></li>` +
		`<li>改動日可以換成別天，例如 ` + marks[0][0][5:] + ` ` + marks[0][1] + `。<div class="src">下方「換改動日」</div></li></ul>`
	to := Now().Format("2006-01-02")
	from := Now().AddDate(0, 0, -29).Format("2006-01-02")
	return `<div id="vbody">` + lede + hero + fs + chart + list +
		fold("怎麼判斷「超出隨機波動」", method) +
		fold("換改動日（預設 "+markDays()+"；前後各 ≥"+strconv.Itoa(minTurns)+" 回合才比較）", sinceBar(since)) +
		cfgFold(from, to) + `</div>`
}

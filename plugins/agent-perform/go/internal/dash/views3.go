package dash

import (
	"sort"
	"strconv"
	"strings"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/toollimits"
)

// SessionView is the /?session= page.
func SessionView(sessions []*Session, sid string) string { return sessionLens(sessions, sid, "") }

// sessionLens is SessionView with the focus lens applied ("" = none).
func sessionLens(sessions []*Session, sid, focus string) string {
	var found *Session
	for _, s := range sessions {
		if s.ID == sid {
			found = s
			break
		}
	}
	if found == nil {
		return sessionMissing()
	}
	return sessionBody(found, baseCost(sessions), focus)
}

func sessionMissing() string {
	return crumbs(`<a href="/">總覽</a>`) + empty("找不到這個對話（可能超出目前的天數範圍）。")
}

// sessionBody renders one session; base is the median cost of 150-300k turns.
func sessionBody(found *Session, base float64, focus string) string {
	turns := found.Turns
	var total Usage
	var resps []float64
	end := found.End // the session runs until its last subagent finishes
	for _, t := range turns {
		total.add(t.Usage)
		if t.First != nil {
			resps = append(resps, turnSpan(t))
		}
		end = max(end, t.DEnd)
	}
	d := day(found.Start)
	dRow := found.Dele
	dCost := 0.0
	if dRow != nil {
		dCost = fnum(dRow, "cost")
	}
	hp := health()
	loud := 0.15
	if v, ok := ojson.Obj(hp).Get("loud"); ok {
		loud, _ = py.Number(v)
	}
	rep := remapRepeats(found, ojson.Obj(ojson.Field(ojson.Field(hp, "sessions"), found.ID)))
	launches := 0
	for _, t := range turns {
		for _, name := range t.Tools.Keys {
			if strings.HasPrefix(name, "agent") {
				launches += t.Tools.N[name]
			}
		}
	}
	launched := launches > 0
	var sizes []int64
	bigN := 0
	for _, t := range turns {
		if c := ctxOf(t); c != 0 {
			sizes = append(sizes, c)
			if ctxBig(t) {
				bigN++
			}
		}
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })
	w := waste(turns, base, rep)
	costAll := total.Cost + dCost
	topTurn := -1
	for i, t := range turns {
		if topTurn < 0 || turnCost(t) > turnCost(turns[topTurn]) {
			topTurn = i
		}
	}
	lede := `<p class="lede">` + sessionSpan(turns) + ` 這個對話花了 ` + money(costAll)
	if w.total > 0 {
		lede += `，其中 <b>` + money(w.total) + `</b> 可以省`
		if name, v := wasteMain(w); v > 0 {
			if v >= w.total*0.9 {
				lede += `，幾乎全部（` + money(v) + `）來自 ` + name
			} else {
				lede += `；可以省的有 ` + pct0(v, w.total) + ` 來自 ` + name + `（` + money(v) + `）`
			}
		}
	}
	lede += `。</p>`
	costSub := `主對話 ` + money(total.Cost)
	if dCost != 0 || !launched {
		costSub += `，委派 ` + money(dCost)
	} else {
		costSub += `；另外委派 ` + strconv.Itoa(launches) + ` 次，委派花費明天早上算出`
	}
	wTip := ""
	if total.Cost != 0 {
		wTip = `可以省的佔主對話花費的 ` + py.F(w.total/total.Cost*100, 0) + `%：context 太長 ` + money(w.ctx) + `，做太久被中止 ` + money(w.slow) + `，Cache 過期重寫 ` + money(w.cache) + `。`
	}
	ctxV, ctxSub := "—", ""
	if len(sizes) > 0 {
		ctxV = toks(pi(sizes[len(sizes)/2]))
		ctxSub = `超過視窗 40%：` + strconv.Itoa(bigN) + ` / ` + strconv.Itoa(len(sizes)) + ` 個回合`
	}
	spanSub := spanText(found.Start, end)
	tileRows := []stat{
		{"花費", money(costAll), costSub, "", ""},
		{"可以省的", money(w.total), `佔這個對話 ` + pct0(w.total, costAll), "", wTip},
		{"回合", strconv.Itoa(len(turns)) + ` 個`, spanSub, "", ""},
	}
	hero := hotTile(stats(tileRows), 1)
	typical := `<p class="lede dim" title="一般回合的 context＝這個對話每個回合最大一次 API 呼叫的 context 大小的中位數；超過視窗 40%＝超過模型視窗（` + toks(pi(ctxLimit(found))) + `）的 40%。">一般回合的 context ` + ctxV + `（` + ctxSub + `）。</p>`
	aside := `<p class="lede dim">共 ` + strconv.Itoa(len(turns)) + ` 個回合，時長 ` + spanText(found.Start, end) +
		`；每回合一般 <span title="中位數；算到這個回合的 subagent 都結束">` + secs(pctAt(resps, 50)) + `</span> 結束，` +
		`最慢的 5% 要 ` + secs(pctAt(resps, 95)) + ` 以上。</p>`
	verdict := diagnose(turns, w, base, dRow, rep, loud)
	listed := listedKinds(w, rep, loud)
	resume := found.Full
	if resume == "" {
		resume = found.ID
	}
	cmd := "claude --resume " + resume
	bar := `<div class="resume">` +
		`<code id="rid">` + Esc(cmd) + `</code>` +
		`<button class="ghost" id="cp" data-t="` + Esc(cmd) + `">複製</button>` +
		`<button class="ghost" id="cp2" data-t="` + Esc(resume) + `">只複製 id</button>` +
		`<span id="cpm" class="dim"></span></div>`
	daySet := map[string]bool{}
	for _, t := range turns {
		daySet[day(t.At)] = true
	}
	var days []string
	for k := range daySet {
		days = append(days, k)
	}
	sort.Strings(days)
	span := ""
	if len(days) > 1 {
		var links []string
		for _, x := range days {
			links = append(links, `<a href="/?day=`+x+`">`+x[5:]+`</a>`)
		}
		span = `<div class="span">這個對話跨 ` + strconv.Itoa(len(days)) + ` 天：` + strings.Join(links, " · ") +
			` <span class="tip" style="margin-left:8px;color:var(--accent)" tabindex="0" title="單日明細只算那天發生的回合；這一頁算整個對話的所有回合。">為什麼單日明細的數字比較小？</span>` + `</div>`
	}
	spansDays := len(days) > 1
	var rows []lrow
	lensN := 0
	prevDay := ""
	repReasons := ojson.Obj(ojson.GetOr(rep, "reasons"))
	for i0, t := range turns {
		i := i0 + 1
		u := t.Usage
		when := fromTimestamp(t.At)
		dk := when.Format("2006-01-02")
		dayChanged := spansDays && dk != prevDay
		prevDay = dk
		stamp := when.Format("15:04:05")
		if dayChanged {
			stamp = `<b class="d">` + when.Format("01-02") + `</b>` + when.Format("15:04:05")
		}
		var used []string
		for _, n := range t.Skills.items() {
			used = append(used, "skill:"+n)
		}
		for _, n := range t.MCP.items() {
			used = append(used, "mcp:"+mcpLabel(n))
		}
		var plain []string
		for _, n := range t.Tools.Keys {
			if strings.HasPrefix(n, "agent") {
				used = append(used, n)
			} else {
				plain = append(plain, n)
			}
		}
		sort.Strings(used)
		sort.Strings(plain)
		names := append(used, plain...)
		var tools strings.Builder
		for k, n := range names {
			if k == 3 {
				break
			}
			tools.WriteString("<span>" + Esc(n))
			if c := t.Tools.N[n]; c > 1 {
				tools.WriteString(" ×" + strconv.Itoa(c))
			}
			tools.WriteString("</span>")
		}
		toolsS := tools.String()
		if toolsS == "" {
			toolsS = "—"
		}
		if len(names) > 3 {
			toolsS += `<span class="dim" title="` + Esc(strings.Join(names[3:], " / ")) + `">+` + strconv.Itoa(len(names)-3) + `</span>`
		}
		var subSec []float64
		for _, a := range t.Agents {
			if a.Sec != nil {
				subSec = append(subSec, *a.Sec)
			}
		}
		subTime := ""
		if len(subSec) > 0 {
			var parts []string
			top := subSec[0]
			for _, x := range subSec {
				xx := x
				parts = append(parts, secs(&xx))
				top = max(top, x)
			}
			subTime = `<div class="dim" title="` + strconv.Itoa(len(t.Agents)) + ` 個 subagent，各自 ` + Esc(strings.Join(parts, " / ")) +
				`">↳ sub ` + secs(&top) + `</div>`
		}
		fl := w.flags[i0]
		seenF := map[string]bool{}
		prob := ""
		for _, f := range fl {
			if seenF[f.name] || f.name == "ctx" { // context size is on the turn's pipeline page
				continue
			}
			seenF[f.name] = true
			lbl, how := fixZH[f.name][0], fixZH[f.name][1]
			if f.name == "rep" {
				if l, tip, ok := repTag(lbl, ojson.Obj(ojson.GetOr(repReasons, strconv.Itoa(i)))); ok {
					lbl, how = l, tip
				}
			}
			prob += `<span class="pb ` + f.name + `" title="` + Esc(how) + `">` + Esc(lbl)
			if f.amt > 0 {
				prob += " " + money(f.amt)
			}
			prob += `</span>`
		}
		type lim struct {
			k     string
			n     int
			where string
		}
		var lims []lim
		for _, k := range t.Limits.Keys {
			lims = append(lims, lim{k, t.Limits.N[k], ""})
		}
		for _, k := range t.SubLimits.Keys {
			lims = append(lims, lim{k, t.SubLimits.N[k], "（在 subagent 裡）"})
		}
		for _, l := range lims {
			seenF["limit"] = true
			lt := toollimits.Limits[l.k]
			prob += `<span class="pb lim" title="` + Esc(lt[1]+l.where) + `">` + Esc(lt[0])
			if l.n > 1 {
				prob += " ×" + strconv.Itoa(l.n)
			}
			prob += `</span>`
		}
		if scr := scriptTagTurn(t.Scripts, t.DScripts); scr != "" {
			seenF["script"] = true
			prob += scr
		}
		costBad := 0.0
		for _, f := range fl {
			costBad += f.amt
		}
		if rewriteFlagged(t, i-1) {
			seenF["cache"] = true
			prob += hitPill(hitRate(u), t.Rewrite, u.Cost, false)
		}
		warn := ""
		for _, f := range fl {
			if listed[f.name] {
				warn = "warn"
				break
			}
		}
		var tags []string
		for k := range seenF {
			tags = append(tags, k)
		}
		sort.Strings(tags)
		if prob == "" {
			prob = "—"
		}
		over := ""
		if ctxBig(t) && listed["ctx"] {
			over = " over"
		}
		var e2e *float64
		if t.First != nil {
			v := t.E2E
			e2e = &v
		}
		costTd := `<td class="num">` + money(turnCost(t)) + `</td>`
		timeTd := `<td class="num">` + secs(e2e) + subTime + `</td>`
		if t.DCost > 0 { // subagent spend is counted in this turn
			costTd = `<td class="num"><span class="tip" title="主對話 ` + money(u.Cost) + ` · subagent ` + money(t.DCost) + `">` + money(turnCost(t)) + `</span></td>`
		}
		if span := turnSpan(t); span > t.E2E+1 && e2e != nil {
			timeTd = `<td class="num"><span class="tip" title="主對話 ` + secs(e2e) + ` · 到最後一個 subagent 結束">` + secs(&span) + `</span>` + subTime + `</td>`
		}
		var queued strings.Builder
		for _, q := range t.Queued {
			queued.WriteString(`<div class="dim">⤷ 中途插話：` + Esc(q) + `</div>`)
		}
		cells := `<td class="mono tid">#` + strconv.Itoa(i) + `</td>` +
			`<td class="ts">` + stamp + `</td>` + costTd +
			`<td class="prob" data-v="` + py.F(costBad, 4) + `" data-tags="` + Esc(strings.Join(tags, " ")) + `">` + prob + `</td>` +
			`<td class="tok dim` + over + `" title="cache 讀 ` + py.Comma(u.Cr) + ` · cache 寫 ` + py.Comma(u.Cw) +
			` · 新 input ` + py.Comma(u.In) + ` · output ` + py.Comma(u.Out) +
			` · API 呼叫 ` + strconv.Itoa(t.Calls) + ` 次 · 最大一次 ` + py.Comma(t.CtxPeak) + `">` + toks(pi(t.CtxPeak)) + `</td>` + `<td class="mdlc">` + modelTag(t.Models) + `</td>` + timeTd
		cells += `<td class="tools dim">` + toolsS + `</td>`
		cells += `<td class="prompt">` + promptCell(t.Prompt) + queued.String() + `</td>`
		rows = append(rows, lrow{`<tr id="t` + strconv.Itoa(i) + `" class="` + warn + `" onclick="location='/pipeline?session=` + found.ID + `&at=` + py.FloatRepr(t.At) + `'">` + cells + `</tr>`, lensHit(focus, t, i0, w)})
		if rows[len(rows)-1].hit {
			lensN++
		}
	}
	limitN := 0
	for _, t := range turns {
		if t.Limits.Len() > 0 || t.SubLimits.Len() > 0 {
			limitN++
		}
	}
	// page template: headline, tiles, findings, chart, table, folds
	title := "對話 " + Esc(found.ID)
	if op := cleanOpening(found.Opening); op != "" { // the opening line stays on hover, as in the day table
		title = `對話 <span title="` + Esc(op) + `">` + Esc(found.ID) + `</span>`
	}
	var fs []nfind
	if topTurn >= 0 && costAll > 0 {
		t := turns[topTurn]
		fs = append(fs, nfind{"hot", money0(turnCost(t)), "#" + strconv.Itoa(topTurn+1) + "「" + Esc(py.Head(cleanOpening(t.Prompt), 20)) + "」一句話就花了 " + money(turnCost(t)) + "，佔整個對話 " + pct0(turnCost(t), costAll) + "；那個回合跑了 " + spoken(turnSpan(t)) + "，context 長到 " + toks(pi(t.CtxPeak)), "#t" + strconv.Itoa(topTurn+1), "看 #" + strconv.Itoa(topTurn+1) + " ↓"})
	}
	var bigTurns []int
	for i, t := range turns {
		if ctxBig(t) {
			bigTurns = append(bigTurns, i+1)
		}
	}
	if len(bigTurns) > 0 {
		fs = append(fs, nfind{"", strconv.Itoa(len(bigTurns)) + " 個", turnRanges(bigTurns) + " 這 " + strconv.Itoa(len(bigTurns)) + " 個回合的 context 都超過視窗 40%，合計多花 " + money(w.ctx), "#chart", "看圖 ↓"})
	}
	rw, rwShare := -1, 0.0
	for i, t := range turns {
		if c := turnCost(t); c > 0 && t.Rewrite/c > rwShare {
			rw, rwShare = i, t.Rewrite/c
		}
	}
	if rw >= 0 && rwShare >= 0.10 {
		t := turns[rw]
		gap := ""
		if t.Gap != nil {
			gap = "隔了 " + spoken(*t.Gap) + "才回來，"
		}
		fs = append(fs, nfind{"", pct0(rwShare, 1), "#" + strconv.Itoa(rw+1) + " " + gap + "Cache 已過期，重寫佔那個回合花費的 " + pct0(rwShare, 1) + "（" + money(t.Rewrite) + "）", "#t" + strconv.Itoa(rw+1), "看 #" + strconv.Itoa(rw+1) + " ↓"})
	}
	var bars []nbar
	for i, t := range turns {
		c := turnCost(t)
		v := []float64{c, 0}
		if ctxBig(t) {
			v = []float64{0, c}
		}
		bars = append(bars, nbar{"#" + strconv.Itoa(i+1), v, "#" + strconv.Itoa(i+1) + " " + money(c) + " · 最大 context " + toks(pi(t.CtxPeak)), "#t" + strconv.Itoa(i+1), false, false})
	}
	chart := chartCard("每個回合花多少", legend([2]string{"var(--accent)", "一般回合"}, [2]string{"var(--hot)", "context 超過視窗 40%"}),
		barSVG(bars, []string{"var(--accent)", "var(--hot)"}, moneyAxis, 0, max(1, len(bars)/12)))
	head := []string{"#", "時間", "花費", "Tag", "最大 context", "模型", "耗時", "工具", "你打的字"}
	attrs := ` data-sortkey="turns" data-defaultsort="0:desc"`
	if focus != "" {
		attrs = ""
	}
	waste0 := "「浪費」怎麼算：" + diagnoseNoteA + money(base) + diagnoseNoteB
	return crumbs(`<a href="/">總覽</a>`, `<a href="/?day=`+d+`">`+d[5:]+`</a>`, `<span class="mono">`+Esc(found.ID)+`</span>`) +
		`<h1 class="view">` + title + `</h1>` +
		lede + bar + span +
		`<div class="hero">` + hero + `</div>` +
		finds(fs) + chart +
		listCard("逐回合", `<div style="font-weight:600;margin:0 0 6px">逐回合明細 <details class="inl"><summary>Tag 欄是什麼</summary><p>`+tagInfo+`</p></details></div>`+`<p class="lede dim">`+strconv.Itoa(len(turns))+` 個回合裡 `+strconv.Itoa(limitN)+` 個碰到工具上限。</p>`+lensBar(lensN, "回合", "/?session="+found.ID, focus)+lensTable(head, rows, "", attrs, focus)) +
		howFold(sessionHow) +
		fold("這個對話的浪費：類別、回合 chip、"+money(base)+" 基準、怎麼算", verdict+`<p class="dim">`+waste0+`</p>`) +
		fold("時長：每回合一般 "+secs(pctAt(resps, 50))+"、最慢 5% "+secs(pctAt(resps, 95))+"；一般回合的 context "+ctxV, aside+typical)
}

var sessionHow = [][2]string{
	{"開始一件跟前面無關的事，就 <code>/clear</code> 或開新對話；舊 context 之後每則訊息都會重讀。", "Manage context proactively"},
	{"要離開超過 1 小時，先收尾；回來的第一則就不用整份重寫 Cache。", "Why usage climbs in a long session"},
}

func ptr(v float64) *float64 { return &v }

// uniqueDays are the days these turns fall on.
func uniqueDays(turns []*Turn) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range turns {
		if d := day(t.At); !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// sessionSpan is "10-19" for a one-day conversation and "10-19～10-20" for a longer one.
func sessionSpan(turns []*Turn) string {
	days := uniqueDays(turns)
	if len(days) == 0 {
		return ""
	}
	sort.Strings(days)
	if len(days) == 1 {
		return days[0][5:]
	}
	return days[0][5:] + "～" + days[len(days)-1][5:]
}

package dash

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"agentperform/internal/ojson"
	"agentperform/internal/prices"
	"agentperform/internal/py"
	"agentperform/internal/toollimits"
)

// sToks is session_scorecard._toks (an int below 1000).
func sToks(v float64) string {
	switch {
	case v >= 1_000_000:
		return py.F(v/1_000_000, 1) + "M"
	case v >= 1000:
		return py.F(v/1000, 0) + "k"
	}
	return strconv.FormatInt(int64(v), 10)
}

func signed(v float64) string {
	sign := "+"
	if v < 0 {
		sign, v = "-", -v
	}
	return sign + sToks(v)
}

// usdText is a dollar amount; under a cent it reads 不到 $0.01.
func usdText(v float64) string {
	if v < 0.01 {
		return "不到 $0.01"
	}
	return "$" + py.CommaF(v, 2)
}

// usdApprox is usdText with 約 in front of an amount that reaches a cent.
func usdApprox(v float64) string {
	if v < 0.01 {
		return usdText(v)
	}
	return "約 " + usdText(v)
}

// resendUSD is what the later API calls pay to re-read the item from the cache.
func resendUSD(it *Item) float64 { return float64(it.N) * it.RateSum / 1e6 }

var modelVer = regexp.MustCompile(`(opus|sonnet|haiku|fable)-(\d)-(\d)`)

// modelName is the model as a person says it: Opus 5.5.
func modelName(model string) string {
	if m := modelVer.FindStringSubmatch(model); m != nil {
		return strings.ToUpper(m[1][:1]) + m[1][1:] + " " + m[2] + "." + m[3]
	}
	return shortModel(model)
}

func clockText(secs float64) string {
	t := int64(secs)
	switch {
	case t >= 3600:
		return fmt.Sprintf("%d 小時 %d 分", t/3600, t%3600/60)
	case t >= 60:
		return fmt.Sprintf("%d 分 %d 秒", t/60, t%60)
	}
	return fmt.Sprintf("%d 秒", t)
}

// callLabel is 「Write · index.html」, or 回覆文字 for a call without tools.
func callLabel(c *tcCall) string {
	if len(c.uses) == 0 {
		return "回覆文字"
	}
	return useLabel(c.uses[0])
}

// turnSummary is the card at the top: the turn's cost (main calls plus the subagents it launched) on the left,
// its three dearest calls or subagents on the right. scope names what the card covers (本回合 or 這個 subagent).
func turnSummary(t *Turn, calls []*tcCall, real *Real, subs []*Sub, scope string) string {
	type spend struct {
		no, label string
		usd       float64
	}
	var main, subUSD float64
	var all []spend
	for i, c := range calls {
		main += c.usd
		all = append(all, spend{"#" + strconv.Itoa(i+1), callLabel(c), c.usd})
	}
	for _, a := range subs {
		subUSD += a.num("cost")
		all = append(all, spend{"委派", a.Desc, a.num("cost")})
	}
	total := main + subUSD
	sort.SliceStable(all, func(a, b int) bool { return all[a].usd > all[b].usd })
	win := t.Window
	if win == 0 {
		win = 1_000_000
	}
	u := t.Usage
	var tip []string
	if real != nil {
		tip = append(tip, `最後一次 API 呼叫送進 `+py.Comma(real.In+real.Cr+real.Cw)+` token（cache 讀 `+py.Comma(real.Cr)+
			` · cache 寫 `+py.Comma(real.Cw)+` · 新 input `+py.Comma(real.In)+`）`)
	}
	tip = append(tip, `最大 context 是視窗的 `+py.F(float64(ctxOf(t))/float64(win)*100, 0)+`%`,
		`Cache hit `+hitStr(hitRate(u)),
		scope+` cache 讀 `+py.Comma(u.Cr)+` · cache 寫 `+py.Comma(u.Cw))
	meta := strconv.Itoa(len(calls)) + ` 次 API 呼叫`
	if len(subs) > 0 {
		meta = `主對話 ` + usdText(main) + ` · ` + strconv.Itoa(len(calls)) + ` 次呼叫<br>subagent ` + usdText(subUSD) + ` · ` + strconv.Itoa(len(subs)) + ` 個`
	}
	// wall clock runs until the last of the main turn and its subagents finishes
	end := t.At + t.E2E
	for _, a := range subs {
		end = endAfter(end, a.start, a.num("secs"))
	}
	if span := end - t.At; span > 0 {
		meta += `<br>` + clockText(span)
		if span > t.E2E+1 {
			meta = strings.Replace(meta, `<br>`+clockText(span), `<br><span class="tip" title="主對話 `+clockText(t.E2E)+`，到最後一個 subagent 結束">`+clockText(span)+`</span>`, 1)
		}
	}
	left := `<div class="sum-l"><div class="sum-big"><span class="tip" title="` + strings.Join(tip, "&#10;") + `">` + usdText(total) + `</span></div>` +
		`<div class="sum-k"><span class="tip" title="照 Anthropic API 牌價（` + Esc(modelName(t.Model)) + `）換算；訂閱方案不是這樣計費">估算花費</span></div>` +
		`<div class="sum-m">` + meta + `</div></div>`
	var rows strings.Builder
	var top3 float64
	for k, x := range all {
		if k == 3 {
			break
		}
		top3 += x.usd
		w := 0.0
		if all[0].usd > 0 {
			w = x.usd / all[0].usd * 100
		}
		rows.WriteString(`<div class="sum-row"><span class="dim">` + x.no + `</span><span title="` + Esc(x.label) + `">` + Esc(x.label) +
			`</span><i style="width:` + py.F(w, 0) + `%"></i><b>` + usdText(x.usd) + `</b></div>`)
	}
	right := ""
	if rows.Len() > 0 && total > 0 {
		unit := ` 次`
		if len(subs) > 0 {
			// subagent rows are whole delegations, not single calls
			unit = ` 筆（呼叫或委派）`
		}
		right = `<div class="sum-r"><div class="sum-k">最貴的 ` + strconv.Itoa(min(3, len(all))) + unit + `，佔` + scope + ` ` +
			py.F(top3/total*100, 0) + `%</div>` + rows.String() + `</div>`
	}
	return `<div class="sum">` + left + right + `</div>`
}

// growthSpan is 「本回合加進 +Xk（平常 +Yk）」 for turn N: the growth of the biggest call's context over the turn before.
func growthSpan(path string, turn int, model string, listed int64) string {
	got := oneCached(path, 0)
	if got == nil {
		return ""
	}
	turns := got.Turns
	if turn < len(turns) {
		turns = turns[:turn]
	}
	delta, usual := ctxGrowth(turns)
	if delta == nil {
		return ""
	}
	tip := "本回合最大一次 API 呼叫的 context 比上一個回合多了 " + py.Comma(int64(delta.v)) + " token（API 回報）；" +
		"之後每次呼叫多帶約 " + usdText(delta.v*prices.CacheRead(model)/1e6) +
		"。「依 API」各次新加進的估算加總是 " + py.Comma(listed) + " token，差額來自估算誤差、延後載入的工具定義等 transcript 沒記的內容"
	text := `本回合加進 <span class="tip" title="` + tip + `">` + signed(delta.v) + `</span>`
	if usual != nil {
		text += "（平常 " + signed(usual.v) + "）"
	}
	return text + "，看「依 API」"
}

// rewriteLine is the turn's Cache 過期重寫 cost, in dollars.
func rewriteLine(t *Turn, total float64) (float64, string) {
	if t.Rewrite <= 0 || total <= 0 {
		return 0, ""
	}
	return t.Rewrite, `Cache 過期重寫 · <b class="tip" title="這個回合開始時 Cache 已過期，重寫多花的錢">` + usdText(t.Rewrite) +
		`</b>（佔本回合 ` + py.F(t.Rewrite/total*100, 0) + `%）`
}

var limitVerb = map[string]string{"read": "檔案太大", "bash_out": "輸出被截斷", "bash_timeout": "超時被移到背景", "edit": "被擋下",
	"glob": "結果被截斷", "fetch": "被轉址或截斷", "search": "多搜了一趟"}

// savingsSection is 可以省的地方: what hit in one turn (or one subagent), dearest first. mine picks the items that
// belong to it, rep is the session-level 重複呼叫 line (empty for a subagent), scope names it (本回合 or 這個 subagent).
func savingsSection(rep string, mine func(*Item) bool, t *Turn, items []*Item, calls []*tcCall, scope string) string {
	var total float64
	for _, c := range calls {
		total += c.usd
	}
	callOf := map[string]int{} // tool_use id -> index into calls
	for i, c := range calls {
		for _, u := range c.uses {
			callOf[py.Str(ojson.Field(u, "id"))] = i
		}
	}
	type line struct {
		usd  float64
		html string
	}
	var lines []line
	for _, k := range toollimits.Order {
		failed, follow := map[int]bool{}, map[int]bool{}
		hits := 0
		for _, it := range items {
			if !mine(it) || !it.HasTid {
				continue
			}
			i, ok := callOf[it.Tid]
			switch {
			case ok && has(it.LimitHit, k):
				hits++
				failed[i] = true
			case ok && it.Cat == "tool_use" && has(it.Limit, k):
				follow[i] = true
			}
		}
		if hits == 0 {
			continue
		}
		var usd float64
		var out int64
		var bad, then []string
		for _, i := range sortedIdx(failed) {
			usd += calls[i].usd
			out += getNum(calls[i].usage, "output_tokens")
			bad = append(bad, "#"+strconv.Itoa(i+1)+" "+strings.Replace(callLabel(calls[i]), " · ", " ", 1))
		}
		for _, i := range sortedIdx(follow) {
			if !failed[i] {
				then = append(then, "#"+strconv.Itoa(i+1)+" "+useName(calls[i].uses[0]))
			}
		}
		text := Esc(strings.Join(bad, "、")) + " " + limitVerb[k]
		if len(then) > 0 {
			text += "，" + Esc(strings.Join(then, "、")) + " 回頭補做"
		}
		lines = append(lines, line{usd, `<p class="lede">` + limitTag([]string{k})[1:] + ` ` + strconv.Itoa(hits) + ` 次 · <b class="tip" title="金額＝失敗那次呼叫的花費（輸出 ` +
			py.Comma(out) + ` token）">` + usdText(usd) + `</b>：` + text + `</p>`})
	}
	var n, nowTok, allN int
	var nowUSD, allUSD float64
	var allResend int64
	var at, langs []string
	for _, it := range items {
		if it.Script == "" {
			continue
		}
		allUSD += resendUSD(it)
		allResend += it.N * it.Resend
		if it.Cat == "tool_use" {
			allN++
		}
		if !mine(it) {
			continue
		}
		nowTok += int(it.N)
		nowUSD += resendUSD(it)
		if it.Cat == "tool_use" {
			n++
			if i, ok := callOf[it.Tid]; ok {
				at = append(at, "#"+strconv.Itoa(i+1))
			}
			if !slices.Contains(langs, it.Script) {
				langs = append(langs, it.Script)
			}
		}
	}
	if n > 0 {
		lines = append(lines, line{nowUSD, `<p class="lede">` + scriptTagHTML + ` ` + strconv.Itoa(n) + ` 次 · <b class="tip" title="` + scope + ` ` + py.Comma(int64(nowTok)) +
			` token。context 裡累積 ` + strconv.Itoa(allN) + ` 次，到目前重送累計 ` + py.Comma(allResend) + ` token，` + usdApprox(allUSD) + `">` +
			usdText(nowUSD) + `</b>：` + Esc(strings.Join(at, "、")+" "+strings.Join(langs, "/")) + `</p>`})
	}
	if usd, html := rewriteLine(t, total); html != "" {
		lines = append(lines, line{usd, `<p class="lede">` + html + `</p>`})
	}
	if len(lines) == 0 && rep == "" {
		return ""
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].usd > lines[j].usd })
	var sum float64
	var b strings.Builder
	for _, l := range lines {
		sum += l.usd
		b.WriteString(l.html)
	}
	title := "可以省的地方"
	if sum >= 0.01 {
		title += " · " + scope + " 約 " + usdText(sum)
	}
	return `<div class="sec">` + title + `</div>` + b.String() + rep
}

func sortedIdx(m map[int]bool) []int {
	var out []int
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// repeatLine is what the turn's 重複呼叫 tag on the session table counted, in full.
func repeatLine(path string, turn int) string {
	found := oneCached(path, 0)
	if found == nil {
		return ""
	}
	rep := remapRepeats(found, ojson.Obj(ojson.Field(healthSessions(), found.ID)))
	reasons := ojson.Obj(ojson.GetOr(ojson.GetOr(rep, "reasons"), strconv.Itoa(turn)))
	lbl, tip, ok := repTag("重複呼叫", reasons)
	if !ok {
		return ""
	}
	return `<p class="lede">` + Esc(lbl) + `：` + Esc(tip) + `</p>`
}

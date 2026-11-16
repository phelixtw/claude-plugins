package dash

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
	"agentperform/internal/refs"
	"agentperform/internal/repetition"
	"agentperform/internal/toollimits"
)

// dash_views.py: overview, day and conversation views.

func costCell(main float64, row *dayDele) string {
	d := 0.0
	if row != nil {
		d = row.cost
	}
	whole := main + d
	if d <= 0 {
		return `<td class="num" data-v="` + py.F(whole, 4) + `">` + money(whole) + `</td>`
	}
	return `<td class="num" data-v="` + py.F(whole, 4) + `" ` +
		`title="主對話 ` + money(main) + ` ＋ 委派 ` + strconv.Itoa(row.n) + ` 次 ` + money(d) +
		`，共 ` + money(whole) + `">` + money(whole) +
		`<div class="dim" style="font-size:10px;font-weight:400">` +
		`委派 ` + money(d) + ` · ` + strconv.Itoa(row.n) + ` 次</div></td>`
}

// ctxGrowth is (this turn's context increase, the usual increase before it).
func ctxGrowth(turns []*Turn) (*pnum, *pnum) {
	var sizes []int64
	for _, t := range turns {
		if c := ctxOf(t); c != 0 {
			sizes = append(sizes, c)
		}
	}
	var grew []int64
	for i := 1; i < len(sizes); i++ {
		grew = append(grew, sizes[i]-sizes[i-1])
	}
	var delta, usual *pnum
	if len(grew) > 0 {
		d := pi(grew[len(grew)-1])
		delta = &d
	}
	if len(grew) > 3 {
		m := median(grew[:len(grew)-1])
		usual = &m
	}
	return delta, usual
}

// ScorecardRow is the statusline fields for one live session (nil when unknown).
func ScorecardRow(sid, tp string) *ojson.Object {
	s := oneCached(tp, 0)
	if s == nil {
		return nil
	}
	applyWindow([]*Session{s})
	attachDelegation([]*Session{s})
	turns := s.Turns
	base := baseCost(Collect(1))
	w := waste(turns, base, nil)
	delta, usual := ctxGrowth(turns)
	rep := ojson.Field(healthSessions(), sid)
	numOrNil := func(p *pnum) any {
		if p == nil {
			return nil
		}
		if p.isInt {
			return int64(p.v)
		}
		return p.v
	}
	var repShare, dCost, dN any
	if rep != nil && ojson.Truthy(ojson.Field(rep, "turns")) {
		repShare = ojson.Field(rep, "share")
	}
	if s.Dele != nil {
		dCost, dN = ojson.Field(s.Dele, "cost"), ojson.Field(s.Dele, "n")
	}
	return ojson.NewObject().Set("id", sid).Set("turn_n", int64(len(turns))).Set("cost", s.Totals.Cost).
		Set("waste", w.total).Set("ctx_delta", numOrNil(delta)).Set("ctx_usual", numOrNil(usual)).
		Set("repeat_share", repShare).
		Set("delegate_cost", dCost).Set("delegate_n", dN)
}

// repTag is one short label with the number of repeated kinds, plus a tooltip.
func repTag(lbl string, reasons *ojson.Object) (string, string, bool) {
	if reasons.Len() == 0 {
		return "", "", false
	}
	keys := append([]string{}, reasons.Keys...)
	sort.Strings(keys)
	var parts []string
	for _, c := range keys {
		label, ok := repetition.ReasonLabel[c]
		if !ok {
			label = c
		}
		d := py.Str(reasons.Vals[c])
		if ojson.Truthy(reasons.Vals[c]) {
			label += "：" + d
		}
		parts = append(parts, label)
	}
	tip := "跟前 3 個回合重複：" + strings.Join(parts, "；")
	if reasons.Len() > 1 {
		return lbl + " ×" + strconv.Itoa(reasons.Len()), tip, true
	}
	return lbl, tip, true
}

func scriptTagTurn(main, sub int) string {
	if main+sub == 0 {
		return ""
	}
	n := strconv.Itoa(main + sub)
	return `<span class="pb scr" title="臨時腳本 ` + n + ` 次：主對話 ` + strconv.Itoa(main) + `、subagent ` + strconv.Itoa(sub) + `。` +
		`指 inline 直譯器程式碼（python3 - &lt;&lt;EOF、node -e），` +
		`或寫到 /tmp 等暫存目錄的腳本檔與之後執行或修改它">` +
		`臨時腳本 ×` + n + `</span>`
}

func sessionTags(picked []*Turn, w wasteT, repeated int, cost float64) string {
	main, sub := 0, 0
	for _, t := range picked {
		main += t.Scripts
		sub += t.DScripts
	}
	out := scriptTagTurn(main, sub)
	if repeated > 0 {
		out += `<span class="pb rep" title="有重複呼叫的回合 ` + strconv.Itoa(repeated) + ` / ` + strconv.Itoa(len(picked)) +
			`。同一個指令跑了不只一次；逐回合內容看對話頁">重複呼叫 ×` + strconv.Itoa(repeated) + `</span>`
	}
	slow := 0
	for _, fl := range w.flags {
		for _, f := range fl {
			if f.name == "slow" {
				slow++
				break
			}
		}
	}
	if slow > 0 {
		out += `<span class="pb slow" title="` + strconv.Itoa(slow) + ` 個回合等太久被中止，花費照算">做太久被中止 ×` + strconv.Itoa(slow) + `</span>`
	}
	total := py.NewCounter()
	for _, t := range picked {
		for _, k := range t.Limits.Keys {
			total.Add(k, t.Limits.N[k])
		}
		for _, k := range t.SubLimits.Keys {
			total.Add(k, t.SubLimits.N[k])
		}
	}
	for _, k := range total.Keys {
		if total.N[k] <= 0 {
			continue
		}
		l := toollimits.Limits[k]
		out += `<span class="pb lim" title="` + Esc(l[1]) + `">` + Esc(l[0]) + ` ×` + strconv.Itoa(total.N[k]) + `</span>`
	}
	if cost != 0 && rewriteUSD(picked)/cost >= hitFlag {
		out += hitPill(nil, rewriteUSD(picked), cost, false)
	}
	return out
}

// listedKinds is the waste kinds the 主對話花費 block lists.
func listedKinds(w wasteT, rep *ojson.Object, loud float64) map[string]bool {
	out := map[string]bool{}
	if w.ctx >= wasteMin {
		out["ctx"] = true
	}
	if w.slow >= wasteMin {
		out["slow"] = true
	}
	if rep != nil && ojson.Truthy(ojson.Field(rep, "turns")) {
		if share, _ := py.Number(ojson.Field(rep, "share")); share >= loud/3 {
			out["rep"] = true
		}
	}
	return out
}

func turnLink(s *Session, n int) string {
	if n == 0 || n > len(s.Turns) {
		return ""
	}
	return `<a href="/pipeline?session=` + s.ID + `&at=` + py.FloatRepr(s.Turns[n-1].At) + `">` + Esc(py.Head(s.ID, 8)) + `#` + strconv.Itoa(n) + `</a>`
}

func linksCell(links []string) string {
	var uniq []string
	seen := map[string]bool{}
	for _, l := range links {
		if !seen[l] {
			seen[l] = true
			uniq = append(uniq, l)
		}
	}
	out := strings.Join(uniq[:min(4, len(uniq))], " ")
	if len(uniq) > 4 {
		out += ` <span class="dim">另有 ` + strconv.Itoa(len(uniq)-4) + ` 個回合</span>`
	}
	return out
}

// scriptRef is one (record, session, turn number) of a throwaway script call.
type scriptRef struct {
	rec  ScriptRec
	s    *Session
	turn int // 0 = None
}

// scriptRecords is fp -> calls for every throwaway script whose session and time pass keep.
func scriptRecords(sessions []*Session, keep func(*Session, float64) bool) ([]string, map[string][]scriptRef) {
	groups := map[string][]scriptRef{}
	var order []string
	add := func(fp string, r scriptRef) {
		if _, ok := groups[fp]; !ok {
			order = append(order, fp)
		}
		groups[fp] = append(groups[fp], r)
	}
	for _, s := range sessions {
		for i, t := range s.Turns {
			if keep(s, t.At) {
				for _, r := range t.ScriptCalls {
					add(py.Str(ojson.Field(r, "fp")), scriptRef{r, s, i + 1})
				}
			}
		}
		for _, l := range launchesOf(s.Dele) {
			if keep(s, fnum(l, "at")) {
				calls, _ := ojson.Field(l, "script_calls").([]any)
				for _, r := range calls {
					add(py.Str(ojson.Field(r, "fp")), scriptRef{ojson.Obj(r), s, int(num(ojson.Field(l, "turn")))})
				}
			}
		}
	}
	return order, groups
}

func recTok(r ScriptRec) int64 { return num(ojson.Field(r, "tok")) + num(ojson.Field(r, "out")) }

// daySpan is (local midnight of day, +86400).
func daySpan(d string) (float64, float64) {
	t, _ := parseDay(d)
	lo := float64(t.UnixNano()) / 1e9
	return lo, lo + 86400
}

type repKey struct{ code, detail string }

// repeatRecords is (reason, detail) -> [(session, turn number)] for the flagged repeat calls.
func repeatRecords(sessions []*Session, hp *ojson.Object, keep func(*Session, float64) bool) ([]repKey, map[repKey][]scriptRef) {
	groups := map[repKey][]scriptRef{}
	var order []repKey
	parallel(len(sessions), func(i int) { // the scans of changed sessions run side by side
		if s := sessions[i]; s.Path != "" && ojson.Obj(ojson.Field(ojson.Field(hp, "sessions"), s.ID)).Len() > 0 {
			if sig, err := mainSig(s.Path); err == nil {
				liveRepeats(s.Path, sig)
			}
		}
	})
	for _, s := range sessions {
		rep := remapRepeats(s, ojson.Obj(ojson.Field(ojson.Field(hp, "sessions"), s.ID)))
		rs := ojson.Obj(ojson.GetOr(rep, "reasons"))
		if rs == nil {
			continue
		}
		for _, nk := range rs.Keys {
			n, _ := strconv.Atoi(nk)
			if n < 1 || n > len(s.Turns) || !keep(s, s.Turns[n-1].At) {
				continue
			}
			reasons := ojson.Obj(rs.Vals[nk])
			for _, code := range reasons.Keys {
				detail := ""
				if d, ok := reasons.Vals[code].(string); ok && code != "4" {
					detail = d
				}
				k := repKey{code, detail}
				if _, ok := groups[k]; !ok {
					order = append(order, k)
				}
				groups[k] = append(groups[k], scriptRef{nil, s, n})
			}
		}
	}
	return order, groups
}

var (
	adviceMu       sync.Mutex
	repeatAdviceRe = map[int]*pyre.Re{}
)

// repeatType is (type label, advice) of one repeated call.
func repeatType(code, detail string) (string, string) {
	for i, r := range repeatAdvice {
		if r.code != code {
			continue
		}
		if r.pat == "" {
			return r.label, r.text
		}
		adviceMu.Lock()
		re, ok := repeatAdviceRe[i]
		if !ok {
			re = pyre.Compile(r.pat)
			repeatAdviceRe[i] = re
		}
		adviceMu.Unlock()
		if re.Has(detail) {
			return r.label, r.text
		}
	}
	return "", ""
}

func mcpLabel(name string) string {
	if strings.HasPrefix(name, "plugin_") {
		return name[strings.LastIndex(name, "_")+1:]
	}
	return name
}

func spanText(start, end float64) string {
	mins := int64((end - start) / 60)
	text := strconv.FormatInt(mins, 10) + " 分鐘"
	if mins >= 60 {
		text = strconv.FormatInt(mins/60, 10) + " 小時 " + strconv.FormatInt(mins%60, 10) + " 分"
	}
	n := daysBetween(dateOf(start), dateOf(end)) + 1
	if n > 1 {
		text += "（跨 " + strconv.Itoa(n) + " 天）"
	}
	return text
}

func dateOf(ts float64) time.Time {
	t := fromTimestamp(ts)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
}

// example is the saving formula worked through once with the pooled medians.
func example(m any) string {
	cr, cw := tmCR/1e6, tmCW/1e6
	g := func(k string) int64 { return num(ojson.Field(m, k)) }
	C, K, r, P, gs, D, Res := g("C"), g("K"), g("r_s"), g("P_s"), g("g_s"), g("D_e"), g("Res")
	tri := r * (r - 1) / 2
	mainV := r*C + gs*tri
	pre := r * P
	tail := (D - Res) * K
	saved := float64(cr * float64(mainV-pre+tail))
	boot := float64(cw * float64(P)) // float64(): no FMA with the sum below
	reread := float64(cr * float64(pre+gs*tri))
	paid := boot + reread
	t := func(v int64) string {
		if v < 10_000 {
			return py.CommaF(float64(v)/1000, 1) + "k"
		}
		return py.CommaF(float64(v)/1000, 0) + "k"
	}
	R, W := "$"+py.G(tmCR)+"/M", "$"+py.G(tmCW)+"/M"
	rs, tris := strconv.FormatInt(r, 10), strconv.FormatInt(tri, 10)
	note := strings.NewReplacer("{r}", rs, "{C}", t(C), "{g}", t(gs), "{tri}", tris, "{n}", strconv.FormatInt(r-1, 10)).Replace(exampleNote)
	return "<pre>" +
		"省下的重讀費 = cache 讀價 × [ 主對話自己做時每次 API 呼叫要讀的 context − subagent 的前綴" +
		"\n              + (cache 寫入 − 回傳結論) × 之後主對話 API 呼叫數 ]" +
		"\n        = " + R + " × [ (" + rs + "×" + t(C) + " + " + t(gs) + "×" + tris + ") − " + rs + "×" + t(P) +
		"\n              + (" + t(D) + " − " + t(Res) + ") × " + strconv.FormatInt(K, 10) + " ]" +
		"\n        = " + R + " × [ " + t(mainV) + " − " + t(pre) + " + " + t(tail) + " ]" +
		"\n        = $" + py.F(saved, 2) +
		"\n\nsubagent 自身花費 = cache 寫價 × subagent 的前綴" +
		"\n           + cache 讀價 × (subagent 每次 API 呼叫重讀的前綴 + subagent 累積的 context)" +
		"\n        = " + W + " × " + t(P) + " + " + R + " × (" + rs + "×" + t(P) + " + " + t(gs) + "×" + tris + ")" +
		"\n        = $" + py.F(boot, 2) + " + $" + py.F(reread, 2) +
		"\n        = $" + py.F(paid, 2) +
		"\n\n淨省     = $" + py.F(saved, 2) + " − $" + py.F(paid, 2) + " = $" + py.F(saved-paid, 2) + note
}

// costTable is the delegation saving table (one pooled row).
func costTable(sessions []*Session) string {
	spent := 0.0
	for _, x := range sessions {
		for _, l := range launchesOf(x.Dele) {
			spent += fnum(l, "cost")
		}
	}
	m := tmCached()
	if ojson.Obj(m).Len() == 0 {
		return empty(noDelegate)
	}
	tk := func(k string) string { return toks(numOf(ojson.Field(m, k))) }
	per := fnum(m, "per")
	perCls := "dim"
	if per > 1 {
		perCls = "good"
	}
	body := `<tr><td class="num dim">` + numOf(ojson.Field(m, "n")).str() + `</td>` +
		`<td class="num">` + tk("C") + `</td>` +
		`<td class="num">` + numOf(ojson.Field(m, "K")).str() + `</td>` +
		`<td class="num">` + numOf(ojson.Field(m, "r_s")).str() + `</td>` +
		`<td class="num">` + tk("D_e") + `</td>` +
		`<td class="num dim">` + tk("Res") + `</td>` +
		`<td class="num good">` + money(fnum(m, "saved")) + `</td>` +
		`<td class="num bad">` + money(fnum(m, "paid")) + `</td>` +
		`<td class="num hit ` + perCls + `">` + money(per) + `</td>` +
		`<td class="num dim">` + money(spent) + `</td></tr>`
	return table([]string{"委派次數", "委派時 context", "之後主對話 API 呼叫數",
		"subagent API 呼叫數", "cache 寫入", "回傳結論",
		"省下的重讀費", "subagent 自身花費", "每次淨省", "委派實際花費"},
		[]string{body}, "", "", []tableGroup{{"主對話", 3}, {"subagent", 3}, {"績效", 4}}) +
		toggles([2]string{"這張表怎麼算的", costTableHowA + example(m) + costTableHowB}, [2]string{"每一欄是什麼", costTableCols})
}

// numOf is a JSON number as a Python int or float.
func numOf(v any) pnum {
	f, _ := py.Number(v)
	return pnum{f, py.IsInt(v)}
}

var _ = refs.Cap

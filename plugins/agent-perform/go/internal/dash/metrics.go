package dash

import (
	"sort"
	"strconv"
	"strings"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
)

// Token, subagent, skill and instruction aggregates behind the
// subagent table, the skill token column and the 最貴的回合 page. The per-turn numbers
// (Usage, SkillTok, Notes, Active) come out of the transcript parse; subagent tokens come
// from the delegation scan (attachDelegation). Everything here only adds them up.

const (
	expensiveRows = 30
)

func usageTokens(u Usage) int64 { return u.In + u.Out + u.Cr + u.Cw }

// launchTokens is every token one subagent launch used (0 when its transcript was not paired).
func launchTokens(l any) int64 {
	t := ojson.GetOr(l, "tok")
	return getNum(t, "in") + getNum(t, "out") + getNum(t, "cr") + getNum(t, "cw")
}

// turnTokens is the tokens of one instruction: its own API calls (background notifications included) plus its subagents.
func turnTokens(t *Turn) int64 { return usageTokens(t.Usage) + t.DTok }

// windowLaunches are the session's subagent launches that started inside the window (the session's parse starts at its floor).
func windowLaunches(s *Session) []*ojson.Object {
	var out []*ojson.Object
	for _, l := range launchesOf(s.Dele) {
		if fnum(l, "at") >= s.Start {
			out = append(out, l)
		}
	}
	return out
}

func sessionTokens(s *Session) (main, sub int64) {
	main = usageTokens(s.Totals)
	for _, l := range windowLaunches(s) {
		sub += launchTokens(l)
	}
	return
}

// tokenSplit is all tokens of the sessions: main conversations and their subagents.
func tokenSplit(sessions []*Session) (main, sub int64) {
	for _, s := range sessions {
		m, b := sessionTokens(s)
		main, sub = main+m, sub+b
	}
	return
}

func shareOf(n, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total)
}

func pct1(v float64) string {
	if v > 0 && v < 0.0005 {
		return "< 0.1%"
	}
	return py.F(v*100, 1) + "%"
}

// activeSplit is (working seconds, wall-clock seconds) summed over the sessions.
func activeSplit(sessions []*Session) (active, wall float64) {
	for _, s := range sessions {
		active += s.Active
		wall += s.End - s.Start
	}
	return
}

func hours(sec float64) string { return py.F(sec/3600, 1) + " 小時" }

type kindStat struct {
	kind  string
	n     int
	tok   int64
	cost  float64
	calls int
}

// subagentKinds is the subagent launches by type, most tokens first.
func subagentKinds(sessions []*Session) []*kindStat {
	by := map[string]*kindStat{}
	var order []*kindStat
	for _, s := range sessions {
		for _, l := range windowLaunches(s) {
			k := py.Str(orNil(ojson.GetOr(l, "kind"), "claude"))
			st := by[k]
			if st == nil {
				st = &kindStat{kind: k}
				by[k] = st
				order = append(order, st)
			}
			st.n++
			st.tok += launchTokens(l)
			st.cost += fnum(l, "cost")
			st.calls += int(fnum(l, "calls"))
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].tok > order[j].tok })
	return order
}

// skillCanon maps a skill name as called to the installed skill it is (the name itself when unknown or ambiguous).
func skillCanon(inv *orderedSkills) func(string) string {
	short := map[string][]string{}
	for _, k := range inv.keys {
		parts := strings.Split(k, ":")
		short[parts[len(parts)-1]] = append(short[parts[len(parts)-1]], k)
	}
	return func(n string) string {
		if _, ok := inv.m[n]; ok {
			return n
		}
		parts := strings.Split(n, ":")
		if hit := short[parts[len(parts)-1]]; len(hit) == 1 {
			return hit[0]
		}
		return n
	}
}

// skillTokens is the tokens attributed to each skill (by canonical name) in the main conversations; a subagent never gets a human message to end a skill, so its runs are left out.
func skillTokens(sessions []*Session, canon func(string) string) map[string]int64 {
	out := map[string]int64{}
	for _, s := range sessions {
		for _, t := range s.Turns {
			if t.SkillTok == nil {
				continue
			}
			for _, k := range t.SkillTok.Keys {
				out[canon(k)] += int64(t.SkillTok.N[k])
			}
		}
	}
	return out
}

type instr struct {
	s   *Session
	t   *Turn
	i   int // index in s.Turns, the #t{i} anchor on the session page
	tok int64
}

// topInstructions is the n instructions that used the most tokens, subagents included.
func topInstructions(sessions []*Session, n int) []instr {
	var all []instr
	for _, s := range sessions {
		for i, t := range s.Turns {
			if t.Calls > 0 {
				all = append(all, instr{s, t, i, turnTokens(t)})
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].tok > all[j].tok })
	if len(all) > n {
		all = all[:n]
	}
	return all
}

// kindTable is the delegate page's table: launches, tokens, share and cost by subagent type.
func kindTable(sessions []*Session) string {
	main, sub := tokenSplit(sessions)
	total := main + sub
	var rows []string
	for _, k := range subagentKinds(sessions) {
		rows = append(rows, `<tr><td>`+Esc(k.kind)+`</td><td class="num" data-v="`+strconv.Itoa(k.n)+`">`+strconv.Itoa(k.n)+`</td>`+
			`<td class="num" data-v="`+strconv.FormatInt(k.tok, 10)+`">`+toks(pi(k.tok))+`</td>`+
			`<td class="num" data-v="`+py.F(shareOf(k.tok, total), 6)+`">`+pct1(shareOf(k.tok, total))+`</td>`+
			`<td class="num" data-v="`+py.F(k.cost, 6)+`">`+money(k.cost)+`</td></tr>`)
	}
	if len(rows) == 0 {
		return ""
	}
	return table([]string{"類型", "次數", "token", "佔總量", "花費"}, rows, "", "", nil) + noteToggle(subagentNote, "說明")
}

// ExpensiveView is the 最貴的回合 page: instructions ranked by all tokens they caused, subagents included.
func ExpensiveView(sessions []*Session, days int) string {
	head := `<h1 class="view">` + expensiveTitle + `</h1>`
	top := topInstructions(sessions, expensiveRows)
	if len(top) == 0 {
		return head + empty(expensiveEmpty)
	}
	main, sub := tokenSplit(sessions)
	total := main + sub
	var rows []string
	for i, x := range top {
		text := Esc(x.t.Prompt)
		if x.t.Notes > 0 {
			text += ` <span class="dim">＋` + strconv.Itoa(x.t.Notes) + ` 則自動接續</span>`
		}
		rows = append(rows, `<tr onclick="location='/?session=`+Esc(x.s.ID)+`#t`+strconv.Itoa(x.i+1)+`'">`+
			`<td class="num">`+strconv.Itoa(i+1)+`</td>`+
			`<td style="white-space:normal;min-width:200px">`+text+`</td>`+
			`<td class="num" data-v="`+strconv.FormatInt(x.tok, 10)+`">`+toks(pi(x.tok))+`</td>`+
			`<td class="num">`+pct1(shareOf(x.tok, total))+`</td>`+
			`<td class="num" data-v="`+py.F(turnCost(x.t), 6)+`">`+money(turnCost(x.t))+`</td>`+
			`<td class="num">`+strconv.Itoa(x.t.Calls+x.t.DCalls)+`</td>`+
			`<td class="num">`+strconv.Itoa(x.t.Dele)+`</td>`+
			`<td class="mono">`+Esc(x.s.ID)+`</td></tr>`)
	}
	// page template: tiles, findings, table
	t0 := top[0]
	sum10, notes, bySess := 0.0, 0, map[string]int{}
	allCost := 0.0
	for _, x := range sessions {
		for _, t := range x.Turns {
			allCost += turnCost(t)
		}
	}
	for i, x := range top {
		if i < 10 {
			sum10 += turnCost(x.t)
			bySess[x.s.ID]++
		}
		if x.t.Notes > 0 {
			notes++
		}
	}
	nDele, short, shortQuotes := 0, 0, []string{}
	for i, x := range top {
		if x.t.Dele > 0 {
			nDele++
		}
		if q := cleanOpening(x.t.Prompt); i < 10 && q != "" && py.Len(q) <= 12 {
			short++
			shortQuotes = append(shortQuotes, "「"+Esc(q)+"」")
		}
	}
	ratio := func(a, b float64) string { return py.F(a/b*100, 1) + "%" }
	tiles := hotTile(stats([]stat{
		{"最貴的一句", money0(turnCost(t0.t)), "佔總花費 " + ratio(turnCost(t0.t), allCost), "", "精確值 " + money(turnCost(t0.t)) + "；" + toks(pi(t0.tok)) + " token，佔總量 " + pct1(shareOf(t0.tok, total))},
		{"前 10 句合計", money0(sum10), "佔總花費 " + ratio(sum10, allCost), "", "精確值 " + money(sum10)},
		{"帶著自動接續", strconv.Itoa(notes) + " 句", "背景通知已併回", "", expensiveLede},
		{"有委派", strconv.Itoa(nDele) + " 句", "含 subagent 花費", "", ""},
	}), 0)
	var fs []nfind
	bn := 0
	for _, n := range bySess {
		bn = max(bn, n)
	}
	var tops []string
	for id, n := range bySess {
		if n == bn {
			tops = append(tops, id)
		}
	}
	sort.Strings(tops)
	for i := range tops {
		tops[i] = "<b>" + Esc(tops[i]) + "</b>"
	}
	if bn > 1 {
		fs = append(fs, nfind{"hot", strconv.Itoa(bn) + " 句", "前 10 名裡，" + strings.Join(tops, "、") + " 各有 " + strconv.Itoa(bn) + " 句", "#list", "看列表 ↓"})
	}
	if len(top) > 1 {
		fs = append(fs, nfind{map[bool]string{true: "", false: "hot"}[bn > 1], money0(turnCost(t0.t)), "第 1 名比第 2 名多花 " + money0(turnCost(t0.t)-turnCost(top[1].t)) + "；它啟動了 " + strconv.Itoa(t0.t.Dele) + " 個 subagent", "#list", "看列表 ↓"})
	}
	if short > 0 {
		fs = append(fs, nfind{"", strconv.Itoa(short) + " 句", "前 10 名裡有 " + strconv.Itoa(short) + " 句只有短短幾個字（" + strings.Join(shortQuotes, "") + "）", "#list", "看列表 ↓"})
	}
	return head + `<p class="lede">近 ` + strconv.Itoa(days) + ` 天最貴的一句話是「` + Esc(py.Head(cleanOpening(t0.t.Prompt), 24)) + `」，連同它啟動的 subagent 一共花了 <b>` + money0(turnCost(t0.t)) + `</b>。</p>` + `<div class="hero">` + tiles + `</div>` + finds(fs) +
		listCard(expensiveTitle, table([]string{"#", expensiveHead, "token", "佔總量", "花費", "API 呼叫", "委派", "對話"}, rows, "click", "", nil)) + fold("說明", expensiveNote+`<p>`+expensiveLede+`</p>`)
}

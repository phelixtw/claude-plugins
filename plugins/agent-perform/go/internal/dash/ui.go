package dash

import (
	"crypto/md5"
	"fmt"
	"math"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentperform/internal/libm"
	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/refs"
)

// dash_ui.py: formatting helpers, cells, charts and before/after stats.

const (
	hitFlag     = 0.10
	ctxRatio    = 0.4
	bigCtx      = 400_000
	slowKillSec = 60
	slowKillMax = 30 * 60
	minTurns    = 30
	minDays     = 7
	wasteMin    = 0.5
	deleMin     = 3.0
)

var (
	marks          = [][2]string{{"2026-10-08", "8 項實驗性調整開始"}}
	modelShortKeys = []string{"opus", "sonnet", "haiku", "fable"}
	modelShort     = map[string]string{"opus": "Opus", "sonnet": "Sonnet", "haiku": "Haiku", "fable": "Fable"}
	fixZH          = map[string][2]string{"slow": {"做太久被中止", "這個回合等太久被你中止，花費照算"},
		"ctx": {"context 太長", "這個回合最大的一次 API 呼叫超過模型視窗的 40%"},
		"rep": {"重複呼叫", "同一個指令跑了不只一次"}}
	rangeHSL = map[string][3]int{"<150k": {190, 30, 55}, "150-300k": {42, 52, 55}, "300-500k": {26, 62, 52}, ">500k": {12, 58, 47}}
)

// num is a Python number that remembers whether it is an int.
type pnum struct {
	v     float64
	isInt bool
}

func pi(v int64) pnum   { return pnum{float64(v), true} }
func pf(v float64) pnum { return pnum{v, false} }

func (n pnum) str() string { return py.NumStr(n.v, n.isInt) }

func money(v float64) string {
	if v >= 0.01 || v == 0 {
		return "$" + py.CommaF(v, 2)
	}
	return "$" + py.F(v, 4)
}

func secs(v *float64) string {
	if v == nil {
		return "—"
	}
	x := *v
	if x >= 60 {
		return strconv.FormatInt(int64(math.Floor(x/60)), 10) + "m" + fmt.Sprintf("%02d", int64(math.Mod(x, 60))) + "s"
	}
	return py.F(x, 1) + "s"
}

func toks(n pnum) string {
	switch {
	case n.v >= 1_000_000:
		return py.F(n.v/1_000_000, 1) + "M"
	case n.v >= 1000:
		return py.F(n.v/1000, 0) + "k"
	}
	return n.str()
}

func pctAt(vals []float64, q float64) *float64 {
	if len(vals) == 0 {
		return nil
	}
	o := append([]float64{}, vals...)
	sort.Float64s(o)
	v := o[min(int(float64(len(o))*q/100), len(o)-1)]
	return &v
}

func modelLabel(name string) string {
	if name == "<synthetic>" {
		return "系統產生（無模型名稱）"
	}
	return name
}

func shortModel(name string) string {
	for _, k := range modelShortKeys {
		if strings.Contains(name, k) {
			return modelShort[k]
		}
	}
	if name == "<synthetic>" {
		return "系統產生"
	}
	return py.Head(name, 10)
}

// modelTag is which model answered this turn, and at what share when more than one.
func modelTag(models *floatCounter) string {
	var keys []string
	total := 0.0
	if models != nil {
		for _, k := range models.keys {
			if models.n[k] > 0 {
				keys = append(keys, k)
				total += models.n[k]
			}
		}
	}
	if len(keys) == 0 {
		return `<span class="dim">—</span>`
	}
	sort.SliceStable(keys, func(i, j int) bool { return models.n[keys[i]] > models.n[keys[j]] })
	var b strings.Builder
	for _, name := range keys {
		cost := models.n[name]
		short := shortModel(name)
		share := ""
		if len(keys) > 1 {
			share = `<em>` + py.F(cost/total*100, 0) + `%</em>`
		}
		b.WriteString(`<span class="mdl ` + strings.ToLower(short) + `" title="` + Esc(modelLabel(name)) + ` · ` + money(cost) + `">` + short + share + `</span>`)
	}
	return b.String()
}

func hitStr(rate *float64) string {
	if rate == nil {
		return "—"
	}
	if *rate >= 1.0 {
		return "100%"
	}
	return py.F(float64(int64(*rate*1000))/10, 1) + "%"
}

func warmUsage(turns []*Turn, sessions []*Session) Usage {
	firsts := map[*Turn]bool{}
	for _, s := range sessions {
		if len(s.Turns) > 0 {
			firsts[s.Turns[0]] = true
		}
	}
	var u Usage
	for _, t := range turns {
		if !firsts[t] {
			u.add(t.Usage)
		}
	}
	return u
}

func rewriteUSD(turns []*Turn) float64 {
	v := 0.0
	for _, t := range turns {
		v += t.Rewrite
	}
	return v
}

func hitPill(rate *float64, rewrite, cost float64, showHit bool) string {
	share := 0.0
	if cost != 0 {
		share = rewrite / cost
	}
	flag := share >= hitFlag
	cls, reach := "rep", "，未達 10%"
	if flag {
		cls, reach = "cache", "，達 10%"
	}
	s := `<span class="pb ` + cls + `" title="Cache hit＝cache 讀 ÷ 送出的 token（每個對話的第一個回合不列入）。Cache 過期重寫 ` +
		money(rewrite) + `，佔這列花費的 ` + py.F(float64(int64(share*1000))/10, 1) + `%` + reach + `">`
	if flag {
		s += `Cache 過期重寫 ` + strconv.FormatInt(int64(share*100), 10) + `%`
	}
	if flag && showHit {
		s += " · "
	}
	if showHit {
		s += `Cache hit ` + hitStr(rate)
	}
	return s + `</span>`
}

func rewriteFlagged(t *Turn, index int) bool {
	u := t.Usage
	return index > 0 && hitRate(u) != nil && u.Cost > 0 && t.Rewrite/u.Cost >= hitFlag
}

func hitTagCell(rate *float64, rewrite, cost float64) string {
	dv := "-1.0000"
	body := "—"
	if rate != nil {
		dv = py.F(*rate, 4)
		body = hitPill(rate, rewrite, cost, true)
	}
	return `<td class="prob" data-v="` + dv + `">` + body + `</td>`
}

func ctxOf(t *Turn) int64 {
	if t.CtxPeak != 0 {
		return t.CtxPeak
	}
	return t.Usage.In + t.Usage.Cr + t.Usage.Cw
}

func ctxBig(t *Turn) bool {
	w := t.Window
	if w == 0 {
		w = 1_000_000
	}
	return float64(ctxOf(t)) > ctxRatio*float64(w)
}

func killedSlow(t *Turn) bool {
	if !t.Killed {
		return false
	}
	return slowKillSec <= t.E2E && t.E2E <= slowKillMax
}

func ctxCell(turns []*Turn) string {
	var sizes []int64
	for _, t := range turns {
		if c := ctxOf(t); c != 0 {
			sizes = append(sizes, c)
		}
	}
	if len(sizes) == 0 {
		return `<td class="hit dim" data-v="-1">—</td>`
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })
	mid := sizes[len(sizes)/2]
	big := 0
	for _, t := range turns {
		if ctxOf(t) != 0 && ctxBig(t) {
			big++
		}
	}
	over := float64(big) / float64(len(sizes))
	tone := "calm"
	if mid > bigCtx {
		tone = "bad"
	} else if mid > 150_000 {
		tone = "mid"
	}
	body := toks(pi(mid))
	if tone == "calm" {
		body = `<span class="dim">` + toks(pi(mid)) + `</span>`
	}
	return `<td class="hit ` + tone + ` num" data-v="` + strconv.FormatInt(mid, 10) + `" ` +
		`title="中位 context ` + py.Comma(mid) + ` · ` + py.F(over*100, 0) + `% 的回合超過視窗 40%">` + body + `</td>`
}

// median is statistics.median of ints: an int for an odd count, a float otherwise.
func median(vals []int64) pnum {
	o := append([]int64{}, vals...)
	sort.Slice(o, func(i, j int) bool { return o[i] < o[j] })
	n := len(o)
	if n%2 == 1 {
		return pi(o[n/2])
	}
	return pf(float64(o[n/2-1]+o[n/2]) / 2)
}

// pstdev is statistics.pstdev of ints, correctly rounded like Python's.
func pstdev(vals []int64) float64 {
	n := big.NewInt(int64(len(vals)))
	sum, sq := new(big.Int), new(big.Int)
	for _, v := range vals {
		x := big.NewInt(v)
		sum.Add(sum, x)
		sq.Add(sq, new(big.Int).Mul(x, x))
	}
	num := new(big.Int).Sub(new(big.Int).Mul(n, sq), new(big.Int).Mul(sum, sum)) // n*Σx² − (Σx)²
	f := new(big.Float).SetPrec(512).SetInt(num)
	f.Sqrt(f)
	f.Quo(f, new(big.Float).SetPrec(512).SetInt(n))
	out, _ := f.Float64()
	return out
}

func ctxStatCell(values []int64, unit string) string {
	if len(values) == 0 {
		return `<td class="hit dim" data-v="-1">—</td>`
	}
	var total int64
	for _, v := range values {
		total += v
	}
	mid := median(values)
	std := pi(0)
	if len(values) > 1 {
		std = pf(pstdev(values))
	}
	tone := "calm"
	if mid.v > bigCtx {
		tone = "bad"
	} else if mid.v > 150_000 {
		tone = "mid"
	}
	return `<td class="hit ` + tone + ` num" data-v="` + mid.str() + `" ` +
		`title="` + strconv.Itoa(len(values)) + ` ` + unit + ` · 總 ` + py.Comma(total) + ` · 中位 ` + py.CommaNum(mid.v, mid.isInt) +
		` · 標準差 ` + py.CommaF(std.v, 0) + `">` + toks(pi(total)) + `/` + toks(mid) + `/` + toks(std) + `</td>`
}

var lineDefs = [][2]string{{"一般", "一般＝由小排到大正中間那個回合（p50）"},
	{"較高", "較高＝每 4 個回合有 1 個比它高（p75）"},
	{"偏高", "偏高＝每 10 個回合有 1 個比它高（p90）"},
	{"極高", "極高＝每 100 個回合有 1 個比它高（p99）"}}

// pctOpt is the echarts option for p50/p75/p90 by day.
func pctOpt(labels []string, series map[int][]any, marksIn [][2]string) *ojson.Object {
	if len(labels) == 0 {
		return nil
	}
	var opts []any
	for _, q := range []struct {
		q           int
		name, color string
	}{{50, "一般", "#5b8def"}, {75, "較高", "#e0a13a"}, {90, "偏高", "#e0553a"}, {99, "極高", "#a33bd6"}} {
		data, ok := series[q.q]
		if !ok {
			continue
		}
		opts = append(opts, ojson.NewObject().Set("name", q.name).Set("type", "line").Set("showSymbol", true).
			Set("symbolSize", int64(6)).Set("emphasis", ojson.NewObject().Set("disabled", true)).
			Set("legendHoverLink", false).Set("itemStyle", ojson.NewObject().Set("color", q.color)).Set("data", data))
	}
	in := map[string]bool{}
	for _, l := range labels {
		in[l] = true
	}
	var lines []any
	for _, m := range marksIn {
		if in[m[0]] {
			lines = append(lines, ojson.NewObject().Set("xAxis", m[0]).Set("label", ojson.NewObject().Set("formatter", m[1])))
		}
	}
	if len(lines) > 0 {
		ojson.Obj(opts[0]).Set("markLine", ojson.NewObject().Set("symbol", "none").
			Set("lineStyle", ojson.NewObject().Set("type", "dashed")).Set("data", lines))
	}
	lab := []any{}
	for _, l := range labels {
		lab = append(lab, l)
	}
	if opts == nil {
		opts = []any{}
	}
	return ojson.NewObject().Set("backgroundColor", "transparent").
		Set("grid", ojson.NewObject().Set("left", int64(56)).Set("right", int64(24)).Set("top", int64(48)).Set("bottom", int64(56))).
		Set("legend", ojson.NewObject().Set("top", int64(4))).
		Set("tooltip", ojson.NewObject().Set("trigger", "axis")).
		Set("xAxis", ojson.NewObject().Set("type", "category").Set("data", lab).
			Set("axisLabel", ojson.NewObject().Set("rotate", int64(0)).Set("hideOverlap", true))).
		Set("yAxis", ojson.NewObject().Set("type", "value")).
		Set("series", opts)
}

type comboEntry struct {
	label, unit string
	opt         *ojson.Object
}

func comboChart(entries []comboEntry) string {
	var live []comboEntry
	for _, e := range entries {
		if e.opt != nil {
			live = append(live, e)
		}
	}
	if len(live) == 0 {
		return empty("這段時間沒有資料。")
	}
	var options strings.Builder
	var payload []any
	drawn := map[string]bool{}
	for i, e := range live {
		options.WriteString(`<option value="` + strconv.Itoa(i) + `">` + Esc(e.label) + `</option>`)
		payload = append(payload, ojson.NewObject().Set("unit", e.unit).Set("opt", e.opt))
		series, _ := ojson.Field(e.opt, "series").([]any)
		for _, s := range series {
			drawn[py.Str(ojson.Field(s, "name"))] = true
		}
	}
	var tips []string
	for _, d := range lineDefs {
		if drawn[d[0]] {
			tips = append(tips, d[1])
		}
	}
	return `<div class="pctc-combo">` +
		`<select class="pctc-sel" title="` + Esc(strings.Join(tips, "；")) + `">` + options.String() + `</select>` +
		`<div class="pctc" data-opts="` + Esc(py.Dumps(payload)) + `"></div>` + `</div>`
}

var (
	baseMu  sync.Mutex
	baseVal *float64
)

// baseCost is the median $/turn among the reader's own 150-300k turns, worked
// out once per process like the Python module global.
func baseCost(sessions []*Session) float64 {
	baseMu.Lock()
	defer baseMu.Unlock()
	if baseVal == nil {
		var cheap []float64
		for _, s := range sessions {
			for _, t := range s.Turns {
				if c := ctxOf(t); 150_000 <= c && c < 300_000 {
					cheap = append(cheap, t.Usage.Cost)
				}
			}
		}
		sort.Float64s(cheap)
		v := 0.0
		if len(cheap) > 0 {
			v = cheap[len(cheap)/2]
		}
		baseVal = &v
	}
	return *baseVal
}

type flag struct {
	name string
	amt  float64
}

type wasteT struct {
	ctx, slow, cache, total float64
	flags                   map[int][]flag
}

// waste is what of this spend was avoidable, split by the lever that fixes it.
func waste(turns []*Turn, base float64, rep *ojson.Object) wasteT {
	w := wasteT{flags: map[int][]flag{}}
	for i, t := range turns {
		switch {
		case killedSlow(t):
			gone := t.Usage.Cost
			w.slow += gone
			w.flags[i] = append(w.flags[i], flag{"slow", gone})
		case ctxBig(t):
			over := max(0.0, t.Usage.Cost-t.Rewrite-base)
			w.ctx += over
			if over > 0 {
				w.flags[i] = append(w.flags[i], flag{"ctx", over})
			}
			fallthrough
		default:
			// rewrite cost is split out of ctx above so it is not counted twice
			if t.Rewrite > 0 {
				w.cache += t.Rewrite // no flag: the turn's Cache hit tag already shows it
			}
		}
	}
	fl, _ := ojson.Field(rep, "flagged").([]any)
	seen := map[int]bool{}
	for _, n := range fl {
		i := int(num(n)) - 1
		if seen[i] {
			continue
		}
		seen[i] = true
		if 0 <= i && i < len(turns) {
			w.flags[i] = append(w.flags[i], flag{"rep", 0})
		}
	}
	w.total = w.ctx + w.slow + w.cache
	return w
}

func wasteCell(w wasteT, cost float64) string {
	v := w.total
	if cost == 0 || v <= 0 {
		return `<td class="hit ok" data-v="` + py.F(v, 4) + `">—</td>`
	}
	if v < 0.5 {
		return `<td class="hit ok num dim" data-v="` + py.F(v, 4) + `">` + money(v) + `</td>`
	}
	share := v / cost
	tone := "calm"
	if v >= 100 {
		tone = "bad"
	} else if v >= 20 {
		tone = "mid"
	}
	return `<td class="hit ` + tone + ` num" data-v="` + py.F(v, 4) + `" ` +
		`title="浪費佔主對話花費 ` + py.F(share*100, 0) + `%：context 太長 ` + money(w.ctx) +
		` · 做太久被中止 ` + money(w.slow) + ` · Cache 過期重寫 ` + money(w.cache) + `">` + money(v) + `</td>`
}

func swatch(kind string, dele bool) string {
	if hsl, ok := rangeHSL[kind]; ok {
		return fmt.Sprintf("hsl(%d %d%% %d%%)", hsl[0], hsl[1], hsl[2])
	}
	sum := fmt.Sprintf("%x", md5.Sum([]byte(kind)))
	h, _ := strconv.ParseInt(sum[:4], 16, 64)
	light := "52%"
	if dele {
		light = "58%"
	}
	return fmt.Sprintf("hsl(%d 45%% %s)", h%360, light)
}

func monthAccordion(blocks [][3]string) string {
	var b strings.Builder
	b.WriteString(`<div class="acc">`)
	for i, x := range blocks {
		on := ""
		if i == 0 {
			on = " on"
		}
		b.WriteString(`<div class="mon` + on + `" data-m="` + Esc(x[0]) + `"><button class="mh">` + x[1] + `</button><div class="mb">` + x[2] + `</div></div>`)
	}
	return b.String() + `</div>`
}

func toggles(pairs ...[2]string) string {
	var b strings.Builder
	b.WriteString(`<div class="tgs">`)
	for _, p := range pairs {
		b.WriteString(`<details class="ex note"><summary>` + Esc(p[0]) + `</summary>` + p[1] + `</details>`)
	}
	return b.String() + `</div>`
}

func noteToggle(text, title string) string {
	return `<details class="ex note solo"><summary>` + Esc(title) + `</summary>` + text + `</details>`
}

func card(title, body, note string) string {
	tail := ""
	if note != "" {
		tail = noteToggle(note, "說明")
	}
	return `<section class="card"><h2>` + Esc(title) + `</h2>` + body + tail + `</section>`
}

func empty(msg string) string { return `<div class="empty">` + Esc(msg) + `</div>` }

// stat is one (label, value, sub, href, tip) tile.
type stat struct{ k, v, sub, href, tip string }

func stats(rows []stat) string {
	var b strings.Builder
	for _, r := range rows {
		tag, attr := "div", ""
		if r.href != "" {
			tag, attr = "a", ` href="`+Esc(r.href)+`"`
		}
		b.WriteString(`<` + tag + ` class="stat"` + attr)
		if r.tip != "" || r.sub != "" {
			t := r.tip
			if t == "" {
				t = r.sub
			}
			b.WriteString(` title="` + Esc(t) + `"`)
		}
		b.WriteString(`><b>` + Esc(r.v) + `</b><span>` + Esc(r.k) + `</span>`)
		if r.sub != "" {
			b.WriteString(`<span class="tip">` + Esc(r.sub) + `</span>`)
		}
		b.WriteString(`</` + tag + `>`)
	}
	return b.String()
}

// hotTile gives the tiles at these positions (0-based) of stats() the hot tone.
func hotTile(tiles string, at ...int) string {
	parts := strings.SplitAfter(tiles, `class="stat"`)
	for _, n := range at {
		if n+1 < len(parts) {
			parts[n] = strings.TrimSuffix(parts[n], `class="stat"`) + `class="stat hot"`
		}
	}
	return strings.Join(parts, "")
}

func turnCost(t *Turn) float64 { return t.Usage.Cost + t.DCost }

// endAfter extends a turn's end time to a subagent that started at start and ran secs.
func endAfter(end, start, secs float64) float64 {
	if start > 0 {
		return max(end, start+secs)
	}
	return end
}

// turnSpan is the wall clock from the prompt to the last of the main turn and its subagents.
func turnSpan(t *Turn) float64 { return max(t.E2E, t.DEnd-t.At) }

type tableGroup struct {
	label string
	span  int
}

func table(head []string, rows []string, cls, attrs string, groups []tableGroup) string {
	banner := ""
	type hcol struct {
		h string
		i int
	}
	var cols []hcol
	if len(groups) > 0 {
		var cells strings.Builder
		names := map[string]bool{}
		for _, g := range groups {
			if g.span != 0 {
				cells.WriteString(`<th class="grp" colspan="` + strconv.Itoa(g.span) + `">` + Esc(g.label) + `</th>`)
			} else {
				cells.WriteString(`<th rowspan="2" class="grp0">` + Esc(g.label) + `</th>`)
				names[g.label] = true
			}
		}
		banner = "<tr>" + cells.String() + "</tr>"
		for i, h := range head {
			if !names[h] {
				cols = append(cols, hcol{h, i})
			}
		}
	} else {
		for i, h := range head {
			cols = append(cols, hcol{h, i})
		}
	}
	var b strings.Builder
	b.WriteString(`<div class="tw"><table class="` + cls + `"` + attrs + `><thead>` + banner + `<tr>`)
	for _, c := range cols {
		b.WriteString(`<th data-col="` + strconv.Itoa(c.i) + `">` + Esc(c.h) + `</th>`)
	}
	b.WriteString("</tr></thead><tbody>" + strings.Join(rows, "") + "</tbody></table></div>")
	return b.String()
}

func twoPropZ(aHit, aN, bHit, bN int) *float64 {
	if aN == 0 || bN == 0 {
		return nil
	}
	p1, p2 := float64(aHit)/float64(aN), float64(bHit)/float64(bN)
	pool := float64(aHit+bHit) / float64(aN+bN)
	se := libm.Pow(pool*(1-pool)*(1/float64(aN)+1/float64(bN)), 0.5)
	if se == 0 {
		return nil
	}
	z := (p1 - p2) / se
	return &z
}

// ── date range ───────────────────────────────────────────────────────────

func today() time.Time {
	n := Now()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.Local)
}

func dstr(t time.Time) string { return t.Format("2006-01-02") }

func parseDay(s string) (time.Time, bool) {
	t, err := time.ParseInLocation("2006-1-2", s, time.Local)
	return t, err == nil
}

func daysBetween(a, b time.Time) int {
	return int(math.Round(b.Sub(a).Hours() / 24))
}

func rangeFile() string { return claudeFile("dashboard_range.json") }

func rememberRange(dFrom, dTo string) {
	t := today()
	endsToday := dTo == "" || dTo == dstr(t)
	var span any
	if dFrom != "" && endsToday {
		if f, ok := parseDay(dFrom); ok {
			span = int64(daysBetween(f, t))
		}
	}
	blob := ojson.NewObject().Set("from", dFrom).Set("to", dTo).Set("span", span)
	os.WriteFile(rangeFile(), []byte(py.DumpsASCII(blob)), 0o644)
}

func rememberedRange() (string, string, bool) {
	raw, err := os.ReadFile(rangeFile())
	if err != nil {
		return "", "", false
	}
	saved, err := ojson.Parse(raw)
	if err != nil {
		return "", "", false
	}
	if sp := ojson.Field(saved, "span"); sp != nil {
		t := today()
		return dstr(t.AddDate(0, 0, -int(num(sp)))), dstr(t), true
	}
	from := py.Str(orNil(ojson.GetOr(saved, "from"), ""))
	to := py.Str(orNil(ojson.GetOr(saved, "to"), ""))
	if from != "" || to != "" {
		return from, to, true
	}
	return "", "", false
}

func rangeBar(dFrom, dTo string) string {
	t := today()
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.Local)
	prevEnd := first.AddDate(0, 0, -1)
	quick := []struct {
		label string
		a, b  time.Time
	}{{"最近 7 天", t.AddDate(0, 0, -6), t}, {"最近 30 天", t.AddDate(0, 0, -29), t}, {"最近 90 天", t.AddDate(0, 0, -89), t},
		{"本月", first, t}, {"上個月", time.Date(prevEnd.Year(), prevEnd.Month(), 1, 0, 0, 0, 0, time.Local), prevEnd}}
	var links strings.Builder
	for _, q := range quick {
		on := ""
		if dstr(q.a) == dFrom && dstr(q.b) == dTo {
			on = " on"
		}
		links.WriteString(`<a class="rq` + on + `" href="/?from=` + dstr(q.a) + `&to=` + dstr(q.b) + `#ctx">` + Esc(q.label) + `</a>`)
	}
	return `<form class="rng" method="get" action="/#ctx" onchange="this.requestSubmit()">` +
		`<label>從<input type="date" name="from" value="` + Esc(dFrom) + `">` +
		`</label><label>到<input type="date" name="to" ` +
		`value="` + Esc(dTo) + `"></label>` +
		`<span class="rqs">` + links.String() + `</span></form>`
}

var _ = refs.Cap

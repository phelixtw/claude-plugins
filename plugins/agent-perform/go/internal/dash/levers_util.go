package dash

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentperform/internal/ojson"
	"agentperform/internal/prices"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
	"agentperform/internal/redact"
)

// ── before / after (dash_ui.py) ──────────────────────────────────────────

const zTip = "括號裡是兩段差距是否超出隨機波動，依兩個比例的 z 值判斷（後減前，正值＝後面變多），|z| 達 1.96 以上算超出"

func dayOfTurn(t *Turn) time.Time { return dateOf(t.At) }

func baDays(pre, post []*Turn, since string) (int, int) {
	start, _ := parseDay(since)
	first := start
	for i, t := range pre {
		if d := dayOfTurn(t); i == 0 || d.Before(first) {
			first = d
		}
	}
	after := 0
	if len(post) > 0 {
		after = daysBetween(start, today()) + 1
	}
	return daysBetween(first, start), after
}

func daysShort(pre, post []*Turn, since string) string {
	d1, d2 := baDays(pre, post, since)
	if d1 >= minDays && d2 >= minDays {
		return ""
	}
	return "前 " + strconv.Itoa(d1) + " 天 / 後 " + strconv.Itoa(d2) + " 天，各需 " + strconv.Itoa(minDays) + " 天"
}

func baWindows(turns []*Turn, since string) ([]*Turn, []*Turn, int) {
	t0 := today()
	start, _ := parseDay(since)
	span := min(30, daysBetween(start, t0)+1)
	lo := start.AddDate(0, 0, -span)
	var pre, post []*Turn
	for _, t := range turns {
		d := dayOfTurn(t)
		if !d.Before(lo) && d.Before(start) {
			pre = append(pre, t)
		}
		if !d.Before(start) && !d.After(t0) {
			post = append(post, t)
		}
	}
	return pre, post, span
}

func beforeAfter(turns []*Turn, since string, metric func([]*Turn) float64) (*float64, *float64, *float64, int, int) {
	pre, post, _ := baWindows(turns, since)
	if len(pre) < minTurns || len(post) < minTurns || daysShort(pre, post, since) != "" {
		return nil, nil, nil, len(pre), len(post)
	}
	a, b := metric(pre), metric(post)
	var pct *float64
	if a != 0 {
		v := (b - a) / a * 100
		pct = &v
	}
	return &a, &b, pct, len(pre), len(post)
}

func defaultSince() string {
	t := dstr(today())
	best := ""
	for _, m := range marks {
		if m[0] <= t && m[0] > best {
			best = m[0]
		}
	}
	if best == "" {
		return t
	}
	return best
}

type clause struct {
	label  string
	metric func([]*Turn) float64
	fmt    func(float64) string
	counts func([]*Turn) (int, int)
}

func shareClause(label string, hit func(*Turn) bool, of func(*Turn) bool) clause {
	if of == nil {
		of = func(*Turn) bool { return true }
	}
	counts := func(ts []*Turn) (int, int) {
		k, n := 0, 0
		for _, t := range ts {
			if of(t) {
				n++
				if hit(t) {
					k++
				}
			}
		}
		return k, n
	}
	metric := func(ts []*Turn) float64 {
		k, n := counts(ts)
		if n == 0 {
			return 0
		}
		return float64(k) / float64(n)
	}
	return clause{label, metric, func(v float64) string { return py.Pct(v, 1) }, counts}
}

func moneyClause(label string, perTurn func([]*Turn) float64) clause {
	return clause{label, perTurn, money, nil}
}

func meanCost(pick func(*Turn) bool) func([]*Turn) float64 {
	return func(ts []*Turn) float64 {
		s, n := 0.0, 0
		for _, t := range ts {
			if pick(t) {
				s += turnCost(t)
				n++
			}
		}
		if n == 0 {
			return 0
		}
		return s / float64(n)
	}
}

func zSpan(z float64, order string) string {
	verdict := "未超出"
	if z >= 1.96 || z <= -1.96 {
		verdict = "超出"
	}
	return `<span title="z=` + py.SignedF(z, 2) + `（` + order + `，正值＝變多）">` + verdict + `隨機波動</span>`
}

func baRow(turns []*Turn, since string, clauses []clause) string {
	pre, post, span := baWindows(turns, since)
	head := "改動前後（" + since + " 起，前後各 " + strconv.Itoa(span) + " 天）"
	n1, n2 := len(pre), len(post)
	short := daysShort(pre, post, since)
	if n1 < minTurns || n2 < minTurns || short != "" {
		why := short
		if why == "" {
			why = "前 " + strconv.Itoa(n1) + " / 後 " + strconv.Itoa(n2) + " 回合"
		}
		return `<p class="ba">` + head + `：資料不足（` + why + `）</p>`
	}
	var parts []string
	for _, c := range clauses {
		a, b := c.metric(pre), c.metric(post)
		if a == 0 && b == 0 {
			parts = append(parts, c.label+" 前後都沒有")
			continue
		}
		txt := c.label + " 前 " + c.fmt(a) + " → 後 " + c.fmt(b)
		if c.counts != nil {
			k1, m1 := c.counts(post)
			k2, m2 := c.counts(pre)
			if z := twoPropZ(k1, m1, k2, m2); z != nil {
				txt += "（差距" + zSpan(*z, "後減前") + "）"
			}
		} else if a != 0 {
			txt += "（" + py.SignedF((b-a)/a*100, 0) + "%）"
		}
		parts = append(parts, txt)
	}
	return `<p class="ba" title="前後各取同樣天數；` + zTip + `">` +
		head + `：` + strings.Join(parts, " · ") + ` · 回合數 ` + py.Comma(int64(n1)) + ` / ` + py.Comma(int64(n2)) + `</p>`
}

func costPerTurn(turns []*Turn) float64 {
	s := 0.0
	for _, t := range turns {
		s += turnCost(t)
	}
	return s / float64(len(turns))
}

// ── candidates.py aggregates ─────────────────────────────────────────────

type candRow struct {
	shape    string
	sessions int
	calls    int
	tok      int64
	cost     float64
	ids      []string
	samples  []scriptRef
	line     string
}

func samples(g []scriptRef, k int) []scriptRef {
	seen := map[string]bool{}
	var first, rest []scriptRef
	for _, x := range g {
		if seen[x.s.ID] {
			rest = append(rest, x)
		} else {
			first = append(first, x)
		}
		seen[x.s.ID] = true
	}
	all := append(first, rest...)
	if len(all) > k {
		all = all[:k]
	}
	return all
}

// scriptCandidates is shapes ranked by sessions x tokens, and the one-off share.
func scriptCandidates(order []string, groups map[string][]scriptRef) ([]candRow, float64) {
	by := map[string][]scriptRef{}
	var shapes []string
	for _, fp := range order {
		for _, x := range groups[fp] {
			if shape := py.Str(orNil(ojson.GetOr(x.rec, "shape"), "")); shape != "" && ojson.Truthy(ojson.Field(x.rec, "shape")) {
				if _, ok := by[shape]; !ok {
					shapes = append(shapes, shape)
				}
				by[shape] = append(by[shape], x)
			}
		}
	}
	total := 0
	for _, s := range shapes {
		total += len(by[s])
	}
	var rows []candRow
	for _, shape := range shapes {
		g := by[shape]
		var ids []string
		seen := map[string]bool{}
		var tok int64
		cost := 0.0
		line := ""
		for _, x := range g {
			if !seen[x.s.ID] {
				seen[x.s.ID] = true
				ids = append(ids, x.s.ID)
			}
			tok += recTok(x.rec)
			model := ""
			if x.turn != 0 && x.turn <= len(x.s.Turns) {
				model = x.s.Turns[x.turn-1].Model
			}
			r := prices.Of(model)
			cost += (float64(float64(num(ojson.Field(x.rec, "tok")))*r[1]) + float64(float64(num(ojson.Field(x.rec, "out")))*r[0])) / 1e6
			if line == "" {
				if l := ojson.GetOr(x.rec, "line"); l != nil {
					line = py.Str(l)
				}
			}
		}
		rows = append(rows, candRow{shape, len(ids), len(g), tok, cost, ids, samples(g, 3), line})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		return int64(rows[i].sessions)*rows[i].tok > int64(rows[j].sessions)*rows[j].tok
	})
	once := 0
	for _, r := range rows {
		if r.sessions == 1 {
			once += r.calls
		}
	}
	share := 0.0
	if total > 0 {
		share = float64(once) / float64(total)
	}
	return rows, share
}

var normRules = []struct {
	re *pyre.Re
	to string
}{
	{pyre.Compile(`^(?:cd \S+\s*(?:&&|;)\s*)+`), ""},
	{pyre.Compile(`"[^"]*"|'[^']*'`), "S"},
	{pyre.Compile(`(?<![\w.:])(?:/|[\w.\-~]+/)[\w.\-~/]+`), "P"},
	{pyre.Compile(`\b[0-9a-fA-F]{8,}\b`), "I"},
	{pyre.Compile(`\b\d+\b`), "N"},
}

func normalise(detail string) string {
	if !strings.Contains(detail, " ") { // a bare file path or URL is the example itself
		return detail
	}
	for _, r := range normRules {
		detail = r.re.Sub(detail, r.to)
	}
	return detail
}

type repRow struct {
	label           string
	occOrder        []string
	occ             map[string][]scriptRef
	calls, sessions int
	turns           int
	cost            float64
	tok             int64
	ex              [][2]any
}

// repeatTypes is rows per repeat type: occurrences, sessions, turns, the turns' cost and tokens.
func repeatTypes(order []repKey, groups map[repKey][]scriptRef) []repRow {
	type acc struct {
		calls     int
		sess      map[string]bool
		turns     map[string]*Turn
		turnOrder []string
		ex        *py.Counter
		occ       map[string][]scriptRef
		occOrder  []string
	}
	by := map[string]*acc{}
	var labels []string
	for _, k := range order {
		g := groups[k]
		label, _ := repeatType(k.code, k.detail)
		a := by[label]
		if a == nil {
			a = &acc{sess: map[string]bool{}, turns: map[string]*Turn{}, ex: py.NewCounter(), occ: map[string][]scriptRef{}}
			by[label] = a
			labels = append(labels, label)
		}
		if _, ok := a.occ[k.detail]; !ok {
			a.occOrder = append(a.occOrder, k.detail)
		}
		a.occ[k.detail] = append(a.occ[k.detail], g...)
		a.calls += len(g)
		for _, x := range g {
			a.sess[x.s.ID] = true
			key := x.s.ID + "\x00" + strconv.Itoa(x.turn)
			if _, ok := a.turns[key]; !ok {
				a.turnOrder = append(a.turnOrder, key)
			}
			a.turns[key] = x.s.Turns[x.turn-1]
		}
		ex := "（內容相似）"
		if k.detail != "" {
			ex = normalise(k.detail)
		}
		a.ex.Add(ex, len(g))
	}
	var out []repRow
	for _, label := range labels {
		a := by[label]
		r := repRow{label: label, occOrder: a.occOrder, occ: a.occ, calls: a.calls, sessions: len(a.sess), turns: len(a.turns)}
		for _, k := range a.turnOrder {
			u := a.turns[k].Usage
			r.cost += u.Cost
			r.tok += u.In + u.Out + u.Cr + u.Cw
		}
		for _, e := range a.ex.MostCommon(3) {
			r.ex = append(r.ex, [2]any{e, a.ex.N[e]})
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].cost > out[j].cost })
	return out
}

// globRec is glob(root/**/name, recursive=True) in Python's order.
func globRec(root, name string) []string {
	var out []string
	var walk func(dir string)
	walk = func(dir string) {
		if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
			out = append(out, filepath.Join(dir, name))
		}
		for _, n := range listdir(dir) {
			p := filepath.Join(dir, n)
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				walk(p)
			}
		}
	}
	if st, err := os.Stat(root); err == nil && st.IsDir() {
		walk(root)
	}
	return out
}

var (
	frontMu sync.Mutex
	frontRe = map[string]*pyre.Re{}
)

func front(text, key string) string {
	frontMu.Lock()
	re, ok := frontRe[key]
	if !ok {
		re = pyre.Compile(`^`+key+`:\s*(.+?)\s*$`, pyre.M)
		frontRe[key] = re
	}
	frontMu.Unlock()
	m := re.Search(text)
	if m == nil {
		return ""
	}
	return strings.Trim(m.Group(1), `"'`)
}

type skillInfo struct {
	name, desc string
	born       float64
}

// installedSkills is every SKILL.md under ~/.claude/skills and the plugin cache.
func installedSkills() []skillInfo {
	h := home()
	var paths []string
	paths = append(paths, globRec(filepath.Join(h, ".claude", "skills"), "SKILL.md")...)
	cache := filepath.Join(h, ".claude", "plugins", "cache")
	for _, a := range listdir(cache) {
		for _, b := range listdir(filepath.Join(cache, a)) {
			for _, c := range listdir(filepath.Join(cache, a, b)) {
				dir := filepath.Join(cache, a, b, c, "skills")
				for _, d := range listdir(dir) {
					p := filepath.Join(dir, d, "SKILL.md")
					if _, err := os.Lstat(p); err == nil {
						paths = append(paths, p)
					}
				}
			}
		}
	}
	found := map[string]*skillInfo{}
	var order []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		head := py.Head(strings.ReplaceAll(strings.ReplaceAll(strings.ToValidUTF8(string(raw), "�"), "\r\n", "\n"), "\r", "\n"), 4000)
		name := front(head, "name")
		if name == "" {
			name = filepath.Base(filepath.Dir(p))
		}
		born := birthTime(st)
		old := found[name]
		if old == nil {
			found[name] = &skillInfo{name, front(head, "description"), born}
			order = append(order, name)
			continue
		}
		desc := old.desc
		if desc == "" {
			desc = front(head, "description")
		}
		found[name] = &skillInfo{name, desc, min(born, old.born)}
	}
	var out []skillInfo
	for _, n := range order {
		out = append(out, *found[n])
	}
	return out
}

func loadedSkills(s *Session) []string {
	names := map[string]bool{}
	for _, l := range s.Loads {
		names[l.Name] = true
	}
	for _, t := range s.Turns {
		for _, n := range t.Skills.items() {
			names[n] = true
		}
	}
	short := map[string]bool{}
	for n := range names {
		parts := strings.Split(n, ":")
		short[parts[len(parts)-1]] = true
	}
	var out []string
	for n := range short {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// ── bundle.py ────────────────────────────────────────────────────────────

const (
	bundleMinSessions = 2
	bundleMaxRows     = 200
	sampleGroups      = 50
)

var emailRe = pyre.Compile(`[\w.+-]+@[\w-]+(?:\.[\w-]+)+`)

func bAt(s *Session, n int) string {
	t := s.Start
	if n != 0 && 1 <= n && n <= len(s.Turns) {
		t = s.Turns[n-1].At
	}
	return fromTimestamp(t).Format("2006-01-02 15:04")
}

func bCell(x string) string {
	return strings.ReplaceAll(strings.ReplaceAll(x, "|", `\|`), "\n", " ")
}

func bClean(text string) string { return emailRe.Sub(redact.Redact(text), "***@***") }

func bFence(code string) string {
	return "```\n" + strings.ReplaceAll(bClean(code), "```", "'''") + "\n```"
}

func skillsBlock(skills []skillInfo) string {
	sorted := append([]skillInfo{}, skills...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.name != b.name {
			return a.name < b.name
		}
		if a.desc != b.desc {
			return a.desc < b.desc
		}
		return a.born < b.born
	})
	lines := []string{"## 已安裝的 skill（名稱 | SKILL.md 建立時間 | 描述）"}
	for _, s := range sorted {
		lines = append(lines, "- "+s.name+" | "+fromTimestamp(s.born).Format("2006-01-02")+" | "+py.Head(bClean(bCell(s.desc)), 200))
	}
	return strings.Join(lines, "\n")
}

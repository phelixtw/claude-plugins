package dash

// One page template for every view: headline sentence, tiles, findings, one chart, one table, folds.
// Every number comes from the same computations the views already use.

import (
	"math"
	"strconv"
	"strings"

	"agentperform/internal/py"
	"agentperform/internal/pyre"
)

type nfind struct{ tone, fig, txt, href, goTxt string }

func finds(fs []nfind) string { return findsUnder("你可能沒注意到", fs) }

func findsUnder(title string, fs []nfind) string {
	if len(fs) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<h2 class="nh">` + title + `</h2><div class="notice">`)
	for _, f := range fs {
		b.WriteString(`<a class="find ` + f.tone + `" href="` + f.href + `"><span class="fig">` + f.fig + `</span><span class="txt">` + f.txt + `</span><span class="go">` + f.goTxt + `</span></a>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// crumbs is the path above a drill-down page: 總覽 ／ day ／ conversation ...; each part is ready HTML.
func crumbs(parts ...string) string {
	return `<div class="crumb">` + strings.Join(parts, " ／ ") + `</div>`
}

func chartCard(title, legend, svg string) string {
	return `<h2 class="nh" id="chart">` + title + `</h2><div class="card nchart">` + legend + svg + `</div>`
}

func listCard(title, inner string) string {
	return `<h2 class="nh" id="list">` + title + `</h2><div class="card nlist">` + inner + `</div>`
}

func foldID(id, title, inner string) string {
	return `<details class="nfold" id="` + id + `"><summary>` + title + `</summary><div class="nfold-b">` + inner + `</div></details>`
}

func fold(title, inner string) string {
	return `<details class="nfold"><summary>` + title + `</summary><div class="nfold-b">` + inner + `</div></details>`
}

func legend(items ...[2]string) string {
	var b strings.Builder
	b.WriteString(`<div class="nleg">`)
	for _, it := range items {
		b.WriteString(`<span><i style="background:` + it[0] + `"></i>` + it[1] + `</span>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

// bar is one column of barSVG: stacked values with their colours, a label, and an optional link.
type nbar struct {
	label string
	vals  []float64
	tip   string
	href  string
	mark  bool // a detected config change that day
	vline bool // dashed vertical line at this column (the change date)
}

// barSVG draws stacked columns; fmtv formats the axis; colours apply per stack index.
func barSVG(bars []nbar, colours []string, fmtv func(float64) string, top float64, every int) string {
	return barSVGPop(bars, nil, colours, fmtv, top, every)
}

// barSVGPop is barSVG where pops[i] (ready HTML, may be nil) turns column i into a clickable
// hit rect: the page JS shows pops[i] as a card and a click follows the bar's href.
func barSVGPop(bars []nbar, pops []string, colours []string, fmtv func(float64) string, top float64, every int) string {
	const W, H, pad = 1000.0, 180.0, 52.0
	if len(bars) == 0 {
		return ""
	}
	mx := 0.0
	for _, b := range bars {
		s := 0.0
		for _, v := range b.vals {
			s += v
		}
		mx = max(mx, s)
	}
	ticks := 4
	if top <= 0 {
		top, ticks = niceAxis(mx)
	}
	bw := (W - pad) / float64(len(bars))
	var g strings.Builder
	for t := 0; t <= ticks; t++ {
		y := H - (H-14)*float64(t)/float64(ticks)
		g.WriteString(`<line x1="` + f1(pad) + `" x2="` + f1(W) + `" y1="` + f1(y) + `" y2="` + f1(y) + `" stroke="var(--grid)"/>`)
		g.WriteString(`<text x="` + f1(pad-6) + `" y="` + f1(y+4) + `" font-size="11" text-anchor="end" fill="var(--dim)">` + fmtv(top*float64(t)/float64(ticks)) + `</text>`)
	}
	if every < 1 {
		every = 1
	}
	for i, b := range bars {
		x := pad + float64(i)*bw
		y := H
		open, closeA := "", ""
		pop := ""
		if i < len(pops) {
			pop = pops[i]
		}
		if b.href != "" && pop == "" {
			open, closeA = `<a href="`+b.href+`">`, `</a>`
		}
		g.WriteString(open)
		for k, v := range b.vals {
			h := (H - 14) * v / top
			if h <= 0 {
				continue
			}
			y -= h
			g.WriteString(`<rect x="` + f1(x+2) + `" y="` + f1(y) + `" width="` + f1(max(bw-4, 1)) + `" height="` + f1(h) + `" rx="2" fill="` + colours[min(k, len(colours)-1)] + `"><title>` + Esc(b.tip) + `</title></rect>`)
		}
		g.WriteString(closeA)
		if pop != "" {
			on := ""
			if b.vline {
				on = " on"
			}
			g.WriteString(`<rect class="hit` + on + `" x="` + f1(x) + `" y="0" width="` + f1(bw) + `" height="` + f1(H) + `" data-go="` + Esc(b.href) + `" data-pop="` + Esc(pop) + `"/>`)
		}
		if b.mark {
			g.WriteString(`<rect x="` + f1(x+4) + `" y="182" width="` + f1(max(bw-8, 1)) + `" height="3" rx="1.5" fill="var(--mid)"/>`)
		}
		if b.vline {
			g.WriteString(`<line x1="` + f1(x) + `" x2="` + f1(x) + `" y1="6" y2="` + f1(H) + `" stroke="var(--fg)" stroke-dasharray="4 3"/>` +
				`<text x="` + f1(x+4) + `" y="12" font-size="11" fill="var(--dim)">改動日</text>`)
		}
		if i%every == 0 || i == len(bars)-1 {
			lx, anchor := x+bw/2, "middle"
			if i == len(bars)-1 && i > 0 { // the last label would run past the right edge
				lx, anchor = W, "end"
			}
			g.WriteString(`<text x="` + f1(lx) + `" y="` + f1(H+16) + `" font-size="11" text-anchor="` + anchor + `" fill="var(--dim)">` + Esc(b.label) + `</text>`)
		}
	}
	return `<svg class="nsvg" viewBox="0 0 1000 202" role="img">` + g.String() + `</svg>`
}

// spoken is a duration for a sentence: 45 秒, 12 分鐘, 2.4 小時, 10 小時.
func spoken(sec float64) string {
	switch {
	case sec < 60:
		return py.F(sec, 0) + " 秒"
	case sec < 3600:
		return py.F(sec/60, 0) + " 分鐘"
	case sec < 36000:
		return strings.TrimSuffix(py.F(sec/3600, 1), ".0") + " 小時"
	}
	return py.F(sec/3600, 0) + " 小時"
}

func f1(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }

// niceAxis picks a 1/2/5 × 10ⁿ step so every gridline is a round number; at most 5 intervals.
func niceAxis(mx float64) (float64, int) {
	if mx <= 0 {
		return 1, 4
	}
	for p := 1e-6; ; p *= 10 {
		for _, m := range []float64{1, 2, 5} {
			if st := m * p; mx/st <= 5 {
				n := int(math.Ceil(mx/st - 1e-9))
				return st * float64(max(n, 1)), max(n, 1)
			}
		}
	}
}

// niceTop rounds up to 1, 2, 2.5 or 5 × 10ⁿ so the axis ticks are round numbers.
func niceTop(v float64) float64 {
	if v <= 0 {
		return 1
	}
	p := 1.0
	for p*10 <= v {
		p *= 10
	}
	for p > v {
		p /= 10
	}
	for _, m := range []float64{1, 2, 2.5, 4, 5, 10} {
		if m*p >= v {
			return m * p
		}
	}
	return 10 * p
}

// turnRanges writes turn numbers as "#26、#31～#37".
func turnRanges(ns []int) string {
	var parts []string
	for i := 0; i < len(ns); {
		j := i
		for j+1 < len(ns) && ns[j+1] == ns[j]+1 {
			j++
		}
		if j > i+1 {
			parts = append(parts, `#`+strconv.Itoa(ns[i])+`～#`+strconv.Itoa(ns[j]))
		} else {
			for k := i; k <= j; k++ {
				parts = append(parts, `#`+strconv.Itoa(ns[k]))
			}
		}
		i = j + 1
	}
	return strings.Join(parts, "、")
}

func pct0(a, b float64) string {
	if b == 0 {
		return "—"
	}
	return strconv.Itoa(int(a/b*100+0.5)) + "%"
}

var (
	reTag = pyre.Compile(`<[^>]+>`)
	reURL = pyre.Compile(`https?://\S+`)
	reWS  = pyre.Compile(`\s+`)
)

// cleanOpening is a prompt fit for a title: no tags, no URLs, no command output.
func cleanOpening(s string) string {
	if isCmdOutput(s) {
		return ""
	}
	s = reTag.Sub(s, " ")
	s = reURL.Sub(s, " ")
	return py.Strip(reWS.Sub(s, " "))
}

// isCmdOutput: the "prompt" is the output of a ! command or slash command, not something the user typed.
func isCmdOutput(s string) bool {
	t := py.Strip(s)
	for _, p := range []string{"<bash-stdout>", "<bash-stderr>", "<local-command-stdout>", "<local-command-stderr>"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

func promptCell(s string) string {
	if isCmdOutput(s) {
		return `<span class="dim">（指令輸出，不是你打的字）</span>`
	}
	return Esc(s)
}

func orStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// sessionTurnNo is the session page's number for the pipeline's n-th prompt (0 if that prompt made no API call).
func sessionTurnNo(path string, meta *Meta, n int) int {
	if n < 1 || n > len(meta.PromptAt) || meta.PromptAt[n-1] == nil {
		return 0
	}
	got := oneCached(path, 0)
	if got == nil {
		return 0
	}
	for j, t := range got.Turns {
		if t.At == *meta.PromptAt[n-1] {
			return j + 1
		}
	}
	return 0
}

// memoTurnNo is sessionTurnNo for one page, remembered per turn.
func memoTurnNo(path string, meta *Meta) func(int) int {
	seen := map[int]int{}
	return func(n int) int {
		if v, ok := seen[n]; ok {
			return v
		}
		seen[n] = sessionTurnNo(path, meta, n)
		return seen[n]
	}
}

// tilde shortens the home directory to ~ wherever it appears in a label.
func tilde(s string) string {
	if h := home(); h != "" {
		return strings.ReplaceAll(s, h+"/", "~/")
	}
	return s
}

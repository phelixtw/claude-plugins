package dash

// Focus lens: a topic page is a lens on one problem. Tables on the lens
// highlight the rows that carry the problem and the column that measures it,
// sort those rows first and dim the rest. Drill-down links keep the lens.

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
)

type lensDef struct {
	name   string
	topic  string   // topic page that owns the lens
	cols   []string // table headers that measure the problem
	anchor string   // section of the turn page that explains it
	tools  bool     // the session table needs its 工具 column
}

var lenses = map[string]lensDef{
	"context":    {"context 太長", "context", []string{"最大 context", "可以省的", "此類多花"}, "ctxw", false},
	"cachebreak": {"Cache 過期重寫", "context", []string{"Tag", "此類多花"}, "ctxw", false},
	"fixed":      {"每回合固定載入", "context", []string{"最大 context"}, "ctxw", false},
	"killed":     {"做太久被中止", "tools", []string{"耗時", "可以省的", "此類多花"}, "ctxw", false},
	"repeat":     {"重複呼叫", "tools", []string{"Tag"}, "ctxw", true},
	"delegate":   {"委派", "delegate", []string{"花費", "此類多花"}, "subs", true},
}

// topicLens is the lens a topic page opens with.
var topicLens = map[string]string{"context": "context", "tools": "repeat", "delegate": "delegate"}

func activeLens(focus string) (lensDef, bool) {
	d, ok := lenses[focus]
	return d, ok
}

// lensTurns marks the turns of s that carry the problem; base is the 150-300k median cost.
func lensTurns(key string, s *Session, base float64) map[*Turn]bool {
	hits := map[*Turn]bool{}
	if _, ok := lenses[key]; !ok {
		return hits
	}
	rep := remapRepeats(s, ojson.Obj(ojson.Field(ojson.Field(health(), "sessions"), s.ID)))
	w := waste(s.Turns, base, rep)
	for i, t := range s.Turns {
		if lensHit(key, t, i, w) {
			hits[t] = true
		}
	}
	return hits
}

func lensHit(key string, t *Turn, i int, w wasteT) bool {
	switch key {
	case "context":
		return ctxBig(t)
	case "cachebreak":
		return t.Rewrite != 0
	case "killed":
		return killedSlow(t)
	case "delegate":
		return t.Dele > 0
	case "repeat":
		for _, f := range w.flags[i] {
			if f.name == "rep" {
				return true
			}
		}
	}
	return false
}

type lrow struct {
	html string
	hit  bool
}

// lensTable is table() with the lens applied: hit rows first and marked, others dimmed, measuring column marked.
func lensTable(head []string, rows []lrow, cls, attrs, focus string) string {
	d, on := activeLens(focus)
	var out []string
	if on {
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].hit && !rows[j].hit })
		for _, c := range d.cols {
			for i, h := range head {
				if h == c && !strings.Contains(attrs, "data-fc") {
					attrs += ` data-fc="` + strconv.Itoa(i+1) + `"`
				}
			}
		}
		attrs += ` data-fixed`
	}
	for _, r := range rows {
		h := r.html
		if on {
			mark := "off"
			if r.hit {
				mark = "hit"
			}
			h = strings.Replace(h, "<tr", `<tr data-lens="`+mark+`"`, 1)
		}
		out = append(out, h)
	}
	return table(head, out, cls, attrs, nil)
}

// lensBar is the line above a lensed table; clear is the same page without the lens ("" on a topic page).
func lensBar(n int, unit, clear, focus string) string {
	d, ok := activeLens(focus)
	if !ok {
		return ""
	}
	s := `<div class="lbar">聚焦：<b>` + Esc(d.name) + `</b> · 共 ` + py.Comma(int64(n)) + ` 個` + unit
	if clear != "" {
		s += ` · <a href="` + clear + `&focus=">清除</a>`
	}
	return s + `</div>`
}

// info is a hover tooltip for an explanation that used to be a fold.
func info(tip string) string {
	return `<span class="ti" tabindex="0" title="` + Esc(py.Strip(anyTag.Sub(tip, ""))) + `">ⓘ</span>`
}

const tagInfo = "Tag 欄是這個回合踩到的問題：做太久被中止、重複呼叫、碰到 Claude Code 的工具上限（滑鼠移上去看上限）、Cache 過期重寫佔花費 10% 以上。context 大小與 Cache hit 點進回合看。"

var reLink = regexp.MustCompile(`((?:href="|location=')(?:/\?(?:day|session)=[^"'#]*|/pipeline\?[^"'#]*))(#[^"']*)?(["'])`)

// withFocus makes the day, session and turn links of a page carry the lens;
// turn links also open at the section that explains the problem.
func withFocus(body, focus string) string {
	d, ok := activeLens(focus)
	if !ok {
		return body
	}
	return `<i hidden id="navtab" data-tab="/` + d.topic + `"></i>` + reLink.ReplaceAllStringFunc(body, func(m string) string {
		g := reLink.FindStringSubmatch(m)
		if strings.Contains(g[1], "focus=") {
			return m
		}
		link, frag := g[1]+"&focus="+focus, g[2]
		if frag == "" && strings.Contains(link, "&at=") {
			frag = "#" + d.anchor
		}
		return link + frag + g[3]
	})
}

// money0 drops the cents from $10 up; below that they still matter.
func money0(v float64) string {
	if v < 10 {
		return money(v)
	}
	return "$" + py.CommaF(v, 0)
}

// sessionModels adds up the per-turn model spend of these turns.
func sessionModels(turns []*Turn) *floatCounter {
	c := newFloatCounter()
	for _, t := range turns {
		if t.Models != nil {
			for _, k := range t.Models.keys {
				c.add(k, t.Models.n[k])
			}
		}
	}
	return c
}

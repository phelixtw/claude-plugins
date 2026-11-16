// Package refs is the one shared 「依據與門檻」 section (refs.py): every page
// passes its items, nothing else renders references.
package refs

import (
	"fmt"
	"strings"

	"agentperform/internal/common"
	"agentperform/internal/py"
)

const Title = "依據與門檻"

// Cap is the reason text for a display limit.
const Cap = "顯示上限，只決定列幾列，不影響任何計算。"

func link(url, text string) string {
	return fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener">%s</a>`, url, py.HTMLEscape(text))
}

// Src is SRC[key](): the full source link plus its note.
func Src(key string) string {
	s := srcs[key]
	return link(s.url, s.full) + s.note
}

func inlineLabel(key string) string {
	s := srcs[key]
	if s.short != "Claude Code 文件" || !strings.Contains(s.full, "〈") {
		return s.short
	}
	tail := strings.SplitN(s.full, "〈", 2)[1]
	tail = strings.TrimRight(tail, "〉")
	parts := strings.Split(tail, "：")
	return s.short + "〈" + parts[len(parts)-1] + "〉"
}

// Cite is the small inline source marker for the keys an item passes to Section.
func Cite(keys []string) string {
	if len(keys) == 0 || !common.Experiment("refs_footer") {
		return ""
	}
	var urls []string
	label := map[string]string{}
	for _, k := range keys {
		u := srcs[k].url
		if _, ok := label[u]; !ok {
			label[u] = inlineLabel(k)
			urls = append(urls, u)
		}
	}
	var parts []string
	for _, u := range urls {
		parts = append(parts, link(u, label[u]))
	}
	return `<sup class="cite">〔` + strings.Join(parts, "、") + `〕</sup>`
}

// Item is one row: (flag or "", element, source keys, reason or "").
type Item struct {
	Flag, Element string
	Keys          []string
	Reason        string
}

// Section renders the items; an item with neither sources nor reason panics.
func Section(items []Item) string {
	if !common.Experiment("refs_footer") {
		return ""
	}
	var rows []string
	for _, it := range items {
		if it.Flag != "" && !common.Experiment(it.Flag) {
			continue
		}
		var parts []string
		for _, k := range it.Keys {
			parts = append(parts, Src(k))
		}
		if it.Reason != "" {
			parts = append(parts, py.HTMLEscape(it.Reason))
		}
		if len(parts) == 0 {
			panic("refs item without source or reason: " + it.Element)
		}
		rows = append(rows, "<li><b>"+py.HTMLEscape(it.Element)+"</b> → "+strings.Join(parts, "；")+"</li>")
	}
	if len(rows) == 0 {
		return ""
	}
	return `<ul class="pp-ul pp-refs">` + strings.Join(rows, "") + `</ul>`
}

// Keys is every source key, in table order.
func Keys() []string { return order }

// Short is the short inline label of a source key.
func Short(key string) string { return srcs[key].short }

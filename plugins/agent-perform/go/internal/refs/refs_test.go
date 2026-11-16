package refs

import (
	"strings"
	"testing"
)

// The shared 依據與門檻 list: no heading of its own (the caller names it), per-item flags, every item has a source or a reason.
func TestSection(t *testing.T) {
	html := Section([]Item{{Element: "A", Keys: []string{"agentdiet"}}, {Element: "B", Reason: Cap}})
	if strings.Contains(html, Title) || strings.Count(html, "<li>") != 2 {
		t.Errorf("one section, flat list: %s", html)
	}
	if !strings.Contains(html, "arxiv.org/abs/2509.23586") || strings.Contains(html, "2406.18665") {
		t.Error("sources")
	}
	for _, old := range []string{"參考來源", "參考文獻：這一頁為什麼這樣設計", "數字怎麼來的", "這一頁用到的門檻數字"} {
		if strings.Contains(html, old) {
			t.Errorf("old heading %q", old)
		}
	}
}

func TestFlags(t *testing.T) {
	t.Setenv("AP_EXP_POLLING_BLOCK", "0")
	if got := Section([]Item{{Flag: "polling_block", Element: "x", Reason: "r"}}); got != "" {
		t.Errorf("flag off hides the item: %q", got)
	}
	if strings.Contains(Section([]Item{{Flag: "polling_block", Element: "x", Reason: "r"}, {Element: "y", Reason: "r"}}), "polling_block") {
		t.Error("flag name leaked")
	}
	t.Setenv("AP_EXP_REFS_FOOTER", "0")
	if Section([]Item{{Element: "x", Keys: []string{"ctx_eng"}}}) != "" || Cite([]string{"agentdiet"}) != "" {
		t.Error("whole section off")
	}
}

func TestCite(t *testing.T) {
	if !strings.Contains(Cite([]string{"agentdiet"}), "arxiv.org/abs/2509.23586") || Cite(nil) != "" {
		t.Error("cite")
	}
	out := Cite([]string{"cc_cache_miss", "cc_compact"})
	if strings.Count(out, "<a ") != 1 || !strings.Contains(out, "Claude Code 文件〈Why usage climbs in a long session〉") {
		t.Errorf("same url merges: %s", out)
	}
}

func TestItemNeedsSourceOrReason(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	Section([]Item{{Element: "x"}})
}

func TestSources(t *testing.T) {
	var all []string
	for _, k := range Keys() {
		if Src(k) == "" {
			t.Errorf("%s resolves to nothing", k)
		}
		all = append(all, Src(k))
	}
	for _, k := range []string{"thariq_rot", "dex_60", "cc_clear", "cc_cache_miss", "cc_compact", "cc_model",
		"cc_effort", "cc_mcp", "cc_claude_md", "cc_complex", "cc_specific", "cc_flags", "cc_baseline"} {
		if _, ok := srcs[k]; !ok {
			t.Errorf("missing key %s", k)
		}
	}
	if !strings.Contains(Src("thariq_rot"), "2044548257058328723") || !strings.Contains(Src("dex_60"), "HumanLayer") {
		t.Error("attribution")
	}
	if strings.Contains(srcs["dex_60"].full, "Anthropic") {
		t.Error("dex_60 is not Anthropic's")
	}
	text := strings.Join(all, " ")
	for _, bad := range []string{"2406.18665", "2502.08235", "859", "function calling"} {
		if strings.Contains(text, bad) {
			t.Errorf("stale claim %q", bad)
		}
	}
}

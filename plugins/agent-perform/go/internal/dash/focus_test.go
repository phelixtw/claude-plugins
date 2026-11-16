package dash

import (
	"strings"
	"testing"
)

func TestWithFocusCarriesTheLens(t *testing.T) {
	body := `<a href="/?day=2026-10-01">d</a><tr onclick="location='/?session=abc'">` +
		`<a href="/pipeline?session=abc&at=1.5">t</a><a href="/pipeline?session=abc&at=1.5#subs">s</a><a href="/how">h</a>`
	if withFocus(body, "") != body || strings.Contains(withFocus(body, ""), "navtab") {
		t.Error("no lens must leave links alone")
	}
	if withFocus(body, "bogus") != body {
		t.Error("unknown lens must leave links alone")
	}
	out := withFocus(body, "cachebreak")
	mustHave(t, out, `href="/?day=2026-10-01&focus=cachebreak"`)
	mustHave(t, out, `location='/?session=abc&focus=cachebreak'`)
	mustHave(t, out, `href="/pipeline?session=abc&at=1.5&focus=cachebreak#ctxw"`)
	mustHave(t, out, `href="/pipeline?session=abc&at=1.5&focus=cachebreak#subs"`) // an explicit anchor wins
	mustHave(t, out, `href="/how"`)
	mustHave(t, out, `data-tab="/context"`)
}

func TestLensClearLinkKeepsNoLens(t *testing.T) {
	out := withFocus(lensBar(3, "回合", "/?session=abc", "context"), "context")
	mustHave(t, out, `href="/?session=abc&focus="`)
	if strings.Contains(out, "focus=context") {
		t.Error("clear link must not carry the lens")
	}
}

func TestFocusComesFromQueryOrTopic(t *testing.T) {
	testHome(t)
	var got []string
	grab := func(k pageKey) string { got = append(got, k.focus); return "" }
	for _, q := range [][2]string{
		{"/", ""},
		{"/", "day=2026-10-01&focus=cachebreak"},
		{"/", "day=2026-10-01&focus=bogus"},
		{"/context", ""},
		{"/tools", "focus=killed"},
		{"/how", ""},
	} {
		get(q[0], q[1], false, grab)
	}
	if strings.Join(got, ",") != ",cachebreak,,context,killed," {
		t.Errorf("focus %q", got)
	}
}

func TestPageCacheKeepsLensesApart(t *testing.T) {
	a := pageKey{days: 14, sid: "x"}
	b := a
	b.focus = "context"
	if a == b {
		t.Error("pages with and without a lens must not share a cache slot")
	}
	if (pipeKey{sid: "x"}) == (pipeKey{sid: "x", focus: "context"}) {
		t.Error("turn pages with and without a lens must not share a cache slot")
	}
	if (pipeKey{sid: "x"}) == (pipeKey{sid: "x", tool: "t1"}) {
		t.Error("turn pages with and without a picked tool must not share a cache slot")
	}
}

func TestLensMarksRowsAndColumn(t *testing.T) {
	rows := []lrow{{`<tr><td>a</td><td>1</td></tr>`, false}, {`<tr><td>b</td><td>2</td></tr>`, true}}
	plain := lensTable([]string{"對話", "此類多花"}, append([]lrow(nil), rows...), "", "", "")
	mustNot(t, plain, "data-lens")
	if strings.Index(plain, "<td>a</td>") > strings.Index(plain, "<td>b</td>") {
		t.Error("no lens keeps the order")
	}
	on := lensTable([]string{"對話", "此類多花"}, append([]lrow(nil), rows...), "", "", "context")
	if strings.Index(on, "<td>b</td>") > strings.Index(on, "<td>a</td>") {
		t.Error("hit rows come first")
	}
	mustHave(t, on, `data-lens="hit"`)
	mustHave(t, on, `data-lens="off"`)
	mustHave(t, on, `data-fc="2"`)
	if lensBar(3, "回合", "", "") != "" {
		t.Error("no bar without a lens")
	}
	mustHave(t, lensBar(3, "回合", "/?day=d", "context"), "context 太長")
}

func TestSessionLens(t *testing.T) {
	testHome(t)
	s := delegatingSession(t)
	plain := SessionView([]*Session{s}, s.ID)
	mustNot(t, plain, `class="lbar"`)
	on := sessionLens([]*Session{s}, s.ID, "delegate")
	mustHave(t, on, `class="lbar"`)
	mustHave(t, on, "委派")
}

func TestMoney0(t *testing.T) {
	for v, want := range map[float64]string{0: "$0.00", 9.994: "$9.99", 10.4: "$10", 5869.13: "$5,869"} {
		if got := money0(v); got != want {
			t.Errorf("money0(%v) = %q, want %q", v, got, want)
		}
	}
}

func TestAgeSplit(t *testing.T) {
	if groupAge(map[string]bool{"now": true, "old": true}) != "mixed" {
		t.Error("both ages make a mixed group")
	}
	if groupAge(map[string]bool{"now": true}) != "now" || groupAge(map[string]bool{"old": true}) != "old" {
		t.Error("single ages stay")
	}
	if got := ageSplit(1, 10); got != "（本回合 1、先前 9）" {
		t.Errorf("split %q", got)
	}
}

func TestPickerKeepsTheOpenSession(t *testing.T) {
	list := [][2]string{{"aaaaaaaa", "x"}}
	if got := withSession(list, "aaaaaaaa-1234", "/nope"); len(got) != 1 {
		t.Errorf("listed session added again: %v", got)
	}
	if got := withSession(list, "bbbbbbbb-1234", "/nope"); len(got) != 2 || got[0][0] != "bbbbbbbb" {
		t.Errorf("older session missing from the picker: %v", got)
	}
}

func TestCallRowsUseSessionTurnNumbers(t *testing.T) {
	m := &mainCtx{no: func(n int) int { return n - 2 }} // pipeline 34 is the session's 32
	if got := m.label(&Use{Turn: 34, HasTurn: true}); got != "32" {
		t.Errorf("label %q", got)
	}
	m.no = func(int) int { return 0 } // unknown: fall back to the pipeline index
	if got := m.label(&Use{Turn: 34, HasTurn: true}); got != "34" {
		t.Errorf("fallback %q", got)
	}
}

func TestTilde(t *testing.T) {
	h := testHome(t)
	if got := tilde(h + "/.claude/CLAUDE.md"); got != "~/.claude/CLAUDE.md" {
		t.Errorf("tilde %q", got)
	}
}

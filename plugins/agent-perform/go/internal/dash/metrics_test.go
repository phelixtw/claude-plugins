package dash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
)

func toolRec(ts float64, mid, tool string, input *ojson.Object) *ojson.Object {
	r := assistantRec(ts, mid, 0, 0, "")
	ojson.Obj(ojson.Field(r, "message")).Set("content", []any{obj("type", "tool_use", "id", "tu-"+mid, "name", tool, "input", input)})
	return r
}

func notifRec(ts float64) *ojson.Object {
	return userRec(ts, "<task-notification><task-id>b1</task-id></task-notification>")
}

func parseRows(t *testing.T, rows ...any) *Session {
	t.Helper()
	h := testHome(t)
	s := one(writeRows(t, filepath.Join(h, "s.jsonl"), rows...), 0)
	if s == nil {
		t.Fatal("no session")
	}
	return s
}

// an assistant row of assistantRec(cw=cw, cr=0) uses 1 + 10 + cw tokens
func tokOf(cw int64) int64 { return 11 + cw }

func TestNotificationIsMergedIntoItsInstruction(t *testing.T) {
	s := parseRows(t, userRec(1000, "build it"), assistantRec(1001, "m1", 100, 0, ""),
		notifRec(1100), assistantRec(1101, "m2", 200, 0, ""),
		userRec(2000, "next"), assistantRec(2001, "m3", 300, 0, ""))
	if len(s.Turns) != 2 {
		t.Fatalf("%d turns, want 2 (notification merged)", len(s.Turns))
	}
	first := s.Turns[0]
	if first.Notes != 1 || first.Calls != 2 || usageTokens(first.Usage) != tokOf(100)+tokOf(200) {
		t.Errorf("notes %d calls %d tokens %d", first.Notes, first.Calls, usageTokens(first.Usage))
	}
	if first.Last == nil || *first.Last != 1101 {
		t.Errorf("turn should run to the notification's reply: %v", first.Last)
	}
}

func TestCompactSummaryIsMergedIntoItsInstruction(t *testing.T) {
	summary := userRec(1100, "This session is being continued from a previous conversation")
	summary.Set("isCompactSummary", true)
	s := parseRows(t, userRec(1000, "build it"), assistantRec(1001, "m1", 100, 0, ""), summary, assistantRec(1101, "m2", 200, 0, ""))
	if len(s.Turns) != 1 || s.Turns[0].Notes != 1 || s.Turns[0].Calls != 2 {
		t.Errorf("%d turns, notes %d", len(s.Turns), s.Turns[0].Notes)
	}
}

func TestNotificationAfterIdleStillCountsCacheRewrite(t *testing.T) {
	// 1h TTL rows; the notification comes 2h after the first reply
	s := parseRows(t, userRec(1000, "build it"), assistantRec(1001, "m1", 6000, 0, ""),
		notifRec(8300), assistantRec(8301, "m2", 9000, 0, ""))
	if len(s.Turns) != 1 || s.Turns[0].RewriteTok != 9000 || s.Turns[0].Rewrite <= 0 {
		t.Errorf("rewrite %d / %v on %d turns", s.Turns[0].RewriteTok, s.Turns[0].Rewrite, len(s.Turns))
	}
}

func TestSkillTokensRunUntilTheNextHumanMessage(t *testing.T) {
	s := parseRows(t, userRec(1000, "do it"),
		toolRec(1001, "m1", "Skill", obj("skill", "ponytail:ponytail")), // its own call counts
		assistantRec(1002, "m2", 200, 0, ""),
		userRec(2000, "another thing"), assistantRec(2001, "m3", 400, 0, ""),
		userRec(3000, "<command-name>/review</command-name>"), assistantRec(3001, "m4", 50, 0, ""))
	if got := s.Turns[0].SkillTok; got == nil || got.N["ponytail:ponytail"] != int(tokOf(0)+tokOf(200)) {
		t.Errorf("first turn skill tokens %+v", got)
	}
	if s.Turns[1].SkillTok != nil && s.Turns[1].SkillTok.N["ponytail:ponytail"] != 0 {
		t.Errorf("human message did not end the skill: %+v", s.Turns[1].SkillTok)
	}
	// a slash command entry is not a prompt: its reply lands on the previous turn, attributed to the command
	if got := s.Turns[1].SkillTok; got == nil || got.N["review"] != int(tokOf(50)) {
		t.Errorf("slash command tokens %+v", got)
	}
}

func TestQuotedCommandTagInALaterBlockStartsNoSkill(t *testing.T) {
	quoted := obj("type", "user", "timestamp", iso(1000), "message", obj("role", "user", "content", []any{
		obj("type", "text", "text", "do the task"),
		obj("type", "text", "text", "<command-name>/fowler-refactoring</command-name>")}))
	s := parseRows(t, userRec(999, "start"), quoted, assistantRec(1001, "m1", 100, 0, ""))
	if s.Turns[0].SkillTok != nil {
		t.Errorf("quoted command tag counted: %+v", s.Turns[0].SkillTok)
	}
}

func TestActiveSecondsSkipIdleGaps(t *testing.T) {
	s := parseRows(t, userRec(1000, "a"), assistantRec(1060, "m1", 1, 0, ""),
		userRec(1120, "b"), assistantRec(5000, "m2", 1, 0, ""), // 3880 s idle
		userRec(5030, "c"), assistantRec(5031, "m3", 1, 0, ""))
	if s.Active != 60+60+30+1 {
		t.Errorf("active %v, want 151", s.Active)
	}
	if s.End-s.Start != 4031 {
		t.Errorf("wall %v", s.End-s.Start)
	}
}

func delegatedSession(id string, turn *Turn, launches ...*ojson.Object) *Session {
	var ls []any
	for _, l := range launches {
		ls = append(ls, l)
	}
	return &Session{ID: id, Turns: []*Turn{turn}, Totals: turn.Usage, Dele: obj("launches", ls)}
}

func launch(kind string, tok int64, cost float64) *ojson.Object {
	return obj("kind", kind, "tok", obj("in", tok, "out", int64(0), "cr", int64(0), "cw", int64(0)), "cost", cost, "calls", int64(2))
}

func TestSubagentKindsSumTokensAndCost(t *testing.T) {
	a := delegatedSession("a", &Turn{}, launch("implementer", 100, 1.5), launch("reviewer", 40, 0.5))
	b := delegatedSession("b", &Turn{}, launch("implementer", 300, 2.5))
	got := subagentKinds([]*Session{a, b})
	if len(got) != 2 || got[0].kind != "implementer" || got[0].n != 2 || got[0].tok != 400 || got[0].cost != 4 || got[1].tok != 40 {
		t.Errorf("%+v %+v", got[0], got[1])
	}
}

func TestInstructionTokensIncludeSubagents(t *testing.T) {
	cheap := &Turn{Prompt: "cheap", Calls: 1, Usage: Usage{In: 1000}}
	dear := &Turn{Prompt: "dear", Calls: 1, Usage: Usage{In: 10}, DTok: 5000}
	silent := &Turn{Prompt: "no calls", DTok: 9_999_999}
	top := topInstructions([]*Session{{ID: "a", Turns: []*Turn{cheap, dear, silent}}}, 5)
	if len(top) != 2 || top[0].t != dear || top[0].tok != 5010 {
		t.Errorf("%+v", top)
	}
}

func TestAttachDelegationCountsSubagentTokensOnTheLaunchingTurn(t *testing.T) {
	testHome(t)
	s := delegatingSession(t)
	attachDelegation([]*Session{s})
	var tok int64
	for _, tt := range s.Turns {
		tok += tt.DTok
	}
	if tok == 0 {
		t.Error("subagent tokens not placed on any turn")
	}
}

func TestParseCacheContinuesThroughNotificationAndSkill(t *testing.T) {
	h := testHome(t)
	p := filepath.Join(h, ".claude", "projects", "p", "s.jsonl")
	writeRows(t, p, userRec(1000, "a"), toolRec(1001, "m1", "Skill", obj("skill", "x")), assistantRec(1002, "m2", 5000, 0, ""))
	forgetParses()
	oneCached(p, 0)
	appendRows(t, p, notifRec(1100), assistantRec(1101, "m3", 6000, 0, ""), userRec(2000, "b"), assistantRec(2001, "m4", 7000, 0, ""))
	sameAsFresh(t, p)
	appendRows(t, p, notifRec(2100), assistantRec(2101, "m5", 8000, 0, ""))
	sameAsFresh(t, p)
}

func TestExpensiveViewRanksAndLinks(t *testing.T) {
	testHome(t)
	dear := &Turn{Prompt: "dear <one>", Calls: 1, Notes: 2, Usage: Usage{In: 10}, DTok: 5000, Dele: 1}
	cheap := &Turn{Prompt: "cheap", Calls: 1, Usage: Usage{In: 1000}}
	html := ExpensiveView([]*Session{{ID: "abcd1234", Turns: []*Turn{cheap, dear}, Totals: Usage{In: 1010}}}, 7)
	mustHave(t, html, "dear &lt;one&gt;")
	mustHave(t, html, "＋2 則自動接續")
	mustHave(t, html, "location='/?session=abcd1234#t1'")
	if strings.Index(html, "dear") > strings.Index(html, "cheap") {
		t.Error("not sorted by tokens")
	}
	mustHave(t, ExpensiveView(nil, 7), "近期沒有可排行的回合")
}

func TestRouteKnowsExpensivePage(t *testing.T) {
	if k, name := route("/expensive"); k != "page" || name != "expensive" {
		t.Errorf("%s %s", k, name)
	}
}

// A subagent that loads a skill first would otherwise charge its whole run to it.
func TestSubagentRunsAreNotChargedToSkills(t *testing.T) {
	h := testHome(t)
	side := writeRows(t, filepath.Join(h, "agent-x.jsonl"), userRec(1000, "task for the subagent"),
		toolRec(1001, "s1", "Skill", obj("skill", "plug:alpha")), assistantRec(1002, "s2", 100, 0, ""))
	l := scanAgent(side).Set("at", 5.0)
	turn := &Turn{SkillTok: py.NewCounter()}
	turn.SkillTok.Add("alpha", 1000)
	s := &Session{Start: 1, Turns: []*Turn{turn}, Dele: obj("launches", []any{l})}
	canon := func(n string) string { return strings.TrimPrefix(n, "plug:") }
	if got := skillTokens([]*Session{s}, canon)["alpha"]; got != 1000 {
		t.Errorf("alpha tokens %d, want only the main conversation's 1000", got)
	}
}

func TestLaunchesBeforeTheWindowAreNotCounted(t *testing.T) {
	early, late := launch("k", 100, 0), launch("k", 10, 0)
	early.Set("at", 50.0)
	late.Set("at", 500.0)
	s := &Session{Start: 100, Dele: obj("launches", []any{early, late})}
	if _, sub := sessionTokens(s); sub != 10 {
		t.Errorf("subagent tokens %d, want only the launch inside the window", sub)
	}
}

// An untyped launch takes the subagent's agentType from its .meta.json, and
// the Skill calls inside it are recorded for skill usage.
func TestUntypedLaunchUsesMetaAgentTypeAndKeepsSkillCalls(t *testing.T) {
	folder := t.TempDir()
	start := assistantRec(1005, "m1", 100, 0, "")
	ojson.Obj(ojson.Field(start, "message")).Set("content", []any{obj("type", "tool_use", "id", "tu1", "name", "Agent",
		"input", obj("description", "dig"))})
	result := obj("type", "user", "timestamp", iso(1009), "toolUseResult", ojson.NewObject(), "message", obj("role", "user",
		"content", []any{obj("type", "tool_result", "tool_use_id", "tu1", "content", "agentId: abc123")}))
	parent := writeRows(t, filepath.Join(folder, "s1.jsonl"), userRec(1000, "go"), start, result)
	sub := filepath.Join(folder, "s1", "subagents")
	writeRows(t, filepath.Join(sub, "agent-abc123.jsonl"), userRec(1006, "task"),
		toolRec(1007, "a1", "Skill", obj("skill", "plug:alpha")), assistantRec(1008, "a2", 100, 0, ""))
	if err := os.WriteFile(filepath.Join(sub, "agent-abc123.meta.json"), []byte(`{"agentType":"general-purpose"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ls := scanParent(parent, scanFolder(sub))
	if len(ls) != 1 || getStr(ls[0], "kind", "") != "general-purpose" {
		t.Fatalf("launches %v", ls)
	}
	if called, _ := ojson.Field(ls[0], "skills_called").([]any); len(called) != 1 || called[0] != "plug:alpha" {
		t.Errorf("skills_called %v", ojson.Field(ls[0], "skills_called"))
	}
}

func TestTypedLaunchKeepsItsSubagentType(t *testing.T) {
	folder := t.TempDir()
	start := assistantRec(1005, "m1", 100, 0, "")
	ojson.Obj(ojson.Field(start, "message")).Set("content", []any{obj("type", "tool_use", "id", "tu1", "name", "Agent",
		"input", obj("description", "dig", "subagent_type", "reviewer"))})
	result := obj("type", "user", "timestamp", iso(1009), "toolUseResult", ojson.NewObject(), "message", obj("role", "user",
		"content", []any{obj("type", "tool_result", "tool_use_id", "tu1", "content", "agentId: abc123")}))
	parent := writeRows(t, filepath.Join(folder, "s1.jsonl"), userRec(1000, "go"), start, result)
	sub := filepath.Join(folder, "s1", "subagents")
	writeRows(t, filepath.Join(sub, "agent-abc123.jsonl"), assistantRec(1008, "a2", 100, 0, ""))
	if err := os.WriteFile(filepath.Join(sub, "agent-abc123.meta.json"), []byte(`{"agentType":"general-purpose"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if ls := scanParent(parent, scanFolder(sub)); len(ls) != 1 || getStr(ls[0], "kind", "") != "reviewer" {
		t.Fatalf("launches %v", ls)
	}
}

func TestSkillUsageCountsSubagentCallsOncePerLaunch(t *testing.T) {
	testHome(t)
	l := launch("x", 10, 0).Set("at", 50.0).Set("skills_called", []any{"alpha", "alpha"})
	s := &Session{Start: 1, Turns: []*Turn{{At: 10, Skills: newSet(), MCP: newSet()}}, Dele: obj("launches", []any{l})}
	_, total, last := skillUsage([]*Session{s})
	if total.N["alpha"] != 1 || last["alpha"] != 50 {
		t.Errorf("alpha %d last %v", total.N["alpha"], last["alpha"])
	}
}

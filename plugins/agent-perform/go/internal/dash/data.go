package dash

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"agentperform/internal/common"
	"agentperform/internal/ojson"
	"agentperform/internal/prices"
	"agentperform/internal/py"
	"agentperform/internal/toollimits"
	"agentperform/internal/touched"
	"agentperform/internal/transcript"
)

// dash_data.py: transcript loading, caching and delegation data behind the dashboard.

const (
	rewriteMin = 5_000 // a first call writing less than this is new content, not a lapsed cache
	ttl5m      = 300
	ttl1h      = 3600
	activeGap  = 300 // entries further apart than this are idle time, not work
)

// skillCmdRe is the slash command a user entry starts (it activates a skill like a Skill call does).
var skillCmdRe = regexp.MustCompile(`<command-(?:name|message)>/?([^<]+)</command-`)

// slashCommand is the slash command a user message starts, "" when it starts none; only the first
// text block counts, as skill bodies and dispatched prompts further down quote command tags.
func slashCommand(msg any) string {
	content := ojson.GetOr(msg, "content")
	text, _ := content.(string)
	if parts, ok := content.([]any); ok && len(parts) > 0 && ojson.Obj(parts[0]) != nil && typ(parts[0]) == "text" {
		text = getStr(parts[0], "text", "")
	}
	if m := skillCmdRe.FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// Usage is one blank() accumulator.
type Usage struct {
	Cost            float64
	In, Out, Cr, Cw int64
	Miss            float64
}

func (u *Usage) add(o Usage) {
	u.Cost += o.Cost
	u.In += o.In
	u.Out += o.Out
	u.Cr += o.Cr
	u.Cw += o.Cw
	u.Miss += o.Miss
}

// hitRate is the share of prompt tokens served from cache (nil when none).
func hitRate(u Usage) *float64 {
	p := u.Cr + u.Cw + u.In
	if p == 0 {
		return nil
	}
	v := float64(u.Cr) / float64(p)
	return &v
}

// estTokens is the rough token count: CJK runs about one token per character.
func estTokens(text string) int64 {
	cjk, n := 0, 0
	for _, r := range text {
		n++
		if r >= 0x4e00 && r <= 0x9fff {
			cjk++
		}
	}
	return int64(float64(cjk)*0.7 + float64(n-cjk)/4)
}

// AgentRun is (model, seconds) of one Agent call.
type AgentRun struct {
	Model string
	Sec   *float64
}

// Load is a skill body injected into context.
type Load struct {
	Name   string
	Tokens int64
	At     float64
	Turn   int
}

// Recap is a /recap or away summary.
type Recap struct {
	At   float64
	Text string
}

// Turn is one dashboard turn record.
type Turn struct {
	At          float64
	First, Last *float64
	Tools       *py.Counter
	Limits      *py.Counter
	SubLimits   *py.Counter
	Agents      []AgentRun
	Calls       int
	Scripts     int
	ScriptCalls []ScriptRec
	Skills, MCP *orderedSet
	CtxPeak     int64
	Killed      bool
	Gap         *float64
	TTL         int
	Rewrite     float64
	RewriteTok  int64
	Usage       Usage
	Hit         *touched.Hit
	Prompt      string
	Queued      []string
	Model       string
	Models      *floatCounter
	Reply       string
	Resp, E2E   float64
	Window      int64
	DCost       float64
	DEnd        float64 // when the last subagent launched by this turn finished; 0 if none
	Dele        int
	DScripts    int
	Sid         string
	Fixed       int64
	FixedUSD    float64
	Notes       int         // automatic continuations (background notifications, /compact summaries) merged into this instruction
	SkillTok    *py.Counter // tokens of the calls made while a skill was active, by skill; nil when none
	DTok        int64       // tokens of the subagents launched by this turn (attachDelegation)
	DCalls      int         // API calls of those subagents
}

// Session is one conversation as the dashboard sees it.
type Session struct {
	ID, Path, Full, Proj string
	Totals               Usage
	Turns                []*Turn
	Loads                []Load
	Models               *floatCounter
	Cwd                  string
	Prefix               int64
	Start, End           float64
	Opening              string
	Recaps               []Recap
	Dele                 *ojson.Object
	Active               float64 // seconds between consecutive entries closer than activeGap
}

func (s *Session) clone() *Session {
	c := *s
	c.Turns = make([]*Turn, len(s.Turns))
	for i, t := range s.Turns {
		tt := *t
		c.Turns[i] = &tt
	}
	c.Loads = append([]Load{}, s.Loads...)
	return &c
}

func agentSide(path string, agentID string) string {
	return filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents", "agent-"+agentID+".jsonl")
}

// subagentRun is (model, seconds) of one Agent call, or nil.
func subagentRun(path string, result any) *AgentRun {
	res := ojson.Obj(result)
	if res == nil || !ojson.Truthy(ojson.Field(res, "agentId")) {
		return nil
	}
	ms, hasMs := py.Number(ojson.Field(res, "totalDurationMs"))
	if ojson.Field(res, "totalDurationMs") == nil {
		var stamps []float64
		for e := range transcript.Entries(agentSide(path, py.Str(ojson.Field(res, "agentId")))) {
			if v, ok := transcript.When(e); ok && v != 0 {
				stamps = append(stamps, v)
			}
		}
		hasMs = len(stamps) > 1
		if hasMs {
			ms = (stamps[len(stamps)-1] - stamps[0]) * 1000
		}
	}
	model := ""
	if v := ojson.GetOr(res, "resolvedModel"); v != nil {
		model = py.Str(v)
	}
	run := &AgentRun{Model: model}
	if hasMs {
		s := ms / 1000
		run.Sec = &s
	}
	return run
}

// cacheRewrite is (tokens, dollars) of a Cache 過期重寫.
func cacheRewrite(gap *float64, ttl int, model string, cw, cr int64) (int64, float64) {
	if gap == nil || *gap <= float64(ttl) || cw < rewriteMin || cw <= cr {
		return 0, 0
	}
	short := ttl == ttl5m
	return cw, prices.Price(model, 0, 0, 0, float64(cw), short) - prices.Price(model, 0, 0, float64(cw), 0, short)
}

func ttlOf(usage any) int {
	cc := ojson.GetOr(usage, "cache_creation")
	if f, _ := py.Number(ojson.GetOr(cc, "ephemeral_5m_input_tokens")); f > 0 {
		return ttl5m
	}
	if f, _ := py.Number(ojson.GetOr(cc, "ephemeral_1h_input_tokens")); f > 0 {
		return ttl1h
	}
	return 0
}

// one parses a transcript into per-turn records (nil when it cost nothing).
func one(path string, floor float64) *Session {
	s, _, _ := parse(path, floor)
	return s
}

// parse is one() plus the oldest timestamp it kept (lo) and the newest it
// dropped for being before floor (hi): the parse is the same for any floor in (hi, lo].
func parse(path string, floor float64) (s *Session, lo, hi float64) {
	s, lo, hi, _ = parseFrom(path, floor, nil)
	return
}

// carry is the parse state just before the last real prompt: enough to continue
// from that line's byte offset when the transcript only grew.
type carry struct {
	Off                     int64
	NTurns, NLoads, NRecaps int // how many of the previous parse's turns, loads and recaps lie before Off
	Totals                  Usage
	Models                  *floatCounter
	Cwds                    *py.Counter
	Cwd                     string
	TTL                     int
	LastCall                float64 // 0: none
	FirstCall               int64
	HasFirst                bool
	Start                   float64 // 0: none
	Seen, Written           []string
	Lo, Hi                  float64
	Active, PrevTs          float64
}

// resume continues a parse from c over the turns, loads and recaps prev kept before c.Off.
type resume struct {
	c    *carry
	prev *Session
}

// parseFrom is parse, optionally continuing rs; next is the carry for the following continuation (nil without a prompt).
func parseFrom(path string, floor float64, rs *resume) (s *Session, lo, hi float64, next *carry) {
	lo, hi = math.Inf(1), math.Inf(-1)
	var turns []*Turn
	cwds := py.NewCounter()
	var loads []Load
	var recaps []Recap
	var totals Usage
	models := newFloatCounter()
	seen := map[string]bool{}
	var seenList []string
	var start, end *float64
	var cur *Turn
	cwd := ""
	callsByID := map[string]bool{}
	var events []toollimits.Event
	var firstCall *int64
	var lastCall *float64
	ttl := ttl5m
	written := newSet()
	scriptByID := map[string]ScriptRec{}
	scripted := map[string]bool{}
	var from int64
	var active, prevTs float64
	curSkill := ""
	var notifGap *float64 // gap before the first API call after a merged notification
	if rs != nil {
		c := rs.c
		from, lo, hi = c.Off, c.Lo, c.Hi
		turns = slices.Clone(rs.prev.Turns[:c.NTurns])
		loads = slices.Clone(rs.prev.Loads[:c.NLoads])
		recaps = slices.Clone(rs.prev.Recaps[:c.NRecaps])
		totals, models, cwds, cwd, ttl = c.Totals, c.Models.clone(), c.Cwds.Clone(), c.Cwd, c.TTL
		active, prevTs = c.Active, c.PrevTs
		for _, id := range c.Seen {
			seen[id] = true
		}
		seenList = slices.Clone(c.Seen)
		for _, w := range c.Written {
			written.add(w)
		}
		if c.LastCall != 0 {
			lastCall = &c.LastCall
		}
		if c.HasFirst {
			firstCall = &c.FirstCall
		}
		if c.Start != 0 {
			start = &c.Start
		}
	}

	closeTurn := func() {
		if cur != nil && cur.First != nil {
			for _, h := range toollimits.Scan(events) {
				if h.Counted {
					cur.Limits.Add(h.Key, 1)
				}
			}
			cur.Resp = *cur.First - cur.At
			cur.E2E = *cur.Last - cur.At
			turns = append(turns, cur)
		}
	}

	for off, entry := range transcript.EntriesAt(path, from) {
		st, ok := transcript.When(entry)
		if ok && st < floor {
			hi = max(hi, st)
		} else if ok {
			lo = min(lo, st)
		}
		if !ok || st < floor || ojson.Truthy(ojson.Field(entry, "isSidechain")) {
			continue
		}
		stamp := st
		preActive, prePrev := active, prevTs
		if prevTs != 0 {
			if g := st - prevTs; g > 0 && g < activeGap {
				active += g
			}
		}
		prevTs = st
		prevCwd, entryCwd, startWasNil := cwd, "", start == nil
		if start == nil {
			start = &stamp
		}
		end = &stamp
		if v := ojson.GetOr(entry, "cwd"); v != nil {
			entryCwd = py.Str(v)
			cwds.Add(entryCwd, 1)
			cwd = entryCwd
		}
		msg := transcript.Msg(entry)
		if getStr(entry, "subtype", "") == "away_summary" && ojson.Truthy(ojson.Field(entry, "content")) {
			text := py.Str(ojson.Field(entry, "content"))
			recaps = append(recaps, Recap{stamp, py.Strip(strings.ReplaceAll(text, "(disable recaps in /config)", ""))})
			continue
		}
		etype := typ(entry)
		if etype == "attachment" && cur != nil {
			if q, ok := queuedPrompt(entry); ok && q != "" {
				cur.Queued = append(cur.Queued, py.Head(promptLabel(strings.Join(py.Split(q), " ")), 160))
			}
			continue
		}
		switch etype {
		case "user":
			body := transcript.TextOf(msg)
			if c := slashCommand(msg); c != "" {
				curSkill = c
			}
			if ojson.Truthy(ojson.Field(entry, "isMeta")) && strings.Contains(body, skillMark) {
				rest := py.Strip(strings.SplitN(body, skillMark, 2)[1])
				words := py.Split(rest)
				head := ""
				if len(words) > 0 {
					head = words[0]
				}
				loads = append(loads, Load{Name: basename(strings.TrimRight(head, "/")), Tokens: estTokens(body), At: stamp, Turn: len(turns) + 1})
				continue
			}
			if strings.Contains(body, "Request interrupted") && cur != nil {
				curSkill = ""
				cur.Killed = true
				continue
			}
			auto := common.AutoContinuation(entry, body)
			if !transcript.IsPrompt(entry) && !auto {
				if res := ojson.Field(entry, "toolUseResult"); ojson.Truthy(res) && cur != nil {
					if sub := subagentRun(path, res); sub != nil {
						cur.Agents = append(cur.Agents, *sub)
						side := agentSide(path, py.Str(ojson.Field(res, "agentId")))
						for _, h := range toollimits.Scan(toollimits.EventsOf(transcript.Entries(side))) {
							if h.Counted {
								cur.SubLimits.Add(h.Key, 1)
							}
						}
					}
					touched.TakeResult(entry, cur.Hit)
					touched.FoldSubagent(entry, cur.Hit)
				}
				for _, part := range transcript.Parts(msg) {
					if ojson.Obj(part) != nil && typ(part) == "tool_result" && cur != nil {
						takeResult(scriptByID, part)
						content, _ := ojson.Obj(part).Get("content")
						events = append(events, toollimits.Event{Kind: "result", ID: idStr(ojson.Field(part, "tool_use_id")),
							Text: transcript.TextOf(ojson.NewObject().Set("content", content))})
					}
				}
				continue
			}
			if cur != nil && auto {
				cur.Notes++
				events = append(events, toollimits.Event{Kind: "prompt", Text: body})
				if cur.Calls > 0 && lastCall != nil && *lastCall != 0 {
					g := stamp - *lastCall
					notifGap = &g
				}
				continue
			}
			closeTurn()
			curSkill, notifGap = "", nil
			next = &carry{Off: off, NTurns: len(turns), NLoads: len(loads), NRecaps: len(recaps), Totals: totals,
				Models: models.clone(), Cwds: cwds.Clone(), Cwd: prevCwd, TTL: ttl, Seen: seenList, Written: written.keys, Lo: lo, Hi: hi,
				Active: preActive, PrevTs: prePrev}
			if entryCwd != "" {
				next.Cwds.N[entryCwd]--
			}
			if lastCall != nil {
				next.LastCall = *lastCall
			}
			if firstCall != nil {
				next.FirstCall, next.HasFirst = *firstCall, true
			}
			if !startWasNil {
				next.Start = *start
			}
			next.Seen, next.Written = next.Seen[:len(next.Seen):len(next.Seen)], next.Written[:len(next.Written):len(next.Written)]
			events = []toollimits.Event{{Kind: "prompt", Text: body}}
			var gap *float64
			if lastCall != nil && *lastCall != 0 {
				g := stamp - *lastCall
				gap = &g
			}
			cur = &Turn{At: stamp, Tools: py.NewCounter(), Limits: py.NewCounter(), SubLimits: py.NewCounter(),
				Skills: newSet(), MCP: newSet(), Gap: gap, TTL: ttl, Hit: touched.Blank(),
				Prompt: py.Head(promptLabel(strings.Join(py.Split(body), " ")), 160), Models: newFloatCounter()}
		case "assistant":
			for _, part := range transcript.Parts(msg) { // a Skill call's own API call already counts for the skill
				if ojson.Obj(part) != nil && typ(part) == "tool_use" && getStr(part, "name", "") == "Skill" {
					if inp := ojson.GetOr(part, "input"); inp != nil {
						if sk := getStr(inp, "skill", ""); sk != "" {
							curSkill = sk
						}
					}
				}
			}
			usage := ojson.GetOr(msg, "usage")
			if role, _ := ojson.Str(ojson.Field(msg, "role")); usage != nil && role == "assistant" {
				mid := ojson.GetOr(msg, "id")
				if mid == nil {
					mid = ojson.Field(entry, "uuid")
				}
				if !ojson.Truthy(mid) || !seen[py.Repr(mid)] {
					if ojson.Truthy(mid) {
						seen[py.Repr(mid)] = true
						seenList = append(seenList, py.Repr(mid))
					}
					model := getStr(msg, "model", "?")
					in, out := getNum(usage, "input_tokens"), getNum(usage, "output_tokens")
					cr, cw := getNum(usage, "cache_read_input_tokens"), getNum(usage, "cache_creation_input_tokens")
					step := Usage{In: in, Out: out, Cr: cr, Cw: cw}
					step.Cost = prices.Price(model, float64(in), float64(out), float64(cr), float64(cw), false)
					step.Miss = prices.Price(model, float64(in), 0, 0, float64(cw), false) - prices.Price(model, 0, 0, float64(in+cw), 0, false)
					totals.add(step)
					models.add(model, step.Cost)
					seat := in + cr + cw
					if firstCall == nil {
						firstCall = &seat
					}
					if cur != nil && cur.Calls == 0 {
						cur.RewriteTok, cur.Rewrite = cacheRewrite(cur.Gap, cur.TTL, model, cw, cr)
					} else if cur != nil && notifGap != nil {
						rt, rw := cacheRewrite(notifGap, ttl, model, cw, cr)
						cur.RewriteTok, cur.Rewrite = cur.RewriteTok+rt, cur.Rewrite+rw
					}
					notifGap = nil
					lc := stamp
					lastCall = &lc
					if t := ttlOf(usage); t != 0 {
						ttl = t
					}
					if cur != nil {
						cur.CtxPeak = max(cur.CtxPeak, seat)
						cur.Calls++
						cur.Hit.Tokens += float64(in + out + cr + cw)
						cur.Usage.add(step)
						if curSkill != "" {
							if cur.SkillTok == nil {
								cur.SkillTok = py.NewCounter()
							}
							cur.SkillTok.Add(curSkill, int(in+out+cr+cw))
						}
						cur.Model = model
						cur.Models.add(model, step.Cost)
					}
				}
			}
			for _, part := range transcript.Parts(msg) {
				if ojson.Obj(part) == nil || cur == nil {
					continue
				}
				if typ(part) == "text" {
					if py.Len(cur.Reply) < 1600 {
						cur.Reply += py.Head(getStr(part, "text", ""), 1600)
					}
					continue
				}
				if typ(part) != "tool_use" {
					continue
				}
				touched.Take(part, cur.Hit, cwd)
				name := getStr(part, "name", "?")
				inp := ojson.GetOr(part, "input")
				if inp == nil {
					inp = ojson.NewObject()
				}
				id := idStr(ojson.Field(part, "id"))
				callsByID[id] = true
				if _, done := scripted[id]; !done {
					lang, code, ok := adhocScript(name, inp, written)
					scripted[id] = ok
					if ok {
						cur.Scripts++
					}
					takeScript(&cur.ScriptCalls, scriptByID, id, lang, code, ok)
				}
				events = append(events, toollimits.Event{Kind: "use", ID: id, Name: name, Inp: inp})
				switch {
				case name == "Skill":
					cur.Skills.add(getStr(inp, "skill", "?"))
				case strings.HasPrefix(name, "mcp__"):
					cur.MCP.add(strings.Split(name, "__")[1])
				case name == "Agent":
					if kind := ojson.GetOr(inp, "subagent_type"); kind != nil {
						cur.Tools.Add("agent:"+py.Str(kind), 1)
					} else {
						cur.Tools.Add("agent", 1)
					}
				default:
					cur.Tools.Add(name, 1)
				}
			}
			if cur != nil {
				if cur.First == nil {
					f := stamp
					cur.First = &f
				}
				l := stamp
				cur.Last = &l
			}
		}
	}
	closeTurn()
	if totals.Cost <= 0 || len(turns) == 0 {
		return nil, lo, hi, next
	}
	where := ""
	if cwds.Len() > 0 {
		where = cwds.MostCommon(1)[0]
	}
	proj := basename(strings.TrimRight(where, "/"))
	if proj == "" {
		proj = "?"
	}
	var fc int64
	if firstCall != nil {
		fc = *firstCall
	}
	base := filepath.Base(path)
	return &Session{ID: py.Head(base, 8), Path: path, Full: strings.TrimSuffix(base, ".jsonl"), Proj: proj,
		Totals: totals, Turns: turns, Loads: loads, Models: models, Cwd: where,
		Prefix: fc - estTokens(turns[0].Prompt), Start: *start, End: *end, Active: active,
		Opening: py.Head(turns[0].Prompt, 70), Recaps: recaps}, lo, hi, next
}

var _ = os.Stat

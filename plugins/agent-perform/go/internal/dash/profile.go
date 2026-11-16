package dash

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"agentperform/internal/ojson"
	"agentperform/internal/prices"
	"agentperform/internal/py"
	"agentperform/internal/toollimits"
	"agentperform/internal/transcript"
)

// Use is one tool call on the 依次數 tab (pipeline's uses dicts).
type Use struct {
	Kind     string
	Pos      int
	Turn     int
	HasTurn  bool
	Ts       *float64
	Input    string
	Desc     *string // Agent/Task description, nil otherwise
	Script   string
	Limit    []string
	Result   *string
	N        int64
	RPos     int
	HasRPos  bool
	Error    bool
	Resend   int64
	Usd      float64 // result tokens x the cache-read rate of every later call
	HasRes   bool
	Tid      string
	Lang     string
	CodeN    int64
	CodeRsnd int64
}

// Call is one API call of a profile.
type Call struct {
	Pos        int
	Ts         *float64
	In, Cr, Cw int64
	Cw1h, Cw5m int64
	Ctx        int64
	Model      string
	Turn       int
	InTurn     bool
}

type group struct {
	kind string
	uses []*Use
}

// toolGroups is [(kind, [use...])] most-called first; each use gets resend.
func toolGroups(uses []*Use, calls []*Call) []group {
	byKind := map[string][]*Use{}
	var order []string
	for _, u := range uses {
		if !u.HasRes {
			rpos := 1_000_000_000
			if u.HasRPos {
				rpos = u.RPos
			}
			var n int64
			for _, c := range calls {
				if c.Pos > rpos {
					n++
				}
			}
			u.Resend, u.HasRes = n, true
		}
		if _, ok := byKind[u.Kind]; !ok {
			order = append(order, u.Kind)
		}
		byKind[u.Kind] = append(byKind[u.Kind], u)
	}
	var out []group
	for _, k := range order {
		out = append(out, group{k, byKind[k]})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].uses) != len(out[j].uses) {
			return len(out[i].uses) > len(out[j].uses)
		}
		return sumN(out[i].uses) > sumN(out[j].uses)
	})
	return out
}

func sumN(us []*Use) int64 {
	var n int64
	for _, u := range us {
		n += u.N
	}
	return n
}

// mainToolUses pairs every tool_use item with its result item.
func mainToolUses(items []*Item) []*Use {
	uses := map[string]*Use{}
	var order []string
	for _, it := range items {
		switch it.Cat {
		case "tool_use":
			input := it.Text
			if strings.HasPrefix(it.Src, "Bash") {
				if cmd, ok := ojson.Field(it.Inp, "command").(string); ok {
					input = cmd
				}
			}
			u := &Use{Kind: it.Src, Pos: it.Pos, Turn: it.Turn, HasTurn: it.HasTurn, Ts: it.Ts, Input: input,
				Script: it.Script, Limit: append([]string{}, it.Limit...)}
			if it.Src == "Agent" || it.Src == "Task" {
				var d *string
				if v := ojson.Field(it.Inp, "description"); v != nil {
					s := py.Str(v)
					d = &s
				}
				u.Desc = d
			}
			if _, ok := uses[it.Tid]; !ok {
				order = append(order, it.Tid)
			}
			uses[it.Tid] = u
		case "tool_result", "subagent":
			if u, ok := uses[it.Tid]; ok && it.HasTid {
				text := it.Text
				u.Result, u.N, u.RPos, u.HasRPos, u.Error = &text, it.N, it.Pos, true, it.Err
				u.Resend, u.HasRes = it.Resend, true
				u.Usd = resendUSD(it)
				u.Limit = append(u.Limit, it.Limit...)
			}
		}
	}
	var out []*Use
	for _, tid := range order {
		u := uses[tid]
		u.Tid = tid
		if !u.HasRes {
			u.Resend, u.HasRes = 0, true
		}
		out = append(out, u)
	}
	return out
}

var listingEvents = setOf("deferred_tools_delta", "mcp_instructions_delta", "agent_listing_delta", "skill_listing")

// mainProfile is the main conversation's API calls up to the end of the turn.
func mainProfile(path string, meta *Meta) ([]*Call, int) {
	recs := readRecs(path, false)
	if meta.End < len(recs) {
		recs = recs[:meta.End]
	}
	calls, cuts := profileCalls(recs)
	for _, c := range calls {
		c.Turn = sort.SearchInts(meta.PromptsPos, c.Pos+1)
		c.InTurn = meta.Span[0] <= c.Pos && c.Pos < meta.Span[1]
	}
	return calls, len(cuts)
}

// profileCalls is every API call of recs, deduplicated by message id, and the position of every compact_boundary.
func profileCalls(recs []any) (calls []*Call, cuts []int) {
	order := map[string]int{}
	for i, r := range recs {
		msg := msgOf(r)
		switch {
		case getStr(r, "subtype", "") == "compact_boundary":
			cuts = append(cuts, i)
		case typ(r) == "assistant":
			u := ojson.Field(msg, "usage")
			mid := ojson.GetOr(msg, "id")
			if mid == nil {
				mid = ojson.Field(r, "uuid")
			}
			if !ojson.Truthy(u) {
				continue
			}
			c := usageCall(u, getStr(msg, "model", ""))
			key := py.Repr(mid)
			if k, ok := order[key]; ok {
				old := calls[k]
				c.Pos, c.Ts = old.Pos, old.Ts
				calls[k] = c
			} else if c.Ctx != 0 {
				order[key] = len(calls)
				c.Pos, c.Ts = i, when(r)
				calls = append(calls, c)
			}
		}
	}
	return calls, cuts
}

func usageCall(u any, model string) *Call {
	cc := ojson.GetOr(u, "cache_creation")
	c := &Call{In: getNum(u, "input_tokens"), Cr: getNum(u, "cache_read_input_tokens"),
		Cw: getNum(u, "cache_creation_input_tokens"), Cw1h: getNum(cc, "ephemeral_1h_input_tokens"),
		Cw5m: getNum(cc, "ephemeral_5m_input_tokens"), Model: model}
	c.Ctx = c.In + c.Cr + c.Cw
	return c
}

// toolLife is one tool result's whole stay in context. Estimate: the first call after the result writes it
// to the cache once, at that call's TTL rate; every later call until the next compaction reads it.
// Re-writes after the cache expired are not counted.
type toolLife struct {
	tok       int64
	ctxPct    float64 // of the writing call's context
	writeUSD  float64
	short     bool // written at the 5-minute rate
	reads     int
	readUSD   float64
	compacted bool // a compaction ended it; else it is still in context
	sent      bool // some call carried it
}

func (l toolLife) total() float64 { return l.writeUSD + l.readUSD }

// lifeOf is the life of a tok-token result at record rpos, given every call and compaction position.
func lifeOf(tok int64, rpos int, calls []*Call, cuts []int) toolLife {
	l := toolLife{tok: tok}
	cut := -1
	for _, p := range cuts {
		if p > rpos {
			cut = p
			break
		}
	}
	l.compacted = cut >= 0
	for _, c := range calls {
		if c.Pos <= rpos {
			continue
		}
		if cut >= 0 && c.Pos > cut {
			break
		}
		if !l.sent {
			l.sent, l.short = true, c.Cw5m > 0
			l.writeUSD = float64(tok) * prices.CacheWrite(c.Model, l.short) / 1e6
			if c.Ctx > 0 {
				l.ctxPct = float64(tok) / float64(c.Ctx) * 100
			}
			continue
		}
		l.reads++
		l.readUSD += float64(tok) * prices.CacheRead(c.Model) / 1e6
	}
	return l
}

// html is the cost block inside the picked tool row.
func (l toolLife) html() string {
	if !l.sent {
		why := "之後還沒有 API 呼叫送出它"
		if l.compacted {
			why = "送出前就被壓縮掉了"
		}
		return `<div class="pp-kv"><div><b>` + py.Comma(l.tok) + `</b><span>結果 token（估）</span></div></div>` +
			`<p class="dim">還沒產生 cache 費用：` + why + `。</p>`
	}
	ttl, until := "1h", "到目前為止，還在 context 裡"
	if l.short {
		ttl = "5m"
	}
	if l.compacted {
		until = "到下次壓縮為止"
	}
	return `<div class="pp-kv">` +
		`<div><b>` + py.Comma(l.tok) + `</b><span>結果 token（估）</span></div>` +
		`<div><b>` + py.F(l.ctxPct, 1) + `%</b><span>佔第一次送出時的 context</span></div>` +
		`<div><b>` + money(l.total()) + `</b><span>累計花費（估）：寫入 ` + money(l.writeUSD) + `（` + ttl + `）＋ 之後 ` + strconv.Itoa(l.reads) + ` 次讀取 ` + money(l.readUSD) + `，` + until + `</span></div>` +
		`</div><p class="dim">估算：寫入 cache 一次、之後每次 API 呼叫讀一次；不計 Cache 過期後的重寫，照 API 牌價換算。</p>`
}

// toolPick is the one call a tool= page opens on.
type toolPick struct {
	tid  string
	hit  bool      // tid is among the page's uses
	life *toolLife // nil: no result yet, or none found
}

// pickTool finds tid among uses, or else among recs (a call compacted out of the page's context), and only then
// makes the untruncated pass over recs for its life; nil for tid "".
func pickTool(tid string, uses []*Use, recs func() []any) *toolPick {
	if tid == "" {
		return nil
	}
	p := &toolPick{tid: tid}
	var all []any
	rpos, tok, ok := 0, int64(0), false
	if i := slices.IndexFunc(uses, func(u *Use) bool { return u.Tid == tid }); i >= 0 {
		p.hit = true
		rpos, tok, ok = uses[i].RPos, uses[i].N, uses[i].HasRPos
	} else {
		all = recs()
		rpos, tok, ok = resultAt(all, tid)
	}
	if ok {
		if all == nil {
			all = recs()
		}
		calls, cuts := profileCalls(all)
		l := lifeOf(tok, rpos, calls, cuts)
		p.life = &l
	}
	return p
}

// resultAt is the position of tid's tool_result in recs and its size by the uncalibrated tokenizer.
func resultAt(recs []any, tid string) (int, int64, bool) {
	for i, r := range recs {
		if typ(r) != "user" {
			continue
		}
		for _, x := range transcript.Parts(msgOf(r)) {
			if ojson.Obj(x) == nil || typ(x) != "tool_result" || idStr(ojson.Field(x, "tool_use_id")) != tid {
				continue
			}
			body := ojson.Field(x, "content")
			text, isStr := body.(string)
			if !isStr {
				text = textOf(body)
			}
			return i, int64(countTokens(text)), true
		}
	}
	return 0, 0, false
}

// tab is the content tab the page opens on: 依次數 for a hit, else the first.
func (p *toolPick) tab() string {
	if p != nil && p.hit {
		return "cnt"
	}
	return ""
}

// on reports whether u is the picked call.
func (p *toolPick) on(u *Use) bool { return p != nil && p.hit && u.Tid == p.tid }

// note is for a call that is not in where's context: its life if its result was found, else that it was not.
func (p *toolPick) note(where, why string) string {
	switch {
	case p == nil || p.hit:
		return ""
	case p.life != nil:
		return `<div class="card"><p class="lede">這次工具呼叫已經不在` + where + `的 context 裡（被壓縮掉了）；它的結果在那之前的花費：</p>` + p.life.html() + `</div>`
	}
	return `<p class="lede dim">` + where + `的 context 裡找不到這次工具呼叫（` + why + `）。</p>`
}

// Sub is one subagent run next to a session (pipeline.subagents).
type Sub struct {
	Scan   *ojson.Object
	ID     string
	Desc   string
	Type   string
	Launch any
	Limits *py.Counter
	start  float64
}

func (s *Sub) num(k string) float64 { f, _ := py.Number(ojson.Field(s.Scan, k)); return f }

// subagentsOf is scan_agent per transcript next to the session's, optionally
// only those launched by the given tool_use ids (from the .meta.json).
func subagentsOf(path string, toolIDs map[string]bool) []*Sub {
	dir := filepath.Join(strings.TrimSuffix(path, ".jsonl"), "subagents")
	var out []*Sub
	for _, name := range listdir(dir) {
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".meta.json") {
			continue
		}
		m := filepath.Join(dir, name)
		raw, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		meta, err := ojson.Parse(raw)
		if err != nil {
			continue
		}
		launch := ojson.Field(meta, "toolUseId")
		if toolIDs != nil && !toolIDs[idStr(launch)] {
			continue
		}
		f := strings.TrimSuffix(m, ".meta.json") + ".jsonl"
		lim := py.NewCounter()
		for _, h := range toollimits.Scan(toollimits.EventsOf(transcript.Entries(f))) {
			if h.Counted {
				lim.Add(h.Key, 1)
			}
		}
		base := filepath.Base(f)
		s := &Sub{Scan: scanAgentCached(f), ID: base[len("agent-") : len(base)-len(".jsonl")],
			Desc: getStr(meta, "description", ""), Type: getStr(meta, "agentType", ""), Launch: launch, Limits: lim}
		s.start, _ = py.Number(ojson.Field(s.Scan, "start"))
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// launchTurn is the main-conversation turn that contains the tool_use launch, or 0.
func launchTurn(path string, launch any) int {
	recs := readRecs(path, false)
	prompts := promptPositions(recs)
	for pos, r := range recs {
		for _, p := range transcript_parts(r) {
			if ojson.Obj(p) != nil && typ(p) == "tool_use" && py.Repr(ojson.Field(p, "id")) == py.Repr(launch) {
				n := 0
				for k, i := range prompts {
					if i <= pos {
						n = k + 1
					}
				}
				return n
			}
		}
	}
	return 0
}

func transcript_parts(r any) []any {
	parts, _ := ojson.GetOr(msgOf(r), "content").([]any)
	return parts
}

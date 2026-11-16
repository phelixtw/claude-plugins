package dash

import (
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
	"agentperform/internal/transcript"
)

// Item is one segment of a turn's context (pipeline.items_for).
type Item struct {
	Cat      string
	Turn     int // 0 = None (system items)
	HasTurn  bool
	Pos      int // -1 = None
	Src      string
	Text     string
	NImg     int
	HasTid   bool
	Tid      string
	Inp      any
	Ts       *float64
	Tool     string
	HasErr   bool
	Err      bool
	Agent    any
	Kind     string
	N        int64
	Resend   int64
	RateSum  float64 // cache-read $/MTok summed over the later calls that resend this item
	HasRes   bool
	Script   string // "" = no script
	Limit    []string
	LimitHit []string
}

// Meta is items_for's meta dict.
type Meta struct {
	Real       *Real
	StartTurn  int
	Compacts   int
	Cwd        string
	ToolIDs    map[string]bool
	Start, End int
	PromptsPos []int
	Span       [2]int
	At         *float64
	PromptAt   []*float64
	ok         bool
}

// Real is the last API call of the turn: what it actually occupied.
type Real struct {
	In, Cr, Cw int64
	Model      string
}

type promptPick struct {
	N    int
	Text string
}

var (
	attachZH = map[string]string{"queued_command": "排隊中的輸入", "total_tokens_reminder": "token 餘額提醒",
		"agent_listing_delta": "subagent 清單更新", "skill_listing": "skill 清單",
		"mcp_instructions_delta": "MCP 說明更新", "deferred_tools_delta": "延後載入工具清單更新"}
	attachCat = map[string]string{
		"instructions": "claudemd", "skill_listing": "listing", "agent_listing_delta": "listing",
		"mcp_instructions_delta": "listing", "deferred_tools_delta": "listing",
		"invoked_skills": "skill", "file": "file", "edited_text_file": "file"}
	blockedRe    = pyre.Compile(`(\w+)(?::\S+)? hook error:`)
	agentIDRe    = pyre.Compile(`agentId: (\w+)`)
	stopHookRe   = pyre.Compile(`(Stop|SubagentStop) hook feedback:`)
	agentFromRe  = pyre.Compile(`<agent-message from="([^"]+)"`)
	tnToolRe     = pyre.Compile(`<tool-use-id>([^<]+)</tool-use-id>`)
	tnTaskRe     = pyre.Compile(`<task-id>([^<]+)</task-id>`)
	commandRe    = pyre.Compile(`<command-(?:name|message)>/?([^<]*)<`)
	whitespaceRe = pyre.Compile(`\s+`)
)

func typ(r any) string { s, _ := ojson.Str(ojson.Field(r, "type")); return s }

func attachmentOf(r any) *ojson.Object { return ojson.Obj(ojson.Field(r, "attachment")) }

// queuedPrompt is what the user typed while a turn was still running, or ("", false).
func queuedPrompt(r any) (string, bool) {
	a := attachmentOf(r)
	if typ(a) != "queued_command" {
		return "", false
	}
	if m, _ := ojson.Str(ojson.Field(a, "commandMode")); m != "prompt" {
		return "", false
	}
	if k, _ := ojson.Str(ojson.Field(ojson.GetOr(a, "origin"), "kind")); k != "human" {
		return "", false
	}
	p := ojson.Field(a, "prompt")
	if s, ok := p.(string); ok {
		return s, true
	}
	var content any = ""
	if ojson.Truthy(p) {
		content = p
	}
	return transcript.TextOf(ojson.NewObject().Set("content", content)), true
}

type subMsg struct {
	kind    string
	agent   any // string or nil
	toolUse any
	text    string
}

// subagentMessage is a background subagent's report as it reaches the main context.
func subagentMessage(r any) *subMsg {
	if typ(r) != "user" || ojson.Truthy(ojson.Field(r, "queueTranscriptOnly")) {
		return nil
	}
	text := transcript.TextOf(transcript.Msg(r))
	if strings.Contains(text, "[Subagent hand-back]") && strings.Contains(text, "<agent-message") {
		var agent any
		if m := agentFromRe.Search(text); m != nil {
			agent = m.Group(1)
		} else {
			agent = ojson.Field(ojson.Obj(ojson.Field(r, "origin")), "from")
		}
		return &subMsg{"handback", agent, nil, text}
	}
	if strings.HasPrefix(py.LStrip(text), "<task-notification>") {
		var agent, tool any
		if m := tnTaskRe.Search(text); m != nil {
			agent = m.Group(1)
		}
		if m := tnToolRe.Search(text); m != nil {
			tool = m.Group(1)
		}
		return &subMsg{"notification", agent, tool, text}
	}
	return nil
}

// promptPositions: indices of real prompts that got an assistant answer before the next one.
func promptPositions(recs []any) []int {
	var marks []int
	for i, r := range recs {
		if transcript.IsPrompt(r) {
			marks = append(marks, i)
		}
	}
	var out []int
	for n, i := range marks {
		end := len(recs)
		if n+1 < len(marks) {
			end = marks[n+1]
		}
		for _, r := range recs[i:end] {
			if typ(r) == "assistant" {
				out = append(out, i)
				break
			}
		}
	}
	return out
}

// held is the main-conversation records of the transcript a /pipeline page is rendering, read once for the page's
// several readers; the page's release drops them.
var held struct {
	sync.Mutex
	path string
	recs []any
	read bool
}

func holdRecs(path string) (release func()) {
	held.Lock()
	held.path, held.recs, held.read = path, nil, false
	held.Unlock()
	return func() {
		held.Lock()
		if held.path == path {
			held.path, held.recs, held.read = "", nil, false
		}
		held.Unlock()
	}
}

func readRecs(path string, side bool) []any {
	if side {
		return readRecsFile(path, true)
	}
	held.Lock()
	defer held.Unlock()
	if held.path != path {
		return readRecsFile(path, false)
	}
	if !held.read {
		held.recs, held.read = readRecsFile(path, false), true
	}
	return held.recs
}

func readRecsFile(path string, side bool) []any {
	var recs []any
	for r := range transcript.Entries(path) {
		if side || !ojson.Truthy(ojson.Field(r, "isSidechain")) {
			recs = append(recs, r)
		}
	}
	return recs
}

func when(r any) *float64 {
	if v, ok := transcript.When(r); ok {
		return &v
	}
	return nil
}

func or0(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func num(v any) int64 {
	f, _ := py.Number(v)
	return int64(f)
}

func getNum(o any, k string) int64 {
	v, ok := ojson.Obj(o).Get(k)
	if !ok {
		return 0
	}
	return num(v)
}

func getStr(o any, k, dflt string) string {
	v, ok := ojson.Obj(o).Get(k)
	if !ok {
		return dflt
	}
	return py.Str(v)
}

// itemsFor is (items, prompts, turn index, meta) for the context of one turn's last call.
func itemsFor(path string, at *float64, turn int, side bool) ([]*Item, []promptPick, int, *Meta) {
	recs := readRecs(path, side)
	prompts := promptPositions(recs)
	if len(prompts) == 0 {
		return nil, nil, 1, &Meta{}
	}
	var k int
	if at != nil {
		best := -1.0
		for j, i := range prompts {
			d := or0(when(recs[i])) - *at
			if d < 0 {
				d = -d
			}
			if best < 0 || d < best {
				best, k = d, j
			}
		}
	} else {
		t := turn
		if t == 0 {
			t = 1
		}
		k = max(0, min(t-1, len(prompts)-1))
	}
	end := len(recs)
	if k+1 < len(prompts) {
		end = prompts[k+1]
	}
	turnOf := map[int]int{}
	for n, i := range prompts {
		turnOf[i] = n + 1
	}
	start := 0
	kept := map[string]bool{}
	for i := end - 1; i >= 0; i-- {
		if s, _ := ojson.Str(ojson.Field(recs[i], "subtype")); s == "compact_boundary" {
			start = i + 1
			ids, _ := ojson.GetOr(ojson.GetOr(ojson.GetOr(recs[i], "compactMetadata"), "preservedMessages"), "allUuids").([]any)
			for _, u := range ids {
				kept[py.Repr(u)] = true
			}
			break
		}
	}
	compacts := 0
	for _, r := range recs[:end] {
		if s, _ := ojson.Str(ojson.Field(r, "subtype")); s == "compact_boundary" {
			compacts++
		}
	}
	var real *Real
	seen := map[string]bool{}
	toolIDs := map[string]bool{}
	for _, r := range recs[prompts[k]:end] {
		msg := msgOf(r)
		for _, p := range transcript.Parts(msg) {
			if ojson.Obj(p) != nil && typ(p) == "tool_use" {
				toolIDs[idStr(ojson.Field(p, "id"))] = true
			}
		}
		u := ojson.Field(msg, "usage")
		mid := py.Repr(ojson.Field(msg, "id"))
		if typ(r) == "assistant" && ojson.Truthy(u) && !seen[mid] {
			seen[mid] = true
			real = &Real{In: getNum(u, "input_tokens"), Cr: getNum(u, "cache_read_input_tokens"),
				Cw: getNum(u, "cache_creation_input_tokens"), Model: getStr(msg, "model", "")}
		}
	}
	var snap any
	for i := end - 1; i >= 0; i-- {
		if a := attachmentOf(recs[i]); a != nil && typ(a) == "prompt_snapshot" {
			snap = a
			break
		}
	}
	cwd := ""
	for i := end - 1; i >= 0; i-- {
		if v := ojson.GetOr(recs[i], "cwd"); v != nil {
			cwd = py.Str(v)
			break
		}
	}

	var items []*Item
	sysParts, _ := ojson.GetOr(snap, "systemPrompt").([]any)
	for n, t := range sysParts {
		s, _ := t.(string)
		items = append(items, &Item{Cat: "system", Pos: -1, Src: "系統提示第 " + itoa(n+1) + " 段", Text: s})
	}
	toolName := map[string]string{}
	launchDesc := map[string]string{}
	agentOf := map[string]any{}
	cur := 0
	h := home()
	for i, r := range recs[:end] {
		if n, ok := turnOf[i]; ok {
			cur = n
		}
		if ojson.Truthy(ojson.Field(r, "queueTranscriptOnly")) {
			continue // in the transcript, never sent to the model
		}
		if i < start && !kept[py.Repr(ojson.Field(r, "uuid"))] {
			continue
		}
		msg := msgOf(r)
		add := func(cat, src, text string, img int) *Item {
			it := &Item{Cat: cat, Turn: cur, HasTurn: true, Pos: i, Src: Redact(src), Text: text, NImg: img}
			items = append(items, it)
			return it
		}
		if a := attachmentOf(r); a != nil {
			if q, ok := queuedPrompt(r); ok {
				add("prompt", "你中途插的話", q, 0)
				continue
			}
			at := typ(a)
			if at == "instructions" {
				files, _ := ojson.GetOr(a, "files").([]any)
				for _, f := range files {
					add("claudemd", strings.ReplaceAll(getStr(f, "path", ""), h, "~"), getStr(f, "content", ""), 0)
				}
				continue
			}
			if at == "invoked_skills" {
				skills, _ := ojson.GetOr(a, "skills").([]any)
				for _, s := range skills {
					add("skill", getStr(s, "name", "?")+"（壓縮後重新注入）", getStr(s, "content", ""), 0)
				}
				continue
			}
			var texts []string
			rendered, _ := ojson.GetOr(r, "rendered").([]any)
			for _, x := range rendered {
				if ojson.Obj(x) != nil {
					texts = append(texts, getStr(x, "content", ""))
				}
			}
			text := strings.Join(texts, "\n")
			if text != "" {
				src, ok := attachZH[at]
				if !ok {
					src = at
				}
				hook := strings.HasPrefix(at, "hook") || at == "async_hook_response"
				if hook {
					ev := ojson.GetOr(a, "hookEvent")
					if ev == nil {
						name := ""
						if v := ojson.GetOr(a, "hookName"); v != nil {
							name = py.Str(v)
						}
						ev = strings.SplitN(name, ":", 2)[0]
					}
					src = py.Str(ev) + " hook"
				} else if fn := ojson.GetOr(a, "filename"); fn != nil {
					src = at + "：" + strings.ReplaceAll(py.Str(fn), h, "~")
				}
				cat := "hook"
				if !hook {
					if c, ok := attachCat[at]; ok {
						cat = c
					} else {
						cat = "reminder"
					}
				}
				add(cat, src, text, 0)
			}
			continue
		}
		if typ(r) == "assistant" {
			for _, p := range transcript.Parts(msg) {
				if ojson.Obj(p) == nil {
					continue
				}
				switch typ(p) {
				case "text":
					add("assistant", "回覆", getStr(p, "text", ""), 0)
				case "thinking":
					add("thinking", "thinking", getStr(p, "thinking", ""), 0)
				case "tool_use":
					name := getStr(p, "name", "?")
					tool := name
					inpRaw := ojson.Field(p, "input")
					inp := ojson.GetOr(p, "input")
					if inp == nil {
						inp = ojson.NewObject()
					}
					if name == "Bash" {
						cmd, _ := ojson.GetOr(inp, "command").(string)
						name = bashKind(cmd)
					}
					id := py.Repr(ojson.Field(p, "id"))
					toolName[id] = name
					if name == "Agent" || name == "Task" {
						d, _ := ojson.GetOr(inp, "description").(string)
						launchDesc[id] = d
					}
					it := add("tool_use", name, py.Dumps(inpRaw), 0)
					it.HasTid, it.Tid = true, idStr(ojson.Field(p, "id"))
					it.Inp, it.Ts, it.Tool = inp, when(r), tool
				}
			}
			continue
		}
		if typ(r) != "user" {
			continue
		}
		c := ojson.Field(msg, "content")
		hb := subagentMessage(r)
		switch {
		case hb != nil && !ojson.Truthy(ojson.Field(r, "isCompactSummary")):
			tu := hb.toolUse
			if !ojson.Truthy(tu) {
				tu = agentOf[py.Repr(hb.agent)]
			}
			src := launchDesc[py.Repr(tu)]
			if src == "" {
				if ojson.Truthy(hb.agent) {
					src = py.Str(hb.agent)
				} else {
					src = "?"
				}
			}
			it := add("subagent", src, hb.text, 0)
			it.HasTid, it.Tid = true, idStr(tu)
			it.HasErr, it.Agent, it.Kind = true, hb.agent, hb.kind
		case ojson.Truthy(ojson.Field(r, "isCompactSummary")):
			add("summary", "壓縮後的摘要", textOf(c), 0)
		case turnOf[i] != 0:
			add("prompt", "你打的字", textOf(c), 0)
		case ojson.Truthy(ojson.Field(r, "isMeta")) && strings.Contains(textOf(c), skillMark):
			rest := strings.SplitN(textOf(c), skillMark, 2)[1]
			words := py.Split(rest)
			name := ""
			if len(words) > 0 {
				name = strings.TrimRight(words[0], "/")
			}
			add("skill", filepath.Base(name), textOf(c), 0)
		case hasToolResult(c):
			for _, x := range c.([]any) {
				if ojson.Obj(x) == nil {
					continue
				}
				switch typ(x) {
				case "tool_result":
					tid := ojson.Field(x, "tool_use_id")
					name, ok := toolName[py.Repr(tid)]
					if !ok {
						name = "?"
					}
					body := ojson.Field(x, "content")
					imgs := 0
					if list, ok := body.([]any); ok {
						for _, y := range list {
							if ojson.Obj(y) != nil && typ(y) == "image" {
								imgs++
							}
						}
					}
					text, isStr := body.(string)
					if !isStr {
						text = textOf(body)
					}
					agentTool := name == "Agent" || name == "Task"
					isLaunch := agentTool && strings.HasPrefix(text, launched)
					if isLaunch {
						if m := agentIDRe.Search(text); m != nil {
							agentOf[py.Repr(m.Group(1))] = tid
						}
					}
					isErr := ojson.Truthy(ojson.Field(x, "is_error"))
					if isErr {
						if m := blockedRe.MatchStart(text); m != nil {
							it := add("hook", m.Group(1)+" hook（擋下 "+name+"）", text, imgs)
							it.HasTid, it.Tid, it.HasErr, it.Err = true, idStr(tid), true, true
							continue
						}
					}
					cat, src := "tool_result", name
					if agentTool && !isLaunch {
						cat = "subagent"
						if d := launchDesc[py.Repr(tid)]; d != "" {
							src = d
						}
					}
					it := add(cat, src, text, imgs)
					it.HasTid, it.Tid, it.HasErr, it.Err = true, idStr(tid), true, isErr
				case "text":
					if t := ojson.GetOr(x, "text"); t != nil {
						add("other_user", "夾在工具結果裡的文字", py.Str(t), 0)
					}
				}
			}
		case ojson.Truthy(ojson.Field(r, "isMeta")) && stopHookRe.MatchStart(py.LStrip(textOf(c))) != nil:
			add("hook", py.Split(py.LStrip(textOf(c)))[0]+" hook", textOf(c), 0)
		case py.Strip(textOf(c)) != "":
			add("other_user", otherKind(py.LStrip(textOf(c))), textOf(c), 0)
		}
	}
	startTurn := k + 1
	for _, i := range prompts {
		if i >= start {
			startTurn = turnOf[i]
			break
		}
	}
	meta := &Meta{Real: real, StartTurn: startTurn, Compacts: compacts, Cwd: cwd, ToolIDs: toolIDs,
		Start: start, End: end, PromptsPos: prompts, Span: [2]int{prompts[k], end}, At: when(recs[prompts[k]]), ok: true}
	var picks []promptPick
	for n, i := range prompts {
		meta.PromptAt = append(meta.PromptAt, when(recs[i]))
		picks = append(picks, promptPick{n + 1, py.Head(promptText(recs[i]), 50)})
	}
	return items, picks, k + 1, meta
}

func hasToolResult(c any) bool {
	list, ok := c.([]any)
	if !ok {
		return false
	}
	for _, x := range list {
		if ojson.Obj(x) != nil && typ(x) == "tool_result" {
			return true
		}
	}
	return false
}

// idStr is a tool_use id as a map key ("" for None).
func idStr(v any) string {
	if v == nil {
		return ""
	}
	return py.Str(v)
}

func itoa(n int) string { return strconv.Itoa(n) }

// otherKind is what a user-role message that is not a prompt, tool result or attachment is.
func otherKind(body string) string {
	cmd := commandRe.MatchStart(body)
	switch {
	case strings.HasPrefix(body, "<task-notification"):
		return "背景工作通知（背景指令或 subagent 跑完）"
	case strings.HasPrefix(body, "<bash-") || strings.HasPrefix(body, "<local-command"):
		return "指令輸出（! 指令、斜線指令的輸出）"
	case cmd != nil:
		return "斜線指令 /" + strings.TrimLeft(py.Strip(cmd.Group(1)), "/")
	case strings.HasPrefix(body, "[Image"):
		return "貼上的圖片說明（路徑與尺寸）"
	case strings.HasPrefix(body, "<system-reminder"):
		return "系統提醒"
	case strings.Contains(py.Head(body, 120), "hook is now active"):
		return "hook 啟用時注入的說明"
	}
	return "其他訊息：" + whitespaceRe.Sub(py.Head(body, 24), " ")
}

// OK reports whether the meta describes a turn (items_for returned a non-empty meta).
func (m *Meta) OK() bool { return m != nil && m.ok }

// ItemsForTest exposes itemsFor to the parity harness.
func ItemsForTest(path string, at *float64, turn int, side bool) ([]*Item, []promptPick, int, *Meta) {
	return itemsFor(path, at, turn, side)
}

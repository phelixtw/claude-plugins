// Package toollimits finds the Claude Code tool limits a turn ran into, from
// tool_result text (tool_limits.py). Keys in follow only count once a later
// call in the same turn goes back for the cut-off part.
package toollimits

import (
	"iter"
	"path/filepath"
	"strings"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
)

// Order is LIMITS' key order.
var Order = []string{"read", "bash_out", "bash_timeout", "edit", "glob", "fetch", "search"}

// Limits: key -> (label, how).
var Limits = map[string][2]string{
	"read":         {"讀檔過大", "檔案超過 Read 上限（25,000 token、256 KB，PDF 一次 20 頁），只好分段再讀；拆小檔或指定範圍、頁碼"},
	"bash_out":     {"輸出截斷", "指令輸出超過約 30,000 字元被存成檔，又回頭讀那個檔；指令先接 | tail、grep 只留需要的行"},
	"bash_timeout": {"輪詢等待", "指令超過 Bash 時限被移到背景，之後用 sleep 或 Monitor 反覆查進度；長指令一開始就背景執行、等完成通知"},
	"edit":         {"改檔重來", "Edit 失敗（沒先讀、old_string 不唯一或對不上），重讀或重改同一個檔；給檔案路徑和行號"},
	"glob":         {"搜尋過寬", "Glob 一次最多回 100 個檔案，超過被截掉；限定目錄和副檔名"},
	"fetch":        {"網頁重抓", "WebFetch 遇到跨網域轉址或內容被截斷，又抓了一次；直接給最終網址、說明要看哪一段"},
	"search":       {"多搜一趟", "網址在提示詞裡已經有了，還先 WebSearch 再 WebFetch；已知網址就直接抓"},
}

var (
	follow      = map[string]bool{"bash_out": true, "bash_timeout": true, "edit": true, "fetch": true}
	saved       = pyre.Compile(`Full output saved to: (\S+)`)
	URL         = pyre.Compile(`https?://[^\s"'<>)\]]+`)
	editErr     = pyre.Compile(`File has not been read yet|String to replace not found|matches of the string to replace`)
	readCap     = pyre.Compile(`PARTIAL view|exceeds maximum allowed (?:tokens|size)`)
	timeoutRe   = pyre.Compile(`\s*Command did not complete within its \d+s timeout`)
	fetchNote   = pyre.Compile(`WebFetch note:[^\]]*the final [\d,]+ were not read`)
	redirectURL = pyre.Compile(`Redirect URL[^\n:]*: (\S+)`)
	sleepOrOut  = pyre.Compile(`\bsleep\s+\d|\.output\b`)
)

// Event is one scan() input: ("prompt", text) | ("use", id, name, inp) | ("result", id, text).
type Event struct {
	Kind, ID, Name, Text string
	Inp                  any
}

func str(v any) string { s, _ := ojson.Str(v); return s }

// Detect is the limit keys one tool_result hit (usually zero or one).
func Detect(name string, inp any, text string) []string {
	switch name {
	case "Read":
		if readCap.Has(text) {
			return []string{"read"}
		}
	case "Bash":
		if strings.HasPrefix(text, "<persisted-output>") {
			return []string{"bash_out"}
		}
		// anchored: grep output can quote the docs sentence verbatim
		if timeoutRe.MatchStart(text) != nil {
			return []string{"bash_timeout"}
		}
	case "Edit", "Write", "MultiEdit":
		if strings.HasPrefix(py.LStrip(text), "<tool_use_error>") && editErr.Has(text) {
			return []string{"edit"}
		}
	case "Glob":
		if strings.Contains(strings.ToLower(text), "truncated") {
			return []string{"glob"}
		}
	case "WebFetch":
		if strings.HasPrefix(text, "REDIRECT DETECTED") {
			return []string{"fetch"}
		}
		if fetchNote.Has(text) {
			return []string{"fetch"}
		}
	}
	return nil
}

func inpObj(inp any) any {
	if ojson.Truthy(inp) {
		return inp
	}
	return ojson.NewObject()
}

// refOf is what a later call has to touch for a follow hit to count (nil = None).
func refOf(key string, inp any, text string) any {
	switch key {
	case "bash_out":
		if m := saved.Search(text); m != nil {
			return filepath.Base(m.Group(1))
		}
	case "edit":
		return ojson.Field(inp, "file_path")
	case "fetch":
		if m := redirectURL.Search(text); m != nil {
			return m.Group(1)
		}
		return ojson.Field(inp, "url")
	}
	return nil
}

// getDefault is inp.get(key, dflt).
func getDefault(inp any, key string, dflt any) any {
	if v, ok := ojson.Obj(inp).Get(key); ok {
		return v
	}
	return dflt
}

// followed: did this tool call go back for what the limit cut off?
func followed(key string, ref any, name string, inp any) bool {
	switch key {
	case "bash_out":
		r, _ := ref.(string)
		return r != "" && strings.Contains(py.DumpsASCII(inp), r)
	case "bash_timeout":
		switch name {
		case "Monitor", "TaskStop", "TaskOutput", "BashOutput":
			return true
		case "Read":
			return strings.HasSuffix(py.Str(getDefault(inp, "file_path", "")), ".output")
		}
		cmd, _ := getDefault(inp, "command", "").(string)
		return name == "Bash" && sleepOrOut.Has(cmd)
	case "edit":
		switch name {
		case "Read", "Edit", "Write", "MultiEdit":
			return py.Repr(ojson.Field(inp, "file_path")) == py.Repr(ref)
		}
		return false
	case "fetch":
		r, _ := ref.(string)
		return name == "WebFetch" && ojson.Truthy(ref) &&
			strings.TrimRight(py.Str(getDefault(inp, "url", "")), "/") == strings.TrimRight(r, "/")
	}
	return false
}

// Hit is one scan() row: (key, role "result"/"use", id, counted).
type Hit struct {
	Key, Role, ID string
	Counted       bool
}

type pend struct {
	key string
	ref any
	id  string
}

// Scan is the limit hits of one turn (or one subagent run), in transcript order.
func Scan(events []Event) []Hit {
	var out []Hit
	uses := map[string]Event{}
	var pending []pend
	capped := map[string]bool{}
	prompt, searched := "", false
	for _, ev := range events {
		switch ev.Kind {
		case "prompt":
			prompt += " " + ev.Text
		case "use":
			inp := inpObj(ev.Inp)
			uses[ev.ID] = Event{Name: ev.Name, Inp: inp}
			var keep []pend
			for _, p := range pending {
				if followed(p.key, p.ref, ev.Name, inp) {
					out = append(out, Hit{p.key, "result", p.id, true}, Hit{p.key, "use", ev.ID, false})
				} else {
					keep = append(keep, p)
				}
			}
			pending = keep
			if ev.Name == "Read" && capped[py.Repr(ojson.Field(inp, "file_path"))] && (ojson.Has(inp, "offset") || ojson.Has(inp, "limit")) {
				out = append(out, Hit{"read", "use", ev.ID, true})
			}
			if ev.Name == "WebSearch" {
				searched = true
			} else if ev.Name == "WebFetch" && searched {
				url, _ := getDefault(inp, "url", "").(string)
				if known(url, prompt) {
					out = append(out, Hit{"search", "use", ev.ID, true})
				}
			}
		default:
			u, ok := uses[ev.ID]
			name, inp := "?", any(ojson.NewObject())
			if ok {
				name, inp = u.Name, u.Inp
			}
			for _, key := range Detect(name, inp, ev.Text) {
				if follow[key] {
					pending = append(pending, pend{key, refOf(key, inp, ev.Text), ev.ID})
				} else {
					out = append(out, Hit{key, "result", ev.ID, true})
					if key == "read" {
						capped[py.Repr(ojson.Field(inp, "file_path"))] = true
					}
				}
			}
		}
	}
	return out
}

func bare(u string) string {
	u, _, _ = strings.Cut(u, "?")
	u, _, _ = strings.Cut(u, "#")
	return strings.TrimRight(u, "/")
}

// known: the url, or one with the same host and path, is already in the prompt.
func known(url, prompt string) bool {
	b := bare(url)
	if b == "" {
		return false
	}
	for _, u := range URL.FindAll(prompt) {
		if bare(u) == b {
			return true
		}
	}
	return false
}

// EventsOf is events_of(): scan() events from raw transcript records.
func EventsOf(records iter.Seq[any]) []Event {
	var out []Event
	for r := range records {
		msg := ojson.GetOr(r, "message")
		c := ojson.Field(msg, "content")
		typ := str(ojson.Field(r, "type"))
		if s, ok := c.(string); ok {
			if typ == "user" && !ojson.Truthy(ojson.Field(r, "isMeta")) {
				out = append(out, Event{Kind: "prompt", Text: s})
			}
			continue
		}
		parts, _ := c.([]any)
		for _, p := range parts {
			if ojson.Obj(p) == nil {
				continue
			}
			switch str(ojson.Field(p, "type")) {
			case "tool_use":
				name := "?"
				if v, ok := ojson.Obj(p).Get("name"); ok {
					name = py.Str(v)
				}
				out = append(out, Event{Kind: "use", ID: str(ojson.Field(p, "id")), Name: name, Inp: ojson.Field(p, "input")})
			case "tool_result":
				body := ojson.Field(p, "content")
				text, isStr := body.(string)
				if !isStr {
					list, _ := body.([]any)
					var ts []string
					for _, x := range list {
						if ojson.Obj(x) != nil {
							t := ""
							if v, ok := ojson.Obj(x).Get("text"); ok {
								t, _ = v.(string)
							}
							ts = append(ts, t)
						}
					}
					text = strings.Join(ts, " ")
				}
				out = append(out, Event{Kind: "result", ID: str(ojson.Field(p, "tool_use_id")), Text: text})
			case "text":
				if typ == "user" {
					t := ""
					if v, ok := ojson.Obj(p).Get("text"); ok {
						t, _ = v.(string)
					}
					out = append(out, Event{Kind: "prompt", Text: t})
				}
			}
		}
	}
	return out
}

// Package repetition measures the 重複呼叫 (repeat) rate per session, free and
// mechanical (repetition.py): a turn is flagged when, against the previous 3
// user turns, it re-runs a Bash command, re-calls a Skill/MCP tool with the
// same arguments, re-reads an unchanged file / URL / search, or writes content
// with difflib ratio >= 0.8 to something written before.
package repetition

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"agentperform/internal/common"
	"agentperform/internal/difflib"
	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
	"agentperform/internal/scrub"
	"agentperform/internal/transcript"
)

const (
	Loud      = 0.15
	window    = 3
	simMin    = 0.8
	simLenMin = 40
	simChars  = 2000
)

var ReasonLabel = map[string]string{
	"1": "同一指令又跑一次",
	"2": "同一個 skill/MCP/工具又叫一次",
	"3": "重讀沒改過的檔案、同一個網址或搜尋",
	"4": "內容跟之前寫的高度相似",
}

var (
	filey        = []string{"Read", "Edit", "Write", "MultiEdit", "NotebookEdit"}
	contentTools = []string{"Edit", "Write", "MultiEdit", "NotebookEdit"}
	harnessProbe = pyre.Compile(`python3 -c|node -e`)
)

func in(s string, list []string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func Out() string { return filepath.Join(common.Home(), ".claude", "agent_health.json") }

// Tail keeps a long path's end, where the file name is.
func Tail(s string, n int) string {
	if py.Len(s) <= n {
		return s
	}
	return "…" + py.Tail(s, n-1)
}

func normCmd(cmd string) string { return py.Head(py.Strip(py.CollapseSpace(cmd)), 400) }

func isHarnessProbe(cmd string) bool {
	return harnessProbe.Has(cmd) && strings.Contains(cmd, "session_id") &&
		strings.Contains(cmd, "transcript_path") && strings.Contains(cmd, "context_window")
}

func or(vals ...any) any {
	for _, v := range vals {
		if ojson.Truthy(v) {
			return v
		}
	}
	return nil
}

func strOr(v any) string {
	if v == nil {
		return ""
	}
	return py.Str(v)
}

func filePath(name string, inp any) string {
	if name == "NotebookEdit" {
		return strOr(or(ojson.Field(inp, "notebook_path"), ojson.Field(inp, "file_path")))
	}
	return strOr(or(ojson.Field(inp, "file_path")))
}

func getStr(inp any, k string) string {
	v, ok := ojson.Obj(inp).Get(k)
	if !ok {
		return ""
	}
	return py.Str(v)
}

func globGrepKey(inp any) string { return getStr(inp, "pattern") + "|" + getStr(inp, "path") }

func skillKey(inp any) any { return or(ojson.Field(inp, "skill"), ojson.Field(inp, "command")) }

func mcpKey(inp any) string { return py.Head(py.DumpsSorted(inp), 500) }

func contents(name string, inp any) []string {
	var raw []any
	switch name {
	case "Edit":
		raw = []any{or(ojson.Field(inp, "new_string"))}
	case "Write":
		raw = []any{or(ojson.Field(inp, "content"))}
	case "MultiEdit":
		edits, _ := or(ojson.Field(inp, "edits")).([]any)
		for _, e := range edits {
			raw = append(raw, or(ojson.Field(e, "new_string")))
		}
	case "NotebookEdit":
		raw = []any{or(ojson.Field(inp, "new_source"))}
	}
	var out []string
	for _, v := range raw {
		s, _ := v.(string)
		if py.Len(s) >= simLenMin {
			out = append(out, py.Head(s, simChars))
		}
	}
	return out
}

// Label is a short human label for a call, for the per-reason detail.
func Label(name string, inp any) (string, string) {
	switch {
	case name == "Bash":
		return "Bash", py.Head(normCmd(strOr(or(ojson.Field(inp, "command")))), 80)
	case strings.HasPrefix(name, "mcp__"):
		parts := strings.Split(name, "__")
		tool := name
		if len(parts) > 1 {
			tool = "mcp:" + parts[1]
		}
		return tool, py.Head(py.Str(inp), 80)
	case in(name, filey):
		return name, Tail(filePath(name, inp), 80)
	case name == "WebFetch":
		return name, py.Head(strOr(or(ojson.Field(inp, "url"))), 80)
	case name == "Glob" || name == "Grep":
		return name, py.Head(globGrepKey(inp), 80)
	case name == "Skill":
		return name, py.Head(strOr(skillKey(inp)), 80)
	}
	return name, py.Head(py.DumpsSorted(inp), 80)
}

func getStrDefault(o any, k string) string {
	v, ok := ojson.Obj(o).Get(k)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// Call is one tool call attributed to a turn.
type Call struct {
	Turn int
	Name string
	Inp  any
}

// Scan is (calls, total_turns) for one transcript.
func Scan(path string) ([]Call, int) {
	var calls []Call
	turn := 0
	for line := range transcript.RawLines(path) {
		e, err := ojson.Parse([]byte(common.Lossy([]byte(line))))
		if err != nil || ojson.Obj(e) == nil {
			continue
		}
		if ojson.Truthy(ojson.Field(e, "isSidechain")) {
			continue
		}
		if transcript.IsPrompt(e) {
			turn++
			continue
		}
		if transcript.Type(e) != "assistant" {
			continue
		}
		for _, part := range transcript.Parts(transcript.Msg(e)) {
			if ojson.Obj(part) == nil || transcript.Type(part) != "tool_use" {
				continue
			}
			name := "?"
			if v, ok := ojson.Obj(part).Get("name"); ok {
				name = py.Str(v)
			}
			if turn == 0 {
				continue // tool calls before the first real prompt
			}
			var inp any = ojson.NewObject()
			if v := ojson.GetOr(part, "input"); v != nil {
				inp = v
			}
			calls = append(calls, Call{turn, name, inp})
		}
	}
	return calls, turn
}

type nc struct {
	name string
	inp  any
}

func reread(path string, win, before []nc) bool {
	last := ""
	for _, w := range append(append([]nc{}, win...), before...) {
		if in(w.name, filey) && filePath(w.name, w.inp) == path {
			last = w.name
		}
	}
	if last != "Read" {
		return false
	}
	for _, w := range win {
		if w.name == "Read" && filePath(w.name, w.inp) == path {
			return true
		}
	}
	return false
}

type reason struct{ code, detail string }

func reasons(name string, inp any, win, before []nc) []reason {
	var out []reason
	add := func(code, detail string) { out = append(out, reason{code, detail}) }
	switch {
	case name == "Bash":
		cmd := normCmd(strOr(or(ojson.Field(inp, "command"))))
		if cmd != "" && !isHarnessProbe(cmd) {
			for _, w := range win {
				if w.name == "Bash" && normCmd(strOr(or(ojson.Field(w.inp, "command")))) == cmd {
					add("1", py.Head(cmd, 80))
					break
				}
			}
		}
	case name == "Skill":
		key := skillKey(inp)
		if key != nil {
			for _, w := range win {
				if w.name == "Skill" && sameValue(skillKey(w.inp), key) {
					add("2", py.Head("Skill:"+py.Str(key), 80))
					break
				}
			}
		}
	case strings.HasPrefix(name, "mcp__") && !strings.Contains(name, "agent-perform"):
		key := mcpKey(inp)
		for _, w := range win {
			if w.name == name && mcpKey(w.inp) == key {
				tool, args := Label(name, inp)
				d := tool
				if args != "" && args != "{}" {
					d = tool + " " + args
				}
				add("2", py.Head(d, 80))
				break
			}
		}
	}
	switch {
	case name == "Read":
		if p := filePath(name, inp); p != "" && reread(p, win, before) {
			add("3", Tail(p, 80))
		}
	case name == "WebFetch":
		if u := strOr(or(ojson.Field(inp, "url"))); u != "" {
			for _, w := range win {
				if w.name == "WebFetch" && strOr(or(ojson.Field(w.inp, "url"))) == u {
					add("3", Tail(u, 80))
					break
				}
			}
		}
	case name == "Glob" || name == "Grep":
		key := globGrepKey(inp)
		for _, w := range win {
			if w.name == name && globGrepKey(w.inp) == key {
				add("3", py.Head(key, 80))
				break
			}
		}
	}
	if in(name, contentTools) {
		if mine := contents(name, inp); len(mine) > 0 {
			best := 0.0
			for _, w := range win {
				if !in(w.name, contentTools) {
					continue
				}
				for _, wc := range contents(w.name, w.inp) {
					for _, c := range mine {
						if r := difflib.Ratio(c, wc); r > best {
							best = r
						}
					}
				}
			}
			if best >= simMin {
				add("4", fmt.Sprintf("相似度 %.2f", best))
			}
		}
	}
	return out
}

func sameValue(a, b any) bool { return py.Repr(a) == py.Repr(b) }

// Measure is flagged turns / total turns, with the reasons per flagged turn.
func Measure(calls []Call, total int) *ojson.Object {
	byTurn := map[int][]nc{}
	for _, c := range calls {
		byTurn[c.Turn] = append(byTurn[c.Turn], nc{c.Name, c.Inp})
	}
	flagged := []any{}
	rs := ojson.NewObject()
	for t := 1; t <= total; t++ {
		this := byTurn[t]
		if len(this) == 0 {
			continue
		}
		var win []nc
		for wt := max(1, t-window); wt < t; wt++ {
			win = append(win, byTurn[wt]...)
		}
		if len(win) == 0 {
			continue
		}
		agg := ojson.NewObject()
		for i, c := range this {
			for _, r := range reasons(c.name, c.inp, win, this[:i]) {
				if !ojson.Has(agg, r.code) {
					agg.Set(r.code, r.detail)
				}
			}
		}
		if agg.Len() > 0 {
			flagged = append(flagged, int64(t))
			rs.Set(strconv.Itoa(t), agg)
		}
	}
	share := 0.0
	if total > 0 {
		share = py.Round(float64(len(flagged))/float64(total), 4)
	}
	return ojson.NewObject().Set("turns", int64(total)).Set("calls", int64(len(calls))).Set("flagged", flagged).
		Set("share", share).Set("reasons", rs)
}

// walk is os.walk(root) top-down in directory order: fn(dir, files).
func walk(root string, fn func(dir string, files []string)) {
	fh, err := os.Open(root)
	if err != nil {
		return
	}
	names, _ := fh.Readdirnames(-1)
	fh.Close()
	var dirs, files []string
	for _, n := range names {
		p := filepath.Join(root, n)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			dirs = append(dirs, n)
		} else {
			files = append(files, n)
		}
	}
	fn(root, files)
	for _, d := range dirs {
		p := filepath.Join(root, d)
		if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink == 0 {
			walk(p, fn)
		}
	}
}

// Main rebuilds agent_health.json from every session.
func Main() {
	root := filepath.Join(common.Home(), ".claude", "projects")
	out := ojson.NewObject()
	walk(root, func(dir string, files []string) {
		for _, part := range strings.Split(dir, string(os.PathSeparator)) {
			if part == "subagents" {
				return
			}
		}
		for _, name := range files {
			if !strings.HasSuffix(name, ".jsonl") {
				continue
			}
			calls, total := Scan(filepath.Join(dir, name))
			if total == 0 {
				continue
			}
			out.Set(py.Head(name, 8), Measure(calls, total))
		}
	})
	var turns, flagged int64
	for _, k := range out.Keys {
		t, _ := py.Number(ojson.Field(out.Vals[k], "turns"))
		turns += int64(t)
		f, _ := ojson.Field(out.Vals[k], "flagged").([]any)
		flagged += int64(len(f))
	}
	share := 0.0
	if turns > 0 {
		share = py.Round(float64(flagged)/float64(turns), 4)
	}
	blob := ojson.NewObject().Set("sessions", out).Set("loud", Loud).
		Set("totals", ojson.NewObject().Set("turns", turns).Set("flagged", flagged).Set("share", share))
	os.WriteFile(Out(), []byte(py.Dumps(scrub.Health(blob))), 0o644)
}

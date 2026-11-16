// Package touched pulls out what a session touched: files written, files
// read, URLs hit, from Write/Edit/Read tool calls, Bash write/read patterns
// and URLs (touched.py). No model, no tokens.
package touched

import (
	"os"
	"strings"

	"agentperform/internal/common"
	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
	"agentperform/internal/transcript"
)

var (
	writeCmd = []*pyre.Re{
		// greedy on purpose: `sed -i '' 's/a/b/' path` puts the path last
		pyre.Compile(`\bsed\s+-i\b[^|;&]*\s(\S+)`),
		pyre.Compile(`\btee\s+(?:-a\s+)?(\S+)`),
		pyre.Compile(`>>?\s*([\w./~$-][\w./~$-]*\.\w+)`),
		pyre.Compile(`\bopen\(\s*['"]([^'"]+)['"]\s*,\s*['"][wa]`),
		pyre.Compile(`\b(?:cp|mv)\s+\S+\s+(\S+\.\w+)`),
		pyre.Compile(`\btouch\s+(\S+)`),
	}
	readCmd = []*pyre.Re{
		pyre.Compile(`\b(?:cat|head|tail|less|wc)\s+(?:-\w+\s+)*(\S+\.\w+)`),
		pyre.Compile(`\bgrep\b[^|;]*?\s(\S+\.\w+)\s*$`),
	}
	// a path with an unexpanded shell variable or a glob is not a path
	// (the last branch is a literal backslash then d's, as in the Python source)
	noise   = pyre.Compile(`/dev/null|[$*{}]|^-|^/proc/|^\\d+$`)
	testCmd = pyre.Compile(`\bpytest\b|\bflutter\s+test\b|\bgo\s+test\b|\bnpm\s+(?:run\s+)?test\b`+
		`|\byarn\s+test\b|\bjest\b|\bvitest\b|\bdart\s+test\b`, pyre.I)
	knownExt = pyre.Compile(`\.(kt|kts|java|dart|swift|m|mm|h|c|cc|cpp|cs|go|rb|rs|php|ts|tsx|js|jsx`+
		`|vue|py|sh|gradle|xml|proto|sql|css|scss|less|tf|md|txt|csv|tsv|xlsx?|pdf`+
		`|html?|json|jsonl|ya?ml|ipynb|png|jpe?g|svg|webp|gif|ttf|otf|plist|log`+
		`|bak|lock|toml|ini|cfg|env)$`, pyre.I)
	urlRe = pyre.Compile(`https?://[^\s'"\\)]+`)
)

// Hit is one hit record: counters plus test runs and tokens.
type Hit struct {
	Writes, Reads, URLs, Tools *py.Counter
	Tokens                     float64
	Tests                      int
}

func Blank() *Hit {
	return &Hit{Writes: py.NewCounter(), Reads: py.NewCounter(), URLs: py.NewCounter(), Tools: py.NewCounter()}
}

func basename(p string) string { return p[strings.LastIndexByte(p, '/')+1:] }

// Dirname is posixpath.dirname.
func Dirname(p string) string {
	i := strings.LastIndexByte(p, '/') + 1
	head := p[:i]
	if head != "" && strings.Trim(head, "/") != "" {
		head = strings.TrimRight(head, "/")
	}
	return head
}

// Clean is touched._clean(): "" for noise, home collapsed to ~.
func Clean(path string) string {
	p := strings.Trim(py.Strip(path), `'"`)
	if p == "" || noise.Has(p) || py.Len(p) > 300 {
		return ""
	}
	if !strings.Contains(p, "/") && !knownExt.Has(p) {
		return ""
	}
	if strings.Contains(p, "/") && !strings.Contains(basename(p), ".") && !strings.HasSuffix(p, "/") {
		return "" // a bare directory is not a touched file
	}
	return strings.ReplaceAll(p, common.Home(), "~")
}

// at places a relative path using the cwd the entry recorded.
func at(path, cwd string) string {
	p := Clean(path)
	if p == "" || cwd == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
		return p
	}
	full := cwd
	if !strings.HasSuffix(full, "/") {
		full += "/"
	}
	full = strings.ReplaceAll(full+p, common.Home(), "~")
	if d := Dirname(full); d == "~" || d == common.Home() {
		return p
	}
	return full
}

func str(v any) string {
	s, _ := ojson.Str(v)
	return s
}

// Take folds one tool_use block into a hit record.
func Take(part any, hit *Hit, cwd string) {
	name := ""
	if v := ojson.GetOr(part, "name"); v != nil {
		name = ojson.PyStr(v)
	}
	var got any = ojson.NewObject()
	if v := ojson.GetOr(part, "input"); v != nil {
		got = v
	}
	hit.Tools.Add(name, 1)
	switch name {
	case "Write", "Edit", "NotebookEdit":
		if p := Clean(str(ojson.Field(got, "file_path"))); p != "" {
			hit.Writes.Add(p, 1)
		}
	case "Read":
		if p := Clean(str(ojson.Field(got, "file_path"))); p != "" {
			hit.Reads.Add(p, 1)
		}
	case "Bash":
		cmd := str(ojson.GetOr(got, "command"))
		if testCmd.Has(cmd) {
			hit.Tests++
		}
		for _, rx := range writeCmd {
			for _, g := range rx.FindAll(cmd) {
				if p := at(g, cwd); p != "" {
					hit.Writes.Add(p, 1)
				}
			}
		}
		for _, rx := range readCmd {
			for _, g := range rx.FindAll(cmd) {
				if p := at(g, cwd); p != "" {
					hit.Reads.Add(p, 1)
				}
			}
		}
		for _, u := range urlRe.FindAll(cmd) {
			hit.URLs.Add(u, 1)
		}
	default:
		for _, k := range []string{"url", "query"} {
			if v, ok := ojson.Field(got, k).(string); ok && py.Strip(v) != "" {
				hit.URLs.Add(py.Head(py.Strip(v), 200), 1)
			}
		}
	}
}

// TakeResult counts the files a Bash command changed, as the harness recorded them.
func TakeResult(entry any, hit *Hit) {
	res := ojson.Obj(ojson.Field(entry, "toolUseResult"))
	diff := ojson.Obj(ojson.Field(res, "bashEditDiff"))
	if res == nil || diff == nil {
		return
	}
	paths, _ := ojson.GetOr(diff, "changedFiles").([]any)
	if paths == nil {
		files, _ := ojson.GetOr(diff, "files").([]any)
		for _, f := range files {
			if ojson.Obj(f) != nil {
				paths = append(paths, ojson.Field(f, "filePath"))
			}
		}
	}
	for _, raw := range paths {
		if p := Clean(str(raw)); p != "" {
			hit.Writes.Add(p, 1)
		}
	}
}

// FoldSubagent pulls a delegated agent's writes back into the turn that launched it.
func FoldSubagent(entry any, turn *Hit) {
	res := ojson.Obj(ojson.Field(entry, "toolUseResult"))
	out := str(ojson.GetOr(res, "outputFile"))
	if res == nil || out == "" {
		return
	}
	if _, err := os.Stat(out); err != nil {
		return
	}
	cwd := ""
	for sub := range transcript.Entries(out) {
		if v := ojson.GetOr(sub, "cwd"); v != nil {
			cwd = ojson.PyStr(v)
		}
		if transcript.Type(sub) != "assistant" {
			continue
		}
		for _, part := range transcript.Parts(transcript.Msg(sub)) {
			if ojson.Obj(part) != nil && transcript.Type(part) == "tool_use" {
				Take(part, turn, cwd)
			}
		}
	}
}

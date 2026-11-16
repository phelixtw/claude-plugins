package dash

import (
	"os"
	"sort"
	"strings"
	"unicode"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
	"agentperform/internal/transcript"
)

var (
	subcommandTools = map[string]bool{"git": true, "flutter": true, "dart": true, "fvm": true, "npm": true, "npx": true,
		"pnpm": true, "yarn": true, "pip": true, "pip3": true, "brew": true, "podman": true, "docker": true, "kubectl": true,
		"gh": true, "glab": true, "adb": true, "gcloud": true, "cargo": true, "go": true, "xcrun": true, "pod": true,
		"gradle": true, "./gradlew": true, "gradlew": true, "claude": true, "uv": true}
	hostRe      = pyre.Compile(`https?://([^/\s"']+)`)
	segSplitRe  = pyre.Compile(`&&|\|\||;|\n`)
	segSplitRe2 = pyre.Compile(`&&|\|\||;|\n|\|`)
	assignSubRe = pyre.Compile(`\b\w+=\$\(`)
	assignRe    = pyre.Compile(`^\w+=`)
	setupRe     = pyre.Compile(`^(cd|source|export|set|#)`)
	subcmdRe    = pyre.Compile(`^[a-z][\w:-]*$`)
	agentMsgRe  = pyre.Compile(`<agent-message from="([^"]+)">(.*?)</agent-message>`, pyre.S)
	taskIDRe    = pyre.Compile(`<task-id>([^<]+)</task-id>`)
	resultRe    = pyre.Compile(`<result>(.*?)</result>`, pyre.S)
	agentIDSplt = pyre.Compile(`\n+agentId:`)
	readers     = map[string]bool{"cat": true, "head": true, "tail": true, "sed": true, "less": true, "bat": true}

	scriptExt = `\.(?:py|mjs|cjs|js|ts|sh|rb|pl)`
	scratch   = pyre.Compile(`^(?:/private)?/tmp/|^/var/folders/`)
	inline    = pyre.Compile(`\b(python3?|node|ruby|perl|deno|bun)\b[^\n|;&]*?(?:\s-[ce]\s|\s-\s*<<|<<-?\s*['"]?\w+)`)
	writesRe  = pyre.Compile(`(?:cat\s*>|tee)\s*['"]?(\S+` + scriptExt + `)\b`)
	scriptEnd = pyre.Compile(scriptExt + `$`)
)

func basename(p string) string { return p[strings.LastIndexByte(p, '/')+1:] }

// bashKind is 'Bash · <what it ran>': the first real command after setup, or
// the host when it calls an HTTP API.
func bashKind(cmd string) string {
	if m := hostRe.Search(cmd); m != nil {
		return Redact("Bash · " + m.Group(1))
	}
	for _, seg := range segSplitRe.Split(cmd) {
		seg = assignSubRe.Sub(seg, "") // before=$(git ...) ran git
		var words []string
		for _, w := range py.Split(seg) {
			if assignRe.MatchStart(w) == nil {
				words = append(words, w)
			}
		}
		if len(words) == 0 || setupRe.MatchStart(words[0]) != nil {
			continue
		}
		if (words[0] == "fvm" || words[0] == "sudo" || words[0] == "time") && len(words) > 1 {
			words = words[1:] // fvm flutter test = flutter test
		}
		tool := basename(strings.Trim(words[0], `"(`))
		if tool == "" {
			tool = "?"
		}
		var nxt []string
		skip := false
		for _, w := range words[1:] {
			switch {
			case skip:
				skip = false
			case w == "-c" || w == "-C": // git -c key=val / -C dir take a value
				skip = true
			case !strings.HasPrefix(w, "-"):
				nxt = append(nxt, w)
			}
		}
		if subcommandTools[tool] && len(nxt) > 0 && subcmdRe.MatchStart(nxt[0]) != nil {
			tool += " " + nxt[0] // flutter build, git push
		} else if strings.HasPrefix(tool, "python") && len(words) > 1 {
			arg := words[1]
			if words[1] == "-m" && len(words) > 2 {
				arg = words[2]
			}
			if arg == "-" || strings.HasPrefix(arg, "<<") {
				tool += " heredoc"
			} else {
				tool += " " + basename(strings.Trim(arg, `'"`))
			}
		}
		return Redact("Bash · " + tool)
	}
	return "Bash"
}

// adhocScript is (language, code) when a tool call writes or runs a
// throwaway script, else ok=false. written is updated in place.
func adhocScript(name string, inp any, written *orderedSet) (string, string, bool) {
	switch name {
	case "Write", "Edit", "MultiEdit":
		path := py.Str(orNil(ojson.GetOr(inp, "file_path"), ""))
		if scriptEnd.Has(path) && scratch.Has(path) {
			written.add(path)
			code := ojson.GetOr(inp, "content")
			if code == nil {
				code = ojson.GetOr(inp, "new_string")
			}
			text := ""
			if code == nil {
				text = py.Dumps(inp)
			} else {
				text = py.Str(code)
			}
			return rsplitExt(path), text, true
		}
		return "", "", false
	case "Bash":
	default:
		return "", "", false
	}
	cmd := py.Str(orNil(ojson.GetOr(inp, "command"), ""))
	made := writesRe.FindAll(cmd)
	for _, m := range made {
		written.add(m)
	}
	if m := inline.Search(cmd); m != nil {
		return m.Group(1), cmd, true
	}
	if len(made) > 0 {
		return rsplitExt(made[0]), cmd, true
	}
	// running it with an interpreter, or patching it in place, still counts toward the script
	for _, p := range written.items() {
		re := pyre.Compile(`\b(?:python3?|node|bash|sh|ruby|perl|sed\s+-i\S*(?:\s+''|\s+\S+)*?)\s+(?:-\S+\s+)*\S*` +
			regexpEscape(basename(p)) + `\b`)
		if re.Has(cmd) {
			return rsplitExt(p), cmd, true
		}
	}
	return "", "", false
}

func rsplitExt(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func orNil(v any, dflt any) any {
	if v == nil {
		return dflt
	}
	return v
}

// regexpEscape is re.escape(): every non-alphanumeric ASCII character backslashed.
func regexpEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x80 && !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			if r == '\n' {
				b.WriteString(`\n`)
				continue
			}
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// orderedSet keeps the iteration order a Python set of these strings would
// have only by accident; scripts are looked up in insertion order, which is
// what the transcripts make deterministic enough.
// ponytail: Python iterates a set in hash order; insertion order here, same result unless two scratch scripts both match one command
type orderedSet struct {
	keys []string
	has  map[string]bool
}

func newSet() *orderedSet { return &orderedSet{has: map[string]bool{}} }

func (s *orderedSet) add(k string) {
	if !s.has[k] {
		s.has[k] = true
		s.keys = append(s.keys, k)
	}
}

func (s *orderedSet) items() []string { return s.keys }

// handback is what a subagent handed back into the main transcript, and how
// many main-conversation API calls came after it.
type handback struct {
	text  string
	after int
}

func handbacks(path string, launches map[string]any) map[string]*handback {
	type hit struct {
		i    int
		text string
	}
	got := map[string]hit{}
	var gotOrder []string
	sync := map[string]hit{}
	var syncOrder []string
	var api []int
	seen := map[string]bool{}
	i := -1
	for r := range transcript.Entries(path) {
		i++
		if ojson.Truthy(ojson.Field(r, "isSidechain")) {
			continue
		}
		msg := transcript.Msg(r)
		if typ(r) == "assistant" && ojson.Truthy(ojson.Field(msg, "usage")) {
			mid := ojson.GetOr(msg, "id")
			if mid == nil {
				mid = ojson.Field(r, "uuid")
			}
			if !seen[py.Repr(mid)] {
				seen[py.Repr(mid)] = true
				api = append(api, i)
			}
			continue
		}
		if typ(r) != "user" {
			continue
		}
		c := ojson.Field(msg, "content")
		text := textOf(c)
		for _, m := range agentMsgRe.Iter(text) {
			if _, ok := launches[m.Group(1)]; ok {
				body := m.Group(2)
				if parts := strings.SplitN(body, "The report follows:\n", 2); len(parts) == 2 {
					body = parts[1]
				}
				var lines []string
				for _, l := range strings.Split(strings.Trim(body, "\n"), "\n") {
					if strings.HasPrefix(l, "  ") {
						l = l[2:]
					}
					lines = append(lines, l)
				}
				if _, ok := got[m.Group(1)]; !ok {
					gotOrder = append(gotOrder, m.Group(1))
				}
				got[m.Group(1)] = hit{i, strings.Join(lines, "\n")}
			}
		}
		if strings.Contains(text, "<task-notification>") {
			tid, res := taskIDRe.Search(text), resultRe.Search(text)
			if tid != nil && res != nil {
				if _, ok := launches[tid.Group(1)]; ok && !strings.Contains(res.Group(1), "was delivered to you as a message") {
					if _, ok := got[tid.Group(1)]; !ok {
						gotOrder = append(gotOrder, tid.Group(1))
					}
					got[tid.Group(1)] = hit{i, py.Strip(res.Group(1))}
				}
			}
		}
		list, _ := c.([]any)
		for _, x := range list {
			if ojson.Obj(x) == nil || typ(x) != "tool_result" {
				continue
			}
			body := ojson.Field(x, "content")
			text, isStr := body.(string)
			if !isStr {
				text = textOf(body)
			}
			for _, aid := range sortedKeys(launches) {
				if py.Repr(ojson.Field(x, "tool_use_id")) == py.Repr(launches[aid]) && !strings.HasPrefix(text, "Async agent launched") {
					if _, ok := sync[aid]; !ok {
						syncOrder = append(syncOrder, aid)
					}
					sync[aid] = hit{i, py.Strip(agentIDSplt.Split(text)[0])}
				}
			}
		}
	}
	out := map[string]*handback{}
	merged := map[string]hit{}
	for _, k := range syncOrder {
		merged[k] = sync[k]
	}
	for _, k := range gotOrder {
		merged[k] = got[k]
	}
	for aid, h := range merged {
		after := 0
		for _, p := range api {
			if p > h.i {
				after++
			}
		}
		out[aid] = &handback{h.text, after}
	}
	return out
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// readPaths is the files one tool call reads: Read's file_path, or the path
// arguments of cat/head/tail/sed.
func readPaths(name string, inp any) []string {
	h := home()
	norm := func(p string) string {
		p = strings.ReplaceAll(p, "$HOME", h)
		if p == "~" || strings.HasPrefix(p, "~/") {
			p = h + p[1:]
		}
		return normpath(p)
	}
	if name == "Read" {
		if v := ojson.GetOr(inp, "file_path"); v != nil {
			return []string{norm(py.Str(v))}
		}
	}
	if name != "Bash" {
		return nil
	}
	var out []string
	cmd := py.Str(orNil(ojson.GetOr(inp, "command"), ""))
	for _, seg := range segSplitRe2.Split(cmd) {
		words, err := py.ShlexSplit(seg)
		if err != nil || len(words) == 0 || !readers[basename(words[0])] {
			continue
		}
		var args []string
		for _, w := range words[1:] {
			if !strings.HasPrefix(w, "-") && !isDigits(w) {
				args = append(args, w)
			}
		}
		if basename(words[0]) == "sed" && len(args) > 0 {
			args = args[1:] // the first one is the script
		}
		for _, a := range args {
			if strings.Contains(a, "/") || strings.Contains(a, ".") {
				out = append(out, norm(a))
			}
		}
	}
	return out
}

// isDigits is str.isdigit().
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9') && !isUnicodeDigit(r) {
			return false
		}
	}
	return true
}

// normpath is os.path.normpath.
func normpath(p string) string {
	if p == "" {
		return "."
	}
	initial := 0
	if strings.HasPrefix(p, "/") {
		initial = 1
		if strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
			initial = 2
		}
	}
	var parts []string
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." {
			continue
		}
		if c != ".." || (initial == 0 && len(parts) == 0) || (len(parts) > 0 && parts[len(parts)-1] == "..") {
			parts = append(parts, c)
		} else if len(parts) > 0 {
			parts = parts[:len(parts)-1]
		}
	}
	out := strings.Join(parts, "/")
	if initial > 0 {
		out = strings.Repeat("/", initial) + out
	}
	if out == "" {
		return "."
	}
	return out
}

// parentReads is the files the main conversation read before it launched the subagent.
func parentReads(path string, launchID any) map[string]bool {
	out := map[string]bool{}
	for r := range transcript.Entries(path) {
		if ojson.Truthy(ojson.Field(r, "isSidechain")) || typ(r) != "assistant" {
			continue
		}
		for _, p := range transcript.Parts(transcript.Msg(r)) {
			if ojson.Obj(p) != nil && typ(p) == "tool_use" {
				if py.Repr(ojson.Field(p, "id")) == py.Repr(launchID) {
					return out
				}
				name, _ := ojson.Field(p, "name").(string)
				for _, x := range readPaths(name, ojson.Field(p, "input")) {
					out[x] = true
				}
			}
		}
	}
	return out
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isFileP(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isUnicodeDigit(r rune) bool { return unicode.IsDigit(r) }

package dash

import (
	"sort"
	"strings"

	"agentperform/internal/py"
	"agentperform/internal/pyre"
)

// candidates.py: skill candidates from throwaway scripts, and repeated-call types.

type service struct {
	name  string
	re    *pyre.Re
	words []string
}

var (
	services = []service{
		{"Confluence", pyre.Compile(`confluence|/wiki/`, pyre.I), []string{"confluence"}},
		{"Jira", pyre.Compile(`jira|atlassian\.net|/rest/api/\d|/rest/servicedeskapi`, pyre.I), []string{"jira"}},
		{"Slack", pyre.Compile(`slack\.com|slack_sdk|SLACK_\w*TOKEN|xox[bp]-`, pyre.I), []string{"slack"}},
		{"GitLab", pyre.Compile(`gitlab|/api/v4/`, pyre.I), []string{"gitlab"}},
		{"GitHub", pyre.Compile(`api\.github\.com|\bgh api\b`, pyre.I), []string{"github"}},
		{"Elasticsearch", pyre.Compile(`_search\b|_bulk\b|ELK_|elasticsearch|:9200`, pyre.I), []string{"elk", "elasticsearch"}},
		{"BigQuery", pyre.Compile(`bigquery|\bbq query\b`, pyre.I), []string{"bigquery"}},
	}
	candURL   = pyre.Compile(`https?://([\w.${}\-]+)(?::\d+)?(/[^\s'"\\)<>]*)?`)
	plumbing  = setOf("sys", "os", "re", "pathlib", "datetime", "collections", "typing", "argparse", "time", "glob", "itertools", "functools", "textwrap", "__future__", "shutil", "tempfile")
	notACmd   = setOf("cd", "export", "echo", "printf", "set", "source", "for", "while", "do", "done", "if", "then", "else", "fi", "case", "esac", "true", "unset", "pwd", "}", "{", "(", ")")
	twoWord   = setOf("git", "kubectl", "docker", "podman", "npm", "gh", "gcloud", "bq", "adb", "flutter", "cargo", "brew")
	plainTool = setOf("jq", "grep", "rg", "sed", "awk", "sort", "cut", "head", "tail", "wc", "find", "cat", "uniq", "xargs", "ls")
	langOf    = map[string]string{"py": "Python", "python": "Python", "python3": "Python", "node": "Node", "js": "Node", "mjs": "Node",
		"cjs": "Node", "ts": "Node", "bun": "Node", "deno": "Node", "sh": "Shell", "bash": "Shell",
		"ruby": "Ruby", "rb": "Ruby", "perl": "Perl", "pl": "Perl"}
	idRe       = pyre.Compile(`^(?:\d+|[A-Z][A-Z0-9]*-\d+|[0-9a-fA-F]{8,}|.*[${}<>].*)$`)
	familySkip = setOf("rest", "api", "wiki", "latest", "servicedeskapi", "agile")
	versionSeg = pyre.Compile(`v?\d+(\.\d+)?`)
	apiPath    = pyre.Compile(`(/(?:rest|api|wiki|_search|_bulk)\b[^\s'"\\)<>]*)`)
	leadAssign = pyre.Compile(`^\s*(?:\w+=\S*\s+)+`)
	wordAssign = pyre.Compile(`^\w+=`)
	importRe   = pyre.Compile(`^\s*(?:import|from)\s+([A-Za-z_]\w*)|require\(['"]([\w:/.-]+)['"]\)`, pyre.M)
)

func setOf(xs ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func endpoint(path string) string {
	path, _, _ = strings.Cut(path, "?")
	var segs []string
	for _, s := range strings.Split(path, "/") {
		if s != "" && !familySkip[strings.ToLower(s)] && versionSeg.FullMatch(s) == nil {
			segs = append(segs, s)
		}
	}
	if len(segs) > 3 {
		segs = segs[:3]
	}
	for i, s := range segs {
		if idRe.MatchStart(s) != nil {
			segs[i] = "*"
		}
	}
	return strings.Join(segs, "/")
}

func servicePath(code string, svc *pyre.Re) string {
	for _, m := range candURL.Iter(code) {
		if svc.Has(m.Group(0)) {
			if m.Has(2) && m.Group(2) != "" {
				return m.Group(2)
			}
			break
		}
	}
	if m := apiPath.Search(code); m != nil {
		return m.Group(1)
	}
	return ""
}

func firstCommand(code string) string {
	for _, line := range py.Splitlines(code) {
		words := py.Split(leadAssign.Sub(py.Strip(line), ""))
		if len(words) == 0 || strings.HasPrefix(words[0], "#") || notACmd[words[0]] || wordAssign.MatchStart(words[0]) != nil {
			continue
		}
		cmd := basename(words[0])
		if twoWord[cmd] && len(words) > 1 {
			return cmd + " " + words[1]
		}
		return cmd
	}
	return ""
}

// scriptShape is the short normalised shape of one script.
func scriptShape(code, lang string) string {
	for _, s := range services {
		if s.re.Has(code) {
			return "API|" + s.name + "|" + endpoint(servicePath(code, s.re))
		}
	}
	langs, ok := langOf[lang]
	if !ok {
		langs = lang
	}
	modSet := map[string]bool{}
	for _, g := range importRe.FindAllGroups(code) {
		m := g[0]
		if m == "" {
			m = g[1]
		}
		if !plainTool[m] && !plumbing[m] {
			modSet[m] = true
		}
	}
	var mods []string
	for m := range modSet {
		mods = append(mods, m)
	}
	sort.Strings(mods)
	for _, m := range candURL.Iter(code) {
		if !strings.Contains(m.Group(1), "$") {
			name := m.Group(1)
			if name == "localhost" || name == "127.0.0.1" || name == "0.0.0.0" {
				name = "本機服務"
			}
			return "API|" + name + "|" + endpoint(m.Group(2))
		}
	}
	if len(mods) > 0 || langs == "Python" || langs == "Node" {
		if len(mods) > 3 {
			mods = mods[:3]
		}
		return langs + "|" + strings.Join(mods, ",")
	}
	return langs + "|" + firstCommand(code)
}

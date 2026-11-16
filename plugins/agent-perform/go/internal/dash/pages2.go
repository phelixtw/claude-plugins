package dash

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
)

var (
	slashRe = pyre.Compile(`<command-name>/?([\w:.-]+)</command-name>`)
)

type skillPath struct{ src, path string }

// orderedSkills is an insertion-ordered name -> (source, path) map.
type orderedSkills struct {
	keys []string
	m    map[string]skillPath
}

func (o *orderedSkills) set(k string, v skillPath) {
	if _, ok := o.m[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.m[k] = v
}

// installedSkillPaths is every SKILL.md Claude Code can load on this machine.
func installedSkillPaths() *orderedSkills {
	h := filepath.Join(home(), ".claude")
	out := &orderedSkills{m: map[string]skillPath{}}
	globChild := func(dir string) []string {
		var ps []string
		for _, n := range listdir(dir) {
			p := filepath.Join(dir, n, "SKILL.md")
			if _, err := os.Lstat(p); err == nil {
				ps = append(ps, p)
			}
		}
		return ps
	}
	for _, p := range globChild(filepath.Join(h, "skills")) {
		out.set(filepath.Base(filepath.Dir(p)), skillPath{"個人", p})
	}
	cwd, _ := os.Getwd()
	for _, p := range globChild(filepath.Join(cwd, ".claude", "skills")) {
		if _, ok := out.m[filepath.Base(filepath.Dir(p))]; !ok {
			out.set(filepath.Base(filepath.Dir(p)), skillPath{"專案", p})
		}
	}
	var plugins *ojson.Object
	if raw, err := os.ReadFile(filepath.Join(h, "plugins", "installed_plugins.json")); err == nil {
		if v, err := ojson.Parse(raw); err == nil {
			plugins = ojson.Obj(ojson.Field(v, "plugins"))
		}
	}
	for _, a := range listdir(filepath.Join(h, "skills", "synced")) {
		for _, p := range globChild(filepath.Join(h, "skills", "synced", a)) {
			out.set("anthropic-skills:"+filepath.Base(filepath.Dir(p)), skillPath{syncedSrc, p})
		}
	}
	type root struct{ name, root, src string }
	var roots []root
	if plugins != nil {
		for _, key := range plugins.Keys {
			installs, _ := plugins.Vals[key].([]any)
			for _, e := range installs {
				ip := py.Str(orNil(ojson.GetOr(e, "installPath"), ""))
				roots = append(roots, root{strings.SplitN(key, "@", 2)[0], ip, ""})
			}
		}
	}
	syncedP := filepath.Join(h, "plugins", "synced")
	for _, a := range listdir(syncedP) {
		for _, b := range listdir(filepath.Join(syncedP, a)) {
			d := filepath.Join(syncedP, a, b)
			if st, err := os.Stat(d); err == nil && st.IsDir() && !strings.Contains(d, "~") {
				roots = append(roots, root{filepath.Base(d), d, syncedSrc})
			}
		}
	}
	for _, r := range roots {
		base := r.root
		if base == "" {
			base, _ = os.Getwd()
		}
		for _, p := range globRec(base, "SKILL.md") {
			rel := p
			if r.root != "" {
				rel = p[len(r.root):]
			} else {
				rel, _ = filepath.Rel(base, p)
				p = rel
			}
			if strings.Contains(rel, "/.") { // .cursor/.openclaw mirrors
				continue
			}
			src := r.src
			if src == "" {
				src = "plugin " + r.name
			}
			out.set(r.name+":"+skillName(p), skillPath{src, p})
		}
	}
	return out
}

// skillName is the frontmatter `name:`, else the folder.
func skillName(path string) string {
	raw, err := os.ReadFile(path)
	if err == nil {
		text := py.Head(normNL(strings.ToValidUTF8(string(raw), "�")), 2000)
		lines := py.Splitlines(text)
		if len(lines) > 0 {
			for _, line := range lines[1:] {
				if py.Strip(line) == "---" {
					break
				}
				if strings.HasPrefix(line, "name:") {
					return strings.Trim(py.Strip(line[5:]), `"'`)
				}
			}
		}
	}
	return filepath.Base(filepath.Dir(path))
}

func normNL(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

// skillUsage is (installed, turns per skill, last use).
func skillUsage(sessions []*Session) (*orderedSkills, *py.Counter, map[string]float64) {
	inv := installedSkillPaths()
	canon := skillCanon(inv)
	total := py.NewCounter()
	last := map[string]float64{}
	for _, s := range sessions {
		for _, t := range s.Turns {
			names := map[string]bool{}
			for _, n := range t.Skills.items() {
				names[n] = true
			}
			if m := slashRe.Search(t.Prompt); m != nil {
				if _, ok := inv.m[canon(m.Group(1))]; ok {
					names[m.Group(1)] = true
				}
			}
			canonSet := map[string]bool{}
			for x := range names {
				canonSet[canon(x)] = true
			}
			var cs []string
			for n := range canonSet {
				cs = append(cs, n)
			}
			sort.Strings(cs)
			for _, n := range cs {
				total.Add(n, 1)
				if t.At > last[n] {
					last[n] = t.At
				}
			}
		}
		for _, l := range windowLaunches(s) { // a subagent that called a skill counts as one use
			called, _ := ojson.Field(l, "skills_called").([]any)
			canonSet := map[string]bool{}
			for _, v := range called {
				canonSet[canon(py.Str(v))] = true
			}
			at := fnum(l, "at")
			for n := range canonSet {
				total.Add(n, 1)
				if at > last[n] {
					last[n] = at
				}
			}
		}
	}
	return inv, total, last
}

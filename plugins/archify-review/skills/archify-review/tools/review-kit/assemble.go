// assemble: plan.json (judgment text, layout) + hunks.json -> architecture spec with exact diffs and `lines`.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var codeKeys = []string{"from_symbol", "to_symbol", "story", "action", "note", "line"}

type ctx struct {
	out     *Out
	risk    *Risk
	files   []any           // changes.files without test files
	tests   map[string]bool // changes.tests
	covered map[int]bool
	errs    []string
}

// Test files by naming convention: a Tests/Test suffix on the file stem (FooTests.swift, FooTest.kt),
// a _test/.test/.spec suffix (foo_test.dart, foo.spec.js), a test_ prefix (test_foo.py), or a directory such
// as test/, __tests__/, androidTest/ or AcmeCartUITests/. A CamelCase suffix needs a lowercase letter, a
// digit or "UI" before it, so ABTests.swift (an A/B test feature) is not a test file. A CamelCase Spec suffix
// is not one either (AnimationSpec.kt is app code); spec frameworks keep their files in a test directory.
var (
	testStem = regexp.MustCompile(`(?:[a-z0-9]|UI)Tests?$|.[._](?:test|spec)$|^test_.`)
	testDir  = regexp.MustCompile(`^(?:tests?|Tests|__tests__|androidTest|testDebug|.*(?:[a-z0-9]|UI)Tests)$`)
)

// isTestPath is the one test-file classifier: assemble keeps these files out of the report and risk scores
// them as test code.
func isTestPath(p string) bool {
	segs := strings.Split(p, "/")
	for _, d := range segs[:len(segs)-1] {
		if testDir.MatchString(d) {
			return true
		}
	}
	base := segs[len(segs)-1]
	return testStem.MatchString(strings.TrimSuffix(base, filepath.Ext(base)))
}

// splitTests returns a copy of `changes` whose test files moved from `files` to `tests`.
func splitTests(changes any) (any, []any, map[string]bool) {
	ch, ok := changes.(map[string]any)
	if !ok {
		return changes, nil, map[string]bool{}
	}
	ch = obj(ch)
	all, _ := ch["files"].([]any)
	files, tests, set := []any{}, []any{}, map[string]bool{}
	for _, f := range all {
		m, _ := f.(map[string]any)
		if p, _ := m["path"].(string); isTestPath(p) {
			tests = append(tests, p)
			set[p] = true
		} else {
			files = append(files, f)
		}
	}
	ch["files"], ch["tests"] = files, tests
	return ch, files, set
}

func textOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return compact(v)
}

// Non-negative integer value of a JSON number, like serde_json as_u64.
func asU64(v any) (int, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseUint(n.String(), 10, 63)
	return int(i), err == nil
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	r := make(map[string]any, len(m))
	for k, x := range m {
		r[k] = x
	}
	return r
}

func take(m map[string]any, k string) (any, bool) {
	v, ok := m[k]
	delete(m, k)
	return v, ok
}

// `hunks`: absent or "all" = every hunk of the file, else a list of per-file hunk numbers.
func (c *ctx) sel(file string, hunks any, present bool) ([]numbered, error) {
	all := fileHunks(c.out, file)
	if len(all) == 0 {
		return nil, fmt.Errorf("no hunks for %s", file)
	}
	if !present || hunks == "all" {
		return all, nil
	}
	arr, ok := hunks.([]any)
	if !ok {
		return nil, fmt.Errorf("hunks must be \"all\" or a list of numbers, got %s", compact(hunks))
	}
	r := []numbered{}
	for _, n := range arr {
		i, ok := asU64(n)
		if !ok || i < 1 || i > len(all) {
			return nil, fmt.Errorf("%s has %d hunks, bad hunk ref %s", file, len(all), compact(n))
		}
		r = append(r, all[i-1])
	}
	return r, nil
}

// Marks the hunks covered and returns the verbatim concatenated diff.
func (c *ctx) diff(chosen []numbered) string {
	texts := make([]string, len(chosen))
	for i, x := range chosen {
		c.covered[x.h.N] = true
		texts[i] = x.h.Text
	}
	return strings.Join(texts, "\n")
}

// fnLines canonicalizes a tests function's `lines` against the connection diff. `hunk` (a 1-based position
// in that diff, or a list of them) scopes a number that falls in several hunks.
func fnLines(m map[string]any, file string, chosen []numbered) (string, error) {
	var pool []numbered
	for k, x := range chosen {
		pool = append(pool, numbered{k + 1, x.h})
	}
	scope := ""
	if h, ok := m["hunk"]; ok {
		want, ok := h.([]any)
		if !ok {
			want = []any{h}
		}
		var kept []numbered
		var nums []int
		for _, w := range want {
			n, ok := asU64(w)
			if !ok || n < 1 || n > len(pool) {
				return "", fmt.Errorf("hunk %s is not in the diff (%d hunks)", compact(w), len(pool))
			}
			kept, nums = append(kept, pool[n-1]), append(nums, n)
		}
		pool, scope = kept, " hunk "+strings.Trim(listStr(nums), "[]")
	}
	v, ok := m["lines"]
	if !ok || textOf(v) == "" {
		return "", fmt.Errorf("no lines")
	}
	return canonLines(pool, file, scope, "set the function's hunk", []string{textOf(v)})
}

// testsOf canonicalizes every `tests.functions[].lines`; cases and suggested pass through for review-check (R24).
func testsOf(v any, file string, chosen []numbered) (map[string]any, []string) {
	if _, ok := v.(map[string]any); !ok {
		return nil, []string{"tests must be an object"}
	}
	t := obj(v)
	fns, ok := t["functions"].([]any)
	if !ok {
		return t, []string{"tests.functions must be a list"}
	}
	var errs []string
	out := []any{}
	for i, f := range fns {
		if _, ok := f.(map[string]any); !ok {
			errs = append(errs, fmt.Sprintf("function %d must be an object", i+1))
			continue
		}
		fm := obj(f)
		name := fmt.Sprintf("%d", i+1)
		if s, ok := fm["symbol"].(string); ok && s != "" {
			name = s
		}
		lines, err := fnLines(fm, file, chosen)
		if err != nil {
			errs = append(errs, fmt.Sprintf("function %s: %v", name, err))
			continue
		}
		fm["lines"] = lines
		out = append(out, fm)
	}
	t["functions"] = out
	return t, errs
}

func idOf(m map[string]any) string {
	if v, ok := m["id"]; ok {
		return textOf(v)
	}
	return "?"
}

func (c *ctx) connection(v any) any {
	m := obj(v)
	id := idOf(m)
	cv, _ := take(m, "code")
	code := obj(cv)
	for _, k := range codeKeys {
		if x, ok := take(m, k); ok {
			code[k] = x
		}
	}
	fileV, _ := take(m, "file")
	hunks, hasHunks := take(m, "hunks")
	testsV, hasTests := take(m, "tests")
	file, ok := fileV.(string)
	if !ok {
		if m["kind"] != "counterpart" {
			c.errs = append(c.errs, fmt.Sprintf("connection %s: set file, or kind \"counterpart\" for a connection without a diff", id))
		} else if _, ok := code["story"]; !ok {
			c.errs = append(c.errs, fmt.Sprintf("connection %s: counterpart needs a story", id))
		}
		m["code"] = code
		return m
	}
	if c.tests[file] {
		c.errs = append(c.errs, fmt.Sprintf("connection %s: %s is a test file; drop this connection and list what its tests cover under `tests` of the hop they test", id, file))
		m["code"] = code
		return m
	}
	chosen, err := c.sel(file, hunks, hasHunks)
	if err != nil {
		c.errs = append(c.errs, fmt.Sprintf("connection %s: %v", id, err))
	} else {
		code["path"] = file
		code["diff"] = c.diff(chosen)
		var fns []any
		if hasTests {
			tests, errs := testsOf(testsV, file, chosen)
			for _, e := range errs {
				c.errs = append(c.errs, fmt.Sprintf("connection %s %s", id, e))
			}
			if tests != nil {
				code["tests"] = tests
				fns, _ = tests["functions"].([]any)
			}
		}
		if _, ok := code["line"]; !ok {
			// default: first + line of the first tested function, else the first new-file line of the diff
			line := 0
			if len(fns) > 0 {
				if l, ok := fns[0].(map[string]any)["lines"].(string); ok {
					for _, p := range strings.Split(l, ",") {
						if sign, lo, _, ok := parseRange(p); ok && sign == '+' {
							line = lo
							break
						}
					}
				}
			}
			if line == 0 {
			fallback:
				for _, x := range chosen {
					for _, l := range x.h.Lines {
						if l.NewNo != nil {
							line = *l.NewNo
							break fallback
						}
					}
				}
			}
			if line != 0 {
				code["line"] = line
			}
		}
	}
	m["code"] = code
	return m
}

func (c *ctx) component(v any) any {
	m := obj(v)
	id := idOf(m)
	if label, ok := m["label"].(string); ok && c.testLabel(label) {
		c.errs = append(c.errs, fmt.Sprintf("component %s: %s names a test file; test files are not nodes, so remove it and list what its tests cover under `tests` of the hop they test", id, label))
	}
	cv, _ := take(m, "code")
	code := obj(cv)
	if layers, ok := take(m, "layers"); ok { // plan shorthand: {mvvm|flutter|webview: layer}
		code["layers"] = layers
	}
	if files, ok := take(m, "files"); ok {
		out := []any{}
		arr, _ := files.([]any)
		for _, f := range arr {
			var e any = f
			if p, ok := f.(string); ok {
				e = map[string]any{"path": p}
			} else if _, ok := f.(map[string]any); ok {
				e = obj(f)
			}
			if eo, ok := e.(map[string]any); ok {
				h, has := take(eo, "hunks")
				path, isPath := eo["path"].(string)
				switch {
				case c.tests[path]:
					c.errs = append(c.errs, fmt.Sprintf("component %s: %s is a test file; remove it from the plan and list what its tests cover under `tests` of the hop they test", id, path))
				case isPath && has:
					chosen, err := c.sel(path, h, true)
					if err != nil {
						c.errs = append(c.errs, fmt.Sprintf("component %s: %v", id, err))
					} else {
						eo["diff"] = c.diff(chosen)
					}
				}
			}
			out = append(out, e)
		}
		code["files"] = out
	}
	if c.risk != nil {
		if paths := c.nodeFiles(code, m["label"]); len(paths) > 0 {
			code["risk"] = nodeRisk(c.risk, paths)
		}
	}
	if len(code) > 0 {
		m["code"] = code
	}
	return m
}

func (c *ctx) nodeFiles(code map[string]any, label any) []string {
	var paths []string
	files, _ := code["files"].([]any)
	for _, f := range files {
		if p, ok := f.(map[string]any)["path"].(string); ok {
			paths = append(paths, p)
		}
	}
	if len(paths) > 0 {
		return paths
	}
	for _, f := range c.files {
		e, _ := f.(map[string]any)
		if p, _ := e["path"].(string); e["status"] != "deleted" && label != nil && filepath.Base(p) == label {
			paths = append(paths, p)
		}
	}
	if len(paths) != 1 {
		return nil
	}
	return paths
}

// testLabel: a label is a file name, so a label that only a test file carries names a test node.
func (c *ctx) testLabel(label string) bool {
	named := func(p string) bool { return filepath.Base(p) == label || p == label }
	for p := range c.tests {
		if named(p) {
			for _, f := range c.files {
				if q, _ := f.(map[string]any)["path"].(string); named(q) {
					return false
				}
			}
			return true
		}
	}
	return false
}

func build(plan any, hk *Out, risk *Risk) (map[string]any, string, []string) {
	if _, ok := plan.(map[string]any); !ok {
		return nil, "", []string{"plan must be a JSON object"}
	}
	changes, files, tests := splitTests(hk.Changes)
	c := &ctx{out: hk, risk: risk, files: files, tests: tests, covered: map[int]bool{}}
	spec := obj(plan)
	if _, ok := spec["schema_version"]; !ok {
		spec["schema_version"] = 1
	}
	if _, ok := spec["diagram_type"]; !ok {
		spec["diagram_type"] = "architecture"
	}
	comps, conns := []any{}, []any{}
	cs, _ := spec["components"].([]any)
	for _, x := range cs {
		comps = append(comps, c.component(x))
	}
	ns, _ := spec["connections"].([]any)
	for _, x := range ns {
		conns = append(conns, c.connection(x))
	}
	for _, h := range hk.Hunks {
		if !c.covered[h.N] && !tests[h.File] {
			c.errs = append(c.errs, fmt.Sprintf("uncovered hunk %d (%s %s): add it to a connection or a component file", h.N, h.File, h.Header))
		}
	}
	if len(c.errs) > 0 {
		return nil, "", c.errs
	}
	spec["components"] = comps
	spec["connections"] = conns
	spec["changes"] = changes
	markNew(spec)
	n := 0
	for _, h := range hk.Hunks {
		if !tests[h.File] {
			n++
		}
	}
	summary := fmt.Sprintf("%d components, %d connections, %d hunks covered", len(comps), len(conns), n)
	if len(tests) > 0 {
		summary += fmt.Sprintf(", %d test files in changes.tests", len(tests))
	}
	return spec, summary, nil
}

func cmdAssemble(a []string) {
	const usage = "usage: assemble --hunks hunks.json --plan plan.json [--risk risk.json] --out arch-review.json"
	var hunks, planPath, riskPath, outp string
	for i := 0; i < len(a); i += 2 {
		v := ""
		if i+1 < len(a) {
			v = a[i+1]
		}
		switch a[i] {
		case "--hunks":
			hunks = v
		case "--plan":
			planPath = v
		case "--risk":
			riskPath = v
		case "--out":
			outp = v
		default:
			die(2, usage)
		}
	}
	if hunks == "" || planPath == "" || outp == "" {
		die(2, usage)
	}
	hk := readHunks(hunks)
	raw, err := os.ReadFile(planPath)
	if err != nil {
		die(2, fmt.Sprintf("read %s: %v", planPath, err))
	}
	var plan any
	if err := decode(raw, &plan); err != nil {
		die(2, "parse plan: "+err.Error())
	}
	var risk *Risk
	if riskPath != "" {
		risk = readRisk(riskPath)
	}
	spec, summary, errs := build(plan, &hk, risk)
	if errs != nil {
		fmt.Fprintf(os.Stderr, "%d error(s):\n  %s\n", len(errs), strings.Join(errs, "\n  "))
		os.Exit(1)
	}
	if err := os.WriteFile(outp, pretty(spec), 0o644); err != nil {
		die(2, fmt.Sprintf("write %s: %v", outp, err))
	}
	fmt.Printf("%s -> %s\n", summary, outp)
}

// markNew moves the plan's `new_commits` (sha prefixes) onto changes.commits[].new and `repo` onto changes.repo.
func markNew(spec map[string]any) {
	ch, _ := spec["changes"].(map[string]any)
	if ch == nil {
		return
	}
	newShas, _ := spec["new_commits"].([]any)
	delete(spec, "new_commits")
	if repo, ok := spec["repo"].(string); ok {
		ch["repo"] = repo
		delete(spec, "repo")
	}
	commits, _ := ch["commits"].([]any)
	for _, c := range commits {
		m, _ := c.(map[string]any)
		sha, _ := m["sha"].(string)
		for _, n := range newShas {
			if p, _ := n.(string); p != "" && sha != "" && (strings.HasPrefix(sha, p) || strings.HasPrefix(p, sha)) {
				m["new"] = true
			}
		}
	}
}

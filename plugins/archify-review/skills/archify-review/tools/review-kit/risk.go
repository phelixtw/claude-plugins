package main

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const crgVersion = "2.4."

// crgCommands are the only code-review-graph commands review-kit runs. Its install and init rewrite the
// target repo's .mcp.json, .claude/ and CLAUDE.md, so they are not reachable from here.
var crgCommands = map[string]bool{"--version": true, "build": true, "detect-changes": true, "impact": true}

var factorKeys = []string{"flows", "cross_community", "tests", "security", "callers", "churn"}

const (
	maxCallers  = 20
	maxUntested = 12
)

// crgSignals runs in code-review-graph's own interpreter: the CLI breaks down only its top 10 functions, and
// this gives the same terms (`risk_signals`, which its score is the sum of) for each file's top function. It
// also names the files code-review-graph treats as test code (`is_test_file`), whose helpers would otherwise
// score as untested production code.
const crgSignals = `import json, sys
from code_review_graph.changes import risk_signals
from code_review_graph.parser import is_test_file
from code_review_graph.tools._common import _get_store
req = json.load(sys.stdin)
store, _ = _get_store(sys.argv[1])
try:
    out = {}
    for qn in req["functions"]:
        node = store.get_node(qn)
        if node is not None:
            out[qn] = {k: {"score": round(float(v["score"]), 4), "count": int(v["count"])} for k, v in risk_signals(store, node, None).items()}
    tests = [f for f in req["files"] if is_test_file(f, sys.argv[1])]
    json.dump({"signals": out, "tests": tests}, sys.stdout)
finally:
    store.close()
`

type Risk struct {
	Tool  string               `json:"tool"`
	Base  string               `json:"base"`
	Head  string               `json:"head"`
	Files map[string]*FileRisk `json:"files"`
}

type FileRisk struct {
	Score    float64  `json:"score"`
	Symbol   string   `json:"symbol,omitempty"`
	Factors  []Factor `json:"factors,omitempty"`
	Callers  []Caller `json:"callers"`
	Untested []string `json:"untested"`
	top      string
}

type Factor struct {
	Key   string  `json:"key"`
	Score float64 `json:"score"`
	Count int     `json:"count"`
}

type Caller struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Symbol string `json:"symbol,omitempty"`
}

type crg struct {
	bin, python, version, repo, dataDir string
}

func findCRG() crg {
	const install = "install it with: uv tool install code-review-graph (never run its install subcommand)"
	bin, err := exec.LookPath("code-review-graph")
	if err != nil {
		die(1, "code-review-graph is not on PATH; "+install)
	}
	out, err := exec.Command(bin, "--version").Output()
	version := strings.TrimSpace(string(out))
	if err != nil || !strings.HasPrefix(version, "code-review-graph "+crgVersion) {
		die(1, fmt.Sprintf("%s reports %q; review-kit needs code-review-graph %sx: %s", bin, version, crgVersion, install))
	}
	return crg{bin: bin, version: version}
}

func (c crg) run(args ...string) ([]byte, error) {
	if !crgCommands[args[0]] {
		return nil, fmt.Errorf("review-kit never runs code-review-graph %s", args[0])
	}
	cmd := exec.Command(c.bin, args...)
	cmd.Dir = c.repo
	cmd.Env = append(os.Environ(), "CRG_DATA_DIR="+c.dataDir, "CRG_MAX_CHANGED_FUNCS=1000000")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("code-review-graph %s failed: %v %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (c crg) must(args ...string) []byte {
	out, err := c.run(args...)
	if err != nil {
		die(1, err.Error())
	}
	return out
}

func interpreter(bin string) string {
	f, err := os.Open(bin)
	if err != nil {
		die(1, err.Error())
	}
	defer f.Close()
	line, _ := bufio.NewReader(f).ReadString('\n')
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if !strings.HasPrefix(line, "#!") || len(fields) == 0 {
		die(1, bin+" is not a Python launcher script; install code-review-graph with uv tool install code-review-graph")
	}
	if filepath.Base(fields[0]) == "env" && len(fields) > 1 {
		p, err := exec.LookPath(fields[1])
		if err != nil {
			die(1, fmt.Sprintf("%s runs %s, which is not on PATH", bin, fields[1]))
		}
		return p
	}
	return fields[0]
}

func (c crg) signals(qualified, files []string) (map[string][]Factor, []string) {
	cmd := exec.Command(interpreter(c.bin), "-I", "-c", crgSignals, c.repo)
	cmd.Dir = c.repo
	cmd.Env = append(os.Environ(), "CRG_DATA_DIR="+c.dataDir)
	in, _ := json.Marshal(map[string][]string{"functions": qualified, "files": files})
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		die(1, fmt.Sprintf("reading code-review-graph risk terms failed: %v %s", err, strings.TrimSpace(stderr.String())))
	}
	var raw struct {
		Signals map[string]map[string]struct {
			Score float64 `json:"score"`
			Count int     `json:"count"`
		} `json:"signals"`
		Tests []string `json:"tests"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		die(1, "parse code-review-graph risk terms: "+err.Error())
	}
	got := map[string][]Factor{}
	for qn, terms := range raw.Signals {
		for k := range terms {
			if !slices.Contains(factorKeys, k) {
				die(1, fmt.Sprintf("code-review-graph %s has a risk term %q this review-kit does not know", c.version, k))
			}
		}
		for _, k := range factorKeys {
			if t, ok := terms[k]; ok {
				got[qn] = append(got[qn], Factor{Key: k, Score: t.Score, Count: t.Count})
			}
		}
	}
	return got, raw.Tests
}

type crgDetect struct {
	Changed []struct {
		Name          string  `json:"name"`
		QualifiedName string  `json:"qualified_name"`
		FilePath      string  `json:"file_path"`
		RiskScore     float64 `json:"risk_score"`
	} `json:"changed_functions"`
	Gaps []struct {
		Name     string `json:"name"`
		File     string `json:"file"`
		Coverage string `json:"coverage"`
	} `json:"test_gaps"`
	Truncated bool `json:"functions_truncated"`
}

type crgImpact struct {
	Status   string `json:"status"`
	Impacted []struct {
		Name     string `json:"name"`
		FilePath string `json:"file_path"`
		CallSite *struct {
			Line int    `json:"line"`
			File string `json:"file"`
		} `json:"call_site"`
	} `json:"impacted_nodes"`
}

// code-review-graph writes absolute paths; roots are the repo top level as git and the file system spell it.
func parseCRG(roots []string, files []string, detect []byte, impacts map[string][]byte) (map[string]*FileRisk, error) {
	rel := func(p string) string {
		for _, r := range roots {
			if x, err := filepath.Rel(r, p); err == nil && !strings.HasPrefix(x, "..") {
				return filepath.ToSlash(x)
			}
		}
		return ""
	}
	out := map[string]*FileRisk{}
	for _, f := range files {
		out[f] = &FileRisk{Callers: []Caller{}, Untested: []string{}}
	}
	var d crgDetect
	if t := bytes.TrimSpace(detect); len(t) > 0 && t[0] == '{' {
		if err := json.Unmarshal(t, &d); err != nil {
			return nil, fmt.Errorf("parse detect-changes: %v", err)
		}
	}
	if d.Truncated {
		return nil, fmt.Errorf("detect-changes capped its changed functions even with CRG_MAX_CHANGED_FUNCS=1000000")
	}
	for _, fn := range d.Changed {
		fr := out[rel(fn.FilePath)]
		if fr == nil || (fr.top != "" && fn.RiskScore <= fr.Score) {
			continue
		}
		fr.Score, fr.top = fn.RiskScore, fn.QualifiedName
		fr.Symbol = fn.Name
		if _, after, ok := strings.Cut(fn.QualifiedName, "::"); ok && after != "" {
			fr.Symbol = after
		}
	}
	for _, g := range d.Gaps {
		if fr := out[rel(g.File)]; fr != nil && g.Coverage == "none" && !slices.Contains(fr.Untested, g.Name) {
			fr.Untested = append(fr.Untested, g.Name)
		}
	}
	for f, raw := range impacts {
		var im crgImpact
		if err := json.Unmarshal(raw, &im); err != nil {
			return nil, fmt.Errorf("parse impact of %s: %v", f, err)
		}
		if im.Status != "ok" {
			return nil, fmt.Errorf("impact of %s: status %q", f, im.Status)
		}
		for _, n := range im.Impacted {
			if n.CallSite == nil || n.CallSite.Line < 1 {
				continue
			}
			at := n.FilePath
			if n.CallSite.File != "" {
				at = n.CallSite.File
			}
			if p := rel(at); p != "" {
				out[f].Callers = append(out[f].Callers, Caller{File: p, Line: n.CallSite.Line, Symbol: n.Name})
			}
		}
	}
	return out, nil
}

func crgDataDir(top string) string {
	if d := os.Getenv("CRG_DATA_DIR"); strings.TrimSpace(d) != "" {
		return d
	}
	sum := sha1.Sum([]byte(top))
	return filepath.Join(archifyHome(), "crg", fmt.Sprintf("%s-%x", filepath.Base(top), sum[:4]))
}

func riskBase(repo string, changes map[string]any) string {
	if s, ok := changes["slice"].(map[string]any); ok {
		return s["base"].(string)
	}
	var shas []string
	for _, c := range changes["commits"].([]any) {
		shas = append(shas, c.(map[string]any)["sha"].(string))
	}
	if len(shas) == 0 {
		die(1, "hunks.json has no commits and no slice")
	}
	order := strings.Fields(git(repo, append([]string{"rev-list", "--no-walk", "--date-order"}, shas...)...))
	oldest := order[len(order)-1]
	if p, ok := gitTry(repo, "rev-parse", "--verify", "-q", oldest+"^"); ok {
		return strings.TrimSpace(p)
	}
	return strings.TrimSpace(git(repo, "hash-object", "-t", "tree", "--stdin"))
}

func cmdRisk(a []string) {
	const usage = "usage: risk --repo <repo> --hunks hunks.json --out risk.json | risk --version"
	if len(a) == 1 && a[0] == "--version" {
		fmt.Println(findCRG().version)
		return
	}
	f := parseFlags(a, usage, []string{"repo", "hunks", "out"}, nil, nil)
	if f.vals["repo"] == "" || f.vals["hunks"] == "" || f.vals["out"] == "" {
		die(2, usage)
	}
	gitTop := strings.TrimSpace(git(f.vals["repo"], "rev-parse", "--show-toplevel"))
	top := toplevel(f.vals["repo"])
	hk := readHunks(f.vals["hunks"])
	changes, _ := hk.Changes.(map[string]any)
	var files []string
	for _, x := range changes["files"].([]any) {
		if e := x.(map[string]any); e["status"] != "deleted" {
			files = append(files, e["path"].(string))
		}
	}
	base := riskBase(top, changes)
	c := findCRG()
	c.repo, c.dataDir = top, crgDataDir(top)
	c.must("build", "--repo", top, "-q")
	detect := c.must("detect-changes", "--repo", top, "--base", base)
	impacts := map[string][]byte{}
	for _, x := range files {
		impacts[x] = c.must("impact", "--repo", top, "--files", x, "--depth", "1", "--max-results", "50")
	}
	byFile, err := parseCRG([]string{top, gitTop}, files, detect, impacts)
	if err != nil {
		die(1, err.Error())
	}
	var tops []string
	for _, fr := range byFile {
		if fr.top != "" {
			tops = append(tops, fr.top)
		}
	}
	slices.Sort(tops)
	abs := map[string]string{}
	var absFiles []string
	for _, x := range files {
		abs[filepath.Join(top, x)] = x
		absFiles = append(absFiles, filepath.Join(top, x))
	}
	sig, tests := c.signals(tops, absFiles)
	for _, fr := range byFile {
		fr.Factors = sig[fr.top]
	}
	for _, x := range files {
		if isTestPath(x) { // also the XCTest files code-review-graph 2.4's is_test_file misses
			tests = append(tests, filepath.Join(top, x))
		}
	}
	for _, t := range tests {
		byFile[abs[t]] = &FileRisk{Callers: []Caller{}, Untested: []string{}}
	}
	head := strings.TrimSpace(git(top, "rev-parse", "HEAD"))
	r := Risk{Tool: c.version, Base: base, Head: head, Files: byFile}
	if err := os.WriteFile(f.vals["out"], pretty(r), 0o644); err != nil {
		die(2, fmt.Sprintf("write %s: %v", f.vals["out"], err))
	}
	if s, ok := changes["slice"].(map[string]any); ok && s["head"] != head {
		fmt.Printf("note: risk reflects the working tree at %s, not the range head %s\n", head[:10], s["head"].(string)[:10])
	}
	if strings.TrimSpace(git(top, "status", "--porcelain", "--untracked-files=no")) != "" {
		fmt.Println("note: the working tree has uncommitted changes; risk includes them")
	}
	fmt.Printf("%s: %d files, base %s -> %s\n", c.version, len(files), base[:10], f.vals["out"])
}

func readRisk(path string) *Risk {
	raw, err := os.ReadFile(path)
	if err != nil {
		die(2, fmt.Sprintf("read %s: %v", path, err))
	}
	var r Risk
	if err := json.Unmarshal(raw, &r); err != nil {
		die(2, fmt.Sprintf("parse %s: %v", path, err))
	}
	return &r
}

func nodeRisk(r *Risk, paths []string) map[string]any {
	var best *FileRisk
	var callers []Caller
	untested := []string{}
	for _, p := range paths {
		fr := r.Files[p]
		if fr == nil {
			continue
		}
		if best == nil || fr.Score > best.Score {
			best = fr
		}
		for _, c := range fr.Callers {
			if !slices.ContainsFunc(callers, func(x Caller) bool { return x.File == c.File && x.Line == c.Line }) {
				callers = append(callers, c)
			}
		}
		for _, u := range fr.Untested {
			if !slices.Contains(untested, u) {
				untested = append(untested, u)
			}
		}
	}
	slices.SortStableFunc(callers, func(a, b Caller) int {
		if c := strings.Compare(a.File, b.File); c != 0 {
			return c
		}
		return a.Line - b.Line
	})
	out := map[string]any{"score": 0.0, "callers": capped(callers, maxCallers), "untested": capped(untested, maxUntested), "tool": r.Tool}
	if best != nil && best.Score > 0 {
		out["score"] = best.Score
		if best.Symbol != "" {
			out["symbol"] = best.Symbol
		}
		if len(best.Factors) > 0 {
			out["factors"] = best.Factors
		}
	}
	return out
}

func capped[T any](xs []T, n int) []T {
	if xs == nil {
		return []T{}
	}
	return xs[:min(len(xs), n)]
}

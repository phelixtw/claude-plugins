package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

const (
	planVersion = 2
	batchBudget = 12
)

type Plan struct {
	Version  int       `json:"version"`
	Repo     string    `json:"repo"`
	Base     string    `json:"base"`
	Head     string    `json:"head"`
	Key      string    `json:"key"`
	Features []Feature `json:"features"`
	Batches  []Batch   `json:"batches"`
	Hunks    []Owned   `json:"hunks"`
}

type Feature struct {
	Name    string   `json:"name"`
	Change  string   `json:"change"`
	Commits []string `json:"commits"`
}

type Batch struct {
	Report  string   `json:"report"`
	Feature int      `json:"feature"`
	Files   []string `json:"files"`
}

type Owned struct {
	File    string   `json:"file"`
	Header  string   `json:"header"`
	Commits []string `json:"commits"`
	Lines   []int    `json:"lines"`
}

type Slice struct {
	Report  string     `json:"report"`
	Base    string     `json:"base"`
	Head    string     `json:"head"`
	Feature string     `json:"feature"`
	Batch   int        `json:"batch"`
	Next    *SliceNext `json:"next"`
}

type SliceNext struct {
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`
	Files int    `json:"files"`
}

func archifyHome() string {
	if h := os.Getenv("ARCHIFY_REVIEW_HOME"); h != "" {
		return h
	}
	h, err := os.UserHomeDir()
	if err != nil {
		die(2, "no home directory: "+err.Error())
	}
	return filepath.Join(h, "archify-review")
}

func delivered(home, report string) bool {
	_, err := os.Stat(filepath.Join(home, "reports", report+".js"))
	return err == nil
}

func gitTry(repo string, args ...string) (string, bool) {
	out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).Output()
	return string(out), err == nil
}

func toplevel(repo string) string {
	top := strings.TrimSpace(git(repo, "rev-parse", "--show-toplevel"))
	if real, err := filepath.EvalSymlinks(top); err == nil {
		return real
	}
	return top
}

func fullSha(repo, rev string) string {
	return strings.TrimSpace(git(repo, "rev-parse", "--verify", rev+"^{commit}"))
}

func finalDiff(repo, base, head string, extra []string, files ...string) string {
	args := append([]string{"--literal-pathspecs", "-c", "core.quotepath=false", "diff", base, head}, diffFlags...)
	args = append(args, extra...)
	if len(files) > 0 {
		args = append(append(args, "--"), files...)
	}
	return git(repo, args...)
}

func finalStats(repo, base, head string, files ...string) []fileStat {
	return parseStats(finalDiff(repo, base, head, []string{"--name-status", "-z"}, files...), finalDiff(repo, base, head, []string{"--numstat", "-z"}, files...))
}

var porcelainLine = regexp.MustCompile(`^([0-9a-f]{40,64}) \d+ (\d+)`)

func runs(nums []int) []string {
	nums = slices.Clone(nums)
	slices.Sort(nums)
	nums = slices.Compact(nums)
	var out []string
	for i := 0; i < len(nums); {
		j := i
		for j+1 < len(nums) && nums[j+1] == nums[j]+1 {
			j++
		}
		out = append(out, fmt.Sprintf("%d,%d", nums[i], nums[j]))
		i = j + 1
	}
	return out
}

// A path git cannot blame (a submodule) leaves its lines unattributed.
func blameAt(repo, file, rng string, reverse bool, nums []int) map[int]string {
	got := map[int]string{}
	if len(nums) == 0 {
		return got
	}
	args := []string{"blame", "--porcelain"}
	if reverse {
		args = append(args, "--reverse")
	}
	args = append(args, rng)
	for _, r := range runs(nums) {
		args = append(args, "-L", r)
	}
	out, ok := gitTry(repo, append(args, "--", file)...)
	if !ok {
		return got
	}
	for _, l := range strings.Split(out, "\n") {
		if m := porcelainLine.FindStringSubmatch(l); m != nil {
			n, _ := strconv.Atoi(m[2])
			got[n] = m[1]
		}
	}
	return got
}

type rangeInfo struct {
	order  map[string]int
	merges map[string]bool
}

func readRange(repo, base, head string) rangeInfo {
	r := rangeInfo{order: map[string]int{}, merges: map[string]bool{}}
	for i, c := range strings.Fields(git(repo, "rev-list", "--topo-order", "--reverse", base+".."+head)) {
		r.order[c] = i
	}
	for _, c := range strings.Fields(git(repo, "rev-list", "--merges", base+".."+head)) {
		r.merges[c] = true
	}
	return r
}

func (r rangeInfo) sorted(set map[string]bool) []string {
	out := []string{}
	for c := range set {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b string) int { return r.order[a] - r.order[b] })
	return out
}

func attribute(repo, base, head string) []Owned {
	rg := readRange(repo, base, head)
	var hunks []Hunk
	parsePatch(finalDiff(repo, base, head, []string{"-U2"}), "", &hunks)
	byFile := map[string][]Hunk{}
	for _, h := range hunks {
		byFile[h.File] = append(byFile[h.File], h)
	}
	rng := base + ".." + head
	lastTouch := func(file string, filter ...string) string {
		args := append([]string{"--literal-pathspecs", "log", "-1", "--format=%H"}, filter...)
		return strings.TrimSpace(git(repo, append(args, rng, "--", file)...))
	}
	deleter := map[[2]string]string{}
	deletedBy := func(last, file string) string {
		k := [2]string{last, file}
		if d, ok := deleter[k]; ok {
			return d
		}
		out := strings.Fields(git(repo, "--literal-pathspecs", "rev-list", "--reverse", "--topo-order", "--ancestry-path", last+".."+head, "--", file))
		d := ""
		if len(out) > 0 {
			d = out[0]
		}
		deleter[k] = d
		return d
	}
	owned := []Owned{}
	for _, st := range finalStats(repo, base, head) {
		hs := byFile[st.path]
		if len(hs) == 0 {
			owned = append(owned, Owned{File: st.path, Commits: []string{lastTouch(st.path)}, Lines: []int{1}})
			continue
		}
		var plus, minus []int
		for _, h := range hs {
			for _, l := range h.Lines {
				if l.Sign == "+" {
					plus = append(plus, *l.NewNo)
				} else if l.Sign == "-" {
					minus = append(minus, *l.OldNo)
				}
			}
		}
		wrote := blameAt(repo, st.path, rng, false, plus)
		lastHad := blameAt(repo, st.path, rng, true, minus)
		adder := ""
		if st.status == "added" {
			adder = lastTouch(st.path, "--diff-filter=A")
		}
		for _, h := range hs {
			count := map[string]int{}
			for _, l := range h.Lines {
				c := ""
				if l.Sign == "+" {
					if c = wrote[*l.NewNo]; !hasKey(rg.order, c) {
						c = adder
					}
				} else if l.Sign == "-" {
					if last := lastHad[*l.OldNo]; last != "" {
						c = deletedBy(last, st.path)
					}
				}
				if hasKey(rg.order, c) {
					count[c]++
				}
			}
			o := Owned{File: h.File, Header: h.Header, Commits: rg.sorted(boolSet(count)), Lines: []int{}}
			for _, c := range o.Commits {
				o.Lines = append(o.Lines, count[c])
			}
			owned = append(owned, o)
		}
	}
	return owned
}

func hasKey[V any](m map[string]V, k string) bool {
	_, ok := m[k]
	return ok
}

func (p *Plan) owners() []int {
	featureOf := map[string]int{}
	for i, f := range p.Features {
		for _, c := range f.Commits {
			featureOf[c] = i
		}
	}
	out := make([]int, len(p.Hunks))
	for i, h := range p.Hunks {
		out[i] = len(p.Features) - 1
		wrote := make([]int, len(p.Features))
		for j, c := range h.Commits {
			if f, ok := featureOf[c]; ok {
				wrote[f] += h.Lines[j]
			}
		}
		best := 0
		for f, n := range wrote {
			if n > best {
				out[i], best = f, n
			}
		}
	}
	return out
}

func (p *Plan) files(owners []int, f int) []string {
	var out []string
	for i, h := range p.Hunks {
		if owners[i] == f && !slices.Contains(out, h.File) {
			out = append(out, h.File)
		}
	}
	return out
}

func (p *Plan) candidates(owners []int, after int) (f int, files []string) {
	f = 0
	if after >= 0 {
		f = p.Batches[after].Feature
		done := map[string]bool{}
		for _, b := range p.Batches[:after+1] {
			if b.Feature == f {
				for _, x := range b.Files {
					done[x] = true
				}
			}
		}
		for _, x := range p.files(owners, f) {
			if !done[x] {
				files = append(files, x)
			}
		}
		if len(files) > 0 {
			return f, files
		}
		f++
	}
	for ; f < len(p.Features); f++ {
		if fs := p.files(owners, f); len(fs) > 0 {
			return f, fs
		}
	}
	return -1, nil
}

func (p *Plan) next(owners []int, b int) *SliceNext {
	f, files := p.candidates(owners, b)
	switch {
	case f < 0:
		return nil
	case f == p.Batches[b].Feature:
		return &SliceNext{Kind: "batch", Files: len(files)}
	default:
		return &SliceNext{Kind: "feature", Name: p.Features[f].Name, Files: len(files)}
	}
}

func (p *Plan) batchNo(b int) int {
	n := 0
	for _, x := range p.Batches[:b+1] {
		if x.Feature == p.Batches[b].Feature {
			n++
		}
	}
	return n
}

func planPath(home, base, head string) string {
	return filepath.Join(home, "slices", base[:10]+".."+head[:10]+".json")
}

func readPlan(path string) (*Plan, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse %s: %v", path, err)
	}
	if p.Version != planVersion {
		return nil, fmt.Errorf("%s has plan version %d, this review-kit writes %d: run slice plan --replace", path, p.Version, planVersion)
	}
	return &p, nil
}

func allPlans(home string) map[string]*Plan {
	out := map[string]*Plan{}
	paths, _ := filepath.Glob(filepath.Join(home, "slices", "*.json"))
	for _, path := range paths {
		if p, err := readPlan(path); err == nil {
			out[path] = p
		}
	}
	return out
}

func writeAtomic(path string, data []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		die(2, err.Error())
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".plan-*")
	if err != nil {
		die(2, err.Error())
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		die(2, fmt.Sprintf("write %s: %v %v", path, werr, cerr))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		die(2, err.Error())
	}
}

func findBatch(home, report string) (*Plan, string, int) {
	for path, p := range allPlans(home) {
		for i, b := range p.Batches {
			if b.Report == report {
				return p, path, i
			}
		}
	}
	die(1, report+" is not in a feature plan under "+filepath.Join(home, "slices"))
	return nil, "", 0
}

func abbrev(name string) string {
	var initials, letters []rune
	prev := ' '
	for _, r := range name {
		if unicode.IsLetter(r) && r < unicode.MaxASCII {
			if !unicode.IsLetter(prev) && !unicode.IsDigit(prev) || unicode.IsUpper(r) && unicode.IsLower(prev) {
				initials = append(initials, unicode.ToUpper(r))
			}
			letters = append(letters, unicode.ToUpper(r))
		}
		prev = r
	}
	switch {
	case len(initials) >= 2:
		return string(initials)
	case len(letters) >= 2:
		return string(letters[:2])
	case len(letters) == 1:
		return string(letters) + string(letters)
	}
	return "RV"
}

func newKey(home, abbr, self string) string {
	plans := allPlans(home)
	for k := 1; ; k++ {
		key := abbr + strconv.Itoa(k)
		taken := false
		for path, p := range plans {
			taken = taken || (p.Key == key && path != self)
		}
		if reports, _ := filepath.Glob(filepath.Join(home, "reports", key+"-*.js")); len(reports) > 0 {
			taken = true
		}
		if !taken {
			return key
		}
	}
}

type flags struct {
	vals  map[string]string
	lists map[string][]string
	bools map[string]bool
}

func parseFlags(a []string, usage string, single, list, boolean []string) flags {
	f := flags{map[string]string{}, map[string][]string{}, map[string]bool{}}
	for i := 0; i < len(a); i++ {
		name, ok := strings.CutPrefix(a[i], "--")
		switch {
		case ok && slices.Contains(boolean, name):
			f.bools[name] = true
		case ok && slices.Contains(list, name):
			for i+1 < len(a) && !strings.HasPrefix(a[i+1], "--") {
				i++
				f.lists[name] = append(f.lists[name], a[i])
			}
		case ok && slices.Contains(single, name) && i+1 < len(a):
			i++
			f.vals[name] = a[i]
		default:
			die(2, usage)
		}
	}
	return f
}

func cmdSlice(a []string) {
	const usage = "usage: slice plan --repo <repo> --range <base>..<head> --features features.json [--replace]\n" +
		"       slice next (--repo <repo> --range <base>..<head> | --report <ID>) [--files <path>...]"
	if len(a) == 0 {
		die(2, usage)
	}
	switch a[0] {
	case "plan":
		slicePlan(parseFlags(a[1:], usage, []string{"repo", "range", "features"}, nil, []string{"replace"}))
	case "next":
		sliceNext(parseFlags(a[1:], usage, []string{"repo", "range", "report"}, []string{"files"}, nil))
	default:
		die(2, usage)
	}
}

func resolveRange(repo, rng string) (string, string) {
	b, h, ok := strings.Cut(rng, "..")
	if !ok || b == "" || h == "" || strings.HasPrefix(h, ".") {
		die(2, "--range must be <base>..<head>, got "+strconv.Quote(rng))
	}
	base, head := fullSha(repo, b), fullSha(repo, h)
	if !gitOK(repo, "merge-base", "--is-ancestor", base, head) {
		die(1, fmt.Sprintf("%s is not an ancestor of %s", b, h))
	}
	return base, head
}

func readFeatures(repo, path string, rg rangeInfo) []Feature {
	raw, err := os.ReadFile(path)
	if err != nil {
		die(2, fmt.Sprintf("read %s: %v", path, err))
	}
	var in struct {
		Features []Feature `json:"features"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&in); err != nil {
		die(1, fmt.Sprintf("parse %s: %v", path, err))
	}
	if len(in.Features) == 0 {
		die(1, path+" has no features")
	}
	var errs []string
	seen := map[string]string{}
	for i := range in.Features {
		f := &in.Features[i]
		if strings.TrimSpace(f.Name) == "" || strings.TrimSpace(f.Change) == "" {
			errs = append(errs, fmt.Sprintf("feature %d needs a name and a change", i+1))
		}
		for j, c := range f.Commits {
			full, ok := gitTry(repo, "rev-parse", "--verify", "-q", c+"^{commit}")
			full = strings.TrimSpace(full)
			switch {
			case !ok:
				errs = append(errs, fmt.Sprintf("%s (feature %d) is not a commit", c, i+1))
			case !hasKey(rg.order, full):
				errs = append(errs, fmt.Sprintf("%s (feature %d) is not in the range", c, i+1))
			case seen[full] != "":
				errs = append(errs, fmt.Sprintf("%s is in feature %s and feature %d", c, seen[full], i+1))
			default:
				seen[full] = strconv.Itoa(i + 1)
				f.Commits[j] = full
			}
		}
	}
	for _, c := range rg.sorted(boolSet(rg.order)) {
		if seen[c] == "" && !rg.merges[c] {
			errs = append(errs, fmt.Sprintf("%s %s is in no feature", c[:10], strings.TrimSpace(git(repo, "log", "-1", "--format=%s", c))))
		}
	}
	if len(errs) > 0 {
		die(1, fmt.Sprintf("%s does not partition the range:\n  %s", path, strings.Join(errs, "\n  ")))
	}
	return in.Features
}

func boolSet(m map[string]int) map[string]bool {
	out := map[string]bool{}
	for k := range m {
		out[k] = true
	}
	return out
}

func slicePlan(f flags) {
	repo, rng, featuresPath := f.vals["repo"], f.vals["range"], f.vals["features"]
	if repo == "" || rng == "" || featuresPath == "" {
		die(2, "usage: slice plan --repo <repo> --range <base>..<head> --features features.json [--replace]")
	}
	top := toplevel(repo)
	base, head := resolveRange(top, rng)
	rg := readRange(top, base, head)
	features := readFeatures(top, featuresPath, rg)
	home := archifyHome()
	path := planPath(home, base, head)
	p, err := readPlan(path)
	switch {
	case os.IsNotExist(err):
		p = &Plan{Version: planVersion, Repo: top, Base: base, Head: head, Batches: []Batch{}, Hunks: attribute(top, base, head)}
		p.Key = newKey(home, abbrev(filepath.Base(top)), path)
	case err != nil:
		die(1, err.Error())
	}
	same := featuresEqual(p.Features, features)
	if len(p.Batches) > 0 && (!same || f.bools["replace"]) {
		if !f.bools["replace"] {
			die(1, fmt.Sprintf("%s already has reports %s; pass --replace to drop them and plan again", path, reportList(p.Batches)))
		}
		for _, b := range p.Batches {
			if delivered(home, b.Report) {
				p.Key = newKey(home, abbr(p.Key), path)
				break
			}
		}
		p.Batches = []Batch{}
	}
	p.Features = features
	writeAtomic(path, pretty(p))
	printPlan(p, rg)
	fmt.Printf("-> %s\n", path)
}

func abbr(key string) string { return strings.TrimRight(key, "0123456789") }

func reportList(bs []Batch) string {
	var ids []string
	for _, b := range bs {
		ids = append(ids, b.Report)
	}
	return strings.Join(ids, ", ")
}

func featuresEqual(a, b []Feature) bool {
	return slices.EqualFunc(a, b, func(x, y Feature) bool {
		return x.Name == y.Name && x.Change == y.Change && slices.Equal(x.Commits, y.Commits)
	})
}

func printPlan(p *Plan, rg rangeInfo) {
	owners := p.owners()
	fmt.Printf("plan %s  %s..%s  %d commits, %d files -> %d features\n", p.Key, p.Base[:10], p.Head[:10], len(rg.order), len(finalFiles(p)), len(p.Features))
	fmt.Println(" #  commits  files  hunks  feature: user-facing change")
	for i, ft := range p.Features {
		hunks := 0
		for _, o := range owners {
			if o == i {
				hunks++
			}
		}
		files := len(p.files(owners, i))
		line := fmt.Sprintf("%2d  %7d  %5d  %5d  %s: %s", i+1, len(ft.Commits), files, hunks, ft.Name, ft.Change)
		if files == 0 {
			line += fmt.Sprintf(" (nothing survives to %s)", p.Head[:10])
		}
		fmt.Println(line)
	}
	featureOf := map[string]int{}
	for i, ft := range p.Features {
		for _, c := range ft.Commits {
			featureOf[c] = i
		}
	}
	mixed, orphan := 0, 0
	for _, h := range p.Hunks {
		fs := map[int]bool{}
		for _, c := range h.Commits {
			if f, ok := featureOf[c]; ok {
				fs[f] = true
			}
		}
		switch {
		case len(fs) == 0:
			orphan++
		case len(fs) > 1:
			mixed++
		}
	}
	if mixed > 0 {
		fmt.Printf("note: %d hunks hold lines of several features; each belongs to the feature that wrote most of its lines, the earliest on a tie\n", mixed)
	}
	if orphan > 0 {
		fmt.Printf("note: %d hunks were written by no commit of a feature (a merge resolution, or lines older than the range); they belong to the last feature, %s\n", orphan, p.Features[len(p.Features)-1].Name)
	}
}

func finalFiles(p *Plan) []string {
	var out []string
	for _, h := range p.Hunks {
		if !slices.Contains(out, h.File) {
			out = append(out, h.File)
		}
	}
	return out
}

func sliceNext(f flags) {
	home := archifyHome()
	var p *Plan
	var path string
	after := -1
	switch {
	case f.vals["report"] != "" && f.vals["range"] == "":
		var b int
		p, path, b = findBatch(home, f.vals["report"])
		after = b
	case f.vals["range"] != "" && f.vals["repo"] != "" && f.vals["report"] == "":
		top := toplevel(f.vals["repo"])
		base, head := resolveRange(top, f.vals["range"])
		path = planPath(home, base, head)
		var err error
		if p, err = readPlan(path); err != nil {
			die(1, fmt.Sprintf("no feature plan for %s..%s (%v): run slice plan first", base[:10], head[:10], err))
		}
	default:
		die(2, "usage: slice next (--repo <repo> --range <base>..<head> | --report <ID>) [--files <path>...]")
	}
	owners := p.owners()
	target := after + 1
	picked := f.lists["files"]
	if target < len(p.Batches) {
		b := p.Batches[target]
		if delivered(home, b.Report) {
			if len(picked) > 0 {
				die(1, b.Report+" is already delivered and cannot be re-cut")
			}
			fmt.Printf("%s is already delivered: review.html?report=%s (refresh %s rebuilds it)\n", b.Report, b.Report, b.Report)
			return
		}
		if len(picked) == 0 {
			printBatch(p, owners, target)
			return
		}
		if target != len(p.Batches)-1 {
			die(1, fmt.Sprintf("%s is followed by %s; only the last batch can be re-cut", b.Report, p.Batches[target+1].Report))
		}
		_, cands := p.candidates(owners, target-1)
		p.Batches[target].Files = pickFiles(p, owners, b.Feature, b.Report, cands, picked)
		writeAtomic(path, pretty(p))
		printBatch(p, owners, target)
		return
	}
	feature, cands := p.candidates(owners, after)
	if feature < 0 {
		where := "the start"
		if after >= 0 {
			where = p.Batches[after].Report
		}
		fmt.Printf("nothing left after %s: %s..%s is fully reviewed\n", where, p.Base[:10], p.Head[:10])
		return
	}
	report := fmt.Sprintf("%s-%d", p.Key, target+1)
	files := cands
	if len(picked) > 0 || len(cands) > batchBudget {
		files = pickFiles(p, owners, feature, report, cands, picked)
	}
	p.Batches = append(p.Batches, Batch{Report: report, Feature: feature, Files: files})
	writeAtomic(path, pretty(p))
	printBatch(p, owners, target)
}

func pickFiles(p *Plan, owners []int, feature int, report string, cands, picked []string) []string {
	if len(picked) == 0 {
		fmt.Printf("%s needs --files: %d files are left (at most %d per report; keep a test with its tested file, and a flow's files together)\n", report, len(cands), batchBudget)
		printFiles(p, owners, feature, cands)
		die(1, fmt.Sprintf("pick at most %d files with --files", batchBudget))
	}
	var errs []string
	for i, x := range picked {
		if !slices.Contains(cands, x) {
			errs = append(errs, x+" is not a file this batch can take")
		}
		if slices.Contains(picked[:i], x) {
			errs = append(errs, x+" is listed twice")
		}
	}
	if len(picked) > batchBudget {
		errs = append(errs, fmt.Sprintf("%d files given, at most %d", len(picked), batchBudget))
	}
	if len(errs) > 0 {
		printFiles(p, owners, feature, cands)
		die(1, strings.Join(errs, "; "))
	}
	return picked
}

func printFiles(p *Plan, owners []int, feature int, files []string) {
	for _, x := range files {
		n := 0
		for i, h := range p.Hunks {
			if h.File == x && h.Header != "" && owners[i] == feature {
				n++
			}
		}
		fmt.Printf("  %s [%d hunks]\n", x, n)
	}
}

func printBatch(p *Plan, owners []int, b int) {
	bt := p.Batches[b]
	left := "nothing after it"
	if nx := p.next(owners, b); nx != nil && nx.Kind == "batch" {
		left = fmt.Sprintf("%d files of this feature left after it", nx.Files)
	} else if nx != nil {
		left = "next feature: " + nx.Name
	}
	fmt.Printf("%s  feature %d/%d %s · batch %d · %d files · %s\n", bt.Report, bt.Feature+1, len(p.Features), p.Features[bt.Feature].Name, p.batchNo(b), len(bt.Files), left)
	printFiles(p, owners, bt.Feature, bt.Files)
}

func sliceOut(repo, report string) Out {
	p, path, b := findBatch(archifyHome(), report)
	if top := toplevel(repo); top != p.Repo {
		die(1, fmt.Sprintf("%s belongs to %s, not %s", report, p.Repo, top))
	}
	bt := p.Batches[b]
	owners := p.owners()
	want := map[[2]string]bool{}
	commits := map[string]bool{}
	for i, h := range p.Hunks {
		if owners[i] == bt.Feature && slices.Contains(bt.Files, h.File) {
			want[[2]string{h.File, h.Header}] = true
			for _, c := range h.Commits {
				commits[c] = true
			}
		}
	}
	var all []Hunk
	parsePatch(finalDiff(p.Repo, p.Base, p.Head, []string{"-U2"}, bt.Files...), p.Head[:10], &all)
	hunks := []Hunk{}
	for _, h := range all {
		if want[[2]string{h.File, h.Header}] {
			h.N = len(hunks) + 1
			hunks = append(hunks, h)
			delete(want, [2]string{h.File, h.Header})
		}
	}
	for k := range want {
		if k[1] != "" {
			die(1, fmt.Sprintf("%s: %s %s is not in git diff %s..%s any more; run slice plan --replace", path, k[0], k[1], p.Base[:10], p.Head[:10]))
		}
	}
	files := []any{}
	for _, st := range finalStats(p.Repo, p.Base, p.Head, bt.Files...) {
		e := map[string]any{"path": st.path, "status": st.status}
		if st.add != "-" {
			add, del := 0, 0
			for _, h := range hunks {
				if h.File != st.path {
					continue
				}
				for _, l := range h.Lines {
					switch l.Sign {
					case "+":
						add++
					case "-":
						del++
					}
				}
			}
			if !hasHunks(p, st.path) {
				add, _ = strconv.Atoi(st.add)
				del, _ = strconv.Atoi(st.del)
			}
			e["additions"], e["deletions"] = add, del
		}
		files = append(files, e)
	}
	rg := readRange(p.Repo, p.Base, p.Head)
	list := []any{}
	for _, c := range rg.sorted(commits) {
		list = append(list, map[string]any{"sha": c[:10], "subject": strings.TrimSpace(git(p.Repo, "log", "-1", "--format=%s", c))})
	}
	slice := Slice{Report: bt.Report, Base: p.Base, Head: p.Head, Feature: p.Features[bt.Feature].Name, Batch: p.batchNo(b), Next: p.next(owners, b)}
	return Out{Changes: map[string]any{"commits": list, "files": files, "slice": slice}, Hunks: hunks}
}

func hasHunks(p *Plan, file string) bool {
	for _, h := range p.Hunks {
		if h.File == file && h.Header != "" {
			return true
		}
	}
	return false
}

// review-kit: deterministic git-hunk helpers for archify code-review runs.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Ln struct {
	Sign  string `json:"sign"`
	OldNo *int   `json:"oldNo"`
	NewNo *int   `json:"newNo"`
}

type Hunk struct {
	N      int    `json:"n"`
	Commit string `json:"commit"`
	File   string `json:"file"`
	Header string `json:"header"`
	Text   string `json:"text"`
	Lines  []Ln   `json:"lines"`
}

type Out struct {
	Changes any    `json:"changes"`
	Hunks   []Hunk `json:"hunks"`
}

func die(code int, msg string) {
	fmt.Fprintf(os.Stderr, "review-kit: %s\n", msg)
	os.Exit(code)
}

func git(repo string, args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			die(2, "git: "+err.Error())
		}
		die(1, fmt.Sprintf("git %s failed: %s", strings.Join(args, " "), strings.TrimSpace(stderr.String())))
	}
	return string(out)
}

// Pretty JSON like serde_json::to_string_pretty: 2-space indent, no HTML escaping.
func pretty(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		die(2, "encode: "+err.Error())
	}
	return b.Bytes()
}

// Compact JSON like serde_json Value Display.
func compact(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

func decode(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(v)
}

func intp(n int) *int { return &n }

// Formats an int list as [1, 2].
func listStr(xs []int) string {
	s := make([]string, len(xs))
	for i, x := range xs {
		s[i] = strconv.Itoa(x)
	}
	return "[" + strings.Join(s, ", ") + "]"
}

// Parses "@@ -a[,b] +c[,d] @@" into (old start, new start).
func parseHeader(l string) (int, int, bool) {
	rest, ok := strings.CutPrefix(l, "@@ -")
	if !ok {
		return 0, 0, false
	}
	old, rest, ok := strings.Cut(rest, " +")
	if !ok {
		return 0, 0, false
	}
	nw, _, ok := strings.Cut(rest, " @@")
	if !ok {
		return 0, 0, false
	}
	start := func(s string) (int, bool) {
		f, _, _ := strings.Cut(s, ",")
		n, err := strconv.ParseUint(f, 10, 32)
		return int(n), err == nil
	}
	o, ok1 := start(old)
	n, ok2 := start(nw)
	return o, n, ok1 && ok2
}

func number(header string, body []string) []Ln {
	o, n, _ := parseHeader(header)
	out := make([]Ln, 0, len(body))
	for _, l := range body {
		c := l[:1]
		if c == "\\" {
			out = append(out, Ln{Sign: c})
			continue
		}
		r := Ln{Sign: c}
		if c != "+" {
			r.OldNo = intp(o)
			o++
		}
		if c != "-" {
			r.NewNo = intp(n)
			n++
		}
		out = append(out, r)
	}
	return out
}

func parsePatch(patch, commit string, out *[]Hunk) {
	file := ""
	var header string
	var body []string
	open := false
	flush := func() {
		if !open {
			return
		}
		text := strings.Join(append([]string{header}, body...), "\n")
		*out = append(*out, Hunk{N: len(*out) + 1, Commit: commit, File: file, Header: header, Text: text, Lines: number(header, body)})
		open = false
	}
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			file = ""
		} else if p, ok := strings.CutPrefix(line, "--- a/"); ok && !open {
			file = strings.TrimSuffix(p, "\t") // git appends a tab after names containing spaces
		} else if p, ok := strings.CutPrefix(line, "+++ b/"); ok && !open {
			file = strings.TrimSuffix(p, "\t")
		} else if _, _, ok := parseHeader(line); ok {
			flush()
			header, body, open = line, []string{}, true
		} else if open && line != "" && strings.ContainsRune(" +-\\", rune(line[0])) {
			body = append(body, line)
		}
	}
	flush()
}

// gitOK runs git and reports whether it exited 0 (for yes/no queries such as merge-base --is-ancestor).
func gitOK(repo string, args ...string) bool {
	return exec.Command("git", append([]string{"-C", repo}, args...)...).Run() == nil
}

// diffFlags pin raw UTF-8 paths and a/ b/ prefixes regardless of user git config.
var diffFlags = []string{"--no-renames", "--no-ext-diff", "--src-prefix=a/", "--dst-prefix=b/"}

type commitInfo struct{ full, short string }

// fileStat is one file's status and numstat line, from `--name-status -z` and `--numstat -z` output.
type fileStat struct {
	path, status, add, del string
}

func parseStats(nameStatus, numstat string) []fileStat {
	statusOf := map[string]string{}
	ns := strings.Split(nameStatus, "\x00") // status, path, status, path, ...
	for i := 0; i+1 < len(ns); i += 2 {
		v := "modified"
		if strings.HasPrefix(ns[i], "A") {
			v = "added"
		} else if strings.HasPrefix(ns[i], "D") {
			v = "deleted"
		}
		statusOf[ns[i+1]] = v
	}
	var out []fileStat
	for _, l := range strings.Split(numstat, "\x00") {
		p := strings.SplitN(l, "\t", 3)
		if len(p) < 3 {
			continue
		}
		st := statusOf[p[2]]
		if st == "" {
			st = "modified"
		}
		out = append(out, fileStat{p[2], st, p[0], p[1]})
	}
	return out
}

// netRange returns the base and last commit whose diff is the net change of `touch` to `file`, and the
// unselected commits in that range that changed the file too (their lines are part of the net diff);
// or a reason why the per-commit hunks must stay (the commits are not on one line of history).
func netRange(repo, file string, touch []commitInfo, selected map[string]bool) (base, last string, outside []string, reason string) {
	first, lastC := touch[0], touch[0]
	for _, c := range touch[1:] {
		switch {
		case gitOK(repo, "merge-base", "--is-ancestor", c.full, first.full):
			first = c
		case gitOK(repo, "merge-base", "--is-ancestor", lastC.full, c.full):
			lastC = c
		}
	}
	for _, c := range touch {
		if !gitOK(repo, "merge-base", "--is-ancestor", first.full, c.full) || !gitOK(repo, "merge-base", "--is-ancestor", c.full, lastC.full) {
			return "", "", nil, "its commits are not on one line of history"
		}
	}
	if gitOK(repo, "rev-parse", "--verify", "-q", first.full+"^") {
		base = strings.TrimSpace(git(repo, "rev-parse", first.full+"^"))
	} else {
		base = strings.TrimSpace(git(repo, "hash-object", "-t", "tree", "--stdin")) // root commit: empty tree
	}
	rangeArg := lastC.full
	if !strings.HasPrefix(git(repo, "cat-file", "-t", base), "tree") {
		rangeArg = base + ".." + lastC.full
	}
	for _, h := range strings.Fields(git(repo, "--literal-pathspecs", "log", "--format=%H", rangeArg, "--", file)) {
		if !selected[h] {
			outside = append(outside, h[:10])
		}
	}
	return base, lastC.full, outside, ""
}

func cmdHunks(a []string) {
	var repo, outp, report string
	var shas []string
	for i := 0; i < len(a); i++ {
		switch a[i] {
		case "--repo", "--out", "--report":
			v := ""
			if i+1 < len(a) {
				v = a[i+1]
			}
			switch a[i] {
			case "--repo":
				repo = v
			case "--out":
				outp = v
			default:
				report = v
			}
			i++
		default:
			shas = append(shas, a[i])
		}
	}
	if repo == "" || outp == "" || (len(shas) == 0) == (report == "") {
		die(2, "usage: hunks --repo <repo> (<sha>... | --report <ID>) --out hunks.json")
	}
	if report != "" {
		out := sliceOut(repo, report)
		if err := os.WriteFile(outp, pretty(out), 0o644); err != nil {
			die(2, fmt.Sprintf("write %s: %v", outp, err))
		}
		ch := out.Changes.(map[string]any)
		fmt.Printf("%d hunks, %d files, %d commits -> %s\n", len(out.Hunks), len(ch["files"].([]any)), len(ch["commits"].([]any)), outp)
		return
	}
	show := func(sha string, extra ...string) string {
		return git(repo, append(append([]string{"-c", "core.quotepath=false", "show", sha, "--format="}, diffFlags...), extra...)...)
	}
	var infos []commitInfo
	selected := map[string]bool{}
	commits := []any{}
	stats := map[string][]fileStat{}
	touchers := map[string][]commitInfo{}
	for _, s := range shas {
		full := strings.TrimSpace(git(repo, "rev-parse", "--verify", s+"^{commit}"))
		if len(strings.Fields(git(repo, "rev-list", "--parents", "-n1", s))) > 2 {
			die(1, s+" is a merge commit; pass the non-merge commits it contains instead")
		}
		c := commitInfo{full, full[:10]}
		infos = append(infos, c)
		selected[full] = true
		commits = append(commits, map[string]any{"sha": c.short, "subject": strings.TrimSpace(git(repo, "log", "-1", "--format=%s", s))})
		stats[full] = parseStats(show(full, "--name-status", "-z"), show(full, "--numstat", "-z"))
		for _, st := range stats[full] {
			touchers[st.path] = append(touchers[st.path], c)
		}
	}
	// netAt[file] = the commit whose turn emits the file's net hunks; skipped by the other commits.
	netAt := map[string]string{}
	netBase := map[string]string{}
	for _, c := range infos {
		for _, st := range stats[c.full] {
			t := touchers[st.path]
			if len(t) < 2 || netAt[st.path] != "" || netBase[st.path] == "-" {
				continue
			}
			base, last, outside, reason := netRange(repo, st.path, t, selected)
			if reason != "" {
				netBase[st.path] = "-"
				fmt.Printf("note: %s keeps per-commit hunks: %s\n", st.path, reason)
				continue
			}
			if len(outside) > 0 {
				fmt.Printf("note: %s net diff includes commits outside the selection: %s\n", st.path, strings.Join(outside, ", "))
			}
			netAt[st.path], netBase[st.path] = last, base
		}
	}
	hunks := []Hunk{}
	var files []map[string]any
	fileOf := func(path string) map[string]any {
		for _, e := range files {
			if e["path"] == path {
				return e
			}
		}
		f := map[string]any{"path": path}
		files = append(files, f)
		return f
	}
	addStat := func(f map[string]any, st fileStat) {
		if st.add == "-" {
			f["binary"] = true
			return
		}
		for _, kv := range [][2]string{{"additions", st.add}, {"deletions", st.del}} {
			prev, _ := f[kv[0]].(uint64)
			n, _ := strconv.ParseUint(kv[1], 10, 64)
			f[kv[0]] = prev + n
		}
	}
	for _, c := range infos {
		var own []string
		for _, st := range stats[c.full] {
			last := netAt[st.path]
			if last == "" {
				own = append(own, st.path)
				f := fileOf(st.path)
				// across commits: deleted wins, otherwise keep "added" once seen
				if prev, _ := f["status"].(string); st.status == "deleted" || prev != "added" {
					f["status"] = st.status
				}
				addStat(f, st)
				continue
			}
			if last != c.full {
				continue
			}
			netDiff := func(extra ...string) string {
				args := append([]string{"--literal-pathspecs", "-c", "core.quotepath=false", "diff", netBase[st.path], last}, diffFlags...)
				return git(repo, append(append(args, extra...), "--", st.path)...)
			}
			net := parseStats(netDiff("--name-status", "-z"), netDiff("--numstat", "-z"))
			if len(net) == 0 {
				fmt.Printf("note: %s has no net change across its commits\n", st.path)
				continue
			}
			f := fileOf(st.path)
			f["status"] = net[0].status
			addStat(f, net[0])
			parsePatch(netDiff("-U2"), c.short, &hunks)
		}
		if len(own) > 0 {
			parsePatch(git(repo, append(append([]string{"--literal-pathspecs", "-c", "core.quotepath=false", "show", c.full, "--format="}, diffFlags...), append([]string{"-U2", "--"}, own...)...)...), c.short, &hunks)
		}
	}
	// spec shape: binary files carry no counts and no binary flag
	outFiles := []any{}
	for _, f := range files {
		if _, ok := f["binary"]; ok {
			delete(f, "binary")
			delete(f, "additions")
			delete(f, "deletions")
		}
		outFiles = append(outFiles, f)
	}
	out := Out{Changes: map[string]any{"commits": commits, "files": outFiles}, Hunks: hunks}
	if err := os.WriteFile(outp, pretty(out), 0o644); err != nil {
		die(2, fmt.Sprintf("write %s: %v", outp, err))
	}
	fmt.Printf("%d hunks, %d files, %d commits -> %s\n", len(hunks), len(files), len(shas), outp)
}

func load(a []string) (Out, string, []int, []string) {
	var path, file string
	var sel []int
	var rest []string
	digits := regexp.MustCompile(`^[0-9,]*$`)
	for i := 0; i < len(a); i++ {
		switch a[i] {
		case "--hunks":
			if i+1 < len(a) {
				i++
				path = a[i]
			}
		case "--file":
			if i+1 < len(a) {
				i++
				file = a[i]
			}
		case "--hunk":
			for i+1 < len(a) && digits.MatchString(a[i+1]) {
				i++
				for _, n := range strings.Split(a[i], ",") {
					if v, err := strconv.Atoi(n); err == nil && v >= 0 {
						sel = append(sel, v)
					}
				}
			}
		default:
			rest = append(rest, a[i])
		}
	}
	if path == "" || file == "" {
		die(2, "usage: --hunks hunks.json --file <path> [--hunk N...]")
	}
	return readHunks(path), file, sel, rest
}

func readHunks(path string) Out {
	raw, err := os.ReadFile(path)
	if err != nil {
		die(2, fmt.Sprintf("read %s: %v", path, err))
	}
	var out Out
	if err := decode(raw, &out); err != nil {
		die(2, fmt.Sprintf("parse %s: %v", path, err))
	}
	return out
}

type numbered struct {
	k int
	h *Hunk
}

// Hunks of `file` in commit order, numbered 1.. per file.
func fileHunks(out *Out, file string) []numbered {
	var r []numbered
	for i := range out.Hunks {
		if out.Hunks[i].File == file {
			r = append(r, numbered{len(r) + 1, &out.Hunks[i]})
		}
	}
	return r
}

// --hunk N picks the Nth (1-based) hunk of `file`.
func pick(out *Out, file string, sel []int) []numbered {
	all := fileHunks(out, file)
	if len(all) == 0 {
		die(1, "no hunks for "+file)
	}
	if len(sel) == 0 {
		return all
	}
	var r []numbered
	for _, n := range sel {
		if n < 1 || n > len(all) {
			die(1, fmt.Sprintf("%s has %d hunks, no hunk %d", file, len(all), n))
		}
		r = append(r, all[n-1])
	}
	return r
}

func cmdDiff(a []string) {
	out, file, sel, _ := load(a)
	var texts []string
	for _, x := range pick(&out, file, sel) {
		texts = append(texts, x.h.Text)
	}
	fmt.Println(strings.Join(texts, "\n"))
}

// "+290", "-5", "+290..+300" or "+290..300"
func parseRange(p string) (byte, int, int, bool) {
	if p == "" || (p[0] != '+' && p[0] != '-') {
		return 0, 0, 0, false
	}
	a, b, ok := strings.Cut(p[1:], "..")
	if !ok {
		b = a
	}
	lo, err1 := strconv.ParseUint(a, 10, 32)
	hi, err2 := strconv.ParseUint(strings.TrimLeft(b, "+-"), 10, 32)
	if err1 != nil || err2 != nil || lo > hi {
		return 0, 0, 0, false
	}
	return p[0], int(lo), int(hi), true
}

// Canonical `lines` string for `parts` ("+a..+b,-c"); every number must sit in exactly one hunk of `pool`.
func canonLines(pool []numbered, file, scope, hint string, parts []string) (string, error) {
	var canon []string
	for _, r := range parts {
		for _, p := range strings.Split(r, ",") {
			if p == "" {
				continue
			}
			sign, lo, hi, ok := parseRange(p)
			if !ok {
				return "", fmt.Errorf("bad range %s, use +a, -b or +a..+c", strconv.Quote(p))
			}
			for no := lo; no <= hi; no++ {
				var hits []int
				for _, x := range pool {
					for _, l := range x.h.Lines {
						if (sign == '+' && l.Sign != "-" && l.NewNo != nil && *l.NewNo == no) ||
							(sign == '-' && l.Sign != "+" && l.OldNo != nil && *l.OldNo == no) {
							hits = append(hits, x.k)
							break
						}
					}
				}
				switch len(hits) {
				case 0:
					kind := "old-file"
					if sign == '+' {
						kind = "new-file or context"
					}
					return "", fmt.Errorf("%c%d is not a %s line of %s%s", sign, no, kind, file, scope)
				case 1:
				default:
					return "", fmt.Errorf("%c%d is in hunks %s of %s; %s", sign, no, listStr(hits), file, hint)
				}
			}
			if lo == hi {
				canon = append(canon, fmt.Sprintf("%c%d", sign, lo))
			} else {
				canon = append(canon, fmt.Sprintf("%c%d..%c%d", sign, lo, sign, hi))
			}
		}
	}
	return strings.Join(canon, ","), nil
}

func cmdLines(a []string) {
	out, file, sel, rest := load(a)
	pool := pick(&out, file, sel)
	scope := ""
	if len(sel) > 0 {
		scope = " hunk " + listStr(sel)
	}
	lines, err := canonLines(pool, file, scope, "pass --hunk N", rest)
	if err != nil {
		die(1, err.Error())
	}
	if lines == "" {
		die(2, "usage: lines --hunks hunks.json --file <path> [--hunk N] +a..+b,-c")
	}
	fmt.Println(lines)
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' }

func cmdCaller(a []string) {
	const usage = "usage: caller --repo <repo> --sha <sha> --file <path> --symbol <name>"
	var repo, sha, file, sym string
	for i := 0; i < len(a); i += 2 {
		v := ""
		if i+1 < len(a) {
			v = a[i+1]
		}
		switch a[i] {
		case "--repo":
			repo = v
		case "--sha":
			sha = v
		case "--file":
			file = v
		case "--symbol":
			sym = v
		default:
			die(2, usage)
		}
	}
	if repo == "" || sha == "" || file == "" || sym == "" {
		die(2, usage)
	}
	src := git(repo, "show", sha+":"+file)
	found := false
	for i, l := range strings.Split(strings.TrimSuffix(src, "\n"), "\n") {
		l = strings.TrimSuffix(l, "\r")
		hit := false
		for off := 0; !hit; {
			p := strings.Index(l[off:], sym)
			if p < 0 {
				break
			}
			p += off
			before, _ := utf8.DecodeLastRuneInString(l[:p])
			after, _ := utf8.DecodeRuneInString(l[p+len(sym):])
			hit = !(p > 0 && isWord(before)) && !(p+len(sym) < len(l) && isWord(after))
			off = p + len(sym)
		}
		if hit {
			found = true
			fmt.Printf("%s:%d: %s\n", file, i+1, strings.TrimSpace(l))
		}
	}
	if !found {
		die(1, fmt.Sprintf("%s not found in %s:%s", sym, sha, file))
	}
}

func main() {
	a := os.Args[1:]
	cmd := ""
	if len(a) > 0 {
		cmd = a[0]
	}
	switch cmd {
	case "hunks":
		cmdHunks(a[1:])
	case "lines":
		cmdLines(a[1:])
	case "diff":
		cmdDiff(a[1:])
	case "caller":
		cmdCaller(a[1:])
	case "assemble":
		cmdAssemble(a[1:])
	case "slice":
		cmdSlice(a[1:])
	case "risk":
		cmdRisk(a[1:])
	default:
		die(2, "commands: hunks | lines | diff | caller | assemble | slice | risk (see references/code-review-pipeline.md)")
	}
}

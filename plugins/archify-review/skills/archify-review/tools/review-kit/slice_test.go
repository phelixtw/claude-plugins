package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func sliceRepo(t *testing.T) (repo, home string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo = filepath.Join(t.TempDir(), "acme-cart")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	rkGit(t, repo, "init", "-q", "-b", "main")
	home = filepath.Join(t.TempDir(), "home")
	t.Setenv("ARCHIFY_REVIEW_HOME", home)
	return repo, home
}

func seqLines(prefix string, n int, edit map[int]string, insertAfter map[int]string) string {
	var out []string
	for i := 1; i <= n; i++ {
		if v, ok := edit[i]; !ok {
			out = append(out, fmt.Sprintf("%s%d", prefix, i))
		} else if v != "" {
			out = append(out, v)
		}
		if v, ok := insertAfter[i]; ok {
			out = append(out, v)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

type sliceFixture struct {
	repo, home, base, head string
	c                      map[string]string
}

func newSliceFixture(t *testing.T) sliceFixture {
	t.Helper()
	repo, home := sliceRepo(t)
	promo := func(edit map[int]string, ins map[int]string) {
		rkWrite(t, repo, "lib/promo.dart", seqLines("t", 20, edit, ins))
	}
	chat := func(edit map[int]string) { rkWrite(t, repo, "lib/chat.dart", seqLines("c", 10, edit, nil)) }
	readme := func(edit map[int]string) { rkWrite(t, repo, "docs/readme.md", seqLines("r", 3, edit, nil)) }
	c := map[string]string{}
	promo(nil, nil)
	chat(nil)
	readme(nil)
	rkWrite(t, repo, "lib/config.dart", seqLines("k", 5, nil, nil))
	rkWrite(t, repo, "lib/old.dart", seqLines("o", 5, nil, nil))
	c["base"] = rkCommit(t, repo, "base")
	promo(map[int]string{3: "expiry()"}, map[int]string{15: "scratch()"})
	c["A1"] = rkCommit(t, repo, "promo: expiry label")
	promo(map[int]string{3: "expiry()", 5: "chat-near()", 18: "chat-hook()"}, map[int]string{15: "scratch()"})
	chat(map[int]string{5: "send()"})
	c["B1"] = rkCommit(t, repo, "chat: send")
	promo(map[int]string{3: "expiry()", 5: "chat-near()", 18: "chat-hook()"}, nil)
	c["A2"] = rkCommit(t, repo, "promo: drop scratch")
	promo(map[int]string{3: "expiry()", 5: "chat-near()", 12: "", 18: "chat-hook()"}, nil)
	os.Remove(filepath.Join(repo, "lib/old.dart"))
	c["B2"] = rkCommit(t, repo, "chat: drop old")
	readme(map[int]string{1: "intro"})
	rkWrite(t, repo, "assets/logo.png", "\x89PNG\x00\x01\x02")
	c["D1"] = rkCommit(t, repo, "docs: intro")
	rkGit(t, repo, "checkout", "-q", "-b", "side")
	chat(map[int]string{5: "send()", 9: "receive()"})
	c["S"] = rkCommit(t, repo, "chat: receive")
	rkGit(t, repo, "checkout", "-q", "main")
	readme(map[int]string{1: "intro", 3: "faq"})
	c["M"] = rkCommit(t, repo, "docs: faq")
	rkGit(t, repo, "merge", "-q", "--no-ff", "--no-commit", "side")
	rkWrite(t, repo, "lib/config.dart", seqLines("k", 5, nil, map[int]string{5: "evil()"}))
	c["merge"] = rkCommit(t, repo, "merge side")
	return sliceFixture{repo: repo, home: home, base: c["base"], head: c["merge"], c: c}
}

func (f sliceFixture) features(t *testing.T, groups ...[]string) string {
	t.Helper()
	names := []string{"Promo", "Chat", "Docs"}
	var fs []map[string]any
	for i, g := range groups {
		var shas []string
		for _, k := range g {
			shas = append(shas, f.c[k][:8])
		}
		fs = append(fs, map[string]any{"name": names[i], "change": names[i] + " changes for the user", "commits": shas})
	}
	return writeJSON(t, "features.json", map[string]any{"features": fs})
}

func (f sliceFixture) plan(t *testing.T, extra ...string) string {
	t.Helper()
	feats := f.features(t, []string{"A1", "A2"}, []string{"B1", "B2", "S"}, []string{"D1", "M"})
	code, so, se := cli(t, append([]string{"slice", "plan", "--repo", f.repo, "--range", f.base[:7] + ".." + f.head[:7], "--features", feats}, extra...)...)
	if code != 0 {
		t.Fatalf("slice plan: exit %d\n%s%s", code, so, se)
	}
	return so
}

func mustCLI(t *testing.T, args ...string) string {
	t.Helper()
	code, so, se := cli(t, args...)
	if code != 0 {
		t.Fatalf("%v: exit %d\n%s%s", args, code, so, se)
	}
	return so
}

func (f sliceFixture) hunks(t *testing.T, report string) (Out, map[string]any) {
	t.Helper()
	outp := filepath.Join(t.TempDir(), report+".json")
	mustCLI(t, "hunks", "--repo", f.repo, "--report", report, "--out", outp)
	o := readHunks(outp)
	return o, o.Changes.(map[string]any)
}

func (f sliceFixture) gitHunks(t *testing.T, file string) []string {
	t.Helper()
	var hs []Hunk
	parsePatch(rkGit(t, f.repo, "diff", "--no-renames", "-U2", f.base, f.head, "--", file), "", &hs)
	var out []string
	for _, h := range hs {
		out = append(out, h.Text)
	}
	return out
}

func texts(o Out, file string) []string {
	var out []string
	for _, h := range o.Hunks {
		if h.File == file {
			out = append(out, h.Text)
		}
	}
	return out
}

func paths(ch map[string]any) []string {
	var out []string
	for _, x := range ch["files"].([]any) {
		out = append(out, x.(map[string]any)["path"].(string))
	}
	return out
}

func sliceJSON(ch map[string]any) string { return compact(ch["slice"]) }

func want(t *testing.T, literal string) string {
	t.Helper()
	var v any
	if err := decode([]byte(literal), &v); err != nil {
		t.Fatal(err)
	}
	return compact(v)
}

func TestSliceFeaturesOwnTheFinalDiff(t *testing.T) {
	f := newSliceFixture(t)
	so := f.plan(t)
	has(t, so, "plan AC1  "+f.base[:10]+".."+f.head[:10]+"  8 commits, 6 files -> 3 features")
	has(t, so, "note: 1 hunks hold lines of several features; each belongs to the feature that wrote most of its lines, the earliest on a tie")
	has(t, so, "note: 1 hunks were written by no commit of a feature")

	has(t, mustCLI(t, "slice", "next", "--repo", f.repo, "--range", f.base+".."+f.head), "AC1-1  feature 1/3 Promo · batch 1 · 1 files · next feature: Chat")
	promo, ch := f.hunks(t, "AC1-1")
	git := f.gitHunks(t, "lib/promo.dart")
	eq(t, len(git), 3)
	eq(t, strings.Join(paths(ch), ","), "lib/promo.dart")
	eq(t, strings.Join(texts(promo, "lib/promo.dart"), "\n"), git[0])
	eq(t, sliceJSON(ch), want(t, `{"report":"AC1-1","base":"`+f.base+`","head":"`+f.head+`","feature":"Promo","batch":1,"next":{"kind":"feature","name":"Chat","files":3}}`))
	eq(t, compact(ch["files"]), `[{"additions":2,"deletions":2,"path":"lib/promo.dart","status":"modified"}]`)
	eq(t, compact(ch["commits"]), `[{"sha":"`+f.c["A1"][:10]+`","subject":"promo: expiry label"},{"sha":"`+f.c["B1"][:10]+`","subject":"chat: send"}]`)

	has(t, mustCLI(t, "slice", "next", "--report", "AC1-1"), "AC1-2  feature 2/3 Chat · batch 1 · 3 files · next feature: Docs")
	chat, ch := f.hunks(t, "AC1-2")
	eq(t, strings.Join(paths(ch), ","), "lib/chat.dart,lib/old.dart,lib/promo.dart")
	eq(t, strings.Join(texts(chat, "lib/promo.dart"), "\n"), git[1]+"\n"+git[2])
	has(t, git[1], "-t12")
	eq(t, strings.Join(texts(chat, "lib/old.dart"), "\n"), strings.Join(f.gitHunks(t, "lib/old.dart"), "\n"))
	eq(t, fileEntry(chat, "lib/old.dart")["status"].(string), "deleted")
	eq(t, strings.Join(texts(chat, "lib/chat.dart"), "\n"), strings.Join(f.gitHunks(t, "lib/chat.dart"), "\n"))
	has(t, sliceJSON(ch), `"next":{"files":3,"kind":"feature","name":"Docs"}`)

	mustCLI(t, "slice", "next", "--report", "AC1-2")
	docs, ch := f.hunks(t, "AC1-3")
	eq(t, strings.Join(paths(ch), ","), "assets/logo.png,docs/readme.md,lib/config.dart")
	has(t, strings.Join(texts(docs, "lib/config.dart"), "\n"), "+evil()")
	logo := fileEntry(docs, "assets/logo.png")
	_, counted := logo["additions"]
	eq(t, counted, false)
	has(t, sliceJSON(ch), `"next":null`)
	has(t, mustCLI(t, "slice", "next", "--report", "AC1-3"), "nothing left after AC1-3")

	var got []string
	for _, o := range []Out{promo, chat, docs} {
		for _, h := range o.Hunks {
			got = append(got, h.File+"\n"+h.Text)
			if strings.Contains(h.Text, "scratch") {
				t.Fatalf("scratch() was removed inside the range but shows in %s", h.File)
			}
		}
	}
	var want []Hunk
	parsePatch(rkGit(t, f.repo, "diff", "--no-renames", "-U2", f.base, f.head), "", &want)
	var wantKeys []string
	for _, h := range want {
		wantKeys = append(wantKeys, h.File+"\n"+h.Text)
	}
	slices.Sort(got)
	slices.Sort(wantKeys)
	eq(t, strings.Join(got, "\n---\n"), strings.Join(wantKeys, "\n---\n"))
}

func TestHunkBelongsToTheFeatureThatWroteMostOfIt(t *testing.T) {
	p := Plan{
		Features: []Feature{{Commits: []string{"a"}}, {Commits: []string{"b"}}, {Commits: []string{"c"}}},
		Hunks: []Owned{
			{Commits: []string{"a", "b"}, Lines: []int{16, 580}},
			{Commits: []string{"a", "b"}, Lines: []int{2, 2}},
			{Commits: []string{"a", "b", "c"}, Lines: []int{3, 2, 2}},
			{Commits: []string{"x"}, Lines: []int{9}},
		},
	}
	eq(t, fmt.Sprint(p.owners()), "[1 0 0 2]")
}

func TestSliceNextIsIdempotent(t *testing.T) {
	f := newSliceFixture(t)
	f.plan(t)
	first := mustCLI(t, "slice", "next", "--repo", f.repo, "--range", f.base+".."+f.head)
	eq(t, mustCLI(t, "slice", "next", "--repo", f.repo, "--range", f.base+".."+f.head), first)
	mustCLI(t, "slice", "next", "--report", "AC1-1")
	planFile := planPath(f.home, f.base, f.head)
	before, _ := os.ReadFile(planFile)
	again := mustCLI(t, "slice", "next", "--report", "AC1-1")
	has(t, again, "AC1-2  feature 2/3 Chat")
	after, _ := os.ReadFile(planFile)
	eq(t, string(after), string(before))
	f.plan(t)
	has(t, mustCLI(t, "slice", "next", "--report", "AC1-1"), "AC1-2  feature 2/3 Chat")
}

func TestSliceDeliveredBatches(t *testing.T) {
	f := newSliceFixture(t)
	f.plan(t)
	mustCLI(t, "slice", "next", "--repo", f.repo, "--range", f.base+".."+f.head)
	mustCLI(t, "slice", "next", "--report", "AC1-1")
	rkWrite(t, f.home, "reports/AC1-2.js", "x")
	has(t, mustCLI(t, "slice", "next", "--report", "AC1-1"), "AC1-2 is already delivered")
	wantFail(t, 1, "already delivered and cannot be re-cut", "slice", "next", "--report", "AC1-1", "--files", "lib/chat.dart")
	feats := f.features(t, []string{"A1", "A2", "B1"}, []string{"B2", "S"}, []string{"D1", "M"})
	args := []string{"slice", "plan", "--repo", f.repo, "--range", f.base + ".." + f.head, "--features", feats}
	wantFail(t, 1, "--replace", args...)
	has(t, mustCLI(t, append(args, "--replace")...), "plan AC2  ")
	has(t, mustCLI(t, "slice", "next", "--repo", f.repo, "--range", f.base+".."+f.head), "AC2-1  feature 1/3 Promo")
}

func TestSliceReportIDs(t *testing.T) {
	f := newSliceFixture(t)
	rkWrite(t, f.home, "reports/AC1-5.js", "a report from an older plan")
	has(t, f.plan(t), "plan AC2  ")
	has(t, mustCLI(t, "slice", "next", "--repo", f.repo, "--range", f.base+".."+f.head), "AC2-1  ")
	feats := f.features(t, []string{"A1", "A2"}, []string{"B1"})
	has(t, mustCLI(t, "slice", "plan", "--repo", f.repo, "--range", f.base+".."+f.c["A2"], "--features", feats), "plan AC3  ")
	for name, want := range map[string]string{"acme-cart": "AC", "AcmeCart": "AC", "my_app2go": "MA", "app": "AP", "x": "XX", "42": "RV"} {
		eq(t, abbrev(name), want)
	}
}

func TestSliceBudgetAsksForFiles(t *testing.T) {
	repo, _ := sliceRepo(t)
	rkWrite(t, repo, "README", "x\n")
	base := rkCommit(t, repo, "base")
	var all []string
	for i := 1; i <= 14; i++ {
		p := fmt.Sprintf("lib/f%02d.dart", i)
		all = append(all, p)
		rkWrite(t, repo, p, "v\n")
	}
	head := rkCommit(t, repo, "big feature")
	feats := writeJSON(t, "features.json", map[string]any{"features": []map[string]any{{"name": "Big", "change": "Everything", "commits": []string{head}}}})
	mustCLI(t, "slice", "plan", "--repo", repo, "--range", base+".."+head, "--features", feats)
	rng := base + ".." + head
	code, so, se := cli(t, "slice", "next", "--repo", repo, "--range", rng)
	eq(t, code, 1)
	has(t, se, "pick at most 12 files with --files")
	has(t, so, "AC1-1 needs --files: 14 files are left")
	has(t, so, "  lib/f14.dart [1 hunks]")
	wantFail(t, 1, "lib/nope.dart is not a file this batch can take", append([]string{"slice", "next", "--repo", repo, "--range", rng, "--files"}, append(all[:11:11], "lib/nope.dart")...)...)
	wantFail(t, 1, "13 files given, at most 12", append([]string{"slice", "next", "--repo", repo, "--range", rng, "--files"}, all[:13]...)...)
	has(t, mustCLI(t, append([]string{"slice", "next", "--repo", repo, "--range", rng, "--files"}, all[2:]...)...), "AC1-1  feature 1/1 Big · batch 1 · 12 files · 2 files of this feature left after it")
	out := filepath.Join(t.TempDir(), "h.json")
	mustCLI(t, "hunks", "--repo", repo, "--report", "AC1-1", "--out", out)
	has(t, compact(readHunks(out).Changes.(map[string]any)["slice"]), `"next":{"files":2,"kind":"batch"}`)
	has(t, mustCLI(t, "slice", "next", "--report", "AC1-1"), "AC1-2  feature 1/1 Big · batch 2 · 2 files · nothing after it")
	has(t, mustCLI(t, "slice", "next", "--report", "AC1-1", "--files", all[0]), "AC1-2  feature 1/1 Big · batch 2 · 1 files · 1 files of this feature left after it")
	has(t, mustCLI(t, "slice", "next", "--report", "AC1-2"), "AC1-3  feature 1/1 Big · batch 3 · 1 files · nothing after it")
	wantFail(t, 1, "only the last batch can be re-cut", "slice", "next", "--report", "AC1-1", "--files", all[1])
}

func TestSlicePlanChecksThePartition(t *testing.T) {
	f := newSliceFixture(t)
	rng := f.base + ".." + f.head
	plan := func(groups ...[]string) []string {
		return []string{"slice", "plan", "--repo", f.repo, "--range", rng, "--features", f.features(t, groups...)}
	}
	wantFail(t, 1, f.c["S"][:10]+" chat: receive is in no feature", plan([]string{"A1", "A2"}, []string{"B1", "B2"}, []string{"D1", "M"})...)
	wantFail(t, 1, "is in feature 1 and feature 2", plan([]string{"A1", "A2"}, []string{"A2", "B1", "B2", "S"}, []string{"D1", "M"})...)
	wantFail(t, 1, "is not in the range", plan([]string{"A1", "A2", "base"}, []string{"B1", "B2", "S"}, []string{"D1", "M"})...)
	wantFail(t, 1, "is not an ancestor of", "slice", "plan", "--repo", f.repo, "--range", f.c["S"]+".."+f.c["M"], "--features", f.features(t, []string{"M"}))
	wantFail(t, 2, "--range must be <base>..<head>", "slice", "plan", "--repo", f.repo, "--range", f.base, "--features", "x.json")
	wantFail(t, 2, "usage: slice", "slice", "plan", "--repo", f.repo)
	wantFail(t, 1, "is not in a feature plan", "slice", "next", "--report", "AC9-1")
	wantFail(t, 1, "run slice plan first", "slice", "next", "--repo", f.repo, "--range", rng)
	f.plan(t)
	mustCLI(t, "slice", "next", "--repo", f.repo, "--range", rng)
	other, _ := sliceRepo(t)
	rkWrite(t, other, "a", "1\n")
	rkCommit(t, other, "a")
	t.Setenv("ARCHIFY_REVIEW_HOME", f.home)
	wantFail(t, 1, "AC1-1 belongs to", "hunks", "--repo", other, "--report", "AC1-1", "--out", filepath.Join(t.TempDir(), "h.json"))
	wantFail(t, 2, "usage: hunks", "hunks", "--repo", f.repo, f.base, "--report", "AC1-1", "--out", filepath.Join(t.TempDir(), "h.json"))
}

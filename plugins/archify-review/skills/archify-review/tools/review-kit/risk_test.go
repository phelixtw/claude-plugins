package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stubCRG(t *testing.T, version string, outputs map[string]any) (dir, log string) {
	t.Helper()
	dir = t.TempDir()
	log = filepath.Join(dir, "calls.log")
	for name, v := range outputs {
		b, _ := json.Marshal(v)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	py := `#!/bin/sh
if [ "$1" = "-I" ] && [ "$2" = "-c" ]; then
  echo "python $4 data=$CRG_DATA_DIR stdin=$(cat)" >> "` + log + `"
  cat "` + dir + `/signals.json"
  exit 0
fi
exec /bin/sh "$@"
`
	crg := `#!/usr/bin/env python
echo "crg $* data=$CRG_DATA_DIR max=$CRG_MAX_CHANGED_FUNCS" >> "` + log + `"
case "$1" in
  --version) echo "code-review-graph ` + version + `" ;;
  build) ;;
  detect-changes) cat "` + dir + `/detect.json" ;;
  impact) shift; while [ "$1" != "--files" ]; do shift; done; cat "` + dir + `/impact-$(basename "$2").json" ;;
  *) echo "stub: unexpected $1" >&2; exit 9 ;;
esac
`
	for name, body := range map[string]string{"python": py, "code-review-graph": crg} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CRG_DATA_DIR", "")
	return dir, log
}

func signals(flows, tests, callers float64) map[string]any {
	term := func(s float64, c int) map[string]any { return map[string]any{"score": s, "count": c} }
	return map[string]any{"flows": term(flows, 3), "cross_community": term(0, 0), "tests": term(tests, 0), "security": term(0, 0), "callers": term(callers, 2), "churn": term(0, 0)}
}

func riskFixture(t *testing.T) (repo, hunks, base string, abs func(string) string) {
	t.Helper()
	repo, _ = sliceRepo(t)
	rkWrite(t, repo, "lib/a.ts", "a\n")
	rkWrite(t, repo, "lib/b.ts", "b\n")
	rkWrite(t, repo, "lib/c.ts", "c\n")
	base = rkCommit(t, repo, "base")
	rkWrite(t, repo, "lib/a.ts", "a2\n")
	rkWrite(t, repo, "lib/b.ts", "b2\n")
	os.Remove(filepath.Join(repo, "lib/c.ts"))
	c1 := rkCommit(t, repo, "change")
	hunks = filepath.Join(t.TempDir(), "hunks.json")
	capture(t, func() { cmdHunks([]string{"--repo", repo, c1, "--out", hunks}) })
	top := toplevel(repo)
	return repo, hunks, base, func(p string) string { return filepath.Join(top, p) }
}

func detectOf(abs func(string) string, truncated bool) map[string]any {
	fn := func(file, name string, score float64) map[string]any {
		return map[string]any{"id": 1, "kind": "Function", "name": name, "qualified_name": abs(file) + "::" + name, "file_path": abs(file), "line_start": 1, "line_end": 2, "language": "typescript", "parent_name": nil, "is_test": false, "risk_score": score}
	}
	return map[string]any{
		"summary":           "Analyzed 3 changed file(s)",
		"risk_score":        0.81,
		"changed_functions": []any{fn("lib/a.ts", "Promo.load", 0.42), fn("lib/a.ts", "Promo.redeem", 0.81), fn("lib/b.ts", "render", 0.1)},
		"test_gaps": []any{
			map[string]any{"name": "redeem", "qualified_name": abs("lib/a.ts") + "::Promo.redeem", "file": abs("lib/a.ts"), "line_start": 1, "line_end": 2, "coverage": "none"},
			map[string]any{"name": "load", "qualified_name": abs("lib/a.ts") + "::Promo.load", "file": abs("lib/a.ts"), "line_start": 1, "line_end": 2, "coverage": "indirect"},
		},
		"functions_truncated": truncated,
		"churn_status":        "off",
	}
}

func impactOf(abs func(string) string, sites ...map[string]any) map[string]any {
	var nodes []any
	for _, s := range sites {
		nodes = append(nodes, s)
	}
	return map[string]any{"status": "ok", "impacted_nodes": nodes, "impacted_files": []any{}, "truncated": false}
}

func site(abs func(string) string, file, name string, line int, callFile string) map[string]any {
	n := map[string]any{"kind": "Function", "name": name, "qualified_name": abs(file) + "::" + name, "file_path": abs(file)}
	if line > 0 {
		cs := map[string]any{"line": line}
		if callFile != "" {
			cs["file"] = abs(callFile)
		}
		n["call_site"] = cs
	}
	return n
}

func TestRiskReadsCodeReviewGraph(t *testing.T) {
	repo, hunks, base, abs := riskFixture(t)
	_, log := stubCRG(t, "2.4.0", map[string]any{
		"detect.json": detectOf(abs, false),
		"impact-a.ts.json": impactOf(abs,
			site(abs, "lib/z.ts", "boot", 9, ""),
			site(abs, "lib/home.ts", "build", 88, ""),
			site(abs, "lib/home.ts", "build", 88, ""),
			site(abs, "lib/far.ts", "transitive", 0, ""),
			site(abs, "lib/mixin.ts", "apply", 4, "lib/gen.ts")),
		"impact-b.ts.json": impactOf(abs),
		"signals.json": map[string]any{
			"signals": map[string]any{
				abs("lib/a.ts") + "::Promo.redeem": signals(0.25, 0.3, 0.1),
				abs("lib/b.ts") + "::render":           signals(0.05, 0.05, 0),
			},
			"tests": []any{abs("lib/b.ts")},
		},
	})
	outp := filepath.Join(t.TempDir(), "risk.json")
	so := mustCLI(t, "risk", "--repo", repo, "--hunks", hunks, "--out", outp)
	has(t, so, "code-review-graph 2.4.0: 2 files, base "+base[:10])
	r := readRisk(outp)
	eq(t, r.Tool, "code-review-graph 2.4.0")
	eq(t, r.Base, base)
	eq(t, len(r.Files), 2)
	eq(t, compact(r.Files["lib/a.ts"]), `{"score":0.81,"symbol":"Promo.redeem","factors":[{"key":"flows","score":0.25,"count":3},{"key":"cross_community","score":0,"count":0},{"key":"tests","score":0.3,"count":0},{"key":"security","score":0,"count":0},{"key":"callers","score":0.1,"count":2},{"key":"churn","score":0,"count":0}],"callers":[{"file":"lib/z.ts","line":9,"symbol":"boot"},{"file":"lib/home.ts","line":88,"symbol":"build"},{"file":"lib/home.ts","line":88,"symbol":"build"},{"file":"lib/gen.ts","line":4,"symbol":"apply"}],"untested":["redeem"]}`)
	eq(t, compact(r.Files["lib/b.ts"]), `{"score":0,"callers":[],"untested":[]}`)

	calls, _ := os.ReadFile(log)
	data := filepath.Join(os.Getenv("ARCHIFY_REVIEW_HOME"), "crg", "acme-cart-")
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	eq(t, len(lines), 6)
	for i, want := range []string{"crg --version data= max=", "crg build --repo ", "crg detect-changes --repo ", "crg impact --repo ", "crg impact --repo ", "python "} {
		has(t, lines[i], want)
		if i > 0 {
			has(t, lines[i], "data="+data)
		}
		if i > 0 && i < 5 {
			has(t, lines[i], "max=1000000")
		}
	}
	has(t, lines[2], "--base "+base+" ")
	has(t, lines[3], "--files lib/a.ts --depth 1 --max-results 50")
	has(t, lines[5], `stdin={"files":["`+abs("lib/a.ts")+`","`+abs("lib/b.ts")+`"],"functions":["`+abs("lib/a.ts")+`::Promo.redeem","`+abs("lib/b.ts")+`::render"]}`)
	if strings.Contains(string(calls), "install") || strings.Contains(string(calls), " init") {
		t.Fatalf("review-kit ran a command that rewrites the repo:\n%s", calls)
	}
	if _, err := os.Stat(filepath.Join(repo, ".code-review-graph")); err == nil {
		t.Fatal("the graph must not be written into the reviewed repo")
	}
}

func TestRiskRefusesWhatItCannotTrust(t *testing.T) {
	repo, hunks, _, abs := riskFixture(t)
	outp := filepath.Join(t.TempDir(), "risk.json")
	args := []string{"risk", "--repo", repo, "--hunks", hunks, "--out", outp}
	outputs := map[string]any{"detect.json": detectOf(abs, true), "impact-a.ts.json": impactOf(abs), "impact-b.ts.json": impactOf(abs), "signals.json": map[string]any{"signals": map[string]any{}, "tests": []any{}}}
	stubCRG(t, "2.4.0", outputs)
	wantFail(t, 1, "capped its changed functions", args...)
	has(t, mustCLI(t, "risk", "--version"), "code-review-graph 2.4.0")
	stubCRG(t, "2.5.1", outputs)
	wantFail(t, 1, "needs code-review-graph 2.4.x: install it with: uv tool install code-review-graph", args...)
	wantFail(t, 1, "needs code-review-graph 2.4.x", "risk", "--version")
	t.Setenv("PATH", "/usr/bin:/bin")
	wantFail(t, 1, "code-review-graph is not on PATH", "risk", "--version")
	if _, err := (crg{bin: "/bin/echo"}).run("install", "--platform", "claude-code"); err == nil {
		t.Fatal("install must be refused")
	}
}

func TestAssembleRisk(t *testing.T) {
	r := &Risk{Tool: "code-review-graph 2.4.0", Files: map[string]*FileRisk{
		"lib/a.ts":  {Score: 0.42, Symbol: "load", Callers: []Caller{{"lib/z.ts", 9, "boot"}, {"lib/b.ts", 3, ""}}, Untested: []string{"load"}},
		"lib/b.ts":  {Score: 0.81, Symbol: "save", Factors: []Factor{{"flows", 0.25, 5}}, Callers: []Caller{{"lib/b.ts", 3, ""}, {"lib/a.ts", 1, "x"}}, Untested: []string{"save", "load"}},
		"lib/c.ts":  {Score: 0.1, Symbol: "it", Callers: []Caller{}, Untested: []string{}},
		"res/x.xml": {Callers: []Caller{}, Untested: []string{}},
	}}
	hk := kit(c2)
	hk.Changes = map[string]any{"files": []any{
		map[string]any{"path": "lib/a.ts", "status": "modified"}, map[string]any{"path": "lib/b.ts", "status": "modified"},
		map[string]any{"path": "lib/c.ts", "status": "modified"}, map[string]any{"path": "res/x.xml", "status": "modified"},
	}}
	spec, _, errs := build(rkPlan(`{"components":[
		{"id":"n","label":"Todo","files":["lib/a.ts",{"path":"lib/b.ts","summary":"s"}]},
		{"id":"t","label":"c.ts"},
		{"id":"x","label":"x.xml","files":["res/x.xml"]},
		{"id":"u","label":"Unchanged"}],
		"connections":[{"id":"c","file":"a.kt","story":"s"}]}`), hk, r)
	if errs != nil {
		t.Fatal(errs)
	}
	risk := func(i int) string {
		code, _ := spec["components"].([]any)[i].(map[string]any)["code"].(map[string]any)
		return compact(code["risk"])
	}
	eq(t, risk(0), `{"callers":[{"file":"lib/a.ts","line":1,"symbol":"x"},{"file":"lib/b.ts","line":3},{"file":"lib/z.ts","line":9,"symbol":"boot"}],"factors":[{"key":"flows","score":0.25,"count":5}],"score":0.81,"symbol":"save","tool":"code-review-graph 2.4.0","untested":["load","save"]}`)
	eq(t, risk(1), `{"callers":[],"score":0.1,"symbol":"it","tool":"code-review-graph 2.4.0","untested":[]}`)
	eq(t, risk(2), `{"callers":[],"score":0,"tool":"code-review-graph 2.4.0","untested":[]}`)
	eq(t, risk(3), `null`)
	spec, _, _ = build(rkPlan(`{"components":[{"id":"n","files":["lib/a.ts"]}],"connections":[{"id":"c","file":"a.kt","story":"s"}]}`), hk, nil)
	eq(t, risk(0), `null`)
}

func TestTestPaths(t *testing.T) {
	for p, want := range map[string]bool{
		"AcmeCart/OrderItemsTests.swift": true, "CheckoutUITests.swift": true, "ParserTest.swift": true, "app/FooTest.kt": true,
		"lib/foo_test.dart": true, "pkg/foo_test.go": true, "src/foo.test.ts": true, "src/foo.spec.js": true, "test_foo.py": true,
		"foo_spec.rb": true, "packages/a/test/helpers.dart": true, "src/__tests__/a.ts": true, "app/src/androidTest/A.kt": true,
		"app/src/testDebug/A.kt": true, "AcmeCartTests/Fixtures.swift": true, "AcmeCartUITests/Launch.swift": true, "tests/conftest.py": true,
		"CartStore.swift": false, "ABTests.swift": false, "AnimationSpec.kt": false, "QuickSpec.swift": false, "Contest.swift": false, "Test.swift": false, "lib/latest.dart": false,
		"ABTests/Config.swift": false, "testing/a.go": false, "lib/testimonial.dart": false, "README.md": false, "attest/x.kt": false,
	} {
		if got := isTestPath(p); got != want {
			t.Errorf("%s: test file %v, want %v", p, got, want)
		}
	}
}

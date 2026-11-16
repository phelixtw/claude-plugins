package main

import (
	"encoding/json"
	"strings"
	"testing"
)

const multi = `diff --git a/a.kt b/a.kt
index 1..2 100644
--- a/a.kt
+++ b/a.kt
@@ -10,3 +10,3 @@ class A {
 keep
-old
+new
 tail
@@ -40,2 +40,3 @@ fun f() {
 x
+added
 y
`

const c2 = `diff --git a/a.kt b/a.kt
--- a/a.kt
+++ b/a.kt
@@ -10,3 +10,3 @@
 keep
-new
+newer
 tail
`

func no(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

func eq[T comparable](t *testing.T, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParsePatch(t *testing.T) {
	var v []Hunk
	parsePatch(multi, "c1", &v)
	eq(t, len(v), 2)
	eq(t, v[0].Text, "@@ -10,3 +10,3 @@ class A {\n keep\n-old\n+new\n tail")
	eq(t, no(v[0].Lines[1].OldNo), 11)
	eq(t, no(v[0].Lines[1].NewNo), -1)
	eq(t, no(v[0].Lines[2].NewNo), 11)
	eq(t, no(v[1].Lines[1].NewNo), 41)

	v = nil
	parsePatch("diff --git a/n.xml b/n.xml\n--- /dev/null\n+++ b/n.xml\n@@ -0,0 +1,2 @@\n+<a/>\n+<b/>\ndiff --git a/i.png b/i.png\nBinary files a/i.png and b/i.png differ\n", "c1", &v)
	eq(t, len(v), 1)
	eq(t, no(v[0].Lines[1].NewNo), 2)

	v = nil
	parsePatch("diff --git a/a.txt b/a.txt\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+c\n\\ No newline at end of file\n", "c1", &v)
	eq(t, len(v[0].Lines), 5)
	eq(t, v[0].Lines[2].Sign, "\\")
	eq(t, no(v[0].Lines[2].OldNo), -1)
	eq(t, no(v[0].Lines[3].NewNo), 2)
}

func TestRangesAndHeaders(t *testing.T) {
	s, lo, hi, ok := parseRange("+5..9")
	eq(t, [4]any{s, lo, hi, ok}, [4]any{byte('+'), 5, 9, true})
	_, _, _, ok = parseRange("9")
	eq(t, ok, false)
	_, _, _, ok = parseRange("+9..+5")
	eq(t, ok, false)
	o, n, ok := parseHeader("@@ -0,0 +1,2 @@ x")
	eq(t, [3]any{o, n, ok}, [3]any{0, 1, true})
	_, _, ok = parseHeader("nope")
	eq(t, ok, false)
}

func kit(patches ...string) *Out {
	out := &Out{Changes: map[string]any{"commits": []any{}, "files": []any{}}}
	for i, p := range patches {
		parsePatch(p, "c"+string(rune('1'+i)), &out.Hunks)
	}
	return out
}

func plan(conn string) any {
	var p any
	decode([]byte(`{"meta":{"title":"t"},"x-extra":1,"components":[{"id":"a","label":"A","files":[{"path":"a.kt","hunks":[1]}],"code":{"brief":"b"}}],"connections":[`+conn+`]}`), &p)
	return p
}

func TestAssemble(t *testing.T) {
	hk := kit(multi)
	spec, _, errs := build(plan(`{"id":"c","from":"a","to":"a","labelAt":[1,2],"file":"a.kt","story":"s","tests":{
		"functions":[{"symbol":"f","lines":"+11,-11","status":"new","by":["ATest.f"]},{"symbol":"g","lines":"+41","hunk":2,"status":"none","by":[]}],
		"cases":[{"test":"ATest.f","covers":["f"],"given":"g","when":"w","then":"t"}],"suggested":[{"covers":"g","given":"g","when":"w","then":"t"}]}}`), hk, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	code := spec["connections"].([]any)[0].(map[string]any)["code"].(map[string]any)
	eq(t, code["diff"].(string), hk.Hunks[0].Text+"\n"+hk.Hunks[1].Text)
	eq(t, code["line"].(int), 11)
	tests := code["tests"].(map[string]any)
	fns := tests["functions"].([]any)
	eq(t, fns[0].(map[string]any)["lines"].(string), "+11,-11")
	eq(t, fns[1].(map[string]any)["lines"].(string), "+41")
	eq(t, compact(fns[0].(map[string]any)["by"]), `["ATest.f"]`)
	eq(t, compact(tests["cases"]), `[{"covers":["f"],"given":"g","test":"ATest.f","then":"t","when":"w"}]`)
	eq(t, compact(tests["suggested"]), `[{"covers":"g","given":"g","then":"t","when":"w"}]`)
	eq(t, spec["schema_version"].(int), 1)
	eq(t, spec["x-extra"].(json.Number).String(), "1")
}

func errsOf(t *testing.T, p any, hk *Out) string {
	t.Helper()
	_, _, errs := build(p, hk, nil)
	if errs == nil {
		t.Fatal("expected errors")
	}
	return strings.Join(errs, "\n")
}

func TestAssembleErrors(t *testing.T) {
	fn := func(f string) string { return `"tests":{"functions":[` + f + `]}` }
	e := errsOf(t, plan(`{"id":"c","file":"a.kt","story":"s",`+fn(`{"symbol":"f","lines":"+99"}`)+`}`), kit(multi))
	eq(t, strings.Contains(e, "connection c function f: +99 is not a new-file or context line of a.kt"), true)

	e = errsOf(t, plan(`{"id":"c","file":"a.kt","hunks":[1,3],"story":"s",`+fn(`{"symbol":"f","lines":"+11"}`)+`}`), kit(multi, c2))
	eq(t, strings.Contains(e, "+11 is in hunks [1, 2] of a.kt; set the function's hunk"), true)
	for _, hunk := range []string{"3", "[3]", "[2,3]"} {
		_, _, errs := build(plan(`{"id":"c","file":"a.kt","hunks":"all","story":"s",`+fn(`{"symbol":"f","lines":"+11","hunk":`+hunk+`}`)+`}`), kit(multi, c2), nil)
		eq(t, errs == nil, true)
	}
	e = errsOf(t, plan(`{"id":"c","file":"a.kt","hunks":"all","story":"s",`+fn(`{"symbol":"f","lines":"+11","hunk":[1,3]}`)+`}`), kit(multi, c2))
	eq(t, strings.Contains(e, "+11 is in hunks [1, 3] of a.kt; set the function's hunk"), true)

	e = errsOf(t, plan(`{"id":"c","file":"a.kt","hunks":[1],"story":"s"}`), kit(multi))
	eq(t, strings.Contains(e, "uncovered hunk 2 (a.kt @@ -40,2 +40,3 @@ fun f() {)"), true)

	e = errsOf(t, plan(`{"id":"cp","from":"a","to":"a","kind":"counterpart"}`), kit(multi))
	eq(t, strings.Contains(e, "connection cp: counterpart needs a story"), true)
	e = errsOf(t, plan(`{"id":"cp","from":"a","to":"a"}`), kit(multi))
	eq(t, strings.Contains(e, `set file, or kind "counterpart"`), true)
}

func TestAssembleMarksNewCommits(t *testing.T) {
	hk := kit(multi)
	hk.Changes = map[string]any{"commits": []any{map[string]any{"sha": "abc1234567"}, map[string]any{"sha": "def1234567"}}, "files": []any{}}
	p := plan(`{"id":"c","from":"a","to":"a","file":"a.kt","story":"s"}`)
	p.(map[string]any)["new_commits"] = []any{"def1234"}
	p.(map[string]any)["repo"] = "/r"
	spec, _, errs := build(p, hk, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	cs := spec["changes"].(map[string]any)["commits"].([]any)
	_, firstNew := cs[0].(map[string]any)["new"]
	eq(t, firstNew, false)
	eq(t, cs[1].(map[string]any)["new"].(bool), true)
	eq(t, spec["changes"].(map[string]any)["repo"].(string), "/r")
	_, kept := spec["new_commits"]
	eq(t, kept, false)
}

func TestAssembleKeepsComponentLayers(t *testing.T) {
	hk := kit(multi)
	p := plan(`{"id":"c","from":"a","to":"a","file":"a.kt","story":"s"}`)
	p.(map[string]any)["components"] = []any{
		map[string]any{"id": "a", "layers": map[string]any{"mvvm": "view", "flutter": "ui"}, "files": []any{"a.kt"}},
		map[string]any{"id": "b", "code": map[string]any{"layers": map[string]any{"webview": "content"}, "brief": map[string]any{"role": "r", "task": "t"}}},
	}
	spec, _, errs := build(p, hk, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	comps := spec["components"].([]any)
	a := comps[0].(map[string]any)
	_, topLevel := a["layers"]
	eq(t, topLevel, false)
	eq(t, a["code"].(map[string]any)["layers"].(map[string]any)["mvvm"].(string), "view")
	eq(t, comps[1].(map[string]any)["code"].(map[string]any)["layers"].(map[string]any)["webview"].(string), "content")
}

const testPatch = `diff --git a/test/a_test.dart b/test/a_test.dart
--- a/test/a_test.dart
+++ b/test/a_test.dart
@@ -1,2 +1,3 @@
 void main() {
+  test('a', () {});
 }
`

func withFiles(hk *Out, paths ...string) *Out {
	files := []any{}
	for _, p := range paths {
		files = append(files, map[string]any{"path": p, "status": "modified"})
	}
	hk.Changes = map[string]any{"commits": []any{}, "files": files}
	return hk
}

func TestAssembleMovesTestFilesToChangesTests(t *testing.T) {
	hk := withFiles(kit(c2, testPatch), "a.kt", "test/a_test.dart")
	p := rkPlan(`{"components":[{"id":"a","label":"a.kt","files":["a.kt"]}],"connections":[{"id":"c","from":"a","to":"a","file":"a.kt","story":"s"}]}`)
	for range 2 { // the hunks stay untouched, so a second build gives the same spec
		spec, summary, errs := build(p, hk, nil)
		if errs != nil {
			t.Fatal(errs)
		}
		ch := spec["changes"].(map[string]any)
		eq(t, compact(ch["files"]), `[{"path":"a.kt","status":"modified"}]`)
		eq(t, compact(ch["tests"]), `["test/a_test.dart"]`)
		eq(t, summary, "1 components, 1 connections, 1 hunks covered, 1 test files in changes.tests")
	}
	eq(t, len(hk.Changes.(map[string]any)["files"].([]any)), 2)

	spec, _, errs := build(p, withFiles(kit(c2), "a.kt"), nil)
	if errs != nil {
		t.Fatal(errs)
	}
	eq(t, compact(spec["changes"].(map[string]any)["tests"]), `[]`)
}

func TestAssembleRejectsPlanEntriesOnTestFiles(t *testing.T) {
	hk := func() *Out { return withFiles(kit(c2, testPatch), "a.kt", "test/a_test.dart") }
	mk := func(comp, conn string) any {
		return rkPlan(`{"components":[` + comp + `],"connections":[{"id":"c","file":"a.kt","story":"s"}` + conn + `]}`)
	}
	has(t, errsOf(t, mk(`{"id":"t","files":["test/a_test.dart"]}`, ""), hk()), "component t: test/a_test.dart is a test file")
	has(t, errsOf(t, mk(`{"id":"t","files":[{"path":"test/a_test.dart","hunks":"all"}]}`, ""), hk()), "component t: test/a_test.dart is a test file")
	has(t, errsOf(t, mk(`{"id":"t","label":"a_test.dart"}`, ""), hk()), "component t: a_test.dart names a test file")
	has(t, errsOf(t, mk(``, `,{"id":"ct","file":"test/a_test.dart","story":"s"}`), hk()), "connection ct: test/a_test.dart is a test file")

	// a production file with the same name as a test file keeps its node
	_, _, errs := build(mk(`{"id":"a","label":"a.kt"}`, ""), withFiles(kit(c2), "a.kt", "test/a.kt"), nil)
	eq(t, errs == nil, true)
}

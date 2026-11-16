package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Helpers ------------------------------------------------------------------

// TestMain lets the test binary act as the review-kit CLI, so die() paths
// (os.Exit) can be asserted through a subprocess.
func TestMain(m *testing.M) {
	if args, ok := os.LookupEnv("RK_CLI_ARGS"); ok {
		os.Args = []string{"review-kit"}
		if args != "" {
			os.Args = append(os.Args, strings.Split(args, "\x1f")...)
		}
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// cli runs review-kit in a subprocess and returns exit code, stdout, stderr.
func cli(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "RK_CLI_ARGS="+strings.Join(args, "\x1f"))
	var so, se strings.Builder
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, so.String(), se.String()
}

func wantFail(t *testing.T, code int, stderrHas string, args ...string) {
	t.Helper()
	c, _, se := cli(t, args...)
	if c != code {
		t.Fatalf("%v: exit %d, want %d (stderr %q)", args, c, code, se)
	}
	if !strings.Contains(se, stderrHas) {
		t.Fatalf("%v: stderr %q lacks %q", args, se, stderrHas)
	}
}

// capture returns what fn prints to stdout.
func capture(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

// knownBug skips a test that encodes the correct behavior of a confirmed bug.
// Set RK_RUN_KNOWN_BUGS=1 to run it anyway and see the failure.
func knownBug(t *testing.T, msg string) {
	t.Helper()
	if os.Getenv("RK_RUN_KNOWN_BUGS") == "" {
		t.Skip("known bug: " + msg)
	}
}

func has(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Fatalf("%q lacks %q", s, sub)
	}
}

func rkGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	base := []string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}
	out, err := exec.Command("git", append(base, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// rkRepo makes an empty repo; global git config is disabled so core.quotepath etc. stay default.
func rkRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	rkGit(t, dir, "init", "-q", "-b", "main")
	return dir
}

func rkWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rkCommit(t *testing.T, dir, msg string) string {
	t.Helper()
	rkGit(t, dir, "add", "-A")
	rkGit(t, dir, "commit", "-q", "--allow-empty", "-m", msg)
	return strings.TrimSpace(rkGit(t, dir, "rev-parse", "HEAD"))
}

// rkHunks runs `hunks` in-process and returns the parsed output and stdout.
func rkHunks(t *testing.T, repo string, shas ...string) (Out, string) {
	t.Helper()
	outp := filepath.Join(t.TempDir(), "h.json")
	args := append(append([]string{"--repo", repo}, shas...), "--out", outp)
	so := capture(t, func() { cmdHunks(args) })
	return readHunks(outp), strings.ReplaceAll(so, outp, "OUT")
}

func fileEntry(o Out, path string) map[string]any {
	for _, f := range o.Changes.(map[string]any)["files"].([]any) {
		if f.(map[string]any)["path"] == path {
			return f.(map[string]any)
		}
	}
	return nil
}

func hunkOf(o Out, file string) *Hunk {
	for i := range o.Hunks {
		if o.Hunks[i].File == file {
			return &o.Hunks[i]
		}
	}
	return nil
}

func hunkFiles(o Out) []string {
	var r []string
	for _, h := range o.Hunks {
		r = append(r, h.File)
	}
	return r
}

func writeJSON(t *testing.T, name string, v any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// hunks.json for the shared multi/c2 patches: a.kt hunks 1,2 (c1) and 3 (c2).
func kitFile(t *testing.T) string { return writeJSON(t, "hunks.json", kit(multi, c2)) }

// Parsing units --------------------------------------------------------------

func TestParseRangeTable(t *testing.T) {
	for _, c := range []struct {
		in   string
		sign byte
		lo   int
		hi   int
		ok   bool
	}{
		{"+5", '+', 5, 5, true},
		{"-5", '-', 5, 5, true},
		{"+5..+9", '+', 5, 9, true},
		{"-5..-9", '-', 5, 9, true},
		{"", 0, 0, 0, false},
		{"5", 0, 0, 0, false},
		{"+a", 0, 0, 0, false},
		{"+5..", 0, 0, 0, false},
		{"+9..+5", 0, 0, 0, false},
		{"+-5", 0, 0, 0, false},
	} {
		s, lo, hi, ok := parseRange(c.in)
		eq(t, [4]any{s, lo, hi, ok}, [4]any{c.sign, c.lo, c.hi, c.ok})
	}
}

func TestParseHeaderTable(t *testing.T) {
	for _, in := range []string{"@@ -1 +2", "@@ -x +1 @@", "@@ -1 -2 @@", "@@ -1 +y @@", "diff", ""} {
		_, _, ok := parseHeader(in)
		eq(t, ok, false)
	}
	o, n, ok := parseHeader("@@ -7 +9 @@")
	eq(t, [3]any{o, n, ok}, [3]any{7, 9, true})
}

func TestTextOfAndAsU64(t *testing.T) {
	eq(t, textOf("s"), "s")
	eq(t, textOf(json.Number("5")), "5")
	eq(t, textOf(map[string]any{"a": "<b>"}), `{"a":"<b>"}`)
	_, ok := asU64("5")
	eq(t, ok, false)
	_, ok = asU64(json.Number("-1"))
	eq(t, ok, false)
	_, ok = asU64(json.Number("1.5"))
	eq(t, ok, false)
	n, ok := asU64(json.Number("7"))
	eq(t, [2]any{n, ok}, [2]any{7, true})
}

func TestIsWord(t *testing.T) {
	for _, r := range "aZ9_日" {
		eq(t, isWord(r), true)
	}
	for _, r := range ".- (" {
		eq(t, isWord(r), false)
	}
}

// hunks ---------------------------------------------------------------------

func TestHunksBasicAndRoot(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "a.txt", "1\n2\n3\n4\n5\n")
	c1 := rkCommit(t, repo, "first")
	rkWrite(t, repo, "a.txt", "1\n2\nX\n4\n5\n")
	c2 := rkCommit(t, repo, "second")

	o, so := rkHunks(t, repo, c2)
	eq(t, so, "1 hunks, 1 files, 1 commits -> OUT\n")
	eq(t, len(o.Hunks), 1)
	eq(t, o.Hunks[0].File, "a.txt")
	eq(t, o.Hunks[0].Header, "@@ -1,5 +1,5 @@")
	eq(t, o.Hunks[0].Commit, c2[:10])
	cm := o.Changes.(map[string]any)["commits"].([]any)[0].(map[string]any)
	eq(t, cm["sha"].(string), c2[:10])
	eq(t, cm["subject"].(string), "second")
	f := fileEntry(o, "a.txt")
	eq(t, f["status"].(string), "modified")
	eq(t, f["additions"].(json.Number).String(), "1")
	eq(t, f["deletions"].(json.Number).String(), "1")

	// root commit has no parent: git show diffs against the empty tree
	o, _ = rkHunks(t, repo, c1)
	eq(t, fileEntry(o, "a.txt")["status"].(string), "added")
	eq(t, o.Hunks[0].Header, "@@ -0,0 +1,5 @@")
}

func TestHunksAddedDeletedRenamed(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "del.txt", "old\n")
	rkWrite(t, repo, "ren.txt", "r1\nr2\nr3\n")
	rkWrite(t, repo, "keep.txt", "k\n")
	rkCommit(t, repo, "base")
	os.Remove(filepath.Join(repo, "del.txt"))
	os.Rename(filepath.Join(repo, "ren.txt"), filepath.Join(repo, "ren2.txt"))
	rkWrite(t, repo, "added.txt", "new\n")
	c2 := rkCommit(t, repo, "change")

	o, so := rkHunks(t, repo, c2)
	eq(t, so, "4 hunks, 4 files, 1 commits -> OUT\n")
	for path, want := range map[string]string{"del.txt": "deleted", "ren.txt": "deleted", "ren2.txt": "added", "added.txt": "added"} {
		eq(t, fileEntry(o, path)["status"].(string), want)
	}
	d := hunkOf(o, "del.txt")
	eq(t, d.Header, "@@ -1 +0,0 @@")
	eq(t, d.Lines[0].Sign, "-")
	eq(t, no(d.Lines[0].OldNo), 1)
	eq(t, no(d.Lines[0].NewNo), -1)
	a := hunkOf(o, "added.txt")
	eq(t, a.Header, "@@ -0,0 +1 @@")
	eq(t, no(a.Lines[0].NewNo), 1)
	eq(t, no(a.Lines[0].OldNo), -1)
}

func TestHunksBinary(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "bin.dat", "\x00\x01")
	rkCommit(t, repo, "base")
	rkWrite(t, repo, "bin.dat", "\x00\x02\x03")
	rkWrite(t, repo, "new.dat", "\x00")
	rkWrite(t, repo, "t.txt", "x\n")
	c2 := rkCommit(t, repo, "bin")

	o, _ := rkHunks(t, repo, c2)
	eq(t, strings.Join(hunkFiles(o), ","), "t.txt")
	b := fileEntry(o, "bin.dat")
	eq(t, b["status"].(string), "modified")
	for _, k := range []string{"additions", "deletions", "binary"} {
		_, present := b[k]
		eq(t, present, false)
	}
	eq(t, fileEntry(o, "new.dat")["status"].(string), "added")
	eq(t, fileEntry(o, "t.txt")["additions"].(json.Number).String(), "1")
}

func TestHunksMultiCommitStatusMerge(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "f.txt", "1\n2\n3\n")
	c1 := rkCommit(t, repo, "add")
	rkWrite(t, repo, "f.txt", "1\nX\n3\n")
	c2 := rkCommit(t, repo, "modify")
	os.Remove(filepath.Join(repo, "f.txt"))
	c3 := rkCommit(t, repo, "delete")
	rkWrite(t, repo, "f.txt", "1\n2\n3\n")
	c4 := rkCommit(t, repo, "re-add")
	rkWrite(t, repo, "f.txt", "1\nY\n3\n")
	c5 := rkCommit(t, repo, "modify again")

	// added then modified: one net hunk, still added, counts of the net change
	o, so := rkHunks(t, repo, c1, c2)
	eq(t, so, "1 hunks, 1 files, 2 commits -> OUT\n")
	f := fileEntry(o, "f.txt")
	eq(t, f["status"].(string), "added")
	eq(t, f["additions"].(json.Number).String(), "3")
	eq(t, f["deletions"].(json.Number).String(), "0")
	eq(t, o.Hunks[0].Commit, c2[:10])
	eq(t, o.Hunks[0].Header, "@@ -0,0 +1,3 @@")

	o, _ = rkHunks(t, repo, c2, c3)
	eq(t, fileEntry(o, "f.txt")["status"].(string), "deleted")
	// deleted then re-added with other content: the net change is a modification
	o, _ = rkHunks(t, repo, c3, c4)
	eq(t, fileEntry(o, "f.txt")["status"].(string), "modified")
	// c3, c4 changed the file between c2 and c5 but are not selected: still one net diff (the final change),
	// and the note names the outside commits it includes
	o, so = rkHunks(t, repo, c2, c5)
	eq(t, len(o.Hunks), 1)
	eq(t, o.Hunks[0].Commit, c5[:10])
	has(t, so, "note: f.txt net diff includes commits outside the selection: "+c4[:10]+", "+c3[:10])
	eq(t, fileEntry(o, "f.txt")["status"].(string), "modified")
}

// A method added in one commit and renamed in the next shows only under its final name.
func TestHunksNetDiffAcrossCommits(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "share.dart", "class A {}\n\nvoid other() {}\n")
	rkWrite(t, repo, "keep.dart", "k1\n")
	rkCommit(t, repo, "base")
	rkWrite(t, repo, "share.dart", "class A {}\n\nFuture<bool> shareTextToPackage(String pkg) async {\n  return true;\n}\n\nvoid other() {}\n")
	c1 := rkCommit(t, repo, "add shareTextToPackage")
	rkWrite(t, repo, "share.dart", "class A {}\n\nFuture<bool> shareTextToApp(String app) async {\n  return true;\n}\n\nvoid other() {}\n")
	rkWrite(t, repo, "keep.dart", "k2\n")
	c2 := rkCommit(t, repo, "rename to shareTextToApp")

	o, so := rkHunks(t, repo, c1, c2)
	eq(t, so, "2 hunks, 2 files, 2 commits -> OUT\n")
	h := hunkOf(o, "share.dart")
	eq(t, h.Commit, c2[:10])
	has(t, h.Text, "+Future<bool> shareTextToApp(")
	if strings.Contains(h.Text, "shareTextToPackage") {
		t.Fatalf("net hunk still shows the intermediate name:\n%s", h.Text)
	}
	f := fileEntry(o, "share.dart")
	eq(t, f["status"].(string), "modified")
	eq(t, f["additions"].(json.Number).String(), "4")
	eq(t, f["deletions"].(json.Number).String(), "0")
	// a file touched by one commit keeps its own hunk
	eq(t, hunkOf(o, "keep.dart").Commit, c2[:10])

	// changes that cancel out leave no hunk and no file entry
	rkWrite(t, repo, "share.dart", "class A {}\n\nvoid other() {}\n")
	c3 := rkCommit(t, repo, "revert")
	o, so = rkHunks(t, repo, c1, c2, c3)
	has(t, so, "note: share.dart has no net change across its commits")
	eq(t, hunkOf(o, "share.dart") == nil, true)
	eq(t, fileEntry(o, "share.dart") == nil, true)
}

// Commits on two branches are not one line of history: per-commit hunks stay.
func TestHunksNetDiffNeedsOneLine(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "f.txt", "1\n2\n3\n4\n5\n6\n7\n8\n")
	rkCommit(t, repo, "base")
	rkGit(t, repo, "checkout", "-q", "-b", "side")
	rkWrite(t, repo, "f.txt", "1\nX\n3\n4\n5\n6\n7\n8\n")
	c1 := rkCommit(t, repo, "side change")
	rkGit(t, repo, "checkout", "-q", "main")
	rkWrite(t, repo, "f.txt", "1\n2\n3\n4\n5\n6\nY\n8\n")
	c2 := rkCommit(t, repo, "main change")
	o, so := rkHunks(t, repo, c1, c2)
	eq(t, len(o.Hunks), 2)
	has(t, so, "note: f.txt keeps per-commit hunks: its commits are not on one line of history")
}

func TestHunksEmptyCommit(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "a.txt", "1\n")
	rkCommit(t, repo, "base")
	c2 := rkCommit(t, repo, "empty")
	o, so := rkHunks(t, repo, c2)
	eq(t, so, "0 hunks, 0 files, 1 commits -> OUT\n")
	eq(t, len(o.Hunks), 0)
}

func TestHunksNameWithSpace(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "a b.txt", "1\n2\n3\n")
	rkCommit(t, repo, "base")
	rkWrite(t, repo, "a b.txt", "1\nX\n3\n")
	c2 := rkCommit(t, repo, "edit")
	o, _ := rkHunks(t, repo, c2)
	eq(t, o.Hunks[0].File, "a b.txt")
	eq(t, fileEntry(o, "a b.txt")["status"].(string), "modified")
}

func TestHunksBlankContextLines(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "b.txt", "a\n\nb\n\nc\nd\n")
	rkCommit(t, repo, "base")
	rkWrite(t, repo, "b.txt", "a\n\nb\n\nc\nD\n")
	c2 := rkCommit(t, repo, "edit")
	o, _ := rkHunks(t, repo, c2)
	h := o.Hunks[0]
	eq(t, h.Text, "@@ -4,3 +4,3 @@ b\n \n c\n-d\n+D")
	eq(t, len(h.Lines), 4)
	eq(t, h.Lines[0].Sign, " ")
	eq(t, no(h.Lines[0].NewNo), 4)
	eq(t, no(h.Lines[2].OldNo), 6)
	eq(t, no(h.Lines[3].NewNo), 6)

	// the blank context line is addressable by `lines`
	hp := writeJSON(t, "h.json", o)
	got := capture(t, func() { cmdLines([]string{"--hunks", hp, "--file", "b.txt", "+4..+6"}) })
	eq(t, got, "+4..+6\n")
}

func TestHunksChineseFilename(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "中文.txt", "x\ny\nz\n")
	rkCommit(t, repo, "base")
	rkWrite(t, repo, "中文.txt", "x\nY\nz\n")
	c2 := rkCommit(t, repo, "edit")
	o, _ := rkHunks(t, repo, c2)
	eq(t, strings.Join(hunkFiles(o), ","), "中文.txt")
	eq(t, fileEntry(o, "中文.txt") != nil, true)
}

func TestHunksNameContainingSpaceB(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "x b/y.txt", "1\n2\n3\n")
	rkCommit(t, repo, "base")
	rkWrite(t, repo, "x b/y.txt", "1\nX\n3\n")
	c2 := rkCommit(t, repo, "edit")
	o, _ := rkHunks(t, repo, c2)
	eq(t, strings.Join(hunkFiles(o), ","), "x b/y.txt")
}

func TestHunksNoPrefixConfig(t *testing.T) {
	repo := rkRepo(t)
	rkGit(t, repo, "config", "diff.noprefix", "true")
	rkWrite(t, repo, "a.txt", "1\n2\n3\n")
	rkCommit(t, repo, "base")
	rkWrite(t, repo, "a.txt", "1\nX\n3\n")
	c2 := rkCommit(t, repo, "edit")
	o, _ := rkHunks(t, repo, c2)
	eq(t, strings.Join(hunkFiles(o), ","), "a.txt")
}

func TestHunksMergeCommit(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "f1.txt", "1\n2\n3\n")
	rkWrite(t, repo, "f2.txt", "1\n2\n3\n")
	rkCommit(t, repo, "base")
	rkGit(t, repo, "checkout", "-q", "-b", "feat")
	rkWrite(t, repo, "f2.txt", "1\nX\n3\n")
	rkCommit(t, repo, "feat")
	rkGit(t, repo, "checkout", "-q", "main")
	rkWrite(t, repo, "f1.txt", "1\nY\n3\n")
	rkCommit(t, repo, "main change")
	rkGit(t, repo, "merge", "-q", "--no-ff", "feat", "-m", "merge feat")
	merge := strings.TrimSpace(rkGit(t, repo, "rev-parse", "HEAD"))

	outp := filepath.Join(t.TempDir(), "h.json")
	code, so, se := cli(t, "hunks", "--repo", repo, merge, "--out", outp)
	if code != 0 {
		return // failing loudly is an acceptable fix
	}
	if n := len(readHunks(outp).Hunks); n == 0 {
		t.Fatalf("merge commit reported success with 0 hunks (stdout %q, stderr %q)", so, se)
	}
}

func TestHunksErrors(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "a.txt", "1\n")
	c1 := rkCommit(t, repo, "base")
	outp := filepath.Join(t.TempDir(), "h.json")
	const usage = "usage: hunks"
	wantFail(t, 2, usage, "hunks")
	wantFail(t, 2, usage, "hunks", "--repo", repo, c1)
	wantFail(t, 2, usage, "hunks", "--repo", repo, "--out", outp)
	wantFail(t, 2, usage, "hunks", c1, "--out", outp)
	wantFail(t, 2, usage, "hunks", "--repo", repo, c1, "--out")
	wantFail(t, 1, "rev-parse", "hunks", "--repo", repo, "deadbeef", "--out", outp)
	wantFail(t, 1, "git rev-parse", "hunks", "--repo", t.TempDir(), c1, "--out", outp)
	wantFail(t, 2, "write ", "hunks", "--repo", repo, c1, "--out", t.TempDir())
}

// diff / lines --------------------------------------------------------------

func TestDiffCommand(t *testing.T) {
	hp := kitFile(t)
	hk := kit(multi, c2)
	run := func(args ...string) string {
		return capture(t, func() { cmdDiff(append([]string{"--hunks", hp, "--file", "a.kt"}, args...)) })
	}
	eq(t, run("--hunk", "2"), hk.Hunks[1].Text+"\n")
	eq(t, run("--hunk", "3", "1"), hk.Hunks[2].Text+"\n"+hk.Hunks[0].Text+"\n")
	eq(t, run("--hunk", "1,2"), hk.Hunks[0].Text+"\n"+hk.Hunks[1].Text+"\n")
	eq(t, run(), hk.Hunks[0].Text+"\n"+hk.Hunks[1].Text+"\n"+hk.Hunks[2].Text+"\n")
}

func TestDiffErrors(t *testing.T) {
	hp := kitFile(t)
	bad := filepath.Join(t.TempDir(), "bad.json")
	os.WriteFile(bad, []byte("{not json"), 0o644)
	const usage = "usage: --hunks"
	wantFail(t, 2, usage, "diff")
	wantFail(t, 2, usage, "diff", "--hunks", hp)
	wantFail(t, 2, usage, "diff", "--file", "a.kt")
	wantFail(t, 2, usage, "diff", "--hunks")
	wantFail(t, 2, "read ", "diff", "--hunks", "/nonexistent/h.json", "--file", "a.kt")
	wantFail(t, 2, "parse ", "diff", "--hunks", bad, "--file", "a.kt")
	wantFail(t, 1, "no hunks for zzz", "diff", "--hunks", hp, "--file", "zzz")
	wantFail(t, 1, "a.kt has 3 hunks, no hunk 4", "diff", "--hunks", hp, "--file", "a.kt", "--hunk", "4")
	wantFail(t, 1, "no hunk 0", "diff", "--hunks", hp, "--file", "a.kt", "--hunk", "0")
}

func TestLinesCommand(t *testing.T) {
	hp := kitFile(t)
	run := func(args ...string) string {
		return capture(t, func() { cmdLines(append([]string{"--hunks", hp, "--file", "a.kt"}, args...)) })
	}
	eq(t, run("--hunk", "2", "+41"), "+41\n")
	eq(t, run("--hunk", "1", "+10..12"), "+10..+12\n")
	eq(t, run("--hunk", "1", "-11", "+11"), "-11,+11\n")
	eq(t, run("--hunk", "2", "+40,+42"), "+40,+42\n")
	eq(t, run("--hunk", "3", "-12"), "-12\n")
	eq(t, run("+41"), "+41\n")
	eq(t, run("--hunk", "2", "+41,"), "+41\n") // empty comma part is ignored
}

func TestLinesErrors(t *testing.T) {
	hp := kitFile(t)
	base := []string{"lines", "--hunks", hp, "--file", "a.kt"}
	f := func(code int, sub string, extra ...string) {
		t.Helper()
		wantFail(t, code, sub, append(append([]string{}, base...), extra...)...)
	}
	f(2, "usage: lines")
	f(2, "usage: lines", "--hunk", "1")
	f(1, `bad range "abc"`, "abc")
	f(1, "bad range", "+9..+5")
	f(1, "+99 is not a new-file or context line of a.kt", "+99")
	f(1, "-99 is not a old-file line of a.kt hunk [1]", "--hunk", "1", "-99")
	f(1, "+11 is in hunks [1, 3] of a.kt; pass --hunk N", "+11")
	f(1, "no hunk 9", "--hunk", "9", "+11")
}

// caller --------------------------------------------------------------------

const callerSrc = "func foo() {\n" +
	"\tx := foobar()\n" +
	"\tfoo(1)  \r\n" +
	"// a foo b foo\n" +
	"food\n" +
	"foo_bar\n" +
	"xfoo foo\n" +
	"日本foo\n" +
	"日本 foo\n" +
	"last foo" // no trailing newline

func TestCallerMatches(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "src.go", "other\n")
	c1 := rkCommit(t, repo, "base")
	rkWrite(t, repo, "src.go", callerSrc)
	rkWrite(t, repo, "中文.txt", "see foo\n")
	c2 := rkCommit(t, repo, "src")
	call := func(sha, file, sym string) string {
		return capture(t, func() { cmdCaller([]string{"--repo", repo, "--sha", sha, "--file", file, "--symbol", sym}) })
	}
	eq(t, call(c2, "src.go", "foo"), strings.Join([]string{
		"src.go:1: func foo() {",
		"src.go:3: foo(1)",
		"src.go:4: // a foo b foo",
		"src.go:7: xfoo foo",
		"src.go:9: 日本 foo",
		"src.go:10: last foo",
	}, "\n")+"\n")
	eq(t, call(c2, "中文.txt", "foo"), "中文.txt:1: see foo\n")
	// --sha selects the revision: the first commit has no match, so this is checked via `other`
	eq(t, call(c1, "src.go", "other"), "src.go:1: other\n")
}

func TestCallerSymbolWithNonWordEdge(t *testing.T) {
	knownBug(t, "word-boundary check also applies at a non-word symbol edge, so '.foo' never matches 'bar.foo()'")
	repo := rkRepo(t)
	rkWrite(t, repo, "s.go", "bar.foo()\n")
	c1 := rkCommit(t, repo, "base")
	code, so, se := cli(t, "caller", "--repo", repo, "--sha", c1, "--file", "s.go", "--symbol", ".foo")
	eq(t, [2]any{code, so}, [2]any{0, "s.go:1: bar.foo()\n"})
	_ = se
}

func TestCallerErrors(t *testing.T) {
	repo := rkRepo(t)
	rkWrite(t, repo, "s.go", "foo\n")
	c1 := rkCommit(t, repo, "base")
	const usage = "usage: caller"
	wantFail(t, 2, usage, "caller")
	wantFail(t, 2, usage, "caller", "--repo", repo, "--sha", c1, "--file", "s.go")
	wantFail(t, 2, usage, "caller", "--repo", repo, "--sha", c1, "--file", "s.go", "--symbol")
	wantFail(t, 2, usage, "caller", "--repo", repo, "--bogus", "x")
	wantFail(t, 1, "zzz not found in "+c1+":s.go", "caller", "--repo", repo, "--sha", c1, "--file", "s.go", "--symbol", "zzz")
	wantFail(t, 1, "show", "caller", "--repo", repo, "--sha", c1, "--file", "missing.go", "--symbol", "foo")
	wantFail(t, 1, "show", "caller", "--repo", repo, "--sha", "deadbeef", "--file", "s.go", "--symbol", "foo")
}

// main ----------------------------------------------------------------------

func TestMainDispatch(t *testing.T) {
	wantFail(t, 2, "commands: hunks | lines | diff | caller | assemble", "nope")
	wantFail(t, 2, "commands:")
}

// assemble ------------------------------------------------------------------

func rkPlan(s string) any {
	var p any
	if err := decode([]byte(s), &p); err != nil {
		panic(err)
	}
	return p
}

func codeOf(spec map[string]any, i int) map[string]any {
	return spec["connections"].([]any)[i].(map[string]any)["code"].(map[string]any)
}

func TestAssembleCounterpartAndPlanKeys(t *testing.T) {
	hk := kit(c2)
	spec, _, errs := build(plan(`
		{"id":"cp","kind":"counterpart","story":"cs"},
		{"id":"c","from":"a","to":"a","file":"a.kt","from_symbol":"F","to_symbol":"G","story":"S","action":"A","note":"N","line":99,
		 "code":{"brief":"b"},"tests":{"functions":[{"symbol":"f","lines":"+11","status":"none","by":[]}]}}`), hk, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	cp := spec["connections"].([]any)[0].(map[string]any)
	eq(t, cp["code"].(map[string]any)["story"].(string), "cs")
	_, top := cp["story"]
	eq(t, top, false)

	c := spec["connections"].([]any)[1].(map[string]any)
	eq(t, c["from"].(string), "a")
	code := codeOf(spec, 1)
	for k, v := range map[string]string{"from_symbol": "F", "to_symbol": "G", "story": "S", "action": "A", "note": "N", "brief": "b", "path": "a.kt"} {
		eq(t, code[k].(string), v)
	}
	eq(t, code["line"].(json.Number).String(), "99") // explicit line is kept
	eq(t, code["diff"].(string), hk.Hunks[0].Text)
	for _, k := range []string{"story", "from_symbol", "line", "tests", "file"} {
		_, top := c[k]
		eq(t, top, false)
	}
}

func TestAssembleDefaultLine(t *testing.T) {
	line := func(lines ...string) (any, bool) {
		tests := ""
		for i, l := range lines {
			tests += fmt.Sprintf(`,{"symbol":"f%d","lines":%q}`, i, l)
		}
		if tests != "" {
			tests = `,"tests":{"functions":[` + tests[1:] + `]}`
		}
		spec, _, errs := build(plan(`{"id":"c","from":"a","to":"a","file":"a.kt","story":"s"`+tests+`}`), kit(c2), nil)
		if errs != nil {
			t.Fatal(errs)
		}
		l, ok := codeOf(spec, 0)["line"]
		return l, ok
	}
	l, _ := line("+11")
	eq(t, l.(int), 11)
	l, _ = line("+10..+12", "+11")
	eq(t, l.(int), 10)
	// only '-' lines in the first function: falls back to the first new-file line of the diff
	l, _ = line("-11", "+11")
	eq(t, l.(int), 10)
	l, _ = line()
	eq(t, l.(int), 10)
	l, _ = line("-11,+11")
	eq(t, l.(int), 11)

	// deleted file: no new-file line, so no default line
	hk := kit("diff --git a/d.txt b/d.txt\ndeleted file mode 100644\n--- a/d.txt\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-a\n-b\n")
	spec, _, errs := build(rkPlan(`{"components":[],"connections":[{"id":"c","file":"d.txt","story":"s","tests":{"functions":[{"symbol":"f","lines":"-1..2","status":"removed","by":[]}]}}]}`), hk, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	code := codeOf(spec, 0)
	_, hasLine := code["line"]
	eq(t, hasLine, false)
	eq(t, code["tests"].(map[string]any)["functions"].([]any)[0].(map[string]any)["lines"].(string), "-1..-2")
}

func TestAssembleTestsPassThrough(t *testing.T) {
	spec, _, errs := build(plan(`{"id":"c","from":"a","to":"a","file":"a.kt","story":"s","tests":{"functions":[
		{"symbol":"a","lines":"+11,-10..11","status":"existing","by":["T.a"],"via":"A.b"},
		{"symbol":"b","lines":"+10","hunk":[1],"status":"none","by":[]}],
		"cases":[{"test":"T.a","covers":["a"],"given":"g","when":"w","then":"t"}],"x-note":1}}`), kit(c2), nil)
	if errs != nil {
		t.Fatal(errs)
	}
	tests := codeOf(spec, 0)["tests"].(map[string]any)
	fns := tests["functions"].([]any)
	eq(t, compact(fns[0]), `{"by":["T.a"],"lines":"+11,-10..-11","status":"existing","symbol":"a","via":"A.b"}`)
	eq(t, compact(fns[1]), `{"by":[],"hunk":[1],"lines":"+10","status":"none","symbol":"b"}`)
	eq(t, compact(tests["cases"]), `[{"covers":["a"],"given":"g","test":"T.a","then":"t","when":"w"}]`)
	eq(t, tests["x-note"].(json.Number).String(), "1")
}

func TestAssembleConnectionErrors(t *testing.T) {
	cases := []struct{ conn, want string }{
		{`{"id":"c","file":"a.kt","hunks":"some","story":"s"}`, `connection c: hunks must be "all" or a list of numbers, got "some"`},
		{`{"id":"c","file":"a.kt","hunks":[0],"story":"s"}`, "connection c: a.kt has 1 hunks, bad hunk ref 0"},
		{`{"id":"c","file":"a.kt","hunks":[2],"story":"s"}`, "bad hunk ref 2"},
		{`{"id":"c","file":"a.kt","hunks":["x"],"story":"s"}`, `bad hunk ref "x"`},
		{`{"id":"c","file":"zzz","story":"s"}`, "connection c: no hunks for zzz"},
		{`{"id":"c","file":"a.kt","tests":"x"}`, "connection c tests must be an object"},
		{`{"id":"c","file":"a.kt","tests":{}}`, "connection c tests.functions must be a list"},
		{`{"id":"c","file":"a.kt","tests":{"functions":["x"]}}`, "connection c function 1 must be an object"},
		{`{"id":"c","file":"a.kt","tests":{"functions":[{"symbol":"f"}]}}`, "connection c function f: no lines"},
		{`{"id":"c","file":"a.kt","tests":{"functions":[{"symbol":"f","lines":""}]}}`, "no lines"},
		{`{"id":"c","file":"a.kt","tests":{"functions":[{"symbol":"f","lines":"+11","hunk":9}]}}`, "hunk 9 is not in the diff (1 hunks)"},
		{`{"id":"c","file":"a.kt","tests":{"functions":[{"symbol":"f","lines":"+11","hunk":[1,"x"]}]}}`, `hunk "x" is not in the diff (1 hunks)`},
		{`{"id":"c","file":"a.kt","tests":{"functions":[{"symbol":"f","lines":"abc"}]}}`, `bad range "abc"`},
		{`{"id":"c","file":"a.kt","tests":{"functions":[{"symbol":"f","lines":"-99"}]}}`, "-99 is not a old-file line of a.kt"},
		{`{"file":"a.kt","hunks":"x"}`, "connection ?:"},
		{`{"id":7,"file":"zzz"}`, "connection 7: no hunks for zzz"},
		{`{"id":"c","from":"a"}`, `connection c: set file, or kind "counterpart"`},
	}
	for _, c := range cases {
		has(t, errsOf(t, plan(c.conn), kit(c2)), c.want)
	}
	// two failing functions are both reported, by symbol or else by position
	e := errsOf(t, plan(`{"id":"c","file":"a.kt","tests":{"functions":[{"symbol":"f1","lines":"+99"},{"symbol":"f2","lines":"+11"},{"lines":"+98"}]}}`), kit(c2))
	has(t, e, "function f1:")
	has(t, e, "function 3:")
}

func TestAssembleComponents(t *testing.T) {
	cover := `{"id":"c","file":"a.kt","story":"s"}`
	hk := kit(c2)
	mk := func(comp string) string {
		return `{"components":[` + comp + `],"connections":[` + cover + `]}`
	}
	spec, _, errs := build(rkPlan(mk(`{"id":"a","files":["a.kt",{"path":"a.kt","hunks":"all"},{"path":"a.kt","hunks":[1]},{"note":"no path"},5]}`)), hk, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	files := spec["components"].([]any)[0].(map[string]any)["code"].(map[string]any)["files"].([]any)
	eq(t, len(files), 5)
	eq(t, files[0].(map[string]any)["path"].(string), "a.kt")
	_, d := files[0].(map[string]any)["diff"]
	eq(t, d, false)
	eq(t, files[1].(map[string]any)["diff"].(string), hk.Hunks[0].Text)
	_, h := files[1].(map[string]any)["hunks"]
	eq(t, h, false) // consumed
	eq(t, files[2].(map[string]any)["diff"].(string), hk.Hunks[0].Text)
	eq(t, files[3].(map[string]any)["note"].(string), "no path")
	eq(t, files[4].(json.Number).String(), "5")

	// files that is not a list becomes an empty list; no code and no layers leaves no code key
	spec, _, errs = build(rkPlan(mk(`{"id":"a","files":"x"},{"id":"b"}`)), hk, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	comps := spec["components"].([]any)
	eq(t, len(comps[0].(map[string]any)["code"].(map[string]any)["files"].([]any)), 0)
	_, hasCode := comps[1].(map[string]any)["code"]
	eq(t, hasCode, false)

	e := errsOf(t, rkPlan(mk(`{"id":"a","files":[{"path":"a.kt","hunks":[9]}]}`)), hk)
	has(t, e, "component a: a.kt has 1 hunks, bad hunk ref 9")
	e = errsOf(t, rkPlan(mk(`{"files":[{"path":"zzz","hunks":"all"}]}`)), hk)
	has(t, e, "component ?: no hunks for zzz")
}

func TestAssembleBuildBasics(t *testing.T) {
	_, _, errs := build([]any{}, kit(c2), nil)
	eq(t, strings.Join(errs, ""), "plan must be a JSON object")

	spec, _, errs := build(rkPlan(`{"schema_version":3,"diagram_type":"sequence","connections":[{"id":"c","file":"a.kt","story":"s"}]}`), kit(c2), nil)
	if errs != nil {
		t.Fatal(errs)
	}
	eq(t, spec["schema_version"].(json.Number).String(), "3")
	eq(t, spec["diagram_type"].(string), "sequence")
	eq(t, len(spec["components"].([]any)), 0)

	// every hunk must be covered, errors list all of them
	e := errsOf(t, rkPlan(`{"connections":[]}`), kit(multi, c2))
	has(t, e, "uncovered hunk 1 (a.kt @@ -10,3 +10,3 @@ class A {)")
	has(t, e, "uncovered hunk 3 (a.kt @@ -10,3 +10,3 @@)")
}

func TestMarkNewVariants(t *testing.T) {
	hk := kit(c2)
	hk.Changes = map[string]any{"commits": []any{
		map[string]any{"sha": "abc1234567"},
		map[string]any{"sha": "def1234567"},
		map[string]any{"subject": "no sha"},
		map[string]any{"sha": "fff1234567"},
	}, "files": []any{}}
	p := rkPlan(`{"connections":[{"id":"c","file":"a.kt","story":"s"}],"new_commits":["abc1234567890","","def",5]}`)
	spec, _, errs := build(p, hk, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	cs := spec["changes"].(map[string]any)["commits"].([]any)
	isNew := func(i int) bool { v, _ := cs[i].(map[string]any)["new"].(bool); return v }
	eq(t, [4]bool{isNew(0), isNew(1), isNew(2), isNew(3)}, [4]bool{true, true, false, false})
	_, hasRepo := spec["changes"].(map[string]any)["repo"]
	eq(t, hasRepo, false)

	// changes that is not an object: markNew leaves the spec alone
	hk.Changes = nil
	spec, _, errs = build(rkPlan(`{"connections":[{"id":"c","file":"a.kt","story":"s"}],"new_commits":["x"]}`), hk, nil)
	if errs != nil {
		t.Fatal(errs)
	}
	eq(t, spec["changes"] == nil, true)
}

func TestAssembleCommandRoundTrip(t *testing.T) {
	hk := kit(c2)
	hk.Changes = map[string]any{"commits": []any{map[string]any{"sha": "abc1234567", "subject": "s"}}, "files": []any{}}
	hp := writeJSON(t, "hunks.json", hk)
	pp := writeJSON(t, "plan.json", rkPlan(`{"meta":{"title":"t"},"repo":"/r","new_commits":["abc"],
		"components":[{"id":"a","label":"A"}],
		"connections":[{"id":"c","from":"a","to":"a","file":"a.kt","story":"<b>&","tests":{"functions":[{"symbol":"f","lines":"+11","status":"none","by":[]}]}}]}`))
	op := filepath.Join(t.TempDir(), "arch.json")
	so := capture(t, func() { cmdAssemble([]string{"--hunks", hp, "--plan", pp, "--out", op}) })
	eq(t, so, "1 components, 1 connections, 1 hunks covered -> "+op+"\n")

	raw, err := os.ReadFile(op)
	if err != nil {
		t.Fatal(err)
	}
	has(t, string(raw), "\n  \"schema_version\": 1") // 2-space indent
	has(t, string(raw), `"story": "<b>&"`)           // no HTML escaping
	var spec map[string]any
	if err := decode(raw, &spec); err != nil {
		t.Fatal(err)
	}
	ch := spec["changes"].(map[string]any)
	eq(t, ch["repo"].(string), "/r")
	eq(t, ch["commits"].([]any)[0].(map[string]any)["new"].(bool), true)
}

func TestAssembleCommandErrors(t *testing.T) {
	dir := t.TempDir()
	hp := writeJSON(t, "hunks.json", kit(c2))
	good := writeJSON(t, "plan.json", rkPlan(`{"connections":[{"id":"c","file":"a.kt","story":"s"}]}`))
	partial := writeJSON(t, "partial.json", rkPlan(`{"connections":[]}`))
	notObj := writeJSON(t, "notobj.json", []any{1})
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte("{oops"), 0o644)
	out := filepath.Join(dir, "out.json")
	const usage = "usage: assemble"
	wantFail(t, 2, usage, "assemble")
	wantFail(t, 2, usage, "assemble", "--hunks", hp, "--plan", good)
	wantFail(t, 2, usage, "assemble", "--hunks", hp, "--plan", good, "--out")
	wantFail(t, 2, usage, "assemble", "--bogus", "x")
	wantFail(t, 2, "read /nonexistent", "assemble", "--hunks", "/nonexistent", "--plan", good, "--out", out)
	wantFail(t, 2, "parse ", "assemble", "--hunks", bad, "--plan", good, "--out", out)
	wantFail(t, 2, "read /nonexistent", "assemble", "--hunks", hp, "--plan", "/nonexistent", "--out", out)
	wantFail(t, 2, "parse plan:", "assemble", "--hunks", hp, "--plan", bad, "--out", out)
	wantFail(t, 1, "plan must be a JSON object", "assemble", "--hunks", hp, "--plan", notObj, "--out", out)
	wantFail(t, 1, "1 error(s):\n  uncovered hunk 1", "assemble", "--hunks", hp, "--plan", partial, "--out", out)
	wantFail(t, 2, "write ", "assemble", "--hunks", hp, "--plan", good, "--out", dir)
	if _, err := os.Stat(out); err == nil {
		t.Fatal("output must not be written on error")
	}
}

// wrapperRun runs bin/review-kit from an unrelated cwd with the given PATH.
func wrapperRun(t *testing.T, path string, args ...string) (int, string, string) {
	t.Helper()
	script, _ := filepath.Abs("../../bin/review-kit")
	cmd := exec.Command(script, args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PATH="+path)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return code, so.String(), se.String()
}

func TestWrapperRunsLinesOnDarwinArm64(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		t.Skip("wrapper picks review-kit-darwin-arm64 only on darwin/arm64")
	}
	repo := rkRepo(t)
	rkWrite(t, repo, "a.txt", "1\n2\n3\n")
	rkCommit(t, repo, "base")
	rkWrite(t, repo, "a.txt", "1\nX\n3\n")
	o, _ := rkHunks(t, repo, rkCommit(t, repo, "edit"))
	hp := writeJSON(t, "h.json", o)
	code, so, se := wrapperRun(t, os.Getenv("PATH"), "lines", "--hunks", hp, "--file", "a.txt", "+2")
	eq(t, code, 0)
	eq(t, so, "+2\n")
	eq(t, se, "")
}

func TestWrapperUnsupportedPlatformWithoutGo(t *testing.T) {
	stub := t.TempDir() // PATH with fake uname and no go
	for _, n := range []string{"tr", "dirname"} {
		p, err := exec.LookPath(n)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(p, filepath.Join(stub, n)); err != nil {
			t.Fatal(err)
		}
	}
	fake := "#!/bin/sh\nif [ \"$1\" = -m ]; then echo riscv64; else echo Plan9; fi\n"
	if err := os.WriteFile(filepath.Join(stub, "uname"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	code, _, se := wrapperRun(t, stub, "lines")
	eq(t, code, 1)
	has(t, se, "supported: darwin-arm64")
	has(t, se, "Go is not installed")
}

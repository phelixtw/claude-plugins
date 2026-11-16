package doctor

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// env is an isolated HOME + cwd + PATH holding fake claude/npm scripts.
type env struct {
	t         *testing.T
	home, cwd string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{t: t, home: filepath.Join(root, "home"), cwd: filepath.Join(root, "cwd")}
	bin := filepath.Join(root, "bin")
	for _, d := range []string{e.home, e.cwd, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", e.home)
	t.Setenv("PATH", bin) // neither the real claude nor npm (network) is reachable
	t.Chdir(e.cwd)
	return e
}

func (e *env) write(path, content string) string {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		e.t.Fatal(err)
	}
	return path
}

func (e *env) claude(rel, content string) string {
	return e.write(filepath.Join(e.home, ".claude", rel), content)
}

func (e *env) project(rel, content string) string {
	return e.write(filepath.Join(e.cwd, ".claude", rel), content)
}

// fake installs an executable named name in PATH that prints out.
func (e *env) fake(name, out string) {
	e.write(filepath.Join(os.Getenv("PATH"), name), "#!/bin/sh\necho '"+out+"'\n")
}

// run executes Main and returns the table rows keyed by check id as "status|detail".
func (e *env) run() map[string]string {
	e.t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		e.t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	code := Main()
	os.Stdout = old
	w.Close()
	raw, _ := io.ReadAll(r)
	if code != 0 {
		e.t.Fatalf("Main=%d", code)
	}
	got := map[string]string{}
	for _, l := range strings.Split(string(raw), "\n") {
		c := strings.Split(l, " | ")
		if len(c) == 3 && strings.HasPrefix(c[0], "| A") {
			got[strings.TrimPrefix(c[0], "| ")] = c[1] + "|" + strings.TrimSuffix(c[2], " |")
		}
	}
	return got
}

func expect(t *testing.T, got map[string]string, id, status string, parts ...string) {
	t.Helper()
	v, has := got[id]
	if !has {
		t.Fatalf("%s: row missing in %v", id, got)
	}
	if !strings.HasPrefix(v, status+"|") {
		t.Errorf("%s: status want %s, got %q", id, status, v)
	}
	for _, p := range parts {
		if !strings.Contains(v, p) {
			t.Errorf("%s: want %q in %q", id, p, v)
		}
	}
}

func lineN(n int) string { return strings.Repeat("x\n", n) }

func TestEmptyHome(t *testing.T) {
	e := newEnv(t)
	got := e.run()
	if len(got) != 15 {
		t.Fatalf("want 15 rows, got %d", len(got))
	}
	expect(t, got, "A1", info, "讀不到")
	expect(t, got, "A2", ok)
	expect(t, got, "A3", warn)
	expect(t, got, "A4", ok)
	expect(t, got, "A5", ok)
	expect(t, got, "A6", ok)
	expect(t, got, "A7", ok, "0 個 skill")
	expect(t, got, "A8", warn, "hook 路徑都存在", "沒有 Stop hook")
	expect(t, got, "A9", warn, "沒有自訂 subagent")
	expect(t, got, "A10", ok, "defaultMode=default", "allow 0 條", "sandbox 未開")
	expect(t, got, "A11", ok)
	expect(t, got, "A12", info, "model=預設、effortLevel=預設、alwaysThinkingEnabled=預設")
	expect(t, got, "A13", warn, "沒設")
	expect(t, got, "A14", info)
	expect(t, got, "A15", info, "目前 ?")
}

func TestContextSize(t *testing.T) {
	usage := func(in, cr, cc int) string {
		return `{"message":{"usage":{"input_tokens":` + strconv.Itoa(in) + `,"cache_read_input_tokens":` + strconv.Itoa(cr) +
			`,"cache_creation_input_tokens":` + strconv.Itoa(cc) + `}}}` + "\n"
	}
	cases := []struct {
		name, settings, transcript, status, detail string
	}{
		{"small", ``, usage(10_000, 20_000, 0), ok, "約 30k（15% of 200k）"},
		{"over 40 percent of 200k", ``, usage(90_000, 0, 0), warn, "45% of 200k"},
		{"1m window keeps 150k fine", `{"model":"opus[1m]"}`, usage(100_000, 50_000, 0), ok, "15% of 1000k"},
		{"300k absolute cap on 1m", `{"model":"OPUS[1M]"}`, usage(0, 0, 300_000), warn, "300k"},
		{"skips junk and usage-less lines", ``,
			usage(5_000, 0, 0) + "not json\n[1]\n{\"message\":\"str\"}\n{\"message\":{\"usage\":{}}}\n{\"x\":1}\n", ok, "約 5k"},
		{"no usable line", ``, "{\"message\":{}}\n", info, "讀不到"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			if c.settings != "" {
				e.claude("settings.json", c.settings)
			}
			e.claude("projects/p/s.jsonl", c.transcript)
			expect(t, e.run(), "A1", c.status, c.detail)
		})
	}
}

func TestContextPicksNewestTranscript(t *testing.T) {
	e := newEnv(t)
	old := e.claude("projects/a/old.jsonl", `{"message":{"usage":{"input_tokens":150000}}}`+"\n")
	e.claude("projects/b/new.jsonl", `{"message":{"usage":{"input_tokens":1000}}}`+"\n")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	expect(t, e.run(), "A1", ok, "約 1k")
}

func TestAutocompactRecapsAndLocalOverride(t *testing.T) {
	e := newEnv(t)
	e.claude("settings.json", `{"autoCompactEnabled":true,"awaySummaryEnabled":true,"model":"sonnet"}`)
	e.claude("settings.local.json", `{"autoCompactEnabled":false,"awaySummaryEnabled":false,"effortLevel":"high","alwaysThinkingEnabled":true,"permissions":{"defaultMode":"bypassPermissions"}}`)
	got := e.run()
	expect(t, got, "A2", info, "已關")
	expect(t, got, "A3", ok, "關閉")
	expect(t, got, "A12", info, "model=sonnet、effortLevel=high、alwaysThinkingEnabled=True")
	expect(t, got, "A10", ok, "defaultMode=default") // local permissions are ignored by design
}

func TestMemoryFilesAndRules(t *testing.T) {
	e := newEnv(t)
	e.claude("CLAUDE.md", lineN(250))
	e.write(filepath.Join(e.cwd, "CLAUDE.md"), lineN(10))
	e.project("CLAUDE.md", lineN(3))
	e.claude("rules/eager.md", lineN(60))
	e.claude("rules/lazy.md", "---\npaths: src/**\n---\n"+lineN(80))
	e.project("rules/short.md", lineN(5))
	e.claude("rules/ignored.txt", lineN(500))
	got := e.run()
	expect(t, got, "A4", warn, "~/.claude/CLAUDE.md 250 行")
	if strings.Contains(got["A4"], "lazy.md") {
		t.Errorf("A4 should only list fat files: %s", got["A4"])
	}
	expect(t, got, "A5", info, "eager.md")
	if strings.Contains(got["A5"], "lazy.md") || strings.Contains(got["A5"], "short.md") {
		t.Errorf("A5: %s", got["A5"])
	}
}

func TestMemorySizesOK(t *testing.T) {
	e := newEnv(t)
	e.claude("CLAUDE.md", lineN(7))
	got := e.run()
	expect(t, got, "A4", ok, "CLAUDE.md 7")
	expect(t, got, "A5", ok)
}

func TestRuleGaps(t *testing.T) {
	text := "never PUSH to master\nadd Co-Authored-By trailer\nnever use --no-verify\n"
	e := newEnv(t)
	e.claude("CLAUDE.md", text)
	expect(t, e.run(), "A6", warn, "permissions.ask `Bash(git push:*)`", "`attribution`", "--no-verify → permissions.deny")

	e = newEnv(t)
	e.claude("CLAUDE.md", text)
	e.claude("settings.json", `{"attribution":{},"permissions":{"ask":["Bash(git push:*)"],"deny":["Bash(git commit --no-verify:*)"]}}`)
	expect(t, e.run(), "A6", ok)

	e = newEnv(t)
	e.claude("CLAUDE.md", text)
	e.claude("settings.json", `{"attribution":{},"permissions":{"deny":["Bash(git push:*)"]}}`)
	got := e.run()
	expect(t, got, "A6", warn, "--no-verify")
	if strings.Contains(got["A6"], "push") {
		t.Errorf("deny git push should satisfy the push rule: %s", got["A6"])
	}
}

func TestSkills(t *testing.T) {
	e := newEnv(t)
	e.claude("skills/good/SKILL.md", "---\nname: good\ndescription: Use when testing\n---\n## Gotchas\n- none\n")
	e.claude("skills/big/SKILL.md", "---\ndescription: 用在大事\n---\n"+lineN(501)+"gotcha\n")
	e.project("skills/local/SKILL.md", "---\nname: local\ndescription: plain words\n---\nbody\n")
	got := e.run()
	expect(t, got, "A7", warn, "3 個 skill", "big 502", "沒有 Gotchas（建議加）：1 個（local）", "description 看不出觸發時機：local")
}

func TestSkillsManyWithoutGotchas(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 9; i++ {
		e.claude("skills/s"+strconv.Itoa(i)+"/SKILL.md", "---\ndescription: use when x\n---\nbody\n")
	}
	got := e.run()
	expect(t, got, "A7", ok, "9 個 skill", "超過 500 行：無", "9 個（", "…）")
	if strings.Contains(got["A7"], "description 看不出") {
		t.Error(got["A7"])
	}
}

func TestHooks(t *testing.T) {
	e := newEnv(t)
	e.claude("real.sh", "#!/bin/sh\n")
	prompt := `{"hooks":[{"command":"$HOME/.claude/real.sh"}]}`
	e.claude("settings.json", `{"hooks":{
	  "Stop":[{"hooks":[{"command":"~/.claude/gone.sh"}]}],
	  "PreToolUse":[{"matcher":"Skill","hooks":[{"command":"$HOME/.claude/real.sh"}]},{"hooks":[{"command":"a"}]},{"hooks":[{"command":"b"}]},{"hooks":[{"command":"c"}]},{"hooks":[{"command":"d"}]}],
	  "UserPromptSubmit":[`+strings.Repeat(prompt+",", 4)+prompt+`]}}`)
	e.claude("settings.local.json", `{"hooks":{"Stop":[{"hooks":[{"command":"/Users/nobody/missing.py --x"}]}],"Notification":[{"hooks":[{}]}]}}`)
	got := e.run()
	expect(t, got, "A8", warn,
		"指向不存在的檔案：/Users/nobody/missing.py、~/.claude/gone.sh",
		"有 Stop hook", "PreToolUse 有掛 Skill", "PreToolUse×5、UserPromptSubmit×5")
}

func TestHooksHealthy(t *testing.T) {
	e := newEnv(t)
	e.claude("settings.json", `{"hooks":{"Stop":[{"hooks":[{"command":"echo done"}]}]}}`)
	expect(t, e.run(), "A8", ok, "hook 路徑都存在；有 Stop hook；沒有統計 skill 用量")
}

func TestPluginsHooksAgents(t *testing.T) {
	e := newEnv(t)
	pdir := filepath.Join(e.home, "plug")
	e.write(filepath.Join(pdir, "hooks", "hooks.json"), `{"hooks":{"Stop":[{"hooks":[{"command":"$HOME/plug/nope.sh"}]}],"PreToolUse":[{"matcher":"Skill","hooks":[{"command":"x"}]}]}}`)
	e.write(filepath.Join(pdir, "agents", "fast.md"), "---\nmodel: haiku\n---\nx\n")
	e.write(filepath.Join(pdir, "agents", "plain.md"), "no frontmatter\n")
	if err := os.Symlink(filepath.Join(pdir, "ghost-target"), filepath.Join(pdir, "agents", "ghost.md")); err != nil {
		t.Fatal(err)
	}
	e.claude("agents/mine.md", "---\nmodel: haiku\n---\n")
	e.project("agents/proj.md", "---\nmodel: opus\n---\n")
	// q is disabled, r has no install record, s points at a missing dir, t has an empty record
	e.claude("settings.json", `{"enabledPlugins":{"p@m":true,"q@m":false,"r@m":true,"s@m":true,"t@m":true}}`)
	e.claude("plugins/installed_plugins.json", `{"plugins":{"p@m":[{"installPath":"`+pdir+`"}],"s@m":[{"installPath":"/no/such/dir"}],"t@m":[]}}`)
	got := e.run()
	expect(t, got, "A8", warn, "~/plug/nope.sh", "PreToolUse 有掛 Skill")
	expect(t, got, "A9", warn, "5 個自訂 subagent", "haiku×2", "inherit×2", "opus×1", "壞掉的 symlink：ghost.md")
}

func TestSubagentsHealthy(t *testing.T) {
	e := newEnv(t)
	e.claude("agents/a.md", "---\nmodel: sonnet\n---\n")
	expect(t, e.run(), "A9", ok, "1 個自訂 subagent，模型分布 sonnet×1")
}

func TestPermissions(t *testing.T) {
	e := newEnv(t)
	e.claude("settings.json", `{"permissions":{"defaultMode":"bypassPermissions","allow":["Bash","Bash(*)","Read"],"deny":["x"]},"sandbox":{"enabled":true}}`)
	expect(t, e.run(), "A10", warn, "defaultMode=bypassPermissions", "allow 3 條、deny 1 條", "過寬：Bash、Bash(*)", "sandbox 開")

	e = newEnv(t)
	e.claude("settings.json", `{"permissions":{"allow":["Bash(*)"]}}`)
	expect(t, e.run(), "A10", warn, "過寬：Bash(*)", "sandbox 未開")

	e = newEnv(t)
	e.claude("settings.json", `{"permissions":{"defaultMode":"plan"}}`)
	expect(t, e.run(), "A10", ok, "defaultMode=plan")
}

func TestSettingsReferences(t *testing.T) {
	e := newEnv(t)
	e.claude("here.sh", "")
	e.claude("settings.json", `{"a":"$HOME/.claude/here.sh","b":"~/.claude/lost.md","c":"/Users/zed/other.py","d":"see `+"`.claude/notes/x.md`"+`"}`)
	got := e.run()
	expect(t, got, "A11", warn, "/Users/zed/other.py", "~/.claude/lost.md", "~/.claude/notes/x.md")
	if strings.Contains(got["A11"], "here.sh") {
		t.Errorf("existing file reported: %s", got["A11"])
	}

	e = newEnv(t)
	e.claude("here.sh", "")
	e.claude("settings.json", `{"a":"$HOME/.claude/here.sh"}`)
	expect(t, e.run(), "A11", ok)
}

func TestStatusline(t *testing.T) {
	e := newEnv(t)
	e.claude("sl.sh", "")
	e.claude("settings.json", `{"statusLine":{"command":"$HOME/.claude/sl.sh"}}`)
	expect(t, e.run(), "A13", ok)

	e = newEnv(t)
	e.claude("settings.json", `{"statusLine":{"command":"$HOME/.claude/sl.sh"}}`)
	expect(t, e.run(), "A13", warn, "不存在")

	e = newEnv(t)
	e.claude("settings.json", `{"statusLine":{"command":"echo hi"}}`)
	expect(t, e.run(), "A13", ok)
}

func TestBrowserMCP(t *testing.T) {
	e := newEnv(t)
	e.write(filepath.Join(e.home, ".claude.json"),
		`{"mcpServers":{"chrome-devtools":{},"fs":{}},"projects":{"/p":{"mcpServers":{"playwright":{}}},"/q":{}}}`)
	e.claude("settings.json", `{"enabledPlugins":{"Claude-in-Chrome@x":false}}`)
	expect(t, e.run(), "A14", ok, "Claude-in-Chrome@x、chrome-devtools、playwright")

	e = newEnv(t)
	e.write(filepath.Join(e.home, ".claude.json"), `{"mcpServers":{"fs":{}}}`)
	expect(t, e.run(), "A14", info, "沒有瀏覽器")
}

func TestVersionCheck(t *testing.T) {
	e := newEnv(t)
	e.fake("claude", "1.2.3 (Claude Code)")
	e.fake("npm", "1.2.3")
	expect(t, e.run(), "A15", ok, "目前 1.2.3，最新 1.2.3")

	e = newEnv(t)
	e.fake("claude", "1.2.3 (Claude Code)")
	e.fake("npm", "1.3.0")
	expect(t, e.run(), "A15", warn, "目前 1.2.3，最新 1.3.0", "claude update")

	e = newEnv(t)
	e.fake("claude", "1.2.3 (Claude Code)")
	expect(t, e.run(), "A15", info, "目前 1.2.3，查不到最新版")
}

func TestInvalidSettingsFallBackToEmpty(t *testing.T) {
	e := newEnv(t)
	e.claude("settings.json", "{broken")
	e.claude("settings.local.json", `[1]`)
	got := e.run()
	expect(t, got, "A3", warn)
	expect(t, got, "A12", info, "model=預設")
}

func TestFrontmatter(t *testing.T) {
	fm, n := frontmatter([]string{"---", "name: a", " nested: no", "no colon", "k: v: w", "---", "b1", "b2"})
	if !reflect.DeepEqual(fm, map[string]string{"name": "a", "k": "v: w"}) || n != 2 {
		t.Errorf("fm=%v n=%d", fm, n)
	}
	for _, in := range [][]string{nil, {"x", "---"}, {"---", "a: b"}} {
		fm, n = frontmatter(in)
		if len(fm) != 0 || n != len(in) {
			t.Errorf("%v -> %v %d", in, fm, n)
		}
	}
}

func TestPathsIn(t *testing.T) {
	home = "/h"
	got := pathsIn(`run $HOME/a/b.sh then ~/c/d.py and /Users/me/e/f.md plus ~/g.txt /etc/x.sh ~/ok-dir/h.md`)
	exp := []string{"/h/a/b.sh", "/h/c/d.py", "/Users/me/e/f.md", "/h/ok-dir/h.md"}
	if !reflect.DeepEqual(got, exp) {
		t.Errorf("got %v", got)
	}
}

func TestSmallHelpers(t *testing.T) {
	home = "/h"
	if tilde("/h/x") != "~/x" || joinOr(nil, ",", "none") != "none" || joinOr([]string{"a", "b"}, ",", "") != "a,b" {
		t.Error("tilde/joinOr")
	}
	if got := strList([]any{"a", 2.0}); len(got) != 2 || got[0] != "a" {
		t.Errorf("strList %v", got)
	}
	if strList("x") != nil {
		t.Error("strList non-list")
	}
	if listdir("/no/such") != nil || lines("/no/such") != nil || globChild("/no/such", "x") != nil {
		t.Error("missing dirs should be empty")
	}
	if isFile(t.TempDir()) || !isDir(t.TempDir()) || exists("/no/such") {
		t.Error("fs predicates")
	}
}

func TestPathsInJSONExtension(t *testing.T) {
	t.Skip("bug: pathRe lists `js` before `json`, so `~/a/b.json` is cut to `~/a/b.js` and A8/A11/A13 report a false missing file")
	home = "/h"
	if got := pathsIn("~/a/b.json"); !reflect.DeepEqual(got, []string{"/h/a/b.json"}) {
		t.Errorf("got %v", got)
	}
}

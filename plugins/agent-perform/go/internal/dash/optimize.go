package dash

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
)

// The /optimize page runs Claude Code's bundled `/claude-api prompt-audit` over
// the user's CLAUDE.md, rules, skills and installed plugins, on demand, and
// shows the last report.

const auditTimeout = 30 * time.Minute

var (
	auditMu      sync.Mutex
	auditRunning bool
	auditStarted time.Time
	auditBin     = "claude"
)

func auditMD() string   { return filepath.Join(tmState(), "prompt_audit.md") }
func auditMeta() string { return filepath.Join(tmState(), "prompt_audit.json") }

// pluginScope is one installed plugin the audit covers.
type pluginScope struct {
	name, market, path, owner string
}

// Ownership labels, as the report shows them.
const (
	ownMine  = "你維護的"
	ownOrg   = "你所屬組織的"
	ownOther = "別人的：只能回報作者"
)

// freeMail domains say nothing about who someone works with.
var freeMail = map[string]bool{"gmail": true, "googlemail": true, "outlook": true, "hotmail": true, "yahoo": true, "icloud": true, "me": true, "proton": true, "protonmail": true, "qq": true, "163": true}

// orgLabel is the organisation part of an e-mail: "example" for both
// a@example.com and b@example.co.uk; "" for free mail or no address.
func orgLabel(email string) string {
	_, dom, ok := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	if !ok || dom == "" {
		return ""
	}
	l, _, _ := strings.Cut(dom, ".")
	if freeMail[l] {
		return ""
	}
	return l
}

type person struct{ name, email string }

// ownership is how a plugin relates to the user, from its manifest people.
func ownership(me person, people []person) string {
	eq := func(a, b string) bool {
		return a != "" && strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
	}
	org := orgLabel(me.email)
	best := ownOther
	for _, p := range people {
		if eq(p.email, me.email) || eq(p.name, me.name) {
			return ownMine
		}
		if org != "" && orgLabel(p.email) == org {
			best = ownOrg
		}
	}
	return best
}

func personOf(v any) person {
	o := ojson.Obj(v)
	if o == nil {
		return person{}
	}
	return person{py.Str(orNil(ojson.Field(o, "name"), "")), py.Str(orNil(ojson.Field(o, "email"), ""))}
}

func readJSON(path string) any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	v, err := ojson.Parse(raw)
	if err != nil {
		return nil
	}
	return v
}

func gitConfig(key string) string {
	out, _ := exec.Command("git", "config", "--global", key).Output()
	return strings.TrimSpace(string(out))
}

// installedPlugins is every plugin enabled for the user (user scope, or
// project scope on the home folder), one install path each, with its owner.
func installedPlugins(me person) []pluginScope {
	root := filepath.Join(home(), ".claude", "plugins")
	inst := ojson.Obj(ojson.Field(readJSON(filepath.Join(root, "installed_plugins.json")), "plugins"))
	if inst == nil {
		return nil
	}
	var out []pluginScope
	for _, key := range inst.Keys {
		v, _ := inst.Get(key)
		list, _ := v.([]any)
		path := ""
		for _, e := range list {
			sc := py.Str(orNil(ojson.Field(e, "scope"), ""))
			pp := py.Str(orNil(ojson.Field(e, "projectPath"), ""))
			if sc == "user" || (sc == "project" && pp == home()) {
				path = py.Str(orNil(ojson.Field(e, "installPath"), ""))
				break
			}
		}
		if path == "" {
			continue
		}
		name, market, _ := strings.Cut(key, "@")
		people := []person{personOf(ojson.Field(readJSON(filepath.Join(path, ".claude-plugin", "plugin.json")), "author"))}
		mk := readJSON(filepath.Join(root, "marketplaces", market, ".claude-plugin", "marketplace.json"))
		people = append(people, personOf(ojson.Field(mk, "owner")))
		if ps, ok := ojson.Field(mk, "plugins").([]any); ok {
			for _, p := range ps {
				if py.Str(orNil(ojson.Field(p, "name"), "")) == name {
					people = append(people, personOf(ojson.Field(p, "author")))
				}
			}
		}
		out = append(out, pluginScope{name, market, path, ownership(me, people)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// auditPrompt is the `claude -p` prompt: the subcommand, then the scope.
func auditPrompt(plugins []pluginScope) string {
	c := filepath.Join(home(), ".claude")
	var b strings.Builder
	b.WriteString("/claude-api prompt-audit\n\n")
	b.WriteString("Scope (user-level configuration; it is named here, so it is in scope):\n")
	b.WriteString("- " + filepath.Join(c, "CLAUDE.md") + "\n- " + filepath.Join(c, "rules") + "/\n- " + filepath.Join(c, "skills") + "/\n")
	if len(plugins) > 0 {
		b.WriteString("\nInstalled plugins - audit only their skills/, commands/ and agents/ folders. Tag every finding in a plugin with the ownership label given here:\n")
		for _, p := range plugins {
			b.WriteString("- " + p.name + " (marketplace " + p.market + ") [" + p.owner + "]: " + p.path + "\n")
		}
	}
	b.WriteString("\nWrite the report in Traditional Chinese (Taiwan). Group findings by: CLAUDE.md and rules, user skills, then plugins by ownership label. " +
		"For plugins labelled " + ownOther + ", say the fix has to go to the plugin's author (name the marketplace). " +
		"Propose only; do not edit any file. Print the report, then the proposed diff in a ```diff block.\n")
	return b.String()
}

// auditCmd is the headless run: read-only tools, no transcript, subscription
// auth, and the ctx probe guard so its Stop hook does not spawn another claude.
func auditCmd(ctx context.Context, prompt string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, auditBin, "-p", prompt,
		"--no-session-persistence",
		"--permission-mode", "dontAsk",
		"--allowedTools", "Read Grep Glob Bash(git log:*) Bash(git blame:*)",
		"--disallowedTools", "Edit Write NotebookEdit")
	cmd.Dir = filepath.Join(home(), ".claude")
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			env = append(env, e)
		}
	}
	// CLAUDE_AP_CHILD keeps agent-perform's own SessionEnd work out of the run.
	cmd.Env = append(env, "CLAUDE_CTX_PROBE=1", "CLAUDE_AP_CHILD=1")
	return cmd
}

// startAudit starts a run unless one is going; done runs after it finishes.
// It reports whether a run is now going (a new one or the earlier one).
func startAudit(done func()) bool {
	auditMu.Lock()
	defer auditMu.Unlock()
	if auditRunning {
		return true
	}
	auditRunning, auditStarted = true, time.Now()
	go func() {
		defer logPanic("prompt audit")
		runAudit()
		auditMu.Lock()
		auditRunning = false
		auditMu.Unlock()
		if done != nil {
			done()
		}
	}()
	return true
}

func runAudit() {
	me := person{gitConfig("user.name"), gitConfig("user.email")}
	ctx, cancel := context.WithTimeout(context.Background(), auditTimeout)
	defer cancel()
	cmd := auditCmd(ctx, auditPrompt(installedPlugins(me)))
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	t0 := time.Now()
	err := cmd.Run()
	meta := ojson.NewObject().Set("at", t0.Unix()).Set("seconds", int64(time.Since(t0).Seconds()))
	switch {
	case ctx.Err() != nil:
		meta.Set("ok", false).Set("error", "超過 "+strconv.Itoa(int(auditTimeout.Minutes()))+" 分鐘，已中止")
	case err != nil:
		meta.Set("ok", false).Set("error", py.Head(strings.TrimSpace(errb.String()+" "+err.Error()), 300))
	default:
		meta.Set("ok", true)
	}
	os.MkdirAll(tmState(), 0o755)
	if ok, _ := meta.Get("ok"); ok == true {
		os.WriteFile(auditMD(), []byte(reportOnly(out.String())), 0o644)
	}
	os.WriteFile(auditMeta(), []byte(py.Dumps(meta)), 0o644)
}

// reportOnly drops the progress lines a -p run prints before the report's
// first heading.
func reportOnly(s string) string {
	if strings.HasPrefix(s, "# ") {
		return s
	}
	if i := strings.Index(s, "\n# "); i >= 0 {
		return s[i+1:]
	}
	return s
}

// fixPrompt is what the copy button puts on the clipboard: the whole report
// and diff, with how to apply it.
func fixPrompt(report string) string {
	return "Below is a /claude-api prompt-audit report on my Claude Code configuration, with a proposed diff.\n" +
		"1. List the findings and ask me which ones to apply before editing anything.\n" +
		"2. Apply only the ones I pick, to files under ~/.claude (CLAUDE.md, rules/, skills/).\n" +
		"3. Never edit files under ~/.claude/plugins/cache: an update overwrites them. For a finding tagged " + ownMine + " or " + ownOrg + ", find the plugin's source repo and change it there. For one tagged " + ownOther + ", draft an issue for the author instead.\n\n" +
		"<report>\n" + strings.TrimSpace(report) + "\n</report>\n"
}

// OptimizeView is the /optimize page.
func OptimizeView() string {
	auditMu.Lock()
	running, started := auditRunning, auditStarted
	auditMu.Unlock()
	h := `<h1 class="view">優化</h1><p class="lede">用 Claude Code 內建的 <code>/claude-api prompt-audit</code> 檢查 ~/.claude 的 CLAUDE.md、rules、skills，以及已安裝 plugin 的 skills、commands、agents，找出為舊模型寫的、彼此矛盾或已經過時的指示。每次跑幾分鐘，用訂閱額度，只提出建議、不改檔。</p>`
	status := ""
	if m := ojson.Obj(readJSON(auditMeta())); m != nil {
		at := time.Unix(int64(fnum(m, "at")), 0).Format("2006-01-02 15:04")
		secs := strconv.Itoa(int(fnum(m, "seconds")))
		if ok, _ := m.Get("ok"); ok == true {
			status = "上次健檢：" + at + "，花了 " + secs + " 秒"
		} else {
			status = "上次健檢失敗（" + at + "）：" + py.Str(orNil(ojson.Field(m, "error"), ""))
		}
	}
	btn := `<button class="audit-run">執行健檢</button>`
	if running {
		status = "健檢中，開始於 " + started.Format("15:04")
		btn = `<button class="audit-run" disabled data-running="1">健檢中…</button>`
	}
	h += `<p>` + btn + ` <span class="dim">` + Esc(status) + `</span></p>`
	raw, err := os.ReadFile(auditMD())
	if err != nil {
		return h
	}
	report, diff := string(raw), ""
	if i := strings.Index(report, "```diff"); i >= 0 {
		report, diff = report[:i], report[i:]
	}
	h += `<p><button id="cp" data-t="` + Esc(fixPrompt(string(raw))) + `">複製修復 prompt</button> <span class="dim" id="cpm"></span> <span class="dim">貼到 Claude Code，會先讓你挑要改哪幾項</span></p>`
	h += `<div class="card"><pre class="audit">` + Esc(strings.TrimSpace(report)) + `</pre></div>`
	if diff != "" {
		h += fold("建議的修改（diff）", `<pre class="audit">`+Esc(strings.TrimSpace(diff))+`</pre>`)
	}
	return h
}

// Package doctor is the read-only health check of the machine-wide Claude
// Code setup (/agent-perform:doctor): one markdown table row per automated
// check, A1..A15 in skills/doctor/checklist.md. Never writes anything.
package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"agentperform/internal/common"
	"agentperform/internal/ojson"
	"agentperform/internal/py"
)

const (
	ok   = "✅"
	warn = "⚠️"
	info = "➖"
)

var (
	home   string
	claude string
	rows   []string
	// Python's Unicode \w and \s
	pathRe  = regexp.MustCompile(`(?:\$HOME|~|/Users/[^/\s"'\x0b\x1c-\x1f\x{85}\pZ]+)(?:/[\pL\pN_.@+-]+)+\.(?:py|sh|js|ts|md|json)`)
	dotRefs = regexp.MustCompile("`(\\.claude/[\\pL\\pN_./-]+)`")
)

func row(cid, status, detail string) {
	rows = append(rows, fmt.Sprintf("| %s | %s | %s |", cid, status, detail))
}

func load(path string) any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ojson.NewObject()
	}
	v, err := ojson.Parse(raw)
	if err != nil {
		return ojson.NewObject()
	}
	return v
}

func lines(path string) []string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return py.Splitlines(strings.ToValidUTF8(string(raw), ""))
}

// frontmatter is (frontmatter of raw strings, body line count).
func frontmatter(t []string) (map[string]string, int) {
	if len(t) == 0 || py.Strip(t[0]) != "---" {
		return map[string]string{}, len(t)
	}
	for i := 1; i < len(t); i++ {
		if py.Strip(t[i]) == "---" {
			fm := map[string]string{}
			for _, x := range t[1:i] {
				if strings.Contains(x, ":") && !strings.HasPrefix(x, " ") {
					k, v, _ := strings.Cut(x, ":")
					fm[py.Strip(k)] = py.Strip(v)
				}
			}
			return fm, len(t) - i - 1
		}
	}
	return map[string]string{}, len(t)
}

func pathsIn(text string) []string {
	var out []string
	for _, p := range pathRe.FindAllString(text, -1) {
		if p[0] == '$' || p[0] == '~' {
			p = strings.Replace(strings.ReplaceAll(p, "$HOME", home), "~", home, 1)
		}
		out = append(out, p)
	}
	return out
}

// listdir is os.listdir order (unsorted), hidden names dropped like glob's `*`.
func listdir(dir string) []string {
	fh, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer fh.Close()
	names, _ := fh.Readdirnames(-1)
	var out []string
	for _, n := range names {
		if !strings.HasPrefix(n, ".") {
			out = append(out, n)
		}
	}
	return out
}

// globExt is glob.glob(dir/*<ext>) in directory order.
func globExt(dir, ext string) []string {
	var out []string
	for _, n := range listdir(dir) {
		if strings.HasSuffix(n, ext) {
			out = append(out, filepath.Join(dir, n))
		}
	}
	return out
}

// globChild is glob.glob(dir/*/name) in directory order.
func globChild(dir, name string) []string {
	var out []string
	for _, n := range listdir(dir) {
		p := filepath.Join(dir, n, name)
		if _, err := os.Lstat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func lastContextTokens() any {
	var newest string
	var newestT time.Time
	for _, d := range listdir(filepath.Join(claude, "projects")) {
		for _, f := range globExt(filepath.Join(claude, "projects", d), ".jsonl") {
			if st, err := os.Stat(f); err == nil && (newest == "" || st.ModTime().After(newestT)) {
				newest, newestT = f, st.ModTime()
			}
		}
	}
	if newest == "" {
		return nil
	}
	ls := lines(newest)
	ls = ls[max(len(ls)-400, 0):]
	for i := len(ls) - 1; i >= 0; i-- {
		e, err := ojson.Parse([]byte(ls[i]))
		if err != nil || ojson.Obj(e) == nil {
			continue
		}
		msg, has := ojson.Obj(e).Get("message")
		if has && ojson.Obj(msg) == nil {
			continue
		}
		u := ojson.Field(msg, "usage")
		if ojson.Truthy(u) {
			sum := 0.0
			for _, k := range []string{"input_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
				f, _ := py.Number(ojson.Field(u, k))
				sum += f
			}
			return int64(sum)
		}
	}
	return nil
}

func pluginDirs(s any) []string {
	var enabled []string
	if ep := ojson.Obj(ojson.Field(s, "enabledPlugins")); ep != nil {
		for _, k := range ep.Keys {
			if ojson.Truthy(ep.Vals[k]) {
				enabled = append(enabled, k)
			}
		}
	}
	sort.Strings(enabled)
	inst := ojson.Field(load(filepath.Join(claude, "plugins", "installed_plugins.json")), "plugins")
	var out []string
	for _, k := range enabled {
		list, _ := ojson.Field(inst, k).([]any)
		if len(list) == 0 {
			continue
		}
		p, _ := ojson.Str(ojson.Field(list[0], "installPath"))
		if isDir(p) {
			out = append(out, p)
		}
	}
	return out
}

func strList(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, x := range list {
		out = append(out, ojson.PyStr(x))
	}
	return out
}

func tilde(p string) string { return strings.ReplaceAll(p, home, "~") }

func joinOr(xs []string, sep, empty string) string {
	if len(xs) == 0 {
		return empty
	}
	return strings.Join(xs, sep)
}

// Main prints the check table for the current working directory.
func Main() int {
	home = common.Home()
	claude = filepath.Join(home, ".claude")
	rows = nil
	cwd, _ := os.Getwd()

	s := ojson.Obj(load(filepath.Join(claude, "settings.json")))
	if s == nil {
		s = ojson.NewObject()
	}
	local := ojson.Obj(load(filepath.Join(claude, "settings.local.json")))
	if local == nil {
		local = ojson.NewObject()
	}
	if lh := ojson.Obj(ojson.Field(local, "hooks")); lh != nil {
		if !ojson.Has(s, "hooks") {
			s.Set("hooks", ojson.NewObject())
		}
		sh := ojson.Obj(ojson.Field(s, "hooks"))
		for _, ev := range lh.Keys {
			cur, _ := ojson.Field(sh, ev).([]any)
			if !ojson.Has(sh, ev) {
				cur = []any{}
			}
			add, _ := lh.Vals[ev].([]any)
			sh.Set(ev, append(cur, add...))
		}
	}
	for _, k := range local.Keys {
		if k != "hooks" && k != "permissions" {
			s.Set(k, local.Vals[k])
		}
	}
	perm := ojson.Field(s, "permissions")
	if !ojson.Has(s, "permissions") {
		perm = ojson.NewObject()
	}
	model := ""
	if v, has := s.Get("model"); has {
		model = ojson.PyStr(v)
	}
	big := strings.Contains(strings.ToLower(model), "1m")
	window := int64(200_000)
	if big {
		window = 1_000_000
	}

	// A1 context size of the most recent session
	if ctx := lastContextTokens(); ctx == nil {
		row("A1", info, "讀不到最近工作階段的 token 用量")
	} else {
		c := ctx.(int64)
		pct := float64(c) / float64(window)
		st := ok
		if c >= 300_000 || pct >= 0.4 {
			st = warn
		}
		row("A1", st, fmt.Sprintf("最近工作階段的 context 約 %dk（%.0f%% of %dk）；300k／40%% 起建議 /compact 或 /clear",
			c/1000, pct*100, window/1000))
	}

	// A2 autocompact
	if v, has := s.Get("autoCompactEnabled"); has && v == false {
		row("A2", info, "autocompact 已關，靠手動 /compact；壓縮時記得帶提示（focus on X, drop Y）")
	} else {
		row("A2", ok, "autocompact 開著；重要轉折前仍建議手動 /compact 帶提示")
	}

	// A3 recaps
	if v, has := s.Get("awaySummaryEnabled"); has && v == false {
		row("A3", ok, "recaps（awaySummaryEnabled）關閉，改由 auto-recap 在 context 30% 以上時詢問")
	} else {
		row("A3", warn, "recaps（awaySummaryEnabled）開著（建議關閉）")
	}

	// A4 / A5 CLAUDE.md and rules
	mem := []string{filepath.Join(claude, "CLAUDE.md"), filepath.Join(cwd, "CLAUDE.md"), filepath.Join(cwd, ".claude", "CLAUDE.md")}
	rules := append(globExt(filepath.Join(claude, "rules"), ".md"), globExt(filepath.Join(cwd, ".claude", "rules"), ".md")...)
	type sized struct {
		p string
		n int
	}
	var sizes []sized
	seen := map[string]bool{}
	for _, p := range append(append([]string{}, mem...), rules...) {
		if seen[p] {
			continue
		}
		seen[p] = true
		if isFile(p) {
			sizes = append(sizes, sized{p, len(lines(p))})
		}
	}
	var fat, all []string
	for _, x := range sizes {
		if x.n > 200 {
			fat = append(fat, fmt.Sprintf("%s %d 行", tilde(x.p), x.n))
		}
		all = append(all, fmt.Sprintf("%s %d", filepath.Base(x.p), x.n))
	}
	if len(fat) > 0 {
		row("A4", warn, strings.Join(fat, "、"))
	} else {
		row("A4", ok, "各檔都在 200 行內（"+strings.Join(all, "、")+"）")
	}
	var eager []string
	for _, p := range rules {
		fm, _ := frontmatter(lines(p))
		if _, has := fm["paths"]; len(lines(p)) > 50 && !has {
			eager = append(eager, filepath.Base(p))
		}
	}
	if len(eager) > 0 {
		row("A5", info, "超過 50 行又沒有 paths: 的 rules（每次都載入）："+strings.Join(eager, "、"))
	} else {
		row("A5", ok, "rules 檔都很短或已設 paths:")
	}

	// A6 rules that settings.json could enforce
	var memParts []string
	for _, x := range sizes {
		memParts = append(memParts, strings.Join(lines(x.p), "\n"))
	}
	memtext := strings.Join(memParts, "\n")
	gates := strings.Join(append(strList(ojson.Field(perm, "ask")), strList(ojson.Field(perm, "deny"))...), " ")
	var miss []string
	if regexp.MustCompile(`(?i)push`).MatchString(memtext) && !strings.Contains(gates, "git push") {
		miss = append(miss, "push 規則 → permissions.ask `Bash(git push:*)`")
	}
	if regexp.MustCompile(`(?i)co-authored`).MatchString(memtext) && !ojson.Has(s, "attribution") {
		miss = append(miss, "Co-Authored-By → `attribution`")
	}
	if strings.Contains(memtext, "--no-verify") && !strings.Contains(gates, "no-verify") {
		miss = append(miss, "--no-verify → permissions.deny")
	}
	if len(miss) > 0 {
		row("A6", warn, strings.Join(miss, "；"))
	} else {
		row("A6", ok, "CLAUDE.md 裡能硬性執行的規則都已有 settings 對應")
	}

	// A7 skills
	skillSet := map[string]bool{}
	for _, p := range append(globChild(filepath.Join(claude, "skills"), "SKILL.md"), globChild(filepath.Join(cwd, ".claude", "skills"), "SKILL.md")...) {
		skillSet[p] = true
	}
	var skills []string
	for p := range skillSet {
		skills = append(skills, p)
	}
	sort.Strings(skills)
	gotRe := regexp.MustCompile(`(?i)gotcha|地雷|踩坑|常見錯誤`)
	trigRe := regexp.MustCompile(`(?i)use (this )?when|when the user|when |用在|時使用|時用|觸發`)
	var over, nogot, notrig []string
	for _, p := range skills {
		t := lines(p)
		fm, body := frontmatter(t)
		name := filepath.Base(filepath.Dir(p))
		if body > 500 {
			over = append(over, fmt.Sprintf("%s %d", name, body))
		}
		if !gotRe.MatchString(strings.Join(t, "\n")) {
			nogot = append(nogot, name)
		}
		if !trigRe.MatchString(fm["description"]) {
			notrig = append(notrig, name)
		}
	}
	a7 := fmt.Sprintf("%d 個 skill；本體超過 500 行：%s；沒有 Gotchas（建議加）：%d 個", len(skills), joinOr(over, "、", "無"), len(nogot))
	if len(nogot) > 0 {
		more := ""
		if len(nogot) > 8 {
			more = "…"
		}
		a7 += "（" + strings.Join(nogot[:min(8, len(nogot))], "、") + more + "）"
	}
	if len(notrig) > 0 {
		a7 += "；description 看不出觸發時機：" + strings.Join(notrig, "、")
	}
	if len(over) > 0 {
		row("A7", warn, a7)
	} else {
		row("A7", ok, a7)
	}

	// A8 hooks
	hooks := ojson.NewObject()
	if sh := ojson.Obj(ojson.Field(s, "hooks")); sh != nil {
		for _, ev := range sh.Keys {
			list, _ := sh.Vals[ev].([]any)
			hooks.Set(ev, append([]any{}, list...))
		}
	}
	plugins := pluginDirs(s)
	for _, d := range plugins {
		ph := ojson.Obj(ojson.Field(load(filepath.Join(d, "hooks", "hooks.json")), "hooks"))
		if ph == nil {
			continue
		}
		for _, ev := range ph.Keys {
			cur, _ := ojson.Field(hooks, ev).([]any)
			add, _ := ph.Vals[ev].([]any)
			hooks.Set(ev, append(cur, add...))
		}
	}
	type evCmd struct{ ev, cmd string }
	var cmds []evCmd
	for _, ev := range hooks.Keys {
		gs, _ := hooks.Vals[ev].([]any)
		for _, g := range gs {
			hs, _ := ojson.Field(g, "hooks").([]any)
			for _, h := range hs {
				c, _ := ojson.Str(ojson.Field(h, "command"))
				cmds = append(cmds, evCmd{ev, c})
			}
		}
	}
	brokenSet := map[string]bool{}
	for _, c := range cmds {
		for _, p := range pathsIn(c.cmd) {
			if !exists(p) {
				brokenSet[tilde(p)] = true
			}
		}
	}
	var broken []string
	for p := range brokenSet {
		broken = append(broken, p)
	}
	sort.Strings(broken)
	per := map[string]int{}
	for _, c := range cmds {
		per[c.ev]++
	}
	var heavy []string
	for _, ev := range hooks.Keys {
		if (ev == "UserPromptSubmit" || ev == "PreToolUse") && per[ev] >= 5 {
			heavy = append(heavy, fmt.Sprintf("%s×%d", ev, per[ev]))
		}
	}
	hasStop := per["Stop"] > 0
	skillMeter := false
	pre, _ := ojson.Field(hooks, "PreToolUse").([]any)
	for _, g := range pre {
		if m, _ := ojson.Str(ojson.Field(g, "matcher")); strings.Contains(m, "Skill") {
			skillMeter = true
		}
	}
	detail := []string{"hook 路徑都存在", "沒有 Stop hook（回合結束驗證）", "沒有統計 skill 用量的 PreToolUse hook"}
	if len(broken) > 0 {
		detail[0] = "指向不存在的檔案：" + strings.Join(broken, "、")
	}
	if hasStop {
		detail[1] = "有 Stop hook"
	}
	if skillMeter {
		detail[2] = "PreToolUse 有掛 Skill（可統計用量）"
	}
	if len(heavy) > 0 {
		detail = append(detail, "每回合都跑的 hook 偏多："+strings.Join(heavy, "、"))
	}
	if len(broken) > 0 || !hasStop {
		row("A8", warn, strings.Join(detail, "；"))
	} else {
		row("A8", ok, strings.Join(detail, "；"))
	}

	// A9 subagents
	agents := append(globExt(filepath.Join(claude, "agents"), ".md"), globExt(filepath.Join(cwd, ".claude", "agents"), ".md")...)
	for _, d := range plugins {
		agents = append(agents, globExt(filepath.Join(d, "agents"), ".md")...)
	}
	var dead []string
	models := ojson.NewObject()
	for _, a := range agents {
		if !exists(a) {
			dead = append(dead, filepath.Base(a))
		}
		fm, _ := frontmatter(lines(a))
		m, has := fm["model"]
		if !has {
			m = "inherit"
		}
		n, _ := models.Get(m)
		c, _ := n.(int)
		models.Set(m, c+1)
	}
	a9 := "沒有自訂 subagent"
	if len(agents) > 0 {
		var dist []string
		for _, k := range models.Keys {
			dist = append(dist, fmt.Sprintf("%s×%d", k, models.Vals[k].(int)))
		}
		a9 = fmt.Sprintf("%d 個自訂 subagent，模型分布 ", len(agents)) + strings.Join(dist, "、")
	}
	if len(dead) > 0 {
		a9 += "；壞掉的 symlink：" + strings.Join(dead, "、")
	}
	if len(dead) > 0 || len(agents) == 0 {
		row("A9", warn, a9)
	} else {
		row("A9", ok, a9)
	}

	// A10 permissions
	mode := "default"
	if v, has := ojson.Obj(perm).Get("defaultMode"); has {
		mode = ojson.PyStr(v)
	}
	sandbox := ojson.Truthy(ojson.Field(ojson.GetOr(s, "sandbox"), "enabled"))
	allow := strList(ojson.Field(perm, "allow"))
	var wide []string
	for _, a := range allow {
		if a == "Bash" || a == "Bash(*)" {
			wide = append(wide, a)
		}
	}
	a10 := fmt.Sprintf("defaultMode=%s；allow %d 條、deny %d 條", mode, len(allow), len(strList(ojson.Field(perm, "deny"))))
	if len(wide) > 0 {
		a10 += "；過寬：" + strings.Join(wide, "、")
	}
	if sandbox {
		a10 += "；sandbox 開"
	} else {
		a10 += "；sandbox 未開"
	}
	if mode == "bypassPermissions" || len(wide) > 0 {
		row("A10", warn, a10)
	} else {
		row("A10", ok, a10)
	}

	// A11 files referenced inside settings
	blob := py.Dumps(s)
	refs := map[string]bool{}
	for _, p := range pathsIn(blob) {
		refs[p] = true
	}
	for _, m := range dotRefs.FindAllStringSubmatch(blob, -1) {
		refs[filepath.Join(home, m[1])] = true
	}
	var gone []string
	for p := range refs {
		if !exists(p) {
			gone = append(gone, tilde(p))
		}
	}
	sort.Strings(gone)
	if len(gone) > 0 {
		row("A11", warn, "settings 引用了不存在的檔案："+strings.Join(gone, "、"))
	} else {
		row("A11", ok, "settings 引用的檔案都存在")
	}

	// A12 effort / thinking
	setting := func(k string) string {
		if v, has := s.Get(k); has {
			return ojson.PyStr(v)
		}
		return "預設"
	}
	row("A12", info, fmt.Sprintf("model=%s、effortLevel=%s、alwaysThinkingEnabled=%s", setting("model"), setting("effortLevel"), setting("alwaysThinkingEnabled")))

	// A13 statusline
	sl, _ := ojson.Str(ojson.Field(ojson.GetOr(s, "statusLine"), "command"))
	slMissing := false
	for _, p := range pathsIn(sl) {
		if !exists(p) {
			slMissing = true
		}
	}
	switch {
	case sl == "":
		row("A13", warn, "沒設 statusline")
	case slMissing:
		row("A13", warn, "statusline 指向不存在的檔案")
	default:
		row("A13", ok, "statusline 已設定")
	}

	// A14 browser MCP
	cj := load(filepath.Join(home, ".claude.json"))
	servers := map[string]bool{}
	addKeys := func(v any) {
		if o := ojson.Obj(v); o != nil {
			for _, k := range o.Keys {
				servers[k] = true
			}
		}
	}
	addKeys(ojson.Field(cj, "mcpServers"))
	if projects := ojson.Obj(ojson.Field(cj, "projects")); projects != nil {
		for _, k := range projects.Keys {
			addKeys(ojson.Field(projects.Vals[k], "mcpServers"))
		}
	}
	addKeys(ojson.Field(s, "enabledPlugins"))
	browserRe := regexp.MustCompile(`(?i)chrome|playwright`)
	var browser []string
	for k := range servers {
		if browserRe.MatchString(k) {
			browser = append(browser, k)
		}
	}
	sort.Strings(browser)
	if len(browser) > 0 {
		row("A14", ok, "瀏覽器 MCP："+strings.Join(browser, "、"))
	} else {
		row("A14", info, "沒有瀏覽器 MCP")
	}

	// A15 version
	run := func(name string, args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, name, args...).Output()
		if ctx.Err() != nil || (err != nil && out == nil) {
			return ""
		}
		return py.Strip(string(out))
	}
	cur, _, _ := strings.Cut(run("claude", "--version"), " ")
	latest := run("npm", "view", "@anthropic-ai/claude-code", "version")
	switch {
	case cur != "" && latest != "" && cur == latest:
		row("A15", ok, fmt.Sprintf("目前 %s，最新 %s", cur, latest))
	case cur != "" && latest != "":
		row("A15", warn, fmt.Sprintf("目前 %s，最新 %s，跑 `claude update`", cur, latest))
	default:
		if cur == "" {
			cur = "?"
		}
		row("A15", info, fmt.Sprintf("目前 %s，查不到最新版", cur))
	}

	common.Say("| ID | 狀態 | 結果 |\n|---|---|---|")
	common.Say(strings.Join(rows, "\n"))
	return 0
}

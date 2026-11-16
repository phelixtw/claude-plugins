package dash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnership(t *testing.T) {
	me := person{"Alex", "alex@example.co.uk"}
	for _, c := range []struct {
		people []person
		want   string
	}{
		{[]person{{"", "ALEX@example.co.uk"}}, ownMine},
		{[]person{{"Alex", ""}}, ownMine},
		{[]person{{"Acme", ""}, {"AI Team", "ai-team@example.com"}}, ownOrg},
		{[]person{{"Anthropic", "support@anthropic.com"}}, ownOther},
		{[]person{{}}, ownOther},
	} {
		if got := ownership(me, c.people); got != c.want {
			t.Errorf("%v: %s, want %s", c.people, got, c.want)
		}
	}
	if got := ownership(person{"a", "a@gmail.com"}, []person{{"b", "b@gmail.com"}}); got != ownOther {
		t.Errorf("free mail counted as one organisation: %s", got)
	}
}

func TestAuditRun(t *testing.T) {
	h := testHome(t)
	plug := filepath.Join(h, ".claude", "plugins")
	inst := filepath.Join(plug, "cache", "mk", "p1", "1.0")
	os.MkdirAll(filepath.Join(inst, ".claude-plugin"), 0o755)
	os.WriteFile(filepath.Join(inst, ".claude-plugin", "plugin.json"), []byte(`{"name":"p1","author":{"name":"Someone"}}`), 0o644)
	os.MkdirAll(filepath.Join(plug, "marketplaces", "mk", ".claude-plugin"), 0o755)
	os.WriteFile(filepath.Join(plug, "marketplaces", "mk", ".claude-plugin", "marketplace.json"), []byte(`{"owner":{"name":"Vendor"}}`), 0o644)
	os.WriteFile(filepath.Join(plug, "installed_plugins.json"), []byte(`{"plugins":{"p1@mk":[{"scope":"user","installPath":"`+inst+`"}],"p2@mk":[{"scope":"project","projectPath":"/elsewhere","installPath":"/x"}]}}`), 0o644)

	argsFile := filepath.Join(h, "args")
	bin := filepath.Join(h, "fake-claude")
	os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+argsFile+"\necho \"key=$ANTHROPIC_API_KEY probe=$CLAUDE_CTX_PROBE child=$CLAUDE_AP_CHILD\" >> "+argsFile+"\nsleep 0.3\nprintf 'waiting on subagents\\n\\n# report body\\n```diff\\n-old\\n+new\\n```\\n'\n"), 0o755)
	old := auditBin
	auditBin = bin
	t.Cleanup(func() { auditBin = old })
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")

	done := make(chan struct{}, 2)
	startAudit(func() { done <- struct{}{} })
	startAudit(func() { done <- struct{}{} }) // already running: no second run
	mustHave(t, OptimizeView(), "健檢中，開始於")
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("audit did not finish")
	}
	select {
	case <-done:
		t.Fatal("second run started")
	case <-time.After(500 * time.Millisecond):
	}

	raw, _ := os.ReadFile(argsFile)
	args := string(raw)
	for _, want := range []string{"/claude-api prompt-audit", "--no-session-persistence", "--disallowedTools", filepath.Join(h, ".claude", "CLAUDE.md"), "p1 (marketplace mk) [" + ownOther + "]: " + inst, "key= probe=1 child=1"} {
		mustHave(t, args, want)
	}
	mustNot(t, args, "p2 (")
	page := OptimizeView()
	mustHave(t, page, "上次健檢：")
	mustHave(t, page, `<pre class="audit"># report body</pre>`)
	mustNot(t, page, "waiting on subagents")
	mustHave(t, page, "建議的修改（diff）")
	mustHave(t, page, `<button id="cp" data-t="Below is a /claude-api prompt-audit report`)
	mustHave(t, page, "+new\n```\n&lt;/report&gt;")
	if strings.Count(page, "+new") != 2 { // the diff fold and the copy prompt
		t.Error("diff should be in the fold and the copy prompt")
	}
}

package common

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHomeJoinSayLossy(t *testing.T) {
	t.Setenv("HOME", "/h")
	if Home() != "/h" || Join("/h/", ".claude", "x") != "/h/.claude/x" || Join("", "a") != "a" || Join("/a", "b") != "/a/b" {
		t.Error("Home/Join")
	}
	if got := Lossy([]byte("a\xffb")); got != "a�b" {
		t.Errorf("Lossy %q", got)
	}
	if Now() < 1.7e9 || Epoch(time.Now()) < 1.7e9 {
		t.Error("clock")
	}
}

func TestPluginRootOverride(t *testing.T) {
	t.Setenv("AP_PLUGIN_ROOT", "/p")
	if PluginRoot() != "/p" {
		t.Error("env override")
	}
	t.Setenv("AP_PLUGIN_ROOT", "")
	if PluginRoot() == "" {
		t.Error("falls back to the executable's grandparent")
	}
}

func TestReadJSONLSkipsBadLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.jsonl")
	os.WriteFile(p, []byte("{\"a\":1}\n\nnope\n[1]\n{\"b\":2}\n"), 0o644)
	if got := ReadJSONL(p); len(got) != 2 {
		t.Errorf("rows %d", len(got))
	}
	if ReadJSONL(p+".missing") != nil {
		t.Error("missing file")
	}
}

func TestRuneClasses(t *testing.T) {
	for r, want := range map[rune][2]bool{'a': {true, true}, '_': {true, false}, '中': {true, true}, '5': {true, true}, ' ': {false, false}} {
		if IsWord(r) != want[0] || IsAlnum(r) != want[1] {
			t.Errorf("%q: IsWord=%v IsAlnum=%v", r, IsWord(r), IsAlnum(r))
		}
	}
}

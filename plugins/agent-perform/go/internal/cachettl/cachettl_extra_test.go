package cachettl

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"agentperform/internal/ojson"
)

func asstBoth(ts float64, fiveM, oneH int) string {
	return `{"type":"assistant","timestamp":"` + stamp(ts) + `","message":{"usage":{"cache_creation":{"ephemeral_5m_input_tokens":` +
		itoa(fiveM) + `,"ephemeral_1h_input_tokens":` + itoa(oneH) + `}}}}`
}

func itoa(n int) string { return strconv.Itoa(n) }

func setFlag(t *testing.T, flag string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"cachedGrowthBookFeatures":{"tengu_sequential_cloud":`+flag+`}}`), 0o644)
}

// Boundaries: just under the TTL is alive, exactly at it and just over are expired.
func TestTTLBoundaries(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const now = 1_800_000_000.0
	for _, c := range []struct {
		key     string
		gap     float64
		expired bool
	}{
		{"ephemeral_5m_input_tokens", 299, false},
		{"ephemeral_5m_input_tokens", 300, true},
		{"ephemeral_5m_input_tokens", 301, true},
		{"ephemeral_1h_input_tokens", 3599, false},
		{"ephemeral_1h_input_tokens", 3600, true},
		{"ephemeral_1h_input_tokens", 3601, true},
	} {
		st := StateOf(writeTranscript(t, asst(now-c.gap, c.key)), now)
		if st == nil {
			t.Fatalf("%s %v: nil state", c.key, c.gap)
		}
		if got := Label(st) == "Cache 已過期"; got != c.expired {
			t.Errorf("%s gap %v: expired=%v (left %v)", c.key, c.gap, got, st.Left)
		}
	}
}

// The latest usage block decides the TTL (a later 5m call after an older 1h call); 5m wins inside one block.
func TestMixedTTLs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const now = 1_800_000_000.0
	st := StateOf(writeTranscript(t, asst(now-1000, "ephemeral_1h_input_tokens"), asst(now-100, "ephemeral_5m_input_tokens")), now)
	if st.TTL != TTL5m || st.Left != 200 {
		t.Errorf("later 5m: %+v", st)
	}
	st = StateOf(writeTranscript(t, asst(now-1000, "ephemeral_5m_input_tokens"), asst(now-100, "ephemeral_1h_input_tokens")), now)
	if st.TTL != TTL1h || st.Left != 3500 {
		t.Errorf("later 1h: %+v", st)
	}
	row := `{"type":"assistant","timestamp":"` + stamp(now-10) + `","message":{"usage":{"cache_creation":{"ephemeral_5m_input_tokens":5,"ephemeral_1h_input_tokens":5}}}}`
	if st := StateOf(writeTranscript(t, row), now); st.TTL != TTL5m {
		t.Errorf("both in one block: %+v", st)
	}
}

// A newest call without usage falls back to an older call's TTL; the clock still starts at the newest.
func TestMissingUsageUsesEarlierTTL(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const now = 1_800_000_000.0
	bare := `{"type":"assistant","timestamp":"` + stamp(now-60) + `","message":{}}`
	st := StateOf(writeTranscript(t, asst(now-500, "ephemeral_1h_input_tokens"), bare), now)
	if st.TTL != TTL1h || st.Left != 3540 {
		t.Errorf("%+v", st)
	}
}

func TestMissingData(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const now = 1_800_000_000.0
	if StateOf("/nonexistent.jsonl", now) != nil {
		t.Error("missing file")
	}
	if StateOf(writeTranscript(t), now) != nil {
		t.Error("empty file")
	}
	user := `{"type":"user","timestamp":"` + stamp(now) + `"}`
	side := `{"type":"assistant","isSidechain":true,"timestamp":"` + stamp(now-5) + `","message":{}}`
	if StateOf(writeTranscript(t, "garbage{", user, side), now) != nil {
		t.Error("no main-thread assistant message")
	}
	noTS := `{"type":"assistant","message":{}}`
	if StateOf(writeTranscript(t, noTS), now) != nil {
		t.Error("no timestamp")
	}
	// sidechain rows after the main call are skipped
	st := StateOf(writeTranscript(t, asst(now-100, "ephemeral_5m_input_tokens"), side), now)
	if st == nil || st.Left != 200 {
		t.Errorf("%+v", st)
	}
	// no TTL info anywhere: 5m
	bare := `{"type":"assistant","timestamp":"` + stamp(now-100) + `","message":{"usage":{"cache_creation":{}}}}`
	if st := StateOf(writeTranscript(t, bare), now); st.TTL != TTL5m {
		t.Errorf("%+v", st)
	}
	if ttl, ok := ttlOf(nil); ok || ttl != 0 {
		t.Error("nil usage")
	}
	u, _ := ojson.Parse([]byte(`{"input_tokens":1}`))
	if _, ok := ttlOf(u); ok {
		t.Error("usage without cache_creation")
	}
}

// Only the tail of a huge transcript is read.
func TestLargeTranscriptTail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const now = 1_800_000_000.0
	pad := strings.Repeat("x", 5_000_000)
	p := writeTranscript(t, `{"pad":"`+pad+`"}`, asst(now-100, "ephemeral_1h_input_tokens"))
	if st := StateOf(p, now); st == nil || st.TTL != TTL1h {
		t.Errorf("%+v", st)
	}
}

func TestKeepalive(t *testing.T) {
	const now = 1_800_000_000.0
	setFlag(t, `{"enabled":true,"activityWindowMinutes":30}`)
	st := StateOf(writeTranscript(t, asst(now-600, "ephemeral_5m_input_tokens")), now)
	if !st.Kept || st.Left != 1800+300-600 {
		t.Errorf("%+v", st)
	}
	if got := Label(st); got != "Cache 剩 25m00s/背景續期" {
		t.Errorf("%q", got)
	}
	// the 1h cache is not touched by keepalive
	if st := StateOf(writeTranscript(t, asst(now-600, "ephemeral_1h_input_tokens")), now); st.Kept || st.Left != 3000 {
		t.Errorf("1h: %+v", st)
	}
	for flag, want := range map[string]float64{
		`{"enabled":true}`: 3600 + 300,
		`{"enabled":true,"activityWindowMinutes":"15"}`:   900 + 300,
		`{"enabled":true,"activityWindowMinutes":"junk"}`: 300,
		`{"enabled":true,"activityWindowMinutes":2.9}`:    120 + 300,
		`{"enabled":true,"activityWindowMinutes":null}`:   3600 + 300,
		`{"enabled":false}`:                               300,
		`true`:                                            300,
		`{"enabled":true,"activityWindowMinutes":[1]}`:    3600 + 300,
	} {
		setFlag(t, flag)
		st := StateOf(writeTranscript(t, asst(now, "ephemeral_5m_input_tokens")), now)
		if st.Left != want {
			t.Errorf("%s: left %v, want %v", flag, st.Left, want)
		}
	}
	// broken or missing .claude.json
	home := t.TempDir()
	t.Setenv("HOME", home)
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{oops"), 0o644)
	if _, ok := keepaliveWindow(); ok {
		t.Error("broken json")
	}
}

func TestStateFromInputEdges(t *testing.T) {
	const now = 1000.0
	for _, s := range []string{`{"ttl":"2h","expires_at":2000}`, `{"ttl":"5m"}`, `{"expires_at":2000}`} {
		pc, _ := ojson.Parse([]byte(s))
		if StateFromInput(pc, now) != nil {
			t.Errorf("%s should give nil", s)
		}
	}
	if StateFromInput(nil, now) != nil {
		t.Error("nil input")
	}
	pc, _ := ojson.Parse([]byte(`{"ttl":"5m","expires_at":1100}`))
	if st := StateFromInput(pc, now); st.Left != 100 || st.TTL != TTL5m {
		t.Errorf("%+v", st)
	}
}

func TestSplitlinesUnicode(t *testing.T) {
	if got := Splitlines("a b\u0085c\rd"); len(got) != 4 {
		t.Errorf("%q", got)
	}
}

func TestClockHours(t *testing.T) {
	if got := clock(-3700); got != "1h01m" {
		t.Errorf("%q", got)
	}
}

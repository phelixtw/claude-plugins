package cachettl

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"agentperform/internal/ojson"
)

func writeTranscript(t *testing.T, rows ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.jsonl")
	os.WriteFile(p, []byte(strings.Join(rows, "\n")), 0o644)
	return p
}

func stamp(ts float64) string {
	return time.Unix(int64(ts), 0).UTC().Format("2006-01-02T15:04:05.000Z")
}

func asst(ts float64, ttlKey string) string {
	return `{"type": "assistant", "timestamp": "` + stamp(ts) + `", "message": {"role": "assistant", "id": "m1", "usage": ` +
		`{"input_tokens": 1, "cache_creation_input_tokens": 100, "cache_creation": {"` + ttlKey + `": 100}}}}`
}

// The countdown runs from the last assistant call, not a later user line.
func TestStateFromTranscript(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no keepalive flag
	now := float64(time.Now().Unix())
	user := `{"type": "user", "timestamp": "` + stamp(now-10) + `", "message": {"content": "still here"}}`
	if got := Label(StateOf(writeTranscript(t, asst(now-600, "ephemeral_5m_input_tokens"), user), now)); got != "Cache 已過期" {
		t.Errorf("5m after 10 minutes: %q", got)
	}
	if got := Label(StateOf(writeTranscript(t, asst(now-600, "ephemeral_1h_input_tokens")), now)); got != "Cache 剩 50m00s/1h" {
		t.Errorf("1h after 10 minutes: %q", got)
	}
	pc, _ := ojson.Parse([]byte(`{"warm": true, "ttl": "1h", "expires_at": ` + strconv.FormatFloat(now+1200, 'f', -1, 64) + `}`))
	if got := Label(StateFromInput(pc, now)); got != "Cache 剩 20m00s/1h" {
		t.Errorf("statusline expiry: %q", got)
	}
	if pc, _ := ojson.Parse([]byte(`{"ttl": "5m", "expires_at": null}`)); StateFromInput(pc, now) != nil {
		t.Error("no expiry given: the transcript decides")
	}
}

func TestLabels(t *testing.T) {
	cases := []struct {
		st   *State
		want string
	}{
		{&State{3000, TTL1h, false}, "Cache 剩 50m00s/1h"},
		{&State{3180, TTL5m, true}, "Cache 剩 53m00s/背景續期"},
		{&State{240.5, TTL5m, false}, "Cache 剩 4m00s/5m"},
		{&State{7300, TTL1h, false}, "Cache 剩 2h01m/1h"},
		{&State{-1, TTL1h, false}, "Cache 已過期"},
		{nil, "Cache --"},
	}
	for _, c := range cases {
		if got := Label(c.st); got != c.want {
			t.Errorf("Label = %q, want %q", got, c.want)
		}
	}
}

func TestSplitlines(t *testing.T) {
	if got := Splitlines("a\r\nb c\n"); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("got %q", got)
	}
	if got := Splitlines("a\n\nb"); !reflect.DeepEqual(got, []string{"a", "", "b"}) {
		t.Errorf("got %q", got)
	}
}

func TestMissingTranscript(t *testing.T) {
	if _, ok, ttl := lastCall("/nonexistent.jsonl"); ok || ttl != TTL5m {
		t.Error("missing transcript")
	}
}

package dash

import (
	"fmt"
	"strings"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestOneCachedFloorsMatchFreshParse(t *testing.T) {
	h := testHome(t)
	p := filepath.Join(h, ".claude", "projects", "p", "s.jsonl")
	writeRows(t, p, userRec(1000, "a"), assistantRec(1001, "m1", 5000, 0, ""),
		userRec(2000, "b"), assistantRec(2001, "m2", 6000, 0, ""),
		userRec(3000, "c"), assistantRec(3001, "m3", 7000, 0, ""))
	oneMu.Lock()
	delete(oneCache, p)
	oneMu.Unlock()
	for _, floor := range []float64{500, 1500, 2500, 2000, 900, 3500, 100} {
		got, want := oneCached(p, floor), one(p, floor)
		if want != nil {
			want = want.clone() // oneCached hands out clones (nil slices become empty)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("floor %v: cached parse differs from a fresh one", floor)
		}
	}
	oneCached(p, 500)
	oneCached(p, 900) // no entry before 1000: both served from a cached parse without a new one
	if oneCache[p].floor == 900 || oneAlt[p].floor == 900 {
		t.Errorf("floor 900 reparsed; cache floors %v / %v", oneCache[p].floor, oneAlt[p].floor)
	}
}

func TestLiveRepeatsCachesPerTranscript(t *testing.T) {
	h := testHome(t)
	p := writeRows(t, filepath.Join(h, "t.jsonl"), userRec(1000, "a"), assistantRec(1001, "m1", 1, 0, ""))
	a := liveRepeats(p, "s1")
	if liveRepeats(p, "s1") != a {
		t.Error("unchanged transcript rescanned")
	}
	if liveRepeats(p, "s2") == a {
		t.Error("changed transcript served from cache")
	}
}

func TestPollGateBlocksCachedUntilFileChanges(t *testing.T) {
	h := testHome(t)
	p := filepath.Join(h, ".claude", "projects", "p", "s.jsonl")
	line := `{"x":"agent-perform: 前景輪詢"}`
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(line+"\n"), 0o644)
	if n := pollGateBlocks(); n != 1 {
		t.Fatalf("blocks %d, want 1", n)
	}
	os.WriteFile(p, []byte(line+"\n"+line+"\n"), 0o644)
	if n := pollGateBlocks(); n != 2 {
		t.Errorf("blocks %d after growth, want 2", n)
	}
}

func TestSavedPagesComeBackStale(t *testing.T) {
	testHome(t)
	serving.Store(true)
	defer serving.Store(false)
	h := newHandler(0)
	k := pageKey{days: 7, page: "context"}
	h.hits[k], h.order = cacheHit{Now().Add(-time.Hour), "<p>x</p>"}, []pageKey{k}
	snaps = map[string]snapHit{"b?section=repeats": {body: "r"}}
	h.save()
	h.clear()
	snaps = map[string]snapHit{}
	loadOnce = sync.Once{}
	h.load()
	if got := h.hits[k].html; got != "<p>x</p>" {
		t.Errorf("page not restored: %q", got)
	}
	if got := swr("b?section=repeats", func() string { return "fresh" }); got != "r" {
		t.Errorf("snapshot not served first: %q", got)
	}
}

func appendRows(t *testing.T, path string, rows ...any) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range rows {
		if _, err := f.WriteString("\n" + py.Dumps(r)); err != nil {
			t.Fatal(err)
		}
	}
}

// turnRows is n prompt+reply pairs starting at timestamp t0, one minute apart.
func turnRows(t0 float64, n int, tag string) []any {
	var rows []any
	for i := 0; i < n; i++ {
		ts := t0 + float64(i)*60
		rows = append(rows, userRec(ts, fmt.Sprintf("%s%d", tag, i)), assistantRec(ts+1, fmt.Sprintf("%s-m%d", tag, i), 6000+int64(i), 100, ""))
	}
	return rows
}

func forgetParses() {
	oneMu.Lock()
	oneCache, oneAlt = map[string]oneHit{}, map[string]oneHit{}
	oneMu.Unlock()
	parseLoaded = sync.Once{}
}

func sameAsFresh(t *testing.T, p string) {
	t.Helper()
	want := one(p, 0)
	if want != nil {
		want = want.clone()
	}
	if got := oneCached(p, 0); !reflect.DeepEqual(got, want) {
		t.Errorf("cached parse differs from a fresh one:\n got %+v\nwant %+v", got, want)
	}
}

func TestParseCacheRoundTrip(t *testing.T) {
	h := testHome(t)
	p := filepath.Join(h, ".claude", "projects", "p", "s.jsonl")
	writeRows(t, p, turnRows(1000, 4, "a")...)
	forgetParses()
	oneCached(p, 0)
	saveCaches()
	forgetParses()
	loadCaches()
	if oneCache[p].s == nil || oneCache[p].c == nil {
		t.Fatal("saved parse not restored")
	}
	sameAsFresh(t, p)
	if oneCache[p].s.Turns[0].Tools == nil || oneCache[p].s.Turns[0].Tools.N == nil {
		t.Error("restored counters are not usable")
	}
}

func TestParseCacheContinuesAppendedTranscript(t *testing.T) {
	h := testHome(t)
	p := filepath.Join(h, ".claude", "projects", "p", "s.jsonl")
	writeRows(t, p, turnRows(1000, 3, "a")...)
	forgetParses()
	oneCached(p, 0)
	saveCaches()
	forgetParses() // a new wake-up
	appendRows(t, p, turnRows(2000, 2, "b")...)
	sameAsFresh(t, p)
	// Rewrite the first prompt in place (same length): a continuation keeps the old head, a full reparse would not.
	raw, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(raw), `"a0"`, `"z0"`, 1)), 0o644)
	appendRows(t, p, turnRows(3000, 1, "c")...)
	got := oneCached(p, 0)
	if got.Turns[0].Prompt != "a0" || len(got.Turns) != 6 {
		t.Errorf("not continued from the carry: first prompt %q, %d turns", got.Turns[0].Prompt, len(got.Turns))
	}
}

func TestParseCacheReparsesShrunkTranscript(t *testing.T) {
	h := testHome(t)
	p := filepath.Join(h, ".claude", "projects", "p", "s.jsonl")
	writeRows(t, p, turnRows(1000, 4, "a")...)
	forgetParses()
	oneCached(p, 0)
	writeRows(t, p, turnRows(5000, 2, "b")...)
	sameAsFresh(t, p)
	if got := oneCached(p, 0); got.Turns[0].Prompt != "b0" {
		t.Errorf("shrunk file served from the old parse: %q", got.Turns[0].Prompt)
	}
}

func BenchmarkParseCacheWarmStart(b *testing.B) {
	h := testHome(b)
	var paths []string
	for i := 0; i < 200; i++ {
		p := filepath.Join(h, ".claude", "projects", "p", fmt.Sprintf("s%03d.jsonl", i))
		writeRows(b, p, turnRows(1000+float64(i)*1e4, 30, fmt.Sprintf("t%d-", i))...)
		paths = append(paths, p)
	}
	forgetParses()
	for _, p := range paths {
		oneCached(p, 0)
	}
	saveCaches()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		forgetParses()
		for _, p := range paths {
			oneCached(p, 0)
		}
	}
}

func TestParseCacheKeepsDelegationAndFixedTokens(t *testing.T) {
	testHome(t)
	s := delegatingSession(t) // fills deleCache through attachDelegation's liveDelegation
	row, stamps := liveDelegation(s.Path)
	fixedMu.Lock()
	fixedAll[s.Path], fixedSig[s.Path], fixedOne[s.Path] = 1234, [2]int64{5, 6}, [2]int64{77, 0}
	fixedMu.Unlock()
	parseDirty.Store(true)
	saveCaches()
	dropCaches()
	loadCaches()
	got, gotStamps := liveDelegation(s.Path)
	if ojson.Compact(got) != ojson.Compact(row) || !reflect.DeepEqual(gotStamps, stamps) {
		t.Errorf("delegation row changed across the round trip:\n got %s\nwant %s", ojson.Compact(got), ojson.Compact(row))
	}
	if fixedAll[s.Path] != 1234 || fixedSig[s.Path] != [2]int64{5, 6} || fixedOne[s.Path][0] != 77 {
		t.Errorf("fixed tokens not restored: %v %v", fixedAll, fixedSig)
	}
	if parseDirty.Load() {
		t.Error("restored delegation counted as new work")
	}
}

func TestNarrowParseDoesNotEvictTheWideOne(t *testing.T) {
	h := testHome(t)
	p := filepath.Join(h, ".claude", "projects", "p", "s.jsonl")
	writeRows(t, p, turnRows(1000, 4, "a")...) // entries at 1000..1181
	forgetParses()
	oneCached(p, 0)
	wide := oneCache[p].s
	oneCached(p, 1100) // drops entries, so the wide parse cannot serve it
	if oneCache[p].s != wide || oneAlt[p].floor != 1100 {
		t.Fatalf("wide slot floor %v, alt floor %v", oneCache[p].floor, oneAlt[p].floor)
	}
	narrow := oneAlt[p].s
	oneCached(p, 0)
	oneCached(p, 1100)
	if oneCache[p].s != wide || oneAlt[p].s != narrow {
		t.Error("alternating floors reparsed")
	}
	saveCaches()
	forgetParses()
	loadCaches()
	if oneCache[p].floor != 0 || oneAlt[p].floor != 1100 {
		t.Errorf("slots after reload: %v / %v", oneCache[p].floor, oneAlt[p].floor)
	}
}

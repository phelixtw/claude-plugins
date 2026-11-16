package dash

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"agentperform/internal/ojson"
)

func foldersEqual(t *testing.T, folder string) {
	t.Helper()
	got := scanFolder(folder)
	agentMu.Lock()
	agentCache = map[string]agentHit{}
	agentMu.Unlock()
	want := scanFolder(folder)
	if len(got) != len(want) {
		t.Fatalf("incremental scan found %d agents, full rescan %d", len(got), len(want))
	}
	for id, w := range want {
		if ojson.Compact(got[id]) != ojson.Compact(w) {
			t.Errorf("agent %s differs from a full rescan:\n got %s\nwant %s", id, ojson.Compact(got[id]), ojson.Compact(w))
		}
	}
}

func TestScanFolderIsIncremental(t *testing.T) {
	h := testHome(t)
	folder := filepath.Join(h, "sess", "subagents")
	a1 := writeRows(t, filepath.Join(folder, "agent-a1.jsonl"), assistantRec(1000, "x1", 10, 0, ""))
	scanFolder(folder)
	writeRows(t, filepath.Join(folder, "agent-a2.jsonl"), assistantRec(2000, "y1", 20, 0, "")) // a new subagent file
	if got := scanFolder(folder); len(got) != 2 {
		t.Fatalf("new subagent file not picked up: %d agents", len(got))
	}
	foldersEqual(t, folder)
	appendRows(t, a1, assistantRec(1001, "x2", 30, 0, "")) // an appended one
	got := scanFolder(folder)["a1"]
	if tok := ojson.Field(ojson.Field(got, "tok"), "cw"); num(tok) != 40 {
		t.Errorf("appended assistant record not counted: cw %v", tok)
	}
	foldersEqual(t, folder)
}

func TestScanFolderReusesUnchangedAgents(t *testing.T) {
	h := testHome(t)
	folder := filepath.Join(h, "sess", "subagents")
	a1 := writeRows(t, filepath.Join(folder, "agent-a1.jsonl"), assistantRec(1000, "x1", 10, 0, ""))
	scanFolder(folder)
	agentMu.Lock()
	first := agentCache[a1].scan
	agentMu.Unlock()
	writeRows(t, filepath.Join(folder, "agent-a2.jsonl"), assistantRec(2000, "y1", 20, 0, ""))
	scanFolder(folder)
	agentMu.Lock()
	second := agentCache[a1].scan
	agentMu.Unlock()
	if first != second {
		t.Error("an unchanged subagent transcript was scanned again")
	}
}

func TestPromptStampsContinueAppendedTranscript(t *testing.T) {
	h := testHome(t)
	p := writeRows(t, filepath.Join(h, "t.jsonl"), userRec(1000, "a"), assistantRec(1001, "m1", 1, 0, ""), userRec(1100, "b"))
	promptStamps(p)
	appendRows(t, p, assistantRec(1101, "m2", 1, 0, ""), userRec(1200, "c"))
	got := promptStamps(p)
	if want, _ := readPromptStamps(p, stampHit{}); !reflect.DeepEqual(got, want) || len(got) != 3 {
		t.Errorf("got %v, a full read gives %v", got, want)
	}
	stampMu.Lock()
	off := stampCache[p].off
	stampMu.Unlock()
	if off == 0 {
		t.Error("the last prompt's offset was not kept")
	}
	appendRows(t, p, assistantRec(1201, "m3", 1, 0, "")) // no new prompt: the last one is read again, not duplicated
	if got := promptStamps(p); len(got) != 3 {
		t.Errorf("stamps after an assistant-only append: %v", got)
	}
}

func TestParseCacheKeepsStampsRepeatsGateAndAgents(t *testing.T) {
	h := testHome(t)
	p := writeRows(t, filepath.Join(h, ".claude", "projects", "p", "s.jsonl"),
		userRec(1000, "a"), assistantRec(1001, "m1", 1, 0, ""), userRec(1100, "agent-perform: 前景輪詢"))
	a := writeRows(t, filepath.Join(h, ".claude", "projects", "p", "s", "subagents", "agent-a1.jsonl"), assistantRec(1002, "x1", 10, 0, ""))
	stamps := append([]float64{}, promptStamps(p)...)
	sig, _ := mainSig(p)
	rep := liveRepeats(p, sig)
	gate := pollGateBlocks()
	agents := scanFolder(filepath.Dir(a))
	if !strings.Contains(ojson.Compact(rep), "turns") || gate != 1 || len(agents) != 1 {
		t.Fatalf("setup: rep %s gate %d agents %d", ojson.Compact(rep), gate, len(agents))
	}
	parseDirty.Store(true)
	saveCaches()
	dropCaches()
	stampMu.Lock()
	stampCache = map[string]stampHit{}
	stampMu.Unlock()
	loadCaches()
	if got := promptStamps(p); !reflect.DeepEqual(got, stamps) {
		t.Errorf("stamps %v, want %v", got, stamps)
	}
	if got := liveRepeats(p, sig); ojson.Compact(got) != ojson.Compact(rep) {
		t.Errorf("repeats changed across the round trip: %s vs %s", ojson.Compact(got), ojson.Compact(rep))
	}
	if n := pollGateBlocks(); n != gate {
		t.Errorf("gate %d, want %d", n, gate)
	}
	if got := scanFolder(filepath.Dir(a)); ojson.Compact(got["a1"]) != ojson.Compact(agents["a1"]) {
		t.Errorf("agent scan changed across the round trip")
	}
	if parseDirty.Load() {
		t.Error("restored entries counted as new work")
	}
}

func TestOneCachedIgnoresSubagentFileChanges(t *testing.T) {
	h := testHome(t)
	p := writeRows(t, filepath.Join(h, ".claude", "projects", "p", "s.jsonl"), turnRows(1000, 4, "a")...)
	forgetParses()
	oneCached(p, 0)
	first := oneCache[p].s
	writeRows(t, filepath.Join(h, ".claude", "projects", "p", "s", "subagents", "agent-a1.jsonl"), assistantRec(1002, "x1", 10, 0, ""))
	oneCached(p, 0)
	if oneCache[p].s != first {
		t.Error("a new subagent file reparsed the main transcript")
	}
}

package dash

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStatsRoundTripAndIncremental(t *testing.T) {
	h := testHome(t)
	now := float64(time.Now().Unix()) - 5000
	a := filepath.Join(h, ".claude", "projects", "p", "aaaaaaaa-1.jsonl")
	writeRows(t, a, userRec(now, "hi"), assistantRec(now+5, "m1", 200_000, 0, ""))
	d := refreshStats()
	if len(d.Files) != 1 || len(d.Files[0].Ats) != 1 || len(d.Files[0].QCost) != 1 {
		t.Fatalf("build: %+v", d.Files)
	}
	if loaded := loadStats(); loaded == nil || len(loaded.Files) != 1 || loaded.Files[0].Ats[0] != d.Files[0].Ats[0] {
		t.Fatal("round trip")
	}
	// a resumed copy born later: the older file keeps the turn, the baseline counts it once
	time.Sleep(10 * time.Millisecond)
	b := filepath.Join(h, ".claude", "projects", "p", "bbbbbbbb-2.jsonl")
	writeRows(t, b, userRec(now, "hi"), assistantRec(now+5, "m1", 200_000, 0, ""), userRec(now+60, "more"), assistantRec(now+65, "m2", 10_000, 0, ""))
	d = refreshStats()
	if len(d.Files) != 2 || len(d.Files[1].Ats) != 2 {
		t.Fatalf("incremental: %+v", d.Files)
	}
	owner, base := d.owners(now - 1)
	if owner[now] != "aaaaaaaa" || owner[now+60] != "bbbbbbbb" || base != d.Files[0].QCost[0] {
		t.Errorf("owners %v base %v", owner, base)
	}
}

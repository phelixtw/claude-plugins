package dash

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/repetition"
	"agentperform/internal/transcript"
)

const (
	ctxStd = 200_000
	ctx1M  = 1_000_000
)

func claudeFile(name string) string { return filepath.Join(home(), ".claude", name) }

// applyWindow fills each turn's context window size.
func applyWindow(sessions []*Session) {
	for _, s := range sessions {
		window := ctxLimit(s)
		for _, t := range s.Turns {
			t.Window = window
		}
	}
}

// ── caches ───────────────────────────────────────────────────────────────

// oneHit is one parse at `floor`; lo and hi (see parse) say which other
// floors give the same result, so changing days or crossing midnight reparses
// only transcripts with entries in between.
type oneHit struct {
	sig    string
	floor  float64
	lo, hi float64
	s      *Session
	size   int64  // the transcript's size when parsed
	c      *carry // where a parse can continue when the file only grew
}

func (h oneHit) fits(sig string, floor float64) bool {
	return h.sig == sig && (floor == h.floor || floor > h.floor && floor <= h.lo || floor < h.floor && floor > h.hi)
}

// oneCache keeps the widest parse of each transcript (lowest floor asked so far) and oneAlt the latest
// narrower one, so a page over 90 days and the mod over today do not evict each other.
var (
	oneMu    sync.Mutex
	oneCache = map[string]oneHit{}
	oneAlt   = map[string]oneHit{}
	flights  = map[string]*sync.Mutex{}
)

// slotted is the cached parse of path that serves floor, if any.
func slotted(path, sig string, floor float64) (oneHit, bool) {
	for _, m := range []map[string]oneHit{oneCache, oneAlt} {
		if hit, ok := m[path]; ok && hit.fits(sig, floor) {
			return hit, true
		}
	}
	return oneHit{}, false
}

// keep stores a parse: the widest one in oneCache, a narrower one in oneAlt.
func keep(path string, hit oneHit) {
	if wide, ok := oneCache[path]; ok && hit.floor > wide.floor {
		oneAlt[path] = hit
		return
	} else if ok && hit.floor < wide.floor {
		oneAlt[path] = wide
	}
	oneCache[path] = hit
}

// flight serialises the work on one key, so concurrent renders share one scan.
func flight(key string) *sync.Mutex {
	oneMu.Lock()
	defer oneMu.Unlock()
	if flights[key] == nil {
		flights[key] = &sync.Mutex{}
	}
	return flights[key]
}

// mainSig is (mtime, size) of a transcript alone: a parse or repeat scan reads nothing else, so a
// subagent file that grows does not invalidate it.
func mainSig(path string) (string, error) {
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(st.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(st.Size(), 10), nil
}

// statSig is mainSig plus the stats of its subagent sidecar files (what the delegation scan reads).
func statSig(path string) (string, error) {
	main, err := mainSig(path)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(main)
	dir := strings.TrimSuffix(path, ".jsonl") + string(os.PathSeparator) + "subagents"
	if fh, err := os.Open(dir); err == nil {
		names, _ := fh.Readdirnames(-1)
		fh.Close()
		for _, n := range names {
			if s2, err := os.Stat(filepath.Join(dir, n)); err == nil {
				b.WriteString("|" + n + ":" + strconv.FormatInt(s2.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(s2.Size(), 10))
			}
		}
	}
	return b.String(), nil
}

// oneCached is one() memoised on file stats; a copy goes out because callers
// annotate the records in place.
func oneCached(path string, floor float64) *Session {
	loadCaches()
	sig, err := mainSig(path)
	if err != nil {
		return one(path, floor)
	}
	cached := func() (oneHit, bool) {
		oneMu.Lock()
		defer oneMu.Unlock()
		return slotted(path, sig, floor)
	}
	hit, ok := cached()
	if !ok {
		f := flight(path)
		f.Lock()
		if hit, ok = cached(); !ok {
			hit = reparse(path, floor, sig)
			oneMu.Lock()
			keep(path, hit)
			oneMu.Unlock()
			parseDirty.Store(true)
		}
		f.Unlock()
	}
	if hit.s == nil {
		return nil
	}
	return hit.s.clone()
}

// reparse parses path afresh, or from the previous parse's carry when the transcript only grew
// and the floor is unchanged (a shrunk or rewritten file is parsed in full).
func reparse(path string, floor float64, sig string) oneHit {
	var size int64
	if st, err := os.Stat(path); err == nil {
		size = st.Size()
	}
	var rs *resume
	oneMu.Lock()
	for _, m := range []map[string]oneHit{oneCache, oneAlt} {
		if old, ok := m[path]; ok && old.c != nil && old.s != nil && old.floor == floor && size > old.size {
			rs = &resume{old.c, old.s}
		}
	}
	oneMu.Unlock()
	s, lo, hi, c := parseFrom(path, floor, rs)
	return oneHit{sig, floor, lo, hi, s, size, c}
}

func born(path string) float64 {
	st, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return birthTime(st)
}

// dropCopiedTurns: a continued session's transcript starts with the earlier
// conversation again; each turn counts only in the oldest file that has it.
func dropCopiedTurns(sessions []*Session) []*Session {
	byBirth := append([]*Session{}, sessions...)
	sort.SliceStable(byBirth, func(i, j int) bool { return born(byBirth[i].Path) < born(byBirth[j].Path) })
	owner := map[float64]string{}
	for _, s := range byBirth {
		for _, t := range s.Turns {
			if _, ok := owner[t.At]; !ok {
				owner[t.At] = s.ID
			}
		}
	}
	return keepOwned(sessions, owner)
}

// keepOwned trims each session to the turns that `owner` (turn time to session id) assigns to it.
func keepOwned(sessions []*Session, owner map[float64]string) []*Session {
	var out []*Session
	for _, s := range sessions {
		var mine []*Turn
		for _, t := range s.Turns {
			if owner[t.At] == s.ID {
				mine = append(mine, t)
			}
		}
		if len(mine) == len(s.Turns) {
			out = append(out, s)
			continue
		}
		if len(mine) == 0 {
			continue
		}
		var tot Usage
		for _, t := range mine {
			tot.add(t.Usage)
		}
		c := *s
		c.Turns, c.Totals, c.Start = mine, tot, mine[0].At
		c.End = max(s.End, mine[len(mine)-1].At)
		c.Opening = py.Head(mine[0].Prompt, 70)
		c.Loads = nil
		for _, l := range s.Loads {
			if l.At >= mine[0].At {
				c.Loads = append(c.Loads, l)
			}
		}
		out = append(out, &c)
	}
	return out
}

// Now is the dashboard's clock; AP_NOW (epoch seconds) freezes it for comparisons.
func Now() time.Time {
	if v := os.Getenv("AP_NOW"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return fromTimestamp(f)
		}
	}
	return time.Now()
}

// workSem caps the parse workers of all concurrent renders at one per CPU.
var workSem = make(chan struct{}, runtime.NumCPU())

// parallel runs fn(0..n-1) on one worker per CPU.
func parallel(n int, fn func(int)) {
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < runtime.NumCPU(); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				workSem <- struct{}{}
				fn(i)
				<-workSem
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}

// floorOf is the start of the day `days` days back (today counts as one).
func floorOf(days int) float64 {
	now := Now()
	d := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -(days - 1))
	return float64(d.UnixNano()) / 1e9
}

// transcriptPaths are the main transcripts last written at or after floor, in walk order.
func transcriptPaths(floor float64) []string {
	var paths []string
	walkDir(projects(), func(dir string, files []string) {
		if inSubagents(dir) {
			return
		}
		for _, f := range files {
			if !strings.HasSuffix(f, ".jsonl") {
				continue
			}
			p := filepath.Join(dir, f)
			st, err := os.Stat(p)
			if err != nil || float64(st.ModTime().UnixNano())/1e9 < floor {
				continue
			}
			paths = append(paths, p)
		}
	})
	return paths
}

// Collect is every session with a turn in the last `days` days, newest first.
func Collect(days int) []*Session {
	loadCaches()
	floor := floorOf(days)
	paths := transcriptPaths(floor)
	// Transcripts parse independently: fill the per-file caches concurrently,
	// then assemble in walk order so every sum keeps its order.
	got := make([]*Session, len(paths))
	parallel(len(paths), func(i int) {
		got[i] = oneCached(paths[i], floor)
		if got[i] != nil {
			liveDelegation(paths[i])
			FixedTokens(paths[i], CtxProbeCats)
		}
	})
	var out []*Session
	for _, s := range got {
		if s != nil {
			out = append(out, s)
		}
	}
	out = dropCopiedTurns(out)
	applyWindow(out)
	attachDelegation(out)
	saveCachesSoon()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start > out[j].Start })
	return out
}

// ── /context probe ───────────────────────────────────────────────────────

var (
	probeMu    sync.Mutex
	probeCache = map[string]*ojson.Object{}
	probeSeen  = map[string]bool{}
)

// CtxProbeCats is the real /context readout for this cwd+model, if the probe
// ever ran; falls back to the same cwd, then the same model. Cached for the
// life of the process, like the Python lru_cache.
func CtxProbeCats(cwd, model string) *ojson.Object {
	key := cwd + "\x00" + model
	probeMu.Lock()
	if probeSeen[key] {
		v := probeCache[key]
		probeMu.Unlock()
		return v
	}
	probeMu.Unlock()
	v := ctxProbeCats(cwd, model)
	probeMu.Lock()
	probeSeen[key], probeCache[key] = true, v
	probeMu.Unlock()
	return v
}

func ctxProbeCats(cwd, model string) *ojson.Object {
	raw, err := os.ReadFile(claudeFile("ctx-cache.json"))
	if err != nil {
		return nil
	}
	v, err := ojson.Parse(raw)
	cache := ojson.Obj(v)
	if err != nil || cache == nil {
		return nil
	}
	var rows []*ojson.Object
	for _, k := range cache.Keys {
		if r := ojson.Obj(cache.Vals[k]); r != nil && ojson.Truthy(ojson.Field(r, "cats")) {
			rows = append(rows, r)
		}
	}
	if exact := ojson.Obj(ojson.Field(cache, cwd+"|"+model)); exact != nil && ojson.Truthy(ojson.Field(exact, "cats")) {
		return exact
	}
	for _, pick := range []func(*ojson.Object) bool{
		func(r *ojson.Object) bool { s, ok := ojson.Field(r, "cwd").(string); return ok && s == cwd },
		func(r *ojson.Object) bool { s, ok := ojson.Field(r, "model").(string); return ok && s == model },
	} {
		var best *ojson.Object
		bestAt := 0.0
		for _, r := range rows {
			if !pick(r) {
				continue
			}
			at, _ := py.Number(orNil(ojson.Field(r, "at"), int64(0)))
			if best == nil || at > bestAt {
				best, bestAt = r, at
			}
		}
		if best != nil {
			return best
		}
	}
	return nil
}

func ctxMainModel(s *Session) string {
	if s.Models == nil || len(s.Models.keys) == 0 {
		return ""
	}
	return s.Models.mostCommon()
}

// ctxLimit is the one window size every page uses for a session.
func ctxLimit(s *Session) int64 {
	var peak int64
	for _, t := range s.Turns {
		peak = max(peak, t.CtxPeak)
	}
	if strings.Contains(strings.Join(s.Models.keys, " "), "[1m]") || peak > ctxStd {
		return ctx1M
	}
	probe := CtxProbeCats(s.Cwd, ctxMainModel(s))
	if v := ojson.GetOr(probe, "limit"); v != nil {
		return num(v)
	}
	return ctxStd
}

// ── delegation ───────────────────────────────────────────────────────────

var (
	delegMu  sync.Mutex
	delegSig string
	delegVal = ojson.NewObject()
)

func delegates() *ojson.Object {
	path := claudeFile("subagent_costs.json")
	st, err := os.Stat(path)
	if err != nil {
		return ojson.NewObject()
	}
	sig := strconv.FormatInt(st.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(st.Size(), 10)
	delegMu.Lock()
	defer delegMu.Unlock()
	if sig != delegSig {
		raw, err := os.ReadFile(path)
		if err != nil {
			return ojson.NewObject()
		}
		v, err := ojson.Parse(raw)
		if err != nil {
			return ojson.NewObject()
		}
		delegSig, delegVal = sig, ojson.Obj(v)
	}
	return delegVal
}

// promptStamps is the timestamp of every real user prompt, in the numbering
// subagents.py and repetition.py use.
func promptStamps(path string) []float64 {
	loadCaches()
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	sig := fmt.Sprintf("%d:%d", st.ModTime().UnixNano(), st.Size())
	stampMu.Lock()
	hit, ok := stampCache[path]
	stampMu.Unlock()
	if ok && hit.sig == sig {
		return hit.out
	}
	if !ok || st.Size() <= hit.size {
		hit = stampHit{}
	}
	out, off := readPromptStamps(path, hit)
	stampMu.Lock()
	stampCache[path] = stampHit{sig, st.Size(), off, out}
	stampMu.Unlock()
	parseDirty.Store(true)
	return out
}

// stampHit is a transcript's prompt stamps; off is where its last prompt line starts, so a transcript that only grew
// is read again from there.
type stampHit struct {
	sig  string
	size int64
	off  int64
	out  []float64
}

var (
	stampMu    sync.Mutex
	stampCache = map[string]stampHit{} // ponytail: one entry per transcript ever read, never pruned
)

// readPromptStamps continues prev (zero for a fresh read); it returns the stamps and the offset of the last prompt.
func readPromptStamps(path string, prev stampHit) ([]float64, int64) {
	var out []float64
	last, off := 0.0, prev.off
	if n := len(prev.out); n > 0 {
		out = slices.Clone(prev.out[:n-1]) // the last prompt is read again
		if n > 1 {
			last = out[n-2]
		}
	}
	for pos, e := range transcript.EntriesAt(path, prev.off) {
		if ojson.Truthy(ojson.Field(e, "isSidechain")) || !transcript.IsPrompt(e) {
			continue
		}
		if v, ok := transcript.When(e); ok && v != 0 {
			last = v
		}
		out, off = append(out, last), pos
	}
	return out, off
}

// turnAt is the 1-based dashboard turn running at stamp; 0 if before them all.
func turnAt(turns []*Turn, stamp float64) int {
	return sort.Search(len(turns), func(i int) bool { return turns[i].At > stamp })
}

type deleHit struct {
	sig    string
	row    *ojson.Object
	stamps []float64
}

var (
	deleMu    sync.Mutex
	deleCache = map[string]deleHit{}
)

func liveDelegation(path string) (*ojson.Object, []float64) {
	sig, err := statSig(path)
	if err != nil {
		return nil, nil
	}
	deleMu.Lock()
	hit, ok := deleCache[path]
	deleMu.Unlock()
	if !ok || hit.sig != sig {
		hit = deleHit{sig, ForSession(path), promptStamps(path)}
		deleMu.Lock()
		deleCache[path] = hit
		deleMu.Unlock()
		parseDirty.Store(true)
	}
	return hit.row, hit.stamps
}

func copyObj(o *ojson.Object) *ojson.Object {
	c := ojson.NewObject()
	for _, k := range o.Keys {
		c.Set(k, o.Vals[k])
	}
	return c
}

// sessionDelegation places each launch on the dashboard's own turn numbers.
func sessionDelegation(s *Session) *ojson.Object {
	var row *ojson.Object
	var stamps []float64
	if s.Path != "" {
		row, stamps = liveDelegation(s.Path)
	}
	if row == nil {
		row = ojson.Obj(ojson.Field(delegates(), s.ID))
	}
	if row.Len() == 0 {
		return nil
	}
	launches, _ := ojson.Field(row, "launches").([]any)
	var placed []*ojson.Object
	for _, lv := range launches {
		l := ojson.Obj(lv)
		k := int(num(orNil(ojson.GetOr(l, "turn"), int64(1))))
		var ref *float64
		if 1 <= k && k <= len(stamps) {
			v := stamps[k-1]
			ref = &v
		} else if v, ok := py.Number(ojson.Field(l, "start")); ok && ojson.Field(l, "start") != nil {
			ref = &v
		}
		n := 0
		if ref == nil {
			n = min(k, len(s.Turns))
		} else {
			n = turnAt(s.Turns, *ref)
		}
		var at float64
		if v := ojson.GetOr(l, "start"); v != nil {
			at, _ = py.Number(v)
		} else if ref != nil && *ref != 0 {
			at = *ref
		} else if n != 0 {
			at = s.Turns[n-1].At
		} else {
			at = s.Start
		}
		p := copyObj(l)
		if n != 0 {
			p.Set("turn", int64(n))
		} else {
			p.Set("turn", nil)
		}
		p.Set("at", at)
		placed = append(placed, p)
	}
	// a launch with no priced transcript that repeats a priced one is the same subagent seen twice
	priced := map[string]bool{}
	for _, l := range placed {
		if ojson.Has(l, "cost") && ojson.Truthy(ojson.Field(l, "what")) {
			priced[py.Repr(ojson.Field(l, "what"))+"\x00"+py.Repr(ojson.Field(l, "kind"))] = true
		}
	}
	var kept []any
	for _, l := range placed {
		if ojson.Has(l, "cost") || !priced[py.Repr(ojson.Field(l, "what"))+"\x00"+py.Repr(ojson.Field(l, "kind"))] {
			kept = append(kept, l)
		}
	}
	out := copyObj(row)
	out.Set("n", int64(len(kept)))
	if kept == nil {
		kept = []any{}
	}
	return out.Set("launches", kept)
}

func launchesOf(row *ojson.Object) []*ojson.Object {
	list, _ := ojson.Field(row, "launches").([]any)
	var out []*ojson.Object
	for _, l := range list {
		out = append(out, ojson.Obj(l))
	}
	return out
}

func fnum(o any, k string) float64 {
	f, _ := py.Number(ojson.Field(o, k))
	return f
}

// attachDelegation puts each session's placed delegation on s.Dele and its
// cost and count on the launching turn.
func attachDelegation(sessions []*Session) {
	parallel(len(sessions), func(i int) { // the scans of changed sessions run side by side
		if p := sessions[i].Path; p != "" {
			liveDelegation(p)
		}
	})
	for _, s := range sessions {
		s.Dele = sessionDelegation(s)
		for _, t := range s.Turns {
			t.DCost, t.DEnd, t.Dele, t.DScripts, t.DTok, t.DCalls = 0, 0, 0, 0, 0, 0
		}
		for _, l := range launchesOf(s.Dele) {
			if n := int(num(ojson.Field(l, "turn"))); ojson.Truthy(ojson.Field(l, "turn")) {
				t := s.Turns[n-1]
				t.DCost += fnum(l, "cost")
				t.DEnd = endAfter(t.DEnd, fnum(l, "start"), fnum(l, "secs"))
				t.Dele++
				t.DScripts += int(fnum(l, "scripts"))
				t.DTok += launchTokens(l)
				t.DCalls += int(fnum(l, "calls"))
			}
		}
	}
}

func day(ts float64) string { return fromTimestamp(ts).Format("2006-01-02") }

type dayDele struct {
	cost float64
	n    int
}

// dayDeleMap is an insertion-ordered map, so sums run in Python's dict order.
type dayDeleMap struct {
	keys []string
	m    map[string]*dayDele
}

func newDayDeleMap() *dayDeleMap { return &dayDeleMap{m: map[string]*dayDele{}} }

func (d *dayDeleMap) get(k string) *dayDele {
	if d == nil {
		return nil
	}
	return d.m[k]
}

func (d *dayDeleMap) set(k string, v *dayDele) {
	if _, ok := d.m[k]; !ok {
		d.keys = append(d.keys, k)
	}
	d.m[k] = v
}

type byDay struct {
	keys []string
	m    map[string]*dayDeleMap
}

// delegationByDay is {day: {session id: (cost, n)}}, each launch on the day it happened.
func delegationByDay(sessions []*Session) *byDay {
	out := &byDay{m: map[string]*dayDeleMap{}}
	for _, s := range sessions {
		for _, l := range launchesOf(s.Dele) {
			d := day(fnum(l, "at"))
			if out.m[d] == nil {
				out.m[d] = newDayDeleMap()
				out.keys = append(out.keys, d)
			}
			slot := out.m[d].get(s.ID)
			if slot == nil {
				slot = &dayDele{}
				out.m[d].set(s.ID, slot)
			}
			slot.cost += fnum(l, "cost")
			slot.n++
		}
	}
	return out
}

// ── repeats ──────────────────────────────────────────────────────────────

var (
	repMu    sync.Mutex
	repCache = map[string]repHit{}
)

type repHit struct {
	sig string
	v   *ojson.Object
}

// liveRepeats is the repeat scan of one transcript, kept until its stats change.
// ponytail: entries are never pruned; one per transcript ever scanned.
func liveRepeats(path, sig string) *ojson.Object {
	loadCaches()
	repMu.Lock()
	hit, ok := repCache[path]
	repMu.Unlock()
	if ok && hit.sig == sig {
		return hit.v
	}
	f := flight("rep\x00" + path)
	f.Lock()
	defer f.Unlock()
	repMu.Lock()
	hit, ok = repCache[path]
	repMu.Unlock()
	if ok && hit.sig == sig {
		return hit.v
	}
	calls, total := repetition.Scan(path)
	v := repetition.Measure(calls, total)
	repMu.Lock()
	repCache[path] = repHit{sig, v}
	repMu.Unlock()
	parseDirty.Store(true)
	return v
}

// remapRepeats renumbers the repeat-call entry to the dashboard's turns.
func remapRepeats(s *Session, rep *ojson.Object) *ojson.Object {
	if rep.Len() == 0 || s.Path == "" {
		return rep
	}
	if sig, err := mainSig(s.Path); err == nil {
		live := ojson.Obj(ojson.Field(liveRepeats(s.Path, sig), "reasons"))
		reasons := ojson.NewObject()
		if rs := ojson.Obj(ojson.GetOr(rep, "reasons")); rs != nil {
			for _, k := range rs.Keys {
				r := ojson.Obj(rs.Vals[k])
				nr := ojson.NewObject()
				if r != nil {
					for _, c := range r.Keys {
						d := r.Vals[c]
						if lv, ok := ojson.Obj(ojson.Field(live, k)).Get(c); ok {
							d = lv
						}
						nr.Set(c, d)
					}
				}
				reasons.Set(k, nr)
			}
		}
		rep = copyObj(rep)
		rep.Set("reasons", reasons)
	}
	stamps := promptStamps(s.Path)
	var flagged []int
	reasons := ojson.NewObject()
	fl, _ := ojson.GetOr(rep, "flagged").([]any)
	for _, kv := range fl {
		k := int(num(kv))
		if k < 1 || k > len(stamps) {
			continue
		}
		n := turnAt(s.Turns, stamps[k-1])
		if n == 0 {
			continue
		}
		found := false
		for _, f := range flagged {
			if f == n {
				found = true
			}
		}
		if !found {
			flagged = append(flagged, n)
		}
		key := strconv.Itoa(n)
		if !ojson.Has(reasons, key) {
			reasons.Set(key, ojson.NewObject())
		}
		target := ojson.Obj(reasons.Vals[key])
		if src := ojson.Obj(ojson.GetOr(ojson.GetOr(rep, "reasons"), strconv.Itoa(k))); src != nil {
			for _, c := range src.Keys {
				target.Set(c, src.Vals[c])
			}
		}
	}
	sort.Ints(flagged)
	fla := []any{}
	for _, f := range flagged {
		fla = append(fla, int64(f))
	}
	total := len(s.Turns)
	share := 0.0
	if total > 0 {
		share = py.Round(float64(len(flagged))/float64(total), 4)
	}
	out := copyObj(rep)
	return out.Set("flagged", fla).Set("reasons", reasons).Set("turns", int64(total)).Set("share", share)
}

// health is agent_health.json, or an empty one.
func health() *ojson.Object {
	raw, err := os.ReadFile(claudeFile("agent_health.json"))
	if err == nil {
		if v, err := ojson.Parse(raw); err == nil && ojson.Obj(v) != nil {
			return ojson.Obj(v)
		}
	}
	return ojson.NewObject().Set("sessions", ojson.NewObject()).Set("loud", 0.15)
}

func healthSessions() *ojson.Object {
	s := ojson.Obj(ojson.Field(health(), "sessions"))
	if s == nil {
		return ojson.NewObject()
	}
	return s
}

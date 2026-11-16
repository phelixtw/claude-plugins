package dash

import (
	"agentperform/internal/ojson"
	"compress/gzip"
	"encoding/binary"
	"encoding/gob"
	"io"
	"math"
	"net"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ── parse cache on disk ──────────────────────────────────────────────────
//
// Every transcript's parse (oneCache), delegations (deleCache), fixed-token count, prompt stamps (stampCache), repeat scan (repCache),
// poll-gate count (gateCache) and each subagent transcript's scan (agentCache) are kept between wake-ups, so a cold start
// only reads the files whose size or mtime changed, and a transcript
// that only grew is continued from its carry. Bump parseCacheVersion whenever
// Session, Turn or carry change: an older file is then ignored and rebuilt.

const parseCacheVersion = "parse-v4"

var (
	parseDirty  atomic.Bool
	parseLoaded sync.Once
)

func parseCacheFile() string { return claudeFile("agent-perform-parse-cache.gob.gz") }

type oneRec struct {
	Sig           string
	Floor, Lo, Hi float64
	Size          int64
	S             *Session
	C             *carry
}

// pathRec is everything cached for one transcript; the file is a gob stream of a version string, then these.
type pathRec struct {
	Path string
	Ones []oneRec // the parses (oneCache, then oneAlt)
	// its delegations (deleCache); the row is JSON because gob cannot carry nil values inside it
	HasDele bool
	DeleSig string
	DeleRow string
	Stamps  []float64
	// first-turn fixed tokens (fixedAll, fixedOne, fixedSig)
	HasFixedAll bool
	FixedAll    int64
	HasFixedOne bool
	FixedSig    [2]int64
	FixedOne    int64
	// prompt stamps (stampCache)
	HasStamps           bool
	StampSig            string
	StampSize, StampOff int64
	Stamps2             []float64
	// repeat scan (repCache); the row is JSON like DeleRow
	HasRep bool
	RepSig string
	RepRow string
	// poll-gate count (gateCache)
	HasGate             bool
	GateMtime, GateSize int64
	GateN               int
	// scan of a subagent transcript (agentCache); Path is that transcript
	HasAgent bool
	AgentSig string
	AgentRow string
}

// GobEncode and GobDecode: the ordered sets and counters keep their order in unexported fields.
func appendStr(b []byte, s string) []byte {
	return append(binary.AppendUvarint(b, uint64(len(s))), s...)
}

func readStr(data []byte) (string, []byte, error) {
	l, i := binary.Uvarint(data)
	if i <= 0 || uint64(len(data)-i) < l {
		return "", nil, io.ErrUnexpectedEOF
	}
	return string(data[i : i+int(l)]), data[i+int(l):], nil
}

func (c floatCounter) GobEncode() ([]byte, error) {
	b := binary.AppendUvarint(nil, uint64(len(c.keys)))
	for _, k := range c.keys {
		b = binary.BigEndian.AppendUint64(appendStr(b, k), math.Float64bits(c.n[k]))
	}
	return b, nil
}

func (c *floatCounter) GobDecode(data []byte) error {
	n, i := binary.Uvarint(data)
	if i <= 0 {
		return io.ErrUnexpectedEOF
	}
	data = data[i:]
	c.keys, c.n = nil, make(map[string]float64, n)
	for ; n > 0; n-- {
		k, rest, err := readStr(data)
		if err != nil || len(rest) < 8 {
			return io.ErrUnexpectedEOF
		}
		c.keys = append(c.keys, k)
		c.n[k] = math.Float64frombits(binary.BigEndian.Uint64(rest))
		data = rest[8:]
	}
	return nil
}

func (s orderedSet) GobEncode() ([]byte, error) {
	b := binary.AppendUvarint(nil, uint64(len(s.keys)))
	for _, k := range s.keys {
		b = appendStr(b, k)
	}
	return b, nil
}

func (s *orderedSet) GobDecode(data []byte) error {
	n, i := binary.Uvarint(data)
	if i <= 0 {
		return io.ErrUnexpectedEOF
	}
	data = data[i:]
	s.keys, s.has = nil, make(map[string]bool, n)
	for ; n > 0; n-- {
		k, rest, err := readStr(data)
		if err != nil {
			return err
		}
		s.add(k)
		data = rest
	}
	return nil
}

// loadCaches fills the per-transcript caches from disk once per wake-up; changed transcripts still reparse.
func loadCaches() {
	parseLoaded.Do(func() {
		f, err := os.Open(parseCacheFile())
		if err != nil {
			return
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			return
		}
		dec := gob.NewDecoder(zr)
		var ver string
		if dec.Decode(&ver) != nil || ver != parseCacheVersion {
			return
		}
		for {
			var r pathRec
			if dec.Decode(&r) != nil {
				return // EOF, or a torn file: keep what was read
			}
			oneMu.Lock()
			for _, o := range r.Ones {
				keep(r.Path, oneHit{o.Sig, o.Floor, o.Lo, o.Hi, o.S, o.Size, o.C})
			}
			oneMu.Unlock()
			if r.HasDele {
				var row *ojson.Object
				if v, err := ojson.Parse([]byte(r.DeleRow)); err == nil {
					row = ojson.Obj(v)
				}
				deleMu.Lock()
				if _, ok := deleCache[r.Path]; !ok {
					deleCache[r.Path] = deleHit{r.DeleSig, row, r.Stamps}
				}
				deleMu.Unlock()
			}
			loadExtras(&r)
			fixedMu.Lock()
			if r.HasFixedAll {
				fixedAll[r.Path] = r.FixedAll
			}
			if r.HasFixedOne {
				fixedSig[r.Path], fixedOne[r.Path] = r.FixedSig, [2]int64{r.FixedOne, 0}
			}
			fixedMu.Unlock()
		}
	})
}

// loadExtras restores the stamp, repeat, gate and subagent entries of one record.
func loadExtras(r *pathRec) {
	if r.HasStamps {
		stampMu.Lock()
		stampCache[r.Path] = stampHit{r.StampSig, r.StampSize, r.StampOff, r.Stamps2}
		stampMu.Unlock()
	}
	if r.HasRep {
		if v, err := ojson.Parse([]byte(r.RepRow)); err == nil && ojson.Obj(v) != nil {
			repMu.Lock()
			repCache[r.Path] = repHit{r.RepSig, ojson.Obj(v)}
			repMu.Unlock()
		}
	}
	if r.HasGate {
		gateMu.Lock()
		gateCache[r.Path] = gateHit{r.GateMtime, r.GateSize, r.GateN}
		gateMu.Unlock()
	}
	if r.HasAgent {
		if v, err := ojson.Parse([]byte(r.AgentRow)); err == nil && ojson.Obj(v) != nil {
			agentMu.Lock()
			agentCache[r.Path] = agentHit{r.AgentSig, ojson.Obj(v)}
			agentMu.Unlock()
		}
	}
}

// saveExtras adds the same entries to the records being written.
func saveExtras(rec func(string) *pathRec) {
	stampMu.Lock()
	for p, h := range stampCache {
		r := rec(p)
		r.HasStamps, r.StampSig, r.StampSize, r.StampOff, r.Stamps2 = true, h.sig, h.size, h.off, h.out
	}
	stampMu.Unlock()
	repMu.Lock()
	for p, h := range repCache {
		r := rec(p)
		r.HasRep, r.RepSig, r.RepRow = true, h.sig, ojson.Compact(h.v)
	}
	repMu.Unlock()
	gateMu.Lock()
	for p, h := range gateCache {
		r := rec(p)
		r.HasGate, r.GateMtime, r.GateSize, r.GateN = true, h.mtime, h.size, h.n
	}
	gateMu.Unlock()
	agentMu.Lock()
	for p, h := range agentCache {
		r := rec(p)
		r.HasAgent, r.AgentSig, r.AgentRow = true, h.sig, ojson.Compact(h.scan)
	}
	agentMu.Unlock()
}

var saveMu sync.Mutex

// saveCachesSoon is saveCaches behind the answer: rewriting the file takes about half a second.
func saveCachesSoon() {
	if parseDirty.Load() {
		busy.Add(1)
		go func() {
			defer busy.Add(-1)
			defer logPanic("parse cache save")
			saveCaches()
		}()
	}
}

// saveCaches writes the per-transcript caches to disk when something has been computed since the last save.
func saveCaches() {
	saveMu.Lock()
	defer saveMu.Unlock()
	if !parseDirty.Swap(false) {
		return
	}
	recs := map[string]*pathRec{}
	rec := func(p string) *pathRec {
		if recs[p] == nil {
			recs[p] = &pathRec{Path: p}
		}
		return recs[p]
	}
	oneMu.Lock()
	for _, m := range []map[string]oneHit{oneCache, oneAlt} {
		for p, h := range m {
			r := rec(p)
			r.Ones = append(r.Ones, oneRec{h.sig, h.floor, h.lo, h.hi, h.size, h.s, h.c})
		}
	}
	oneMu.Unlock()
	deleMu.Lock()
	for p, h := range deleCache {
		r := rec(p)
		r.HasDele, r.DeleSig, r.DeleRow, r.Stamps = true, h.sig, "null", h.stamps
		if h.row != nil {
			r.DeleRow = ojson.Compact(h.row)
		}
	}
	deleMu.Unlock()
	saveExtras(rec)
	fixedMu.Lock()
	for p, n := range fixedAll {
		r := rec(p)
		r.HasFixedAll, r.FixedAll = true, n
	}
	for p, n := range fixedOne {
		r := rec(p)
		r.HasFixedOne, r.FixedSig, r.FixedOne = true, fixedSig[p], n[0]
	}
	fixedMu.Unlock()
	tmp := parseCacheFile() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		parseDirty.Store(true)
		return
	}
	zw, _ := gzip.NewWriterLevel(f, gzip.BestSpeed)
	enc := gob.NewEncoder(zw)
	err = enc.Encode(parseCacheVersion)
	for p, r := range recs {
		if err != nil {
			break
		}
		if _, e := os.Stat(p); e == nil {
			err = enc.Encode(r)
		}
	}
	if e := zw.Close(); err == nil {
		err = e
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		os.Remove(tmp)
		parseDirty.Store(true)
		return
	}
	os.Rename(tmp, parseCacheFile())
}

// birthTime is st_birthtime (macOS), else the mtime.
func birthTime(st os.FileInfo) float64 {
	if sys, ok := st.Sys().(*syscall.Stat_t); ok {
		return float64(sys.Birthtimespec.Sec) + float64(sys.Birthtimespec.Nsec)/1e9
	}
	return float64(st.ModTime().UnixNano()) / 1e9
}

// ── idle release ─────────────────────────────────────────────────────────
//
// While no page is open the service keeps nothing: after idleAfter without a
// request the rendered pages go to disk, every in-memory cache is dropped and
// the heap is handed back to the OS. The next request loads the pages from
// disk (stale, so they re-render behind the answer).

const idleAfter = 5 * time.Minute

var (
	serving atomic.Bool  // set by Serve: Render (the parity harness) never uses snapshots
	lastReq atomic.Int64 // unix nano of the latest request
	busy    atomic.Int32 // requests and background renders in flight

	snapMu    sync.Mutex
	snaps     = map[string]snapHit{} // bundle and pipeline bodies by path?query
	snapBusy  = map[string]bool{}
	loadOnce  sync.Once
	loadReset sync.Mutex
)

type snapHit struct {
	body string
	disk bool // loaded from disk: stale until re-rendered
}

// swr is fn(), except that a body loaded from disk is answered at once and re-rendered behind it.
func swr(key string, fn func() string) string {
	if !serving.Load() {
		return fn()
	}
	snapMu.Lock()
	hit, ok := snaps[key]
	start := ok && hit.disk && !snapBusy[key]
	if start {
		snapBusy[key] = true
	}
	snapMu.Unlock()
	if ok && hit.disk {
		if start {
			busy.Add(1)
			go func() {
				defer busy.Add(-1)
				defer logPanic("dashboard snapshot refresh")
				body := fn()
				snapMu.Lock()
				snaps[key] = snapHit{body: body}
				delete(snapBusy, key)
				snapMu.Unlock()
			}()
		}
		return hit.body
	}
	body := fn()
	snapMu.Lock()
	if len(snaps) > 100 { // ponytail: crude bound, LRU if it ever matters
		clear(snaps)
	}
	snaps[key] = snapHit{body: body}
	snapMu.Unlock()
	return body
}

type diskPage struct {
	Days            int
	Day, Sid, Page  string
	From, To, Since string
	Focus           string
	At              time.Time
	HTML            string
}

// pageCacheVersion changes whenever page markup changes, so a cache saved by an older build is not served.
const pageCacheVersion = "pages-v3" // v3: tool= rows, their ids and the focus script

type diskCache struct {
	Version string
	Pages   []diskPage
	Snaps   map[string]string
}

func cacheFile() string { return claudeFile("agent-perform-dash-cache.gob.gz") }

func (h *handler) save() {
	d := diskCache{Version: pageCacheVersion}
	d.Snaps = map[string]string{}
	h.mu.Lock()
	for _, k := range h.order {
		if hit, ok := h.hits[k]; ok {
			d.Pages = append(d.Pages, diskPage{k.days, k.day, k.sid, k.page, k.rng[0], k.rng[1], k.since, k.focus, hit.at, hit.html})
		}
	}
	h.mu.Unlock()
	snapMu.Lock()
	for k, v := range snaps {
		d.Snaps[k] = v.body
	}
	snapMu.Unlock()
	tmp := cacheFile() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	zw := gzip.NewWriter(f)
	err = gob.NewEncoder(zw).Encode(&d)
	if e := zw.Close(); err == nil {
		err = e
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		os.Remove(tmp)
		return
	}
	os.Rename(tmp, cacheFile())
}

// load reads the saved pages once per wake-up.
func (h *handler) load() {
	loadReset.Lock()
	defer loadReset.Unlock()
	loadOnce.Do(func() {
		f, err := os.Open(cacheFile())
		if err != nil {
			return
		}
		defer f.Close()
		zr, err := gzip.NewReader(f)
		if err != nil {
			return
		}
		var d diskCache
		if gob.NewDecoder(zr).Decode(&d) != nil || d.Version != pageCacheVersion {
			return
		}
		h.mu.Lock()
		for _, p := range d.Pages {
			k := pageKey{p.Days, p.Day, p.Sid, p.Page, p.Focus, [2]string{p.From, p.To}, p.Since}
			if _, ok := h.hits[k]; !ok {
				h.hits[k] = cacheHit{p.At, p.HTML}
				h.order = append(h.order, k)
			}
		}
		h.mu.Unlock()
		snapMu.Lock()
		for k, b := range d.Snaps {
			if _, ok := snaps[k]; !ok {
				snaps[k] = snapHit{b, true}
			}
		}
		snapMu.Unlock()
	})
}

// release saves what is cached, drops every cache and returns the heap to the OS.
func (h *handler) release() {
	h.setReleased(true)
	saveCaches()
	h.save()
	h.clear()
	snapMu.Lock()
	snaps = map[string]snapHit{}
	snapMu.Unlock()
	dropCaches()
	loadReset.Lock()
	loadOnce = sync.Once{}
	loadReset.Unlock()
	reexec()
}

var listener net.Listener

const listenFD = "AP_LISTEN_FD"

// inheritedListener is the socket a previous incarnation of this process handed over, if any.
func inheritedListener() (net.Listener, error) {
	v := os.Getenv(listenFD)
	if v == "" {
		return nil, nil
	}
	os.Unsetenv(listenFD)
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil, err
	}
	return net.FileListener(os.NewFile(uintptr(n), "listener"))
}

// reexec replaces the process by a fresh one on the same socket. macOS keeps the
// freed heap in the process's RSS, and a fresh process is the only way to give it back.
// Connections that arrive meanwhile wait in the socket's backlog.
func reexec() {
	exe, err := os.Executable()
	tl, ok := listener.(*net.TCPListener)
	if err != nil || !ok {
		runtime.GC()
		debug.FreeOSMemory()
		return
	}
	f, err := tl.File()
	if err != nil {
		return
	}
	fd := f.Fd()
	if _, _, e := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETFD, 0); e != 0 { // keep it across exec
		return
	}
	os.Setenv(listenFD, strconv.Itoa(int(fd)))
	syscall.Exec(exe, os.Args, os.Environ())
}

// watch releases once nothing has asked for a page for idleAfter.
func (h *handler) watch() {
	for range time.Tick(30 * time.Second) {
		if busy.Load() == 0 && time.Since(time.Unix(0, lastReq.Load())) > idleAfter && !h.isReleased() {
			h.release()
		}
	}
}

func (h *handler) isReleased() bool   { h.mu.Lock(); defer h.mu.Unlock(); return h.released }
func (h *handler) setReleased(v bool) { h.mu.Lock(); h.released = v; h.mu.Unlock() }

// dropCaches empties every per-file cache of the dashboard.
func dropCaches() {
	oneMu.Lock()
	oneCache, oneAlt, flights = map[string]oneHit{}, map[string]oneHit{}, map[string]*sync.Mutex{}
	oneMu.Unlock()
	parseLoaded = sync.Once{}
	repMu.Lock()
	repCache = map[string]repHit{}
	repMu.Unlock()
	gateMu.Lock()
	gateCache = map[string]gateHit{}
	gateMu.Unlock()
	pipeMu.Lock()
	pipeCache = map[pipeKey]pipeHit{}
	pipeMu.Unlock()
	ctxMu.Lock()
	ctxCache = map[string]*TurnCtx{}
	ctxMu.Unlock()
	fixedMu.Lock()
	fixedAll, fixedOne, fixedSig = map[string]int64{}, map[string][2]int64{}, map[string][2]int64{}
	fixedMu.Unlock()
	stampMu.Lock()
	stampCache = map[string]stampHit{}
	stampMu.Unlock()
	agentMu.Lock()
	agentCache = map[string]agentHit{}
	agentMu.Unlock()
	deleMu.Lock()
	deleCache = map[string]deleHit{}
	deleMu.Unlock()
}

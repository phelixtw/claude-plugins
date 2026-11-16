package dash

import (
	"compress/gzip"
	"encoding/gob"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"sync"
	"time"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
)

// The stats file holds what one session page needs from the other sessions, so
// a cold ?session= page parses one transcript instead of all of them: per
// transcript its turn times (who owns a copied turn) and the cost of its
// 150-300k-context turns (the baseline median). The context-window fallback
// reads ctx-cache.json directly and needs no entry here.

const statsHorizon = 366 // days of transcripts kept; the page window is at most 365

type fileStat struct {
	Path  string
	Sig   string    // mtime:size of the transcript
	Born  float64   // file birth time: the oldest file owns a copied turn
	Mod   float64   // mtime, seconds
	Ats   []float64 // every turn's start
	QAt   []float64 // start of each turn with 150-300k context
	QCost []float64
}

type statsFile struct{ Files []fileStat } // walk order

var statsMu sync.Mutex

func statsPath() string { return claudeFile("agent-perform-dash-stats.gob.gz") }

func loadStats() *statsFile {
	f, err := os.Open(statsPath())
	if err != nil {
		return nil
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil
	}
	var d statsFile
	if gob.NewDecoder(zr).Decode(&d) != nil {
		return nil
	}
	return &d
}

func (d *statsFile) save() error {
	tmp := statsPath() + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	err = gob.NewEncoder(zw).Encode(d)
	if e := zw.Close(); err == nil {
		err = e
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, statsPath())
}

func statOf(path string) fileStat { return entryOf(path, one(path, 0)) }

// entryOf is the stats entry of a transcript already parsed into s (nil: no turns).
func entryOf(path string, s *Session) fileStat {
	fs := fileStat{Path: path}
	st, err := os.Stat(path)
	if err != nil {
		return fs
	}
	fs.Sig, fs.Born, fs.Mod = statSigOf(st), birthTime(st), float64(st.ModTime().UnixNano())/1e9
	if s != nil {
		for _, t := range s.Turns {
			fs.Ats = append(fs.Ats, t.At)
			if c := ctxOf(t); 150_000 <= c && c < 300_000 {
				fs.QAt, fs.QCost = append(fs.QAt, t.At), append(fs.QCost, t.Usage.Cost)
			}
		}
	}
	return fs
}

func statSigOf(st os.FileInfo) string {
	return strconv.FormatInt(st.ModTime().UnixNano(), 10) + ":" + strconv.FormatInt(st.Size(), 10)
}

// refreshStats brings the stats file up to date, re-reading only transcripts
// whose mtime or size changed (two at a time, so the heap stays small), and
// returns it. A missing file is built from scratch.
func refreshStats() *statsFile {
	statsMu.Lock()
	defer statsMu.Unlock()
	old := loadStats()
	have := map[string]fileStat{}
	if old != nil {
		for _, f := range old.Files {
			have[f.Path] = f
		}
	}
	paths := transcriptPaths(floorOf(statsHorizon))
	out := &statsFile{Files: make([]fileStat, len(paths))}
	var todo []int
	for i, p := range paths {
		st, err := os.Stat(p)
		if f, ok := have[p]; ok && err == nil && f.Sig == statSigOf(st) {
			out.Files[i] = f
		} else {
			todo = append(todo, i)
		}
	}
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				out.Files[i] = statOf(paths[i])
			}
		}()
	}
	for _, i := range todo {
		next <- i
	}
	close(next)
	wg.Wait()
	if len(todo) > 0 || old == nil || len(old.Files) != len(paths) {
		out.save()
	}
	if len(todo) > 8 {
		debug.FreeOSMemory()
	}
	return out
}

// set replaces the entry of the same transcript (the one just read fresh), or adds it.
func (d *statsFile) set(e fileStat) {
	for i := range d.Files {
		if d.Files[i].Path == e.Path {
			d.Files[i] = e
			return
		}
	}
	d.Files = append(d.Files, e)
}

// owners is, for the transcripts live since floor, each turn's owner (the
// oldest file holding it, as dropCopiedTurns decides) and the baseline median.
func (d *statsFile) owners(floor float64) (owner map[float64]string, base float64) {
	var live []fileStat
	for _, f := range d.Files {
		if f.Mod >= floor && len(f.Ats) > 0 && f.Ats[len(f.Ats)-1] >= floor {
			live = append(live, f)
		}
	}
	sort.SliceStable(live, func(i, j int) bool { return live[i].Born < live[j].Born })
	owner = map[float64]string{}
	for _, f := range live {
		id := py.Head(filepath.Base(f.Path), 8)
		for _, at := range f.Ats {
			if _, ok := owner[at]; !ok && at >= floor {
				owner[at] = id
			}
		}
	}
	var cheap []float64
	for _, f := range live {
		id := py.Head(filepath.Base(f.Path), 8)
		for i, at := range f.QAt {
			if at >= floor && owner[at] == id {
				cheap = append(cheap, f.QCost[i])
			}
		}
	}
	sort.Float64s(cheap)
	if len(cheap) > 0 {
		base = cheap[len(cheap)/2]
	}
	return owner, base
}

// RefreshStats is `ap dashboard --refresh-stats`.
func RefreshStats() {
	debug.SetMemoryLimit(600 << 20)
	refreshStats()
}

// startStatsTicker keeps the stats file fresh while the service is up. The
// refresh runs in a child process: parsing big transcripts grows the heap, and
// macOS keeps freed heap in the RSS of a long-lived process.
func startStatsTicker() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	go func() {
		for {
			exec.Command(exe, "dashboard", "--refresh-stats").Run()
			time.Sleep(45 * time.Second)
		}
	}()
}

// sessionFast is the ?session= page from one transcript plus the stats file;
// ok is false when the stats file does not exist yet.
func sessionFast(sid string, days int, focus string) (body string, ok bool) {
	path := Transcript(sid)
	if path == "" {
		return sessionMissing(), true
	}
	floor := floorOf(days)
	// the three scans of a big transcript are independent: run them side by side
	var s *Session
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); s = oneCached(path, floor) }()
	go func() { defer wg.Done(); liveDelegation(path) }()
	go func() {
		defer wg.Done()
		if ojson.Obj(ojson.Field(ojson.Field(health(), "sessions"), sid)).Len() > 0 {
			if sig, err := statSig(path); err == nil {
				liveRepeats(path, sig)
			}
		}
	}()
	d := loadStats()
	wg.Wait()
	if d == nil {
		return "", false
	}
	if s == nil {
		return sessionMissing(), true
	}
	d.set(entryOf(path, s))
	owner, base := d.owners(floor)
	own := keepOwned([]*Session{s}, owner)
	if len(own) == 0 || own[0].ID != sid { // sid is the 8-char id, not any prefix of it
		return sessionMissing(), true
	}
	applyWindow(own)
	attachDelegation(own)
	return sessionBody(own[0], base, focus), true
}

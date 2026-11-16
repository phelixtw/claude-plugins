package dash

// Config-change detection: compares what each session started with (instruction files,
// skill list, MCP servers, model) against the sessions before it. Read from the transcripts' start
// attachments, so it only reaches back as far as the transcripts are kept.

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type cfgEvent struct {
	at         int64 // ms
	day        string
	kind       string // file, skill+, skill-, mcp+, mcp-, model
	name, path string
	from, to   string
	add, del   int
	sample     []string
}

type cfgSession struct {
	at     int64
	files  map[string]string
	skills []string
	hasSk  bool
	mcp    map[string]bool
	model  string
}

type cfgLine struct {
	Timestamp  string `json:"timestamp"`
	Attachment *struct {
		Type      string `json:"type"`
		IsInitial bool   `json:"isInitial"`
		Names     []string
		AddedName []string `json:"addedNames"`
		Files     []struct{ Path, Content string }
		Identity  struct {
			ModelID string `json:"modelId"`
		} `json:"identity"`
	} `json:"attachment"`
}

type cfgHit struct {
	sig string
	s   *cfgSession
}

var (
	cfgMu    sync.Mutex
	cfgEvs   []cfgEvent
	cfgAt    time.Time
	cfgFiles = map[string]cfgHit{} // per transcript, keyed by its mtime and size
)

// cfgTTL is how long one scan is reused; a rescan re-reads only the transcripts that changed.
const cfgTTL = 10 * time.Minute

// configChanges is every detected change in the last 90 days, oldest first.
func configChanges() []cfgEvent {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	if cfgAt.IsZero() || time.Since(cfgAt) > cfgTTL {
		cfgEvs, cfgAt = scanConfigChanges(), time.Now()
	}
	return cfgEvs
}

func utcDay(ms int64) string { return time.UnixMilli(ms).UTC().Format("2006-01-02") }

func scanConfigChanges() []cfgEvent {
	home := home()
	root := projects()
	cut := Now().Add(-90 * 24 * time.Hour)
	var paths, sigs []string
	dirs, _ := os.ReadDir(root)
	for _, d := range dirs {
		fs, _ := os.ReadDir(filepath.Join(root, d.Name()))
		for _, f := range fs {
			if !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			p := filepath.Join(root, d.Name(), f.Name())
			if st, err := os.Stat(p); err == nil && !st.ModTime().Before(cut) {
				paths, sigs = append(paths, p), append(sigs, statSigOf(st))
			}
		}
	}
	got := make([]*cfgSession, len(paths))
	parallel(len(paths), func(i int) {
		if h, ok := cfgFiles[paths[i]]; ok && h.sig == sigs[i] {
			got[i] = h.s
			return
		}
		got[i] = readCfgSession(paths[i])
	})
	fresh := make(map[string]cfgHit, len(paths))
	var sess []*cfgSession
	for i, p := range paths {
		fresh[p] = cfgHit{sigs[i], got[i]}
		if got[i] != nil {
			sess = append(sess, got[i])
		}
	}
	cfgFiles = fresh
	sort.SliceStable(sess, func(i, j int) bool { return sess[i].at < sess[j].at })
	if len(sess) == 0 {
		return nil
	}
	var evs []cfgEvent
	last := map[string]string{}
	for _, s := range sess {
		for p, c := range s.files {
			if strings.HasSuffix(p, "MEMORY.md") {
				continue
			}
			if old, ok := last[p]; ok && old != c {
				add, del, sample := lineDiff(old, c)
				evs = append(evs, cfgEvent{at: s.at, day: utcDay(s.at), kind: "file", path: strings.Replace(p, home, "~", 1), add: add, del: del, sample: sample})
			}
			last[p] = c
		}
	}
	start, end := sess[0].at, sess[len(sess)-1].at
	seen := func(get func(*cfgSession) []string, kind string) {
		first, lastSeen := map[string]int64{}, map[string]int64{}
		var order []string
		for _, s := range sess {
			for _, n := range get(s) {
				if _, ok := first[n]; !ok {
					first[n] = s.at
					order = append(order, n)
				}
				lastSeen[n] = s.at
			}
		}
		const twoDays = 2 * 864e5
		for _, n := range order {
			if float64(first[n]-start) > twoDays {
				evs = append(evs, cfgEvent{at: first[n], day: utcDay(first[n]), kind: kind + "+", name: n})
			}
			if float64(end-lastSeen[n]) > twoDays {
				evs = append(evs, cfgEvent{at: lastSeen[n] + 1, day: utcDay(lastSeen[n] + 864e5), kind: kind + "-", name: n})
			}
		}
	}
	seen(func(s *cfgSession) []string { return s.skills }, "skill")
	seen(func(s *cfgSession) []string {
		var out []string
		for n := range s.mcp {
			out = append(out, n)
		}
		sort.Strings(out)
		return out
	}, "mcp")
	// main model: the day's most used one; a change of that is a switch
	byDay := map[string]map[string]int{}
	for _, s := range sess {
		if s.model != "" {
			d := utcDay(s.at)
			if byDay[d] == nil {
				byDay[d] = map[string]int{}
			}
			byDay[d][s.model]++
		}
	}
	var days []string
	for d := range byDay {
		days = append(days, d)
	}
	sort.Strings(days)
	prev := ""
	for _, d := range days {
		m, best := "", 0
		for k, n := range byDay[d] {
			if n > best || (n == best && k < m) {
				m, best = k, n
			}
		}
		if prev != "" && m != prev {
			t, _ := time.Parse("2006-01-02", d)
			evs = append(evs, cfgEvent{at: t.UnixMilli(), day: d, kind: "model", from: prev, to: m})
		}
		prev = m
	}
	sort.SliceStable(evs, func(i, j int) bool { return evs[i].at < evs[j].at })
	return evs
}

func readCfgSession(p string) *cfgSession {
	f, err := os.Open(p)
	if err != nil {
		return nil
	}
	defer f.Close()
	// ponytail: only the first 3 MB, where the start attachments live; later MCP connects are missed
	buf, _ := io.ReadAll(io.LimitReader(f, 3_000_000))
	s := &cfgSession{files: map[string]string{}, mcp: map[string]bool{}}
	for len(buf) > 0 {
		var line []byte
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			line, buf = buf[:i], buf[i+1:]
		} else {
			line, buf = buf, nil
		}
		// a full decode only for attachments, and for the first timestamp
		if !bytes.Contains(line, []byte(`"attachment"`)) && (s.at != 0 || !bytes.Contains(line, []byte(`"timestamp"`))) {
			continue
		}
		var e cfgLine
		if json.Unmarshal(line, &e) != nil {
			continue
		}
		if s.at == 0 && e.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, e.Timestamp); err == nil {
				s.at = t.UnixMilli()
			}
		}
		a := e.Attachment
		if a == nil {
			continue
		}
		switch a.Type {
		case "instructions":
			if len(s.files) == 0 {
				for _, x := range a.Files {
					s.files[x.Path] = x.Content
				}
			}
		case "skill_listing":
			if a.IsInitial && !s.hasSk {
				s.hasSk, s.skills = true, a.Names
			}
		case "mcp_instructions_delta":
			for _, n := range a.AddedName {
				s.mcp[n] = true
			}
		case "deferred_tools_delta":
			for _, n := range a.AddedName {
				if rest, ok := strings.CutPrefix(n, "mcp__"); ok {
					if name, _, ok := strings.Cut(rest, "__"); ok {
						s.mcp[name] = true
					}
				}
			}
		case "model":
			if s.model == "" {
				s.model = a.Identity.ModelID
			}
		}
	}
	if s.at == 0 || (!s.hasSk && len(s.files) == 0) {
		return nil
	}
	return s
}

// lineDiff counts the non-blank lines only in b (added) and only in a (removed); sample is up to two added lines.
func lineDiff(a, b string) (int, int, []string) {
	set := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, l := range strings.Split(s, "\n") {
			m[l] = true
		}
		return m
	}
	sa, sb := set(a), set(b)
	var add, del int
	var sample []string
	for _, l := range strings.Split(b, "\n") {
		if strings.TrimSpace(l) != "" && !sa[l] {
			add++
			if len(sample) < 2 {
				sample = append(sample, string([]rune(l)[:min(80, len([]rune(l)))]))
			}
		}
	}
	for l := range sa {
		if strings.TrimSpace(l) != "" && !sb[l] {
			del++
		}
	}
	return add, del, sample
}

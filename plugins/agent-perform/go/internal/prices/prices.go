// Package prices is the one price sheet (prices.py), dollars per million tokens.
// The built-in table is the offline fallback; live.go refreshes it from Anthropic's pricing page.
package prices

import (
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
)

// Rates: input, output, cache read, cache write 1h, cache write 5m.
type Rates [5]float64

// sheet is one immutable price list; long holds the rates for prompts over longPrompt.
type sheet struct {
	table map[string]Rates
	long  map[string]Rates
	keys  []string // longest first, so "opus-5-5" wins over "opus-5" and "opus"
}

func newSheet(table, long map[string]Rates) *sheet {
	s := &sheet{table: table, long: long}
	for k := range table {
		s.keys = append(s.keys, k)
	}
	sort.Slice(s.keys, func(i, j int) bool {
		if len(s.keys[i]) != len(s.keys[j]) {
			return len(s.keys[i]) > len(s.keys[j])
		}
		return s.keys[i] < s.keys[j]
	})
	return s
}

func r(in, out, read, w1h, w5m float64) Rates { return Rates{in, out, read, w1h, w5m} }

// builtin mirrors the official model pricing table; the bare family keys catch unknown future models.
var builtin = newSheet(map[string]Rates{
	"fable-5-1":  r(10, 50, 0.25, 20, 12.5),
	"mythos-5-1": r(10, 50, 0.25, 20, 12.5),
	"fable":      r(10, 50, 1, 20, 12.5),
	"mythos":     r(10, 50, 1, 20, 12.5),
	"opus-5-5":   r(4, 20, 0.20, 8, 5),
	"opus-4-1":   r(15, 75, 1.5, 30, 18.75),
	"opus-4-0":   r(15, 75, 1.5, 30, 18.75),
	"opus-3":     r(15, 75, 1.5, 30, 18.75),
	"opus":       r(5, 25, 0.5, 10, 6.25),
	"sonnet-5-5": r(2, 10, 0.10, 4, 2.5),
	"sonnet-4":   r(3, 15, 0.3, 6, 3.75),
	"sonnet-3":   r(3, 15, 0.3, 6, 3.75),
	"sonnet":     r(2, 10, 0.2, 4, 2.5),
	"haiku-5-5":  r(0.10, 0.50, 0.01, 0.20, 0.125),
	"haiku-3-5":  r(0.80, 4, 0.08, 1.60, 1),
	"haiku":      r(1, 5, 0.1, 2, 1.25),
}, map[string]Rates{"haiku-5-5": r(0.50, 2.50, 0.05, 1, 0.625)})

var current atomic.Pointer[sheet]

func init() { current.Store(builtin) }

const def = "sonnet"

// a prompt (input plus cache reads and writes) over this pays the long rates
const longPrompt = 100_000

// old ids put the version first: claude-3-5-haiku-... -> haiku-3-5
var legacyID = regexp.MustCompile(`claude-(\d)-(\d)-([a-z]+)`)

func key(s *sheet, model string) string {
	m := legacyID.ReplaceAllString(strings.ToLower(model), "$3-$1-$2")
	// the 4.0 ids carry a date straight after the major version: claude-opus-4-20250514
	m = strings.Replace(m, "opus-4-2", "opus-4-0-2", 1)
	for _, k := range s.keys {
		if strings.Contains(m, k) {
			return k
		}
	}
	return def
}

// Of is rates(model).
func Of(model string) Rates {
	s := current.Load()
	return s.table[key(s, model)]
}

// Price is price(model, i, o, cr, cw, short_ttl).
func Price(model string, i, o, cr, cw float64, shortTTL bool) float64 {
	s := current.Load()
	k := key(s, model)
	rt := s.table[k]
	if lr, ok := s.long[k]; ok && i+cr+cw > longPrompt {
		rt = lr
	}
	pcw := rt[3]
	if shortTTL {
		pcw = rt[4]
	}
	// explicit conversions round each product, as Python does; without them arm64 fuses into FMA
	return (float64(i*rt[0]) + float64(o*rt[1]) + float64(cr*rt[2]) + float64(cw*pcw)) / 1_000_000
}

// CacheRead is cache_read(model).
func CacheRead(model string) float64 { return Of(model)[2] }

// CacheWrite is the cache-write rate for model: 5-minute when shortTTL, else 1-hour.
func CacheWrite(model string, shortTTL bool) float64 {
	if shortTTL {
		return Of(model)[4]
	}
	return Of(model)[3]
}

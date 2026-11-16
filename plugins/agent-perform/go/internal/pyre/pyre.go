// Package pyre runs the regexes ported from the Python scripts with Python's
// semantics: Unicode \w \b \d \s, lookaround, `$` before a final newline,
// leftmost-first backtracking (regexp2, .NET flavour, matches Python 3 `re`
// on everything these patterns use). A Python `\Z` must be written `\z`.
package pyre

import (
	"strings"

	"github.com/dlclark/regexp2"
)

const (
	I = regexp2.IgnoreCase
	S = regexp2.Singleline
	M = regexp2.Multiline
)

type Re struct {
	re, anchored, full *regexp2.Regexp
}

// Compile panics on a bad pattern, like regexp.MustCompile.
func Compile(pat string, flags ...regexp2.RegexOptions) *Re {
	var opt regexp2.RegexOptions
	for _, f := range flags {
		opt |= f
	}
	return &Re{
		re:       regexp2.MustCompile(pat, opt),
		anchored: regexp2.MustCompile(`\A(?:`+pat+`)`, opt),
		full:     regexp2.MustCompile(`\A(?:`+pat+`)\z`, opt),
	}
}

// Match is one match: Group(0) is the whole text.
type Match struct {
	m   *regexp2.Match
	src string
}

func (m *Match) Group(i int) string {
	g := m.m.GroupByNumber(i)
	if g == nil || len(g.Captures) == 0 {
		return ""
	}
	return g.String()
}

// Has reports whether group i took part in the match (Python: group(i) is not None).
func (m *Match) Has(i int) bool {
	g := m.m.GroupByNumber(i)
	return g != nil && len(g.Captures) > 0
}

// Span is the byte range of group i.
func (m *Match) Span(i int) (int, int) {
	g := m.m.GroupByNumber(i)
	if g == nil || len(g.Captures) == 0 {
		return -1, -1
	}
	start := byteOffset(m.src, g.Index)
	return start, start + len(g.String())
}

func (m *Match) Groups() int { return m.m.GroupCount() - 1 }

func byteOffset(s string, runes int) int {
	if runes <= 0 {
		return 0
	}
	n := 0
	for i := range s {
		if n == runes {
			return i
		}
		n++
	}
	return len(s)
}

func wrap(m *regexp2.Match, src string, err error) *Match {
	if err != nil || m == nil {
		return nil
	}
	return &Match{m, src}
}

// Search is re.search.
func (r *Re) Search(s string) *Match {
	m, err := r.re.FindStringMatch(s)
	return wrap(m, s, err)
}

// MatchStart is re.match (anchored at the start).
func (r *Re) MatchStart(s string) *Match {
	m, err := r.anchored.FindStringMatch(s)
	return wrap(m, s, err)
}

// FullMatch is re.fullmatch.
func (r *Re) FullMatch(s string) *Match {
	m, err := r.full.FindStringMatch(s)
	return wrap(m, s, err)
}

// Has is bool(re.search).
func (r *Re) Has(s string) bool {
	ok, err := r.re.MatchString(s)
	return err == nil && ok
}

// Iter is re.finditer.
func (r *Re) Iter(s string) []*Match {
	var out []*Match
	m, err := r.re.FindStringMatch(s)
	for err == nil && m != nil {
		out = append(out, &Match{m, s})
		m, err = r.re.FindNextMatch(m)
	}
	return out
}

// FindAll is re.findall for a pattern with zero or one group.
func (r *Re) FindAll(s string) []string {
	var out []string
	for _, m := range r.Iter(s) {
		if m.Groups() >= 1 {
			out = append(out, m.Group(1))
		} else {
			out = append(out, m.Group(0))
		}
	}
	return out
}

// FindAllGroups is re.findall for a pattern with several groups (tuples).
func (r *Re) FindAllGroups(s string) [][]string {
	var out [][]string
	for _, m := range r.Iter(s) {
		row := make([]string, m.Groups())
		for i := range row {
			row[i] = m.Group(i + 1)
		}
		out = append(out, row)
	}
	return out
}

// SubFunc is re.sub with a function.
func (r *Re) SubFunc(s string, fn func(*Match) string) string {
	var b strings.Builder
	last := 0
	for _, m := range r.Iter(s) {
		start, end := m.Span(0)
		b.WriteString(s[last:start])
		b.WriteString(fn(m))
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

// Sub is re.sub with a literal replacement (no backreferences).
func (r *Re) Sub(s, repl string) string {
	return r.SubFunc(s, func(*Match) string { return repl })
}

// Split is re.split for a pattern without groups.
func (r *Re) Split(s string) []string {
	var out []string
	last := 0
	for _, m := range r.Iter(s) {
		start, end := m.Span(0)
		out = append(out, s[last:start])
		last = end
	}
	return append(out, s[last:])
}

package py

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"agentperform/internal/ojson"
)

// Repr is repr() of a JSON-derived value: str, int, float, bool, None,
// list and dict (keys in order).
func Repr(v any) string {
	var b strings.Builder
	repr(&b, v)
	return b.String()
}

func repr(b *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		b.WriteString("None")
	case bool:
		if t {
			b.WriteString("True")
		} else {
			b.WriteString("False")
		}
	case string:
		b.WriteString(StrRepr(t))
	case json.Number:
		if IsInt(t) {
			b.WriteString(Num(string(t)))
		} else {
			f, _ := Number(t)
			b.WriteString(FloatRepr(f))
		}
	case float64:
		b.WriteString(FloatRepr(t))
	case int:
		b.WriteString(strconv.Itoa(t))
	case int64:
		b.WriteString(strconv.FormatInt(t, 10))
	case []any:
		b.WriteByte('[')
		for i, x := range t {
			if i > 0 {
				b.WriteString(", ")
			}
			repr(b, x)
		}
		b.WriteByte(']')
	case *ojson.Object:
		b.WriteByte('{')
		for i, k := range t.Keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(StrRepr(k))
			b.WriteString(": ")
			repr(b, t.Vals[k])
		}
		b.WriteByte('}')
	default:
		fmt.Fprint(b, t)
	}
}

func printable(r rune) bool {
	if r == ' ' {
		return true
	}
	return !unicode.In(r, unicode.Cc, unicode.Cf, unicode.Cs, unicode.Co, unicode.Zl, unicode.Zp, unicode.Zs) &&
		(unicode.IsGraphic(r) || unicode.IsMark(r))
}

// StrRepr is repr(str).
func StrRepr(s string) string {
	q := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		q = '"'
	}
	var b strings.Builder
	b.WriteByte(q)
	for _, r := range s {
		switch {
		case r == rune(q) || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x7f:
			b.WriteRune(r)
		case printable(r):
			b.WriteRune(r)
		case r < 0x100:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r < 0x10000:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			fmt.Fprintf(&b, `\U%08x`, r)
		}
	}
	b.WriteByte(q)
	return b.String()
}

// DumpsSorted is json.dumps(v, ensure_ascii=False, sort_keys=True).
func DumpsSorted(v any) string { return Dumps(sortKeys(v)) }

func sortKeys(v any) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = sortKeys(x)
		}
		return out
	case *ojson.Object:
		keys := append([]string{}, t.Keys...)
		sort.Strings(keys)
		o := ojson.NewObject()
		for _, k := range keys {
			o.Set(k, sortKeys(t.Vals[k]))
		}
		return o
	}
	return v
}

// HTMLEscape is html.escape(s) (quote=True).
func HTMLEscape(s string) string {
	if !strings.ContainsAny(s, "&<>\"'") {
		return s
	}
	return htmlRepl.Replace(s)
}

var htmlRepl = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&#x27;")

// Counter is collections.Counter with insertion order (most_common ties keep it).
type Counter struct {
	Keys []string
	N    map[string]int
}

func NewCounter() *Counter { return &Counter{N: map[string]int{}} }

// GobEncode and GobDecode keep an empty counter usable after a round trip (gob drops empty maps).
func (c Counter) GobEncode() ([]byte, error) {
	b := binary.AppendUvarint(nil, uint64(len(c.Keys)))
	for _, k := range c.Keys {
		b = binary.AppendUvarint(b, uint64(len(k)))
		b = append(b, k...)
		b = binary.AppendVarint(b, int64(c.N[k]))
	}
	return b, nil
}

func (c *Counter) GobDecode(data []byte) error {
	n, i := binary.Uvarint(data)
	if i <= 0 {
		return io.ErrUnexpectedEOF
	}
	data = data[i:]
	c.Keys, c.N = nil, make(map[string]int, n)
	for ; n > 0; n-- {
		l, i := binary.Uvarint(data)
		if i <= 0 || uint64(len(data)-i) < l {
			return io.ErrUnexpectedEOF
		}
		k := string(data[i : i+int(l)])
		v, j := binary.Varint(data[i+int(l):])
		if j <= 0 {
			return io.ErrUnexpectedEOF
		}
		data = data[i+int(l)+j:]
		c.Keys = append(c.Keys, k)
		c.N[k] = int(v)
	}
	return nil
}

func (c *Counter) Add(k string, n int) {
	if _, ok := c.N[k]; !ok {
		c.Keys = append(c.Keys, k)
	}
	c.N[k] += n
}

func (c *Counter) Clone() *Counter { return &Counter{Keys: slices.Clone(c.Keys), N: maps.Clone(c.N)} }

func (c *Counter) Len() int { return len(c.Keys) }

// MostCommon is most_common(n); n < 0 means all.
func (c *Counter) MostCommon(n int) []string {
	keys := append([]string{}, c.Keys...)
	sort.SliceStable(keys, func(i, j int) bool { return c.N[keys[i]] > c.N[keys[j]] })
	if n >= 0 && n < len(keys) {
		keys = keys[:n]
	}
	return keys
}

// Total is sum(counter.values()).
func (c *Counter) Total() int {
	t := 0
	for _, k := range c.Keys {
		t += c.N[k]
	}
	return t
}

// Str is str(v) for a JSON-derived value: strings as is, the rest as repr.
func Str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return Repr(v)
}

// DumpsASCII is json.dumps(v) with the default ensure_ascii=True.
func DumpsASCII(v any) string {
	s := Dumps(v)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x7f:
			b.WriteRune(r)
		case r == 0x7f:
			b.WriteString(`\u007f`)
		case r < 0x10000:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			r -= 0x10000
			fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		}
	}
	return b.String()
}

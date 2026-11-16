// Package py reproduces the Python behaviour the ported scripts relied on:
// str.strip / len / slicing by code point, str.splitlines, repr(float),
// round(x, n) and json.dumps(ensure_ascii=False).
package py

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"agentperform/internal/ojson"
)

// IsSpace is str.isspace for one character.
func IsSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

// Strip is str.strip().
func Strip(s string) string { return strings.TrimFunc(s, IsSpace) }

// CollapseSpace is re.sub(r"\s+", " ", s) for a str pattern: every run of
// whitespace becomes one space.
func CollapseSpace(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if IsSpace(r) {
			if !in {
				b.WriteByte(' ')
				in = true
			}
			continue
		}
		in = false
		b.WriteRune(r)
	}
	return b.String()
}

// LStrip is str.lstrip().
func LStrip(s string) string { return strings.TrimLeftFunc(s, IsSpace) }

// Len is len(str): code points.
func Len(s string) int { return utf8.RuneCountInString(s) }

// Head is s[:n] by code point.
func Head(s string, n int) string {
	if n <= 0 {
		return ""
	}
	i := 0
	for k := range s {
		if i == n {
			return s[:k]
		}
		i++
	}
	return s
}

// Tail is s[-n:] by code point.
func Tail(s string, n int) string {
	if n <= 0 {
		return s // s[-0:] is the whole string
	}
	total := Len(s)
	if total <= n {
		return s
	}
	skip := total - n
	i := 0
	for k := range s {
		if i == skip {
			return s[k:]
		}
		i++
	}
	return ""
}

// Splitlines is str.splitlines().
func Splitlines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch r {
		case '\n', '\r', '\x0b', '\x0c', '\x1c', '\x1d', '\x1e', 0x85, 0x2028, 0x2029:
			out = append(out, s[start:i])
			if r == '\r' && i+1 < len(s) && s[i+1] == '\n' {
				size++
			}
			start = i + size
		}
		i += size
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// FloatRepr is repr(float).
func FloatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	case f == 0:
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	e := strconv.FormatFloat(f, 'e', -1, 64)
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	decpt := x + 1
	if decpt <= -4 || decpt > 16 {
		m := digits[:1]
		if len(digits) > 1 {
			m += "." + digits[1:]
		}
		es := "+"
		if x < 0 {
			es, x = "-", -x
		}
		return sign + m + "e" + es + leftPad(strconv.Itoa(x), 2)
	}
	var out string
	switch {
	case decpt <= 0:
		out = "0." + strings.Repeat("0", -decpt) + digits
	case decpt >= len(digits):
		out = digits + strings.Repeat("0", decpt-len(digits)) + ".0"
	default:
		out = digits[:decpt] + "." + digits[decpt:]
	}
	return sign + out
}

func leftPad(s string, n int) string {
	for len(s) < n {
		s = "0" + s
	}
	return s
}

// Round is round(x, n) for a float: correctly rounded, ties to even.
func Round(x float64, n int) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return x
	}
	r, _ := strconv.ParseFloat(strconv.FormatFloat(x, 'f', n, 64), 64)
	return r
}

// Dumps is json.dumps(v, ensure_ascii=False) with the default separators.
// JSON numbers keep Python's reading of them: integer literals as ints,
// everything else as a float.
func Dumps(v any) string {
	var b strings.Builder
	dump(&b, v)
	return b.String()
}

// DumpsIndent is json.dumps(v, ensure_ascii=False, indent=n).
func DumpsIndent(v any, n int) string {
	var b strings.Builder
	dumpIndent(&b, v, n, 0)
	return b.String()
}

func dumpIndent(b *strings.Builder, v any, n, depth int) {
	nl := func(d int) {
		b.WriteByte('\n')
		b.WriteString(strings.Repeat(" ", n*d))
	}
	switch t := v.(type) {
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, x := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(depth + 1)
			dumpIndent(b, x, n, depth+1)
		}
		nl(depth)
		b.WriteByte(']')
	case *ojson.Object:
		if t.Len() == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, k := range t.Keys {
			if i > 0 {
				b.WriteByte(',')
			}
			nl(depth + 1)
			ojson.Quote(b, k)
			b.WriteString(": ")
			dumpIndent(b, t.Vals[k], n, depth+1)
		}
		nl(depth)
		b.WriteByte('}')
	default:
		dump(b, v)
	}
}

func dump(b *strings.Builder, v any) {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		ojson.Quote(b, t)
	case json.Number:
		b.WriteString(Num(string(t)))
	case float64:
		b.WriteString(jsonFloat(t))
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
			dump(b, x)
		}
		b.WriteByte(']')
	case []string:
		b.WriteByte('[')
		for i, x := range t {
			if i > 0 {
				b.WriteString(", ")
			}
			ojson.Quote(b, x)
		}
		b.WriteByte(']')
	case []float64:
		b.WriteByte('[')
		for i, x := range t {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(jsonFloat(x))
		}
		b.WriteByte(']')
	case *ojson.Object:
		b.WriteByte('{')
		for i, k := range t.Keys {
			if i > 0 {
				b.WriteString(", ")
			}
			ojson.Quote(b, k)
			b.WriteString(": ")
			dump(b, t.Vals[k])
		}
		b.WriteByte('}')
	default:
		raw, _ := json.Marshal(t)
		b.Write(raw)
	}
}

func jsonFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return FloatRepr(f)
}

// Num is a JSON number as Python prints it back after json.loads.
func Num(s string) string {
	if !strings.ContainsAny(s, ".eE") {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return strconv.FormatInt(n, 10)
		}
		return strings.TrimPrefix(s, "+") // a big int: Python keeps every digit
	}
	f, _ := strconv.ParseFloat(s, 64)
	return jsonFloat(f)
}

// IsInt reports whether a JSON value is a Python int (an integer literal).
func IsInt(v any) bool {
	switch t := v.(type) {
	case json.Number:
		return !strings.ContainsAny(string(t), ".eE")
	case int, int64:
		return true
	}
	return false
}

// Number is a JSON value as a Python number: (value, isNumber). Bools count,
// as they do in Python arithmetic.
func Number(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		return ojson.Float(t)
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// Split is str.split() with no arguments: runs of whitespace, no empty strings.
func Split(s string) []string { return strings.FieldsFunc(s, IsSpace) }

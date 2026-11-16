// Package ojson is JSON with object keys kept in file order, numbers kept as
// their source text, and output that matches serde_json / Python json.dumps
// (ensure_ascii=False): compact, or indented like json.dumps(indent=N).
package ojson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Object is a JSON object that remembers key order.
type Object struct {
	Keys []string
	Vals map[string]any
}

func NewObject() *Object { return &Object{Vals: map[string]any{}} }

// Get returns the value under key and whether the key is present.
func (o *Object) Get(key string) (any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.Vals[key]
	return v, ok
}

// Set adds key at the end, or replaces its value in place.
func (o *Object) Set(key string, v any) *Object {
	if _, ok := o.Vals[key]; !ok {
		o.Keys = append(o.Keys, key)
	}
	o.Vals[key] = v
	return o
}

func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	return len(o.Keys)
}

// Parse decodes one JSON value: *Object, []any, string, json.Number, bool or nil.
// It accepts what encoding/json's Decoder does and decodes to the same values
// (invalid UTF-8 and lone surrogates become U+FFFD), several times faster.
func Parse(data []byte) (any, error) {
	p := parser{data: data}
	p.space()
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.i != len(p.data) {
		return nil, errors.New("trailing data")
	}
	return v, nil
}

type parser struct {
	data []byte
	i    int
}

var errSyntax = errors.New("invalid JSON")

func (p *parser) space() {
	for p.i < len(p.data) {
		switch p.data[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) value() (any, error) {
	if p.i >= len(p.data) {
		return nil, io.ErrUnexpectedEOF
	}
	switch c := p.data[p.i]; {
	case c == '{':
		p.i++
		o := NewObject()
		p.space()
		if p.i < len(p.data) && p.data[p.i] == '}' {
			p.i++
			return o, nil
		}
		for {
			p.space()
			if p.i >= len(p.data) || p.data[p.i] != '"' {
				return nil, errSyntax
			}
			k, err := p.str()
			if err != nil {
				return nil, err
			}
			p.space()
			if p.i >= len(p.data) || p.data[p.i] != ':' {
				return nil, errSyntax
			}
			p.i++
			p.space()
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			o.Set(k, v)
			p.space()
			if p.i >= len(p.data) {
				return nil, io.ErrUnexpectedEOF
			}
			if p.data[p.i] == ',' {
				p.i++
				continue
			}
			if p.data[p.i] == '}' {
				p.i++
				return o, nil
			}
			return nil, errSyntax
		}
	case c == '[':
		p.i++
		arr := []any{}
		p.space()
		if p.i < len(p.data) && p.data[p.i] == ']' {
			p.i++
			return arr, nil
		}
		for {
			p.space()
			v, err := p.value()
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
			p.space()
			if p.i >= len(p.data) {
				return nil, io.ErrUnexpectedEOF
			}
			if p.data[p.i] == ',' {
				p.i++
				continue
			}
			if p.data[p.i] == ']' {
				p.i++
				return arr, nil
			}
			return nil, errSyntax
		}
	case c == '"':
		return p.str()
	case c == 't':
		return p.lit("true", true)
	case c == 'f':
		return p.lit("false", false)
	case c == 'n':
		return p.lit("null", nil)
	case c == '-' || (c >= '0' && c <= '9'):
		return p.num()
	}
	return nil, errSyntax
}

func (p *parser) lit(word string, v any) (any, error) {
	if !bytes.HasPrefix(p.data[p.i:], []byte(word)) {
		return nil, errSyntax
	}
	p.i += len(word)
	return v, nil
}

func (p *parser) digits() int {
	n := 0
	for p.i < len(p.data) && p.data[p.i] >= '0' && p.data[p.i] <= '9' {
		p.i++
		n++
	}
	return n
}

// num is -?(0|[1-9][0-9]*)(.[0-9]+)?([eE][+-]?[0-9]+)?, kept as its text.
func (p *parser) num() (any, error) {
	start := p.i
	if p.data[p.i] == '-' {
		p.i++
	}
	if p.i >= len(p.data) {
		return nil, io.ErrUnexpectedEOF
	}
	if p.data[p.i] == '0' {
		p.i++
	} else if p.digits() == 0 {
		return nil, errSyntax
	}
	if p.i < len(p.data) && p.data[p.i] == '.' {
		p.i++
		if p.digits() == 0 {
			return nil, errSyntax
		}
	}
	if p.i < len(p.data) && (p.data[p.i] == 'e' || p.data[p.i] == 'E') {
		p.i++
		if p.i < len(p.data) && (p.data[p.i] == '+' || p.data[p.i] == '-') {
			p.i++
		}
		if p.digits() == 0 {
			return nil, errSyntax
		}
	}
	return json.Number(p.data[start:p.i]), nil
}

func hex4(b []byte) (rune, bool) {
	if len(b) < 4 {
		return 0, false
	}
	var r rune
	for _, c := range b[:4] {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return r, true
}

// str reads a string at p.i (the opening quote).
func (p *parser) str() (string, error) {
	p.i++
	start := p.i
	for p.i < len(p.data) { // fast path: no escapes
		c := p.data[p.i]
		if c == '"' {
			s := p.data[start:p.i]
			p.i++
			return validUTF8(s), nil
		}
		if c == '\\' || c < 0x20 {
			break
		}
		p.i++
	}
	b := append([]byte(nil), p.data[start:p.i]...)
	for p.i < len(p.data) {
		c := p.data[p.i]
		switch {
		case c == '"':
			p.i++
			return validUTF8(b), nil
		case c < 0x20:
			return "", errSyntax
		case c != '\\':
			b = append(b, c)
			p.i++
			continue
		}
		if p.i+1 >= len(p.data) {
			return "", io.ErrUnexpectedEOF
		}
		e := p.data[p.i+1]
		p.i += 2
		switch e {
		case '"', '\\', '/':
			b = append(b, e)
		case 'b':
			b = append(b, '\b')
		case 'f':
			b = append(b, '\f')
		case 'n':
			b = append(b, '\n')
		case 'r':
			b = append(b, '\r')
		case 't':
			b = append(b, '\t')
		case 'u':
			r, ok := hex4(p.data[p.i:])
			if !ok {
				return "", errSyntax
			}
			p.i += 4
			if utf16.IsSurrogate(r) {
				// a pair combines; anything else is U+FFFD and the next escape stands alone
				lo, ok := rune(0), false
				if p.i+1 < len(p.data) && p.data[p.i] == '\\' && p.data[p.i+1] == 'u' {
					lo, ok = hex4(p.data[p.i+2:])
				}
				if dec := utf16.DecodeRune(r, lo); ok && dec != utf8.RuneError {
					r = dec
					p.i += 6
				} else {
					r = utf8.RuneError
				}
			}
			b = utf8.AppendRune(b, r)
		default:
			return "", errSyntax
		}
	}
	return "", io.ErrUnexpectedEOF
}

// validUTF8 is b as a string with every invalid byte replaced by U+FFFD.
func validUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	out := make([]byte, 0, len(b)+8)
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == utf8.RuneError && n == 1 {
			out = utf8.AppendRune(out, utf8.RuneError)
		} else {
			out = append(out, b[:n]...)
		}
		b = b[n:]
	}
	return string(out)
}

// Obj is v as an object, or nil.
func Obj(v any) *Object {
	o, _ := v.(*Object)
	return o
}

// Field is o[key] when v is an object, else nil.
func Field(v any, key string) any {
	x, _ := Obj(v).Get(key)
	return x
}

// Has reports whether v is an object holding key (even with a null value).
func Has(v any, key string) bool {
	_, ok := Obj(v).Get(key)
	return ok
}

// Str is v as a string and whether it is one.
func Str(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// Float is v as a number (serde_json as_f64).
func Float(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(string(n), 64)
	if err != nil {
		var ne *strconv.NumError
		if errors.As(err, &ne) && ne.Err == strconv.ErrRange {
			return f, true
		}
		return 0, false
	}
	return f, true
}

// Truthy is Python truthiness: null, false, 0, "", [] and {} are false.
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case json.Number:
		f, ok := Float(t)
		return !ok || f != 0
	case string:
		return t != ""
	case []any:
		return len(t) > 0
	case *Object:
		return t.Len() > 0
	}
	return true
}

// GetOr is `d.get(key) or None`: the value when truthy, else nil.
func GetOr(v any, key string) any {
	x := Field(v, key)
	if Truthy(x) {
		return x
	}
	return nil
}

// PyStr is a value as text: strings bare, null/bools as None/True/False,
// everything else as compact JSON.
func PyStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return "None"
	case bool:
		if t {
			return "True"
		}
		return "False"
	}
	return Compact(v)
}

// Compact is serde_json's to_string.
func Compact(v any) string {
	var b strings.Builder
	write(&b, v, "", -1, 0)
	return b.String()
}

// Indent is json.dumps(v, ensure_ascii=False, indent=n).
func Indent(v any, n int) string {
	var b strings.Builder
	write(&b, v, strings.Repeat(" ", n), n, 0)
	return b.String()
}

func write(b *strings.Builder, v any, unit string, n, depth int) {
	pretty := n >= 0
	nl := func(d int) {
		b.WriteByte('\n')
		b.WriteString(strings.Repeat(unit, d))
	}
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
		Quote(b, t)
	case json.Number:
		b.WriteString(Num(string(t)))
	case float64:
		b.WriteString(Ryu(t))
	case int:
		b.WriteString(strconv.Itoa(t))
	case int64:
		b.WriteString(strconv.FormatInt(t, 10))
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
			if pretty {
				nl(depth + 1)
			}
			write(b, x, unit, n, depth+1)
		}
		if pretty {
			nl(depth)
		}
		b.WriteByte(']')
	case *Object:
		if t.Len() == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteByte('{')
		for i, k := range t.Keys {
			if i > 0 {
				b.WriteByte(',')
			}
			if pretty {
				nl(depth + 1)
			}
			Quote(b, k)
			b.WriteByte(':')
			if pretty {
				b.WriteByte(' ')
			}
			write(b, t.Vals[k], unit, n, depth+1)
		}
		if pretty {
			nl(depth)
		}
		b.WriteByte('}')
	default:
		raw, _ := json.Marshal(t)
		b.Write(raw)
	}
}

// Quote writes s as a JSON string, escaping only what serde_json and Python's
// json (ensure_ascii=False) escape.
func Quote(b *strings.Builder, s string) {
	const hex = "0123456789abcdef"
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if c < 0x20 {
				b.WriteString(`\u00`)
				b.WriteByte(hex[c>>4])
				b.WriteByte(hex[c&0xf])
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
}

// Num is how serde_json prints a parsed number: integers as written, other
// numbers through f64 in ryu's shortest form.
func Num(s string) string {
	if !strings.ContainsAny(s, ".eE") {
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return strings.TrimPrefix(s, "+")
		}
		if _, err := strconv.ParseUint(s, 10, 64); err == nil {
			return s
		}
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil && !math.IsInf(f, 0) {
		return s
	}
	return Ryu(f)
}

// Ryu is the ryu crate's format64 (what serde_json and Rust print for an f64).
func Ryu(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "null"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0.0"
		}
		return "0.0"
	}
	sign := ""
	if f < 0 {
		sign = "-"
		f = -f
	}
	e := strconv.FormatFloat(f, 'e', -1, 64) // d.ddde±XX
	mant, exp, _ := strings.Cut(e, "e")
	digits := strings.Replace(mant, ".", "", 1)
	x, _ := strconv.Atoi(exp)
	length := len(digits)
	k := x - (length - 1) // value = digits * 10^k
	kk := length + k
	var out string
	switch {
	case 0 <= k && kk <= 16:
		out = digits + strings.Repeat("0", k) + ".0"
	case 0 < kk && kk <= 16:
		out = digits[:kk] + "." + digits[kk:]
	case -5 < kk && kk <= 0:
		out = "0." + strings.Repeat("0", -kk) + digits
	case length == 1:
		out = digits + "e" + strconv.Itoa(kk-1)
	default:
		out = digits[:1] + "." + digits[1:] + "e" + strconv.Itoa(kk-1)
	}
	return sign + out
}

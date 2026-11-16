// Package common: transcript reading (same rules as session_scorecard.py's
// read / text_of / is_prompt, which the dashboard uses), timestamps, paths,
// experiment flags.
package common

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"agentperform/internal/ojson"
)

const Interrupted = "Request interrupted by user"

// Say prints one line; a closed stdout (statusline piped to head) is not an error.
func Say(line string) {
	os.Stdout.WriteString(line + "\n")
}

func Now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func Epoch(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

func Home() string { return os.Getenv("HOME") }

// Join is Rust's Path::join for a relative part: no cleaning of the base.
func Join(base string, parts ...string) string {
	for _, p := range parts {
		switch {
		case base == "":
			base = p
		case strings.HasSuffix(base, "/"):
			base += p
		default:
			base += "/" + p
		}
	}
	return base
}

// PluginRoot is the plugin directory: bin/ap lives at <root>/bin/ap.
func PluginRoot() string {
	if p := os.Getenv("AP_PLUGIN_ROOT"); p != "" {
		return p
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(filepath.Dir(exe))
}

// Experiment is experiments.enabled(): AP_EXP_<NAME>, then
// scripts/experiments.json, default on.
func Experiment(name string) bool {
	if v, ok := os.LookupEnv("AP_EXP_" + strings.ToUpper(name)); ok {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "0", "false", "off", "no":
			return false
		}
		return true
	}
	raw, err := os.ReadFile(Join(PluginRoot(), "scripts", "experiments.json"))
	if err == nil {
		if blob, err := ojson.Parse(raw); err == nil {
			if v, ok := ojson.Obj(blob).Get(name); ok {
				return ojson.Truthy(v)
			}
		}
	}
	return true
}

// Lossy is String::from_utf8_lossy.
func Lossy(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == utf8.RuneError && n <= 1 {
			sb.WriteRune(utf8.RuneError)
			b = b[max(n, 1):]
			continue
		}
		sb.Write(b[:n])
		b = b[n:]
	}
	return sb.String()
}

// ReadJSONL is session_scorecard.read(): one JSON object per non-blank line,
// bad lines skipped.
func ReadJSONL(path string) []any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []any
	for _, line := range strings.Split(Lossy(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		v, err := ojson.Parse([]byte(line))
		if err == nil && ojson.Obj(v) != nil {
			out = append(out, v)
		}
	}
	return out
}

func TextOf(msg any) string {
	content, ok := ojson.Obj(msg).Get("content")
	if !ok {
		return ""
	}
	parts, isArr := content.([]any)
	if !isArr {
		return ojson.PyStr(content)
	}
	var texts []string
	for _, p := range parts {
		if t, _ := ojson.Str(ojson.Field(p, "type")); t == "text" {
			s, _ := ojson.Str(ojson.Field(p, "text"))
			texts = append(texts, s)
		}
	}
	return strings.Join(texts, " ")
}

// AutoContinuation is a user entry the harness wrote (background task finished, wake-up, summary after /compact), not something the human typed.
func AutoContinuation(entry any, body string) bool {
	b := strings.TrimLeftFunc(body, unicode.IsSpace)
	return ojson.Truthy(ojson.Field(entry, "isCompactSummary")) || strings.HasPrefix(b, "<task-notification") ||
		strings.HasPrefix(b, "<scheduled-wakeup") || strings.HasPrefix(b, "<background-task")
}

func IsPrompt(entry any) bool {
	if t, _ := ojson.Str(ojson.Field(entry, "type")); t != "user" || ojson.Has(entry, "toolUseResult") {
		return false
	}
	if ojson.Truthy(ojson.Field(entry, "isMeta")) {
		return false
	}
	var msg any = ojson.NewObject()
	if m := ojson.GetOr(entry, "message"); m != nil {
		msg = m
	}
	body := TextOf(msg)
	return !strings.Contains(body, "<command-name>") && !strings.Contains(body, Interrupted) && !AutoContinuation(entry, body)
}

// IsoTS is datetime.fromisoformat(ts.replace("Z", "+00:00")).timestamp().
// ponytail: a timestamp without an offset is read as UTC; transcripts always carry Z
func IsoTS(raw string) (float64, bool) {
	s := strings.ReplaceAll(raw, "Z", "+00:00")
	b := []byte(s)
	num := func(lo, hi int) (int64, bool) {
		if hi > len(s) || lo > hi || !utf8.RuneStart(at(b, lo)) || (hi < len(b) && !utf8.RuneStart(b[hi])) {
			return 0, false
		}
		t := s[lo:hi]
		for i := 0; i < len(t); i++ {
			if t[i] < '0' || t[i] > '9' {
				return 0, false
			}
		}
		n, err := strconv.ParseInt(t, 10, 64)
		return n, err == nil
	}
	if len(b) < 10 || b[4] != '-' || b[7] != '-' {
		return 0, false
	}
	y, ok1 := num(0, 4)
	mo, ok2 := num(5, 7)
	d, ok3 := num(8, 10)
	if !ok1 || !ok2 || !ok3 {
		return 0, false
	}
	var h, mi, sec, micros, off int64
	if len(b) > 10 {
		if b[10] != 'T' && b[10] != ' ' {
			return 0, false
		}
		var ok bool
		if h, ok = num(11, 13); !ok {
			return 0, false
		}
		if len(b) <= 13 || b[13] != ':' {
			return 0, false
		}
		if mi, ok = num(14, 16); !ok {
			return 0, false
		}
		i := 16
		if len(b) > 16 && b[16] == ':' {
			if sec, ok = num(17, 19); !ok {
				return 0, false
			}
			i = 19
			if len(b) > 19 && (b[19] == '.' || b[19] == ',') {
				digits := 0
				for 20+digits < len(b) && b[20+digits] >= '0' && b[20+digits] <= '9' {
					digits++
				}
				if digits == 0 {
					return 0, false
				}
				frac := s[20 : 20+min(digits, 6)]
				f, err := strconv.ParseInt(frac, 10, 64)
				if err != nil {
					return 0, false
				}
				p := int64(1)
				for range 6 - len(frac) {
					p *= 10
				}
				micros = f * p
				i = 20 + digits
			}
		}
		if i < len(b) {
			var sign int64
			switch b[i] {
			case '+':
				sign = 1
			case '-':
				sign = -1
			default:
				return 0, false
			}
			rest := s[i+1:]
			var oh, om int64
			var err1, err2 error
			switch {
			case len(rest) == 5 && rest[2] == ':':
				oh, err1 = parseI64(rest, 0, 2)
				om, err2 = parseI64(rest, 3, 5)
			case len(rest) == 4:
				oh, err1 = parseI64(rest, 0, 2)
				om, err2 = parseI64(rest, 2, 4)
			case len(rest) == 2:
				oh, err1 = parseI64(rest, 0, 2)
			default:
				return 0, false
			}
			if err1 != nil || err2 != nil {
				return 0, false
			}
			off = sign * (oh*3600 + om*60)
		}
	}
	if mo < 1 || mo > 12 || d < 1 || d > 31 || h > 23 || mi > 59 || sec > 59 {
		return 0, false
	}
	// days from civil (Howard Hinnant)
	yy := y
	if mo <= 2 {
		yy = y - 1
	}
	era := floorDiv(yy, 400)
	yoe := yy - era*400
	mp := (mo + 9) % 12
	doy := (153*mp+2)/5 + d - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy
	days := era*146097 + doe - 719468
	secs := days*86400 + h*3600 + mi*60 + sec - off
	return float64(secs*1_000_000+micros) / 1e6, true
}

func at(b []byte, i int) byte {
	if i < len(b) {
		return b[i]
	}
	return 0
}

// parseI64 is Rust's s[lo..hi].parse::<i64>(), which panics-free fails on a
// non-boundary slice.
func parseI64(s string, lo, hi int) (int64, error) {
	if !utf8.RuneStart(at([]byte(s), lo)) || (hi < len(s) && !utf8.RuneStart(s[hi])) {
		return 0, strconv.ErrSyntax
	}
	return strconv.ParseInt(s[lo:hi], 10, 64)
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// IsWord is Rust regex's Unicode \w.
func IsWord(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.Is(unicode.Nd, r) ||
		unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Pc, r) || unicode.Is(unicode.Other_Alphabetic, r) ||
		r == 0x200C || r == 0x200D
}

// IsAlnum is Rust's char::is_alphanumeric.
func IsAlnum(r rune) bool {
	return unicode.IsLetter(r) || unicode.Is(unicode.Nl, r) || unicode.Is(unicode.Other_Alphabetic, r) ||
		unicode.IsNumber(r)
}

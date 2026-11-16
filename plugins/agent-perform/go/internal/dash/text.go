// Package dash is the localhost:8787 dashboard (dashboard.py, dash_*.py,
// pipeline.py and their helpers): the same pages, byte for byte, from the
// same transcripts.
package dash

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pkoukk/tiktoken-go"
	tiktoken_loader "github.com/pkoukk/tiktoken-go-loader"

	"agentperform/internal/common"
	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
)

func home() string     { return common.Home() }
func projects() string { return filepath.Join(home(), ".claude", "projects") }

const (
	skillMark   = "Base directory for this skill:"
	imageTokens = 1_600 // an image block's rough cost; the transcript has only base64
	BigN        = 20
	RepeatListN = 10
	CallsPerGrp = 30
	textCap     = 1500
	firstLineN  = 80
	mask        = "［已遮蔽］"
	launched    = "Async agent launched"
)

var secret = pyre.Compile(
	`-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END[^-]*-----|\z)`+
		`|sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{20,}|github_pat_\w{20,}`+
		`|AKIA[0-9A-Z]{16}|xox[abp]-[\w-]{10,}`+
		`|(?i:(?:token|api[_-]?key|secret|passw(?:or)?d|authorization)["']?\s*[=:]\s*(?:bearer\s+)?["']?)[^\s"',}]+`+
		`|(?i:bearer\s+)[A-Za-z0-9._~+/-]{8,}`+
		`|(?i:issuer\W{0,6})[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`+
		`|(?<![A-Za-z0-9])(?=[A-Z0-9]*\d)(?=[A-Z0-9]*[A-Z])[A-Z0-9]{10}(?![A-Za-z0-9])`, pyre.S)

// Redact masks what looks like a credential before transcript text reaches a page.
func Redact(text string) string { return secret.Sub(text, mask) }

// Esc is html.escape(str(s)).
func Esc(s string) string { return py.HTMLEscape(s) }

// shown is HTML-safe, redacted, length-capped text for a <pre>.
func shown(text string) string {
	text = Redact(text)
	out := Esc(py.Head(text, textCap))
	if py.Len(text) > textCap {
		out += "…（後略）"
	}
	return out
}

// firstLine is the first non-empty line, cut to n characters.
func firstLine(text string, n int) string {
	line := ""
	for _, l := range py.Splitlines(text) {
		if py.Strip(l) != "" {
			line = py.Strip(l)
			break
		}
	}
	out := py.Head(Redact(line), n)
	if py.Len(line) > n {
		out += "…"
	}
	return out
}

// Transcript is the newest transcript whose name starts with the session id.
func Transcript(sid string) string {
	hits := globDirOrder(projects(), sid)
	best, bestT := "", int64(0)
	for _, h := range hits {
		st, err := os.Stat(h)
		if err != nil {
			continue
		}
		if best == "" || st.ModTime().UnixNano() > bestT {
			best, bestT = h, st.ModTime().UnixNano()
		}
	}
	return best
}

// globDirOrder is glob(projects/*/<prefix>*.jsonl) in directory order.
func globDirOrder(root, prefix string) []string {
	var out []string
	for _, d := range listdir(root) {
		dir := filepath.Join(root, d)
		for _, f := range listdir(dir) {
			if strings.HasPrefix(f, prefix) && strings.HasSuffix(f, ".jsonl") {
				out = append(out, filepath.Join(dir, f))
			}
		}
	}
	return out
}

// listdir is os.listdir order without hidden names (glob's `*`).
func listdir(dir string) []string {
	fh, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer fh.Close()
	names, _ := fh.Readdirnames(-1)
	var out []string
	for _, n := range names {
		if !strings.HasPrefix(n, ".") {
			out = append(out, n)
		}
	}
	return out
}

// textOf is pipeline._text(): a content string, or its blocks' text/thinking joined.
func textOf(content any) string {
	if s, ok := content.(string); ok {
		return s
	}
	var out []string
	switch t := content.(type) {
	case []any:
		for _, x := range t {
			if ojson.Obj(x) != nil {
				v := ojson.GetOr(x, "text")
				if v == nil {
					v = ojson.GetOr(x, "thinking")
				}
				s, _ := v.(string)
				out = append(out, s)
			} else if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case *ojson.Object:
		out = append(out, t.Keys...)
	}
	return strings.Join(out, "\n")
}

func msgOf(r any) any { return ojson.GetOr(r, "message") }

func promptText(r any) string {
	return strings.Join(py.Split(textOf(ojson.Field(msgOf(r), "content"))), " ")
}

// promptLabel: a background task notification is not something the user typed.
func promptLabel(text string) string {
	if strings.HasPrefix(py.LStrip(text), "<task-notification") {
		return "（背景工作通知）"
	}
	return text
}

var (
	tokOnce sync.Once
	tokEnc  *tiktoken.Tiktoken
)

// countTokens is OpenAI's o200k tokenizer (tiktoken), encoded as plain text.
func countTokens(t string) int {
	tokOnce.Do(func() {
		tiktoken.SetBpeLoader(tiktoken_loader.NewOfflineLoader())
		tokEnc, _ = tiktoken.GetEncoding("o200k_base")
	})
	if tokEnc == nil {
		cjk, other := 0, 0
		for _, r := range t {
			if r >= '一' && r <= '鿿' {
				cjk++
			} else {
				other++
			}
		}
		return int(float64(cjk)*0.7 + float64(other)/4)
	}
	return len(tokEnc.EncodeOrdinary(t))
}

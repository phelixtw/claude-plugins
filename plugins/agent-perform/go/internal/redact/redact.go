// Package redact masks secrets in text before it is written anywhere (redact.py).
package redact

import (
	"strings"

	"agentperform/internal/pyre"
)

const mask = "***"

var (
	shapes = pyre.Compile(strings.Join([]string{
		`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`,
		`\bsk-(?:ant-)?[A-Za-z0-9_-]{16,}`,
		`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`,
		`\bgithub_pat_[A-Za-z0-9_]{20,}`,
		`\bglpat-[A-Za-z0-9_-]{16,}`,
		`\bxox[abprs]-[A-Za-z0-9-]{10,}`,
		`\bAKIA[0-9A-Z]{16}\b`,
		`\bAIza[0-9A-Za-z_-]{35}\b`,
		`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`,
	}, "|"))
	auth = pyre.Compile(`\b(Bearer|Basic|ApiKey|Token)\s+(?=[A-Za-z0-9._~+/=-]*\d)[A-Za-z0-9._~+/=-]{8,}`, pyre.I)
	// the header form needs no digit: base64 of "user:pass" may have none
	authHdr = pyre.Compile(`(\bAuthorization\s*[:=]\s*(?:Bearer|Basic|ApiKey|Token))\s+[A-Za-z0-9._~+/=-]{8,}`, pyre.I)
	urlPass = pyre.Compile(`(\b[a-z][a-z0-9+.-]*://[^\s:/@]+:)[^\s@/]+(@)`, pyre.I)
	kv      = pyre.Compile(`((?:[\w.-]*(?:password|passwd|pwd|secret|token|api[_-]?key|access[_-]?key|private[_-]?key`+
		`|client[_-]?secret|credential|auth)[\w.-]*|密碼|密钥|金鑰|權杖)`+
		`["']?\s*(?:=|:|：|是|為)\s*["']?)([^\s"',;，。]{4,})`, pyre.I)
)

func notValue(v string) bool {
	if strings.HasPrefix(v, "$") || strings.HasPrefix(v, "*") {
		return true
	}
	switch strings.ToLower(v) {
	case "bearer", "basic", "apikey", "token":
		return true
	}
	return false
}

// Literals at least one of which every match of each pattern contains; texts
// without any skip that regex (regexp2 is slow on long transcripts).
var (
	shapeLits = []string{"-----BEGIN ", "sk-", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-", "xox", "AKIA", "AIza", "eyJ"}
	authLits  = []string{"bearer", "basic", "apikey", "token"}
	kvLits    = []string{"pass", "pwd", "secret", "token", "key", "credential", "auth", "密碼", "密钥", "金鑰", "權杖"}
)

func hasAny(s string, lits []string) bool {
	for _, l := range lits {
		if strings.Contains(s, l) {
			return true
		}
	}
	return false
}

// Redact is redact.redact().
func Redact(text string) string {
	if text == "" {
		return text
	}
	// re.I folds U+017F and U+0131 onto s and i, which ToLower does not; those
	// texts take the full regex path.
	folded := strings.ContainsAny(text, "\u017f\u0131")
	lower := strings.ToLower(text)
	if hasAny(text, shapeLits) {
		text = shapes.Sub(text, mask)
	}
	if folded || hasAny(lower, authLits) {
		text = auth.SubFunc(text, func(m *pyre.Match) string { return m.Group(1) + " " + mask })
		text = authHdr.SubFunc(text, func(m *pyre.Match) string { return m.Group(1) + " " + mask })
	}
	if strings.Contains(text, "://") {
		text = urlPass.SubFunc(text, func(m *pyre.Match) string { return m.Group(1) + mask + m.Group(2) })
	}
	if !folded && !hasAny(lower, kvLits) {
		return text
	}
	return kv.SubFunc(text, func(m *pyre.Match) string {
		if notValue(m.Group(2)) {
			return m.Group(0)
		}
		return m.Group(1) + mask
	})
}

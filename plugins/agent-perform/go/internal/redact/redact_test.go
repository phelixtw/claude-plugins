package redact

import (
	"strings"
	"testing"
)

// Secrets in many formats must never survive.
func TestRedactMasksSecrets(t *testing.T) {
	cases := []struct{ name, in, secret string }{
		{"anthropic key", "key sk-ant-api03-abcdefghijklmnop1234567890 here", "abcdefghijklmnop1234567890"},
		{"openai key", "OPENAI sk-proj1234567890abcdefgh", "proj1234567890abcdefgh"},
		{"github pat", "ghp_abcdefghijklmnopqrstuvwxyz0123", "abcdefghijklmnopqrstuvwxyz0123"},
		{"github fine grained", "github_pat_11ABCDEFG0123456789_abcdefghij", "11ABCDEFG0123456789"},
		{"gitlab", "glpat-abcdefghij1234567890", "abcdefghij1234567890"},
		{"slack", "xoxb-1234567890-abcdefghij", "1234567890-abcdefghij"},
		{"aws", "id AKIAIOSFODNN7EXAMPLE end", "AKIAIOSFODNN7EXAMPLE"},
		{"google", "AIzaSyA1234567890abcdefghijklmnopqrstuv", "SyA1234567890abcdefghijklmnopqrstuv"},
		{"jwt", "tok eyJhbGciOiJIUzI1.eyJzdWIiOiIxMjM0.SflKxwRJSMeKKF2QT4", "SflKxwRJSMeKKF2QT4"},
		{"pem", "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\nabc\n-----END RSA PRIVATE KEY-----\nafter", "MIIEowIBAAKCAQEA"},
		{"pem unterminated", "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBg", "MIIEvQIBADANBg"},
		{"bearer", "Authorization: Bearer abc123def456ghi789", "abc123def456ghi789"},
		{"basic", "Authorization: Basic dXNlcjpwYXNzMTIz", "dXNlcjpwYXNzMTIz"},
		{"url password", "postgres://admin:S3cr3tPw@db.example.com:5432/x", "S3cr3tPw"},
		{"password=", "password=hunter22xx", "hunter22xx"},
		{"json secret", `{"client_secret": "zzzz9999yyyy"}`, "zzzz9999yyyy"},
		{"env token", "GITHUB_TOKEN=abcd1234efgh", "abcd1234efgh"},
		{"api_key colon", "api_key: 0123456789abcdef", "0123456789abcdef"},
		{"chinese", "密碼是 abcd1234", "abcd1234"},
		{"chinese colon", "金鑰：xyz98765", "xyz98765"},
	}
	for _, c := range cases {
		got := Redact(c.in)
		if strings.Contains(got, c.secret) {
			t.Errorf("%s: secret leaked: %q", c.name, got)
		}
		if !strings.Contains(got, mask) {
			t.Errorf("%s: no mask: %q", c.name, got)
		}
	}
}

func TestRedactKeepsContext(t *testing.T) {
	cases := map[string]string{
		"password=hunter22xx":                                                   "password=***",
		"Authorization: Bearer abc123def456ghi789":                              "Authorization: Bearer ***",
		"postgres://admin:S3cr3tPw@db/x":                                        "postgres://admin:***@db/x",
		"after -----BEGIN PRIVATE KEY-----\nzz\n-----END PRIVATE KEY----- tail": "after *** tail",
	}
	for in, want := range cases {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
	}
}

// Ordinary text and non-values must be untouched.
func TestRedactLeavesNormalText(t *testing.T) {
	for _, s := range []string{
		"",
		"just a normal sentence about code, nothing secret here",
		"func main() { fmt.Println(\"hi\") }",
		"see https://example.com/path?q=1 and user@example.com",
		"password=$PASSWORD",
		"token: ****",
		"the token is Bearer",
		"Bearer token without digits",
		"sk-short",
		"AKIA123",
		"password: abc", // under 4 chars after the separator
		"ssh://git@host/repo.git",
		"今天天氣很好，我們去散步",
	} {
		if got := Redact(s); got != s {
			t.Errorf("changed %q -> %q", s, got)
		}
	}
}

// Case-folded characters (long s, dotless i) take the full regex path.
func TestRedactUnicodeFold(t *testing.T) {
	got := Redact("PASSWORD=hunter22xx and ſ")
	if strings.Contains(got, "hunter22xx") {
		t.Errorf("leaked: %q", got)
	}
}

func TestRedactIsIdempotent(t *testing.T) {
	in := "password=hunter22xx Bearer abc123def456ghi789 sk-ant-api03-abcdefghijklmnop1234567890"
	once := Redact(in)
	if twice := Redact(once); twice != once {
		t.Errorf("not idempotent: %q vs %q", once, twice)
	}
}

// A Basic/Bearer credential whose base64 happens to contain no digit is still masked,
// while short plain words after "Bearer"/"Token" are kept.
func TestRedactDigitlessCredential(t *testing.T) {
	if got := Redact("Authorization: Basic dXNlcjpwYXNzTElH"); got != "Authorization: Basic ***" {
		t.Errorf("got %q", got)
	}
	if got := Redact("Token configuration"); got != "Token configuration" {
		t.Errorf("got %q", got)
	}
}

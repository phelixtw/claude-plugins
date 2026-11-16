// Package pollgate is the PreToolUse(Bash) hook that stops the main agent from
// polling in the foreground.
//
// A sleep-then-check line, or a loop that sleeps between checks, keeps the
// turn open and makes one API request per check, each one re-reading the whole
// context. Bash with run_in_background waits for it instead. The call is denied
// with one stderr line and exit 2. Subagents (hook input has agent_id), calls
// already in the background, and a plain sleep of 5 seconds or less pass.
package pollgate

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"unicode/utf8"

	"agentperform/internal/common"
	"agentperform/internal/ojson"
)

const (
	shortSleepSec = 5.0
	Message       = "agent-perform: 前景輪詢（sleep / until / while-sleep）每次檢查都重讀整份 context，改用 run_in_background 等結果。"
	// Unicode \s, as Rust's regex has it
	ws = `[\t\n\v\f\r\x{85}\pZ]`
)

// The number must not run on into a word or a dot (`6x`, `1.5.1`); RE2 has no
// lookahead, so IsPolling checks the character after each match.
var sleepRe = regexp.MustCompile(`(?:^` + ws + `*|[;&|(` + "`" + `\n]` + ws + `*|-c` + ws + `+['"]` + ws + `*)sleep` + ws + `+(\p{Nd}+(?:\.\p{Nd}+)?)([smhd]?)`)

// word finds the first whole-word occurrence of w at or after from (Unicode \b).
func word(s, w string, from int) int {
	for i := from; i+len(w) <= len(s); i++ {
		if s[i:i+len(w)] != w {
			continue
		}
		before, _ := utf8.DecodeLastRuneInString(s[:i])
		after, _ := utf8.DecodeRuneInString(s[i+len(w):])
		if (i == 0 || !common.IsWord(before)) && (i+len(w) == len(s) || !common.IsWord(after)) {
			return i
		}
	}
	return -1
}

// loop is `(?s)\b(?:until|while|for)\b.*?\bdo\b.*?\bsleep\b`.
func loop(cmd string) bool {
	start := -1
	for _, kw := range []string{"until", "while", "for"} {
		if i := word(cmd, kw, 0); i >= 0 && (start < 0 || i+len(kw) < start) {
			start = i + len(kw)
		}
	}
	if start < 0 {
		return false
	}
	do := word(cmd, "do", start)
	return do >= 0 && word(cmd, "sleep", do+2) >= 0
}

// IsPolling is "loop", "sleep" or "".
func IsPolling(cmd string) string {
	if loop(cmd) {
		return "loop"
	}
	for _, m := range sleepRe.FindAllStringSubmatchIndex(cmd, -1) {
		if next, size := utf8.DecodeRuneInString(cmd[m[1]:]); size > 0 && (common.IsAlnum(next) || next == '_' || next == '.') {
			continue
		}
		unit := 86400.0
		switch cmd[m[4]:m[5]] {
		case "s", "":
			unit = 1
		case "m":
			unit = 60
		case "h":
			unit = 3600
		}
		if n, err := strconv.ParseFloat(cmd[m[2]:m[3]], 64); err == nil && n*unit > shortSleepSec {
			return "sleep"
		}
	}
	return ""
}

// Verdict is the deny reason, or "" to let the call through.
func Verdict(hook any) string {
	if !common.Experiment("polling_block") {
		return ""
	}
	if ojson.Truthy(ojson.Field(hook, "agent_id")) {
		return ""
	}
	if name, _ := ojson.Str(ojson.Field(hook, "tool_name")); name != "Bash" {
		return ""
	}
	var inp any = ojson.NewObject()
	if v := ojson.GetOr(hook, "tool_input"); v != nil {
		inp = v
	}
	if ojson.Obj(inp) == nil || ojson.Truthy(ojson.Field(inp, "run_in_background")) {
		return ""
	}
	cmd := ""
	switch v := ojson.Field(inp, "command").(type) {
	case string:
		cmd = v
	default:
		if ojson.Truthy(v) {
			return "" // not a command line: never block on input we cannot read
		}
	}
	if IsPolling(cmd) != "" {
		return Message
	}
	return ""
}

func Main(stdin string) int {
	if stdin == "" {
		stdin = "{}"
	}
	hook, err := ojson.Parse([]byte(stdin))
	if err != nil || ojson.Obj(hook) == nil {
		return 0
	}
	if reason := Verdict(hook); reason != "" {
		fmt.Fprintln(os.Stderr, reason)
		return 2
	}
	return 0
}

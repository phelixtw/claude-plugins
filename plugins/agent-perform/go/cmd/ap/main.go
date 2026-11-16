// bin/ap: every agent-perform hook, the statusline and the mod, in one binary.
//
//	ap poll-gate                      PreToolUse(Bash)
//	ap ctx-probe [--print]            Stop (async)
//	ap session-start nudge|dashboard
//	ap session-end                    SessionEnd (async)
//	ap scorecard [--mod]             mod
//	ap statusline                     legacy statusLine command
//	ap doctor                         /agent-perform:doctor
//	ap dashboard --serve [port]       localhost dashboard (default 8787)
//
// Build (from go/, needs cgo):
//
//	go build -trimpath -ldflags "-s -w -X main.version=$(jq -r .version ../.claude-plugin/plugin.json)" -o ../bin/ap ./cmd/ap
package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"slices"
	"strconv"
	"syscall"
	"unicode/utf8"

	"agentperform/internal/common"
	"agentperform/internal/ctxprobe"
	"agentperform/internal/dash"
	"agentperform/internal/doctor"
	"agentperform/internal/hooks"
	"agentperform/internal/pollgate"
	"agentperform/internal/scorecard"
)

var version = "dev"

func main() {
	signal.Ignore(syscall.SIGPIPE) // a closed stdout (statusline piped to head) is not an error
	os.Exit(run(os.Args[1:], os.Stdin))
}

// run dispatches one subcommand; tests call it with a fake stdin.
func run(args []string, stdin io.Reader) int {
	flag := func(f string) bool { return slices.Contains(args, f) }
	read := func() (string, bool) {
		b, err := io.ReadAll(stdin)
		return string(b), err == nil && utf8.Valid(b) // Rust read_to_string
	}
	code := 0
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "poll-gate":
		if in, ok := read(); ok { // a hook must never break a turn
			code = pollgate.Main(in)
		}
	case "scorecard":
		if in, ok := read(); ok {
			code = scorecard.Main(in, flag("--mod"))
		}
	case "ctx-probe":
		in, _ := read()
		code = ctxprobe.Main(in, flag("--print"))
	case "session-start":
		sub := ""
		if len(args) > 1 {
			sub = args[1]
		}
		switch sub {
		case "nudge":
			code = hooks.Nudge()
		case "dashboard":
			code = hooks.EnsureDashboard()
		default:
			code = 64
		}
	case "session-end":
		code = hooks.SessionEnd()
	case "statusline":
		in, _ := read()
		code = hooks.Statusline(in)
	case "doctor":
		code = doctor.Main()
	case "dashboard":
		code = serveDashboard(args[1:])
	case "--version":
		common.Say("ap " + version)
	default:
		fmt.Fprintln(os.Stderr, "usage: ap poll-gate | ctx-probe | session-start <step> | session-end | scorecard | statusline | doctor | dashboard --serve [port]")
		code = 64
	}
	return code
}

// serveDashboard is `ap dashboard`: --serve takes the next argument as
// the port when it is all digits, else 8787.
func serveDashboard(args []string) int {
	if slices.Contains(args, "--refresh-stats") {
		dash.RefreshStats()
		return 0
	}
	i := slices.Index(args, "--serve")
	if i < 0 {
		fmt.Fprintln(os.Stderr, "usage: ap dashboard --serve [port]")
		return 64
	}
	port := 8787
	if i+1 < len(args) {
		if n, err := strconv.ParseUint(args[i+1], 10, 31); err == nil {
			port = int(n)
		}
	}
	debug.SetGCPercent(400) // a long-lived server parsing large transcripts; trade memory for GC time
	if err := dash.Serve(port); err != nil {
		fmt.Fprintln(os.Stderr, "ap dashboard:", err)
		return 1
	}
	return 0
}

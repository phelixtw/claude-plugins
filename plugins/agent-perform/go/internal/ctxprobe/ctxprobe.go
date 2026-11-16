// Package ctxprobe is the Stop hook that records the real `/context` category
// breakdown in ~/.claude/ctx-cache.json, so the dashboard does not have to guess it.
//
// The numbers `/context` shows are computed from live state and never written
// to the transcript, but `claude -p "/context"` prints the same table as
// markdown, and every category except Skills and Messages is constant for a
// (cwd, model, config) triple. So: probe once per config change, cache it.
// A probe spawns a real `claude`, whose own Stop hook would probe again;
// CLAUDE_CTX_PROBE in its environment stops that.
package ctxprobe

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"agentperform/internal/common"
	"agentperform/internal/ojson"
)

const (
	ttl   = 24 * 3600.0
	guard = "CLAUDE_CTX_PROBE"
	ws    = `[\t\n\v\f\r\x{85}\pZ]`
)

var (
	watch = []string{"settings.json", "settings.local.json", "CLAUDE.md", "rules", "skills", "plugins/config.json", ".mcp.json"}
	row   = regexp.MustCompile(`(?i)^\|` + ws + `*([A-Za-z][A-Za-z ()]+?)` + ws + `*\|` + ws + `*([\p{Nd}.]+)` + ws + `*([km]?)` + ws + `*\|` + ws + `*([\p{Nd}.]+)%` + ws + `*\|`)
	total = regexp.MustCompile(`(?i)([\p{Nd}.]+)([km]?)` + ws + `*/` + ws + `*([\p{Nd}.]+)([km]?)` + ws + `*(?:tokens)?` + ws + `*\(`)
)

func fingerprint(cwd string) string {
	claude := common.Join(common.Home(), ".claude")
	var parts []string
	for _, rel := range watch {
		for _, base := range []string{claude, cwd} {
			p := common.Join(base, rel)
			if st, err := os.Stat(p); err == nil {
				parts = append(parts, p+":"+strconv.FormatInt(int64(math.Floor(common.Epoch(st.ModTime()))), 10))
			}
		}
	}
	return strings.Join(parts, "|")
}

func num(value, suffix string) (int64, bool) {
	scale := 1.0
	switch strings.ToLower(suffix) {
	case "k":
		scale = 1_000
	case "m":
		scale = 1_000_000
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil || strings.ContainsAny(value, "_") {
		return 0, false
	}
	return int64(f * scale), true
}

// lines is Rust's str::lines: split on \n, drop one trailing \r per line.
func lines(text string) []string {
	out := strings.Split(text, "\n")
	if len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	for i, l := range out {
		out[i] = strings.TrimSuffix(l, "\r")
	}
	return out
}

// Parse pulls the category table out of the markdown `/context` prints.
func Parse(text string) (*ojson.Object, int64, bool) {
	cats := ojson.NewObject()
	var limit int64
	for _, line := range lines(text) {
		if limit == 0 {
			if h := total.FindStringSubmatch(line); h != nil {
				n, ok := num(h[3], h[4])
				if !ok {
					return nil, 0, false
				}
				limit = n
			}
		}
		if h := row.FindStringSubmatch(strings.TrimSpace(line)); h != nil {
			name := strings.TrimSpace(h[1])
			if strings.ToLower(name) != "category" {
				n, ok := num(h[2], h[3])
				if !ok {
					return nil, 0, false
				}
				cats.Set(name, n)
			}
		}
	}
	if cats.Len() == 0 || limit == 0 {
		return nil, 0, false
	}
	return cats, limit, true
}

func probe(cwd, model string) (*ojson.Object, int64, bool) {
	args := []string{"-p", "/context"}
	if model != "" {
		args = append(args, "--model", model)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), guard+"=1")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, 0, false
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return nil, 0, false
	}
	return Parse(common.Lossy(out.Bytes()))
}

func lastModel(path string) string {
	found := ""
	raw, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(raw) {
		return found
	}
	for _, line := range lines(string(raw)) {
		if !strings.Contains(line, `"model"`) {
			continue
		}
		v, err := ojson.Parse([]byte(line))
		if err != nil {
			continue
		}
		if m, ok := ojson.Str(ojson.Field(ojson.Field(v, "message"), "model")); ok && m != "" {
			found = m
		}
	}
	return found
}

func Main(stdin string, print bool) int {
	if os.Getenv(guard) != "" {
		return 0
	}
	hook, err := ojson.Parse([]byte(stdin))
	if err != nil {
		hook = ojson.NewObject()
	}
	if ojson.Obj(hook) == nil {
		return 1
	}
	cwd, _ := ojson.Str(ojson.Field(hook, "cwd"))
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	model, _ := ojson.Str(ojson.Field(ojson.Field(hook, "model"), "id"))
	if model == "" {
		tp, _ := ojson.Str(ojson.Field(hook, "transcript_path"))
		model = lastModel(tp)
	}
	key := cwd + "|" + model
	path := common.Join(common.Home(), ".claude", "ctx-cache.json")

	cache := ojson.NewObject()
	if raw, err := os.ReadFile(path); err == nil {
		if v, err := ojson.Parse(raw); err == nil && ojson.Obj(v) != nil {
			cache = ojson.Obj(v)
		}
	}
	fp := fingerprint(cwd)
	if old, ok := cache.Get(key); ok && ojson.Obj(old).Len() > 0 {
		at, _ := ojson.Float(ojson.Field(old, "at"))
		if f, _ := ojson.Str(ojson.Field(old, "fp")); f == fp && common.Now()-at < ttl {
			if print {
				common.Say(ojson.Indent(old, 1))
			}
			return 0
		}
	}

	cats, limit, ok := probe(cwd, model)
	if !ok {
		return 0
	}
	entry := ojson.NewObject().Set("cats", cats).Set("limit", limit).Set("fp", fp).
		Set("at", int64(common.Now())).Set("model", model).Set("cwd", cwd)
	cache.Set(key, entry)
	tmp := strings.TrimSuffix(path, ".json") + ".json.tmp"
	if os.WriteFile(tmp, []byte(ojson.Indent(cache, 1)), 0o644) != nil || os.Rename(tmp, path) != nil {
		return 1
	}
	if print {
		common.Say(ojson.Indent(entry, 1))
	}
	return 0
}

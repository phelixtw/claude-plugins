// Package transcript reads Claude Code transcripts the way the Python scripts
// did (session_scorecard.read / when / text_of / is_prompt, turn_list,
// vote.turns_of).
package transcript

import (
	"bufio"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"agentperform/internal/common"
	"agentperform/internal/ojson"
	"agentperform/internal/py"
)

// Lines splits text the way a Python text-mode file iterates it: on \n, \r\n
// and \r (universal newlines).
func Lines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n")
}

// RawLines streams the file's lines (same splitting as Lines) without holding
// the whole file in memory.
func RawLines(path string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, l := range linesAt(path, 0) {
			if !yield(l) {
				return
			}
		}
	}
}

// linesAt is RawLines from byte offset off (a line start); each line comes with the offset of the line it was read from.
func linesAt(path string, off int64) iter.Seq2[int64, string] {
	return func(yield func(int64, string) bool) {
		f, err := os.Open(path)
		if err != nil {
			return
		}
		defer f.Close()
		if _, err := f.Seek(off, 0); err != nil {
			return
		}
		r := bufio.NewReaderSize(f, 1<<20)
		pos := off
		for {
			line, err := r.ReadString('\n')
			if line != "" || err == nil {
				for _, l := range Lines(strings.TrimSuffix(line, "\n")) {
					if !yield(pos, l) {
						return
					}
				}
			}
			pos += int64(len(line))
			if err != nil {
				return
			}
		}
	}
}

// Entries is Read as a stream: one JSON object per non-blank line, bad lines
// skipped, only one line parsed at a time.
func Entries(path string) iter.Seq[any] {
	return func(yield func(any) bool) {
		for _, e := range EntriesAt(path, 0) {
			if !yield(e) {
				return
			}
		}
	}
}

// EntriesAt is Entries from byte offset off (a line start); each entry comes with the offset of its line.
func EntriesAt(path string, off int64) iter.Seq2[int64, any] {
	return func(yield func(int64, any) bool) {
		for pos, line := range linesAt(path, off) {
			line = py.Strip(common.Lossy([]byte(line)))
			if line == "" {
				continue
			}
			if v, err := ojson.Parse([]byte(line)); err == nil && ojson.Obj(v) != nil {
				if !yield(pos, v) {
					return
				}
			}
		}
	}
}

// Read is session_scorecard.read(): every Entries object of the file.
func Read(path string) []any { return slices.Collect(Entries(path)) }

// When is the entry's timestamp in epoch seconds.
func When(e any) (float64, bool) {
	ts, _ := ojson.Str(ojson.Field(e, "timestamp"))
	return common.IsoTS(ts)
}

// TextOf is session_scorecard.text_of().
func TextOf(msg any) string { return common.TextOf(msg) }

// IsPrompt is session_scorecard.is_prompt().
func IsPrompt(e any) bool { return common.IsPrompt(e) }

// Msg is `entry.get("message") or {}`.
func Msg(e any) any {
	if m := ojson.GetOr(e, "message"); m != nil {
		return m
	}
	return ojson.NewObject()
}

// Parts is `msg.get("content") or []` as a list (a non-list yields nothing).
func Parts(msg any) []any {
	parts, _ := ojson.GetOr(msg, "content").([]any)
	return parts
}

// Type is entry.get("type") as a string.
func Type(v any) string {
	s, _ := ojson.Str(ojson.Field(v, "type"))
	return s
}

// Turn is one turn_list() row.
type Turn struct {
	Key, Session, Prompt, Reply string
	At, ReplyEnd                float64
	TurnNo                      int
}

// Session8 is the first 8 characters of a transcript's file name.
func Session8(path string) string { return py.Head(filepath.Base(path), 8) }

// TurnList is turn_list(): every main-thread turn, in order.
func TurnList(path string) []*Turn {
	sid := Session8(path)
	var out []*Turn
	var cur *Turn
	for _, e := range Read(path) {
		if ojson.Truthy(ojson.Field(e, "isSidechain")) {
			continue
		}
		if IsPrompt(e) {
			body := TextOf(Msg(e))
			if py.Strip(body) == "" {
				continue
			}
			at, ok := When(e)
			if !ok {
				continue
			}
			cur = &Turn{Key: sid + ":" + strconv.FormatInt(int64(at), 10), Session: sid, Prompt: body, At: at, ReplyEnd: at, TurnNo: len(out) + 1}
			out = append(out, cur)
			continue
		}
		if Type(e) != "assistant" || cur == nil {
			continue
		}
		if at, ok := When(e); ok && at != 0 {
			cur.ReplyEnd = at
		}
		for _, p := range Parts(Msg(e)) {
			if Type(p) == "text" {
				s, _ := ojson.Str(ojson.Field(p, "text"))
				cur.Reply += s
			}
		}
	}
	return out
}

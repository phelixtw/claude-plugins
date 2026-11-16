package dash

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"agentperform/internal/common"
	"agentperform/internal/ojson"
	"agentperform/internal/py"
)

// forget.py: the dashboard's delete button.

var forgetFiles = []string{"subagent_costs.json", "session_types.json", "delegate_types.json"}

// errUncaught is what Python raised outside the handler's except clause
// (AttributeError on a non-dict payload or a non-string session): no reply,
// the connection just drops.
var errUncaught = errors.New("uncaught")

// transcriptsOf is every transcript whose name starts with sid. sid goes into
// the glob pattern unescaped, as in Python.
func transcriptsOf(sid string) []string {
	hits, _ := filepath.Glob(projects() + "/*/" + sid + "*.jsonl")
	sort.Strings(hits)
	return hits
}

// forget drops one conversation from the board: its rows in the derived files,
// and its transcript moved to the Trash. Logged so it can be restored.
// session is the payload's raw value: Python called .strip() on it.
func forget(session any, dry bool) (*ojson.Object, error) {
	sid := ""
	if s, ok := session.(string); ok {
		sid = py.Head(py.Strip(s), 8)
	} else if ojson.Truthy(session) {
		return nil, errUncaught
	}
	if sid == "" {
		return nil, errors.New("要給對話 id（前 8 碼）")
	}
	claudeDir := filepath.Join(common.Home(), ".claude")
	removed := ojson.NewObject()
	for _, name := range forgetFiles {
		path := filepath.Join(claudeDir, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		v, err := ojson.Parse(raw)
		if err != nil {
			continue
		}
		blob := ojson.Obj(v)
		if blob == nil {
			return nil, errUncaught
		}
		hits := ojson.NewObject()
		for _, k := range blob.Keys {
			if k == sid || strings.HasPrefix(k, sid+":") {
				hits.Set(k, blob.Vals[k])
			}
		}
		if hits.Len() == 0 {
			continue
		}
		removed.Set(name, hits)
		if dry {
			continue
		}
		rest := ojson.NewObject()
		for _, k := range blob.Keys {
			if _, hit := hits.Get(k); !hit {
				rest.Set(k, blob.Vals[k])
			}
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, []byte(py.Dumps(rest)), 0o666); err != nil {
			return nil, errors.New(osMsg(err))
		}
		if err := os.Rename(tmp, path); err != nil {
			return nil, errors.New(osMsg(err))
		}
	}
	// the transcript itself keeps the session on the board; it goes to the
	// Trash rather than being unlinked, so `claude --resume` stops working
	// either way but the file can be put back.
	trash := filepath.Join(common.Home(), ".Trash")
	moved := []any{}
	for _, path := range transcriptsOf(sid) {
		moved = append(moved, path)
		if dry {
			continue
		}
		dest := filepath.Join(trash, filepath.Base(path))
		if _, err := os.Stat(dest); err == nil {
			dest = fmt.Sprintf("%s.%d", dest, Now().Unix())
		}
		if err := os.Rename(path, dest); err != nil {
			if err := moveAcross(path, dest); err != nil {
				return nil, errors.New(osMsg(err))
			}
		}
	}

	log := filepath.Join(claudeDir, ".agent_perform_forgotten.jsonl")
	if (removed.Len() > 0 || len(moved) > 0) && !dry {
		fh, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
		if err != nil {
			return nil, errors.New(osMsg(err))
		}
		entry := ojson.NewObject().Set("at", common.Epoch(Now())).Set("session", sid).
			Set("removed", removed).Set("transcripts", moved)
		_, err = fh.WriteString(py.Dumps(entry) + "\n")
		fh.Close()
		if err != nil {
			return nil, errors.New(osMsg(err))
		}
	}
	names := ojson.NewObject()
	for _, name := range removed.Keys {
		keys := append([]string{}, ojson.Obj(removed.Vals[name]).Keys...)
		sort.Strings(keys)
		list := make([]any, len(keys))
		for i, k := range keys {
			list[i] = k
		}
		names.Set(name, list)
	}
	return ojson.NewObject().Set("session", sid).Set("dry_run", dry).Set("removed", names).
		Set("transcripts", moved).Set("transcripts_now_in", trash).Set("restorable_from", log), nil
}

// moveAcross is shutil.move's fallback when rename fails: copy, then unlink.
func moveAcross(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	os.Chtimes(dst, st.ModTime(), st.ModTime())
	return os.Remove(src)
}

// osMsg is str(OSError): "[Errno 2] No such file or directory: 'x'".
func osMsg(err error) string {
	var en syscall.Errno
	if !errors.As(err, &en) {
		return err.Error()
	}
	what := en.Error()
	what = strings.ToUpper(what[:1]) + what[1:]
	var pe *os.PathError
	var le *os.LinkError
	switch {
	case errors.As(err, &pe):
		return fmt.Sprintf("[Errno %d] %s: %s", int(en), what, py.StrRepr(pe.Path))
	case errors.As(err, &le):
		return fmt.Sprintf("[Errno %d] %s: %s -> %s", int(en), what, py.StrRepr(le.Old), py.StrRepr(le.New))
	}
	return fmt.Sprintf("[Errno %d] %s", int(en), what)
}

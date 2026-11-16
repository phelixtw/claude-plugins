package dash

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agentperform/internal/ojson"
	"agentperform/internal/py"
	"agentperform/internal/pyre"
	"agentperform/internal/transcript"
)

// tokenmodel.py: the closed-form token model for what delegation costs, and saves.

const (
	tmIN    = 4.00
	tmOUT   = 20.00
	tmCR    = 0.20
	tmCW    = 8.00
	tmCap   = 1_000_000
	tmDays  = 90
	tmBkt   = 10
	tmRound = 2
)

func tmState() string { return filepath.Join(home(), ".claude", "agent-perform") }

type step struct {
	turn                         int
	in, out, cr, cw, seat, typed int64
}

// steps is the per-request rows for one transcript.
func steps(path string, sidechain bool) []step {
	var out []step
	seen := map[string]bool{}
	turn := 0
	var typed int64
	for e := range transcript.Entries(path) {
		if ojson.Truthy(ojson.Field(e, "isSidechain")) != sidechain {
			continue
		}
		msg := transcript.Msg(e)
		if typ(e) == "user" && !sidechain {
			if transcript.IsPrompt(e) && !ojson.Truthy(ojson.Field(e, "isMeta")) {
				turn++
				typed += estTokens(transcript.TextOf(msg))
			}
			continue
		}
		if typ(e) != "assistant" {
			continue
		}
		usage := ojson.GetOr(msg, "usage")
		if role, _ := ojson.Str(ojson.Field(msg, "role")); usage == nil || role != "assistant" {
			continue
		}
		mid := ojson.GetOr(msg, "id")
		if mid == nil {
			mid = ojson.Field(e, "uuid")
		}
		if seen[py.Repr(mid)] {
			continue
		}
		seen[py.Repr(mid)] = true
		r := step{turn: turn, in: getNum(usage, "input_tokens"), out: getNum(usage, "output_tokens"),
			cr: getNum(usage, "cache_read_input_tokens"), cw: getNum(usage, "cache_creation_input_tokens")}
		r.seat = r.in + r.cr + r.cw
		r.typed = typed
		typed = 0
		out = append(out, r)
	}
	return out
}

func costOf(r step) float64 {
	// float64() rounds each product as Python does; arm64 otherwise fuses x*y+z into one FMA
	return (float64(float64(r.in)*tmIN) + float64(float64(r.out)*tmOUT) + float64(float64(r.cw)*tmCW) + float64(float64(r.cr)*tmCR)) / 1e6
}

// saving is the net dollars saved by delegating, all four token streams accounted.
func saving(C, rS, dE, res, K, pS, gS int64) (float64, float64, float64) {
	var seats int64
	for j := int64(0); j < rS; j++ {
		seats += min(C+gS*j, tmCap)
	}
	room := max(tmCap-C, 0)
	extra := min(max(dE-res, 0), room) * K
	saved := tmCR * float64(seats-rS*pS+extra) / 1e6
	paid := (float64(tmCW*float64(pS)) + float64(tmCR*(float64(rS*pS)+float64(gS*rS*(rS-1))/2))) / 1e6
	return max(saved, 0.0), paid, max(saved, 0.0) - paid
}

func agentFiles(mainPath string) []string {
	folder := filepath.Join(strings.TrimSuffix(mainPath, ".jsonl"), "subagents")
	fh, err := os.Open(folder)
	if err != nil {
		return nil
	}
	names, _ := fh.Readdirnames(-1)
	fh.Close()
	var out []string
	for _, n := range names {
		if strings.HasSuffix(n, ".jsonl") {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	for i, n := range out {
		out[i] = filepath.Join(folder, n)
	}
	return out
}

var agentIDHex2 = pyre.Compile(`agentId:\s*([a-f0-9]+)`)

// launchParams is one row per subagent run, with every quantity the formula needs.
func launchParams(mainPath string) []*ojson.Object {
	main := steps(mainPath, false)
	if len(main) == 0 {
		return nil
	}
	totalR := len(main)
	where := map[string]int{}
	pending := map[string]int{}
	seen := map[string]bool{}
	idx := 0
	for e := range transcript.Entries(mainPath) {
		if ojson.Truthy(ojson.Field(e, "isSidechain")) {
			continue
		}
		msg := transcript.Msg(e)
		if typ(e) == "assistant" {
			usage := ojson.GetOr(msg, "usage")
			mid := ojson.GetOr(msg, "id")
			if mid == nil {
				mid = ojson.Field(e, "uuid")
			}
			if role, _ := ojson.Str(ojson.Field(msg, "role")); usage != nil && role == "assistant" && !seen[py.Repr(mid)] {
				seen[py.Repr(mid)] = true
				idx++
			}
			for _, part := range transcript.Parts(msg) {
				if ojson.Obj(part) != nil && typ(part) == "tool_use" {
					if n, _ := ojson.Field(part, "name").(string); n == "Task" || n == "Agent" {
						pending[py.Repr(ojson.Field(part, "id"))] = max(idx-1, 0)
					}
				}
			}
		}
		list, _ := ojson.Field(msg, "content").([]any)
		for _, part := range list {
			if ojson.Obj(part) == nil || typ(part) != "tool_result" {
				continue
			}
			key := py.Repr(ojson.Field(part, "tool_use_id"))
			pos, ok := pending[key]
			if !ok {
				continue
			}
			body := ojson.Field(part, "content")
			text, isStr := body.(string)
			if !isStr {
				text = py.Dumps(body)
			}
			delete(pending, key)
			if m := agentIDHex2.Search(text); m != nil {
				where[m.Group(1)] = pos
			}
		}
	}
	var out []*ojson.Object
	for _, path := range agentFiles(mainPath) {
		rows := steps(path, false)
		if len(rows) == 0 {
			rows = steps(path, true)
		}
		if len(rows) == 0 {
			continue
		}
		base := filepath.Base(path)
		name := ""
		if len(base) > len("agent-")+len(".jsonl") {
			name = base[len("agent-") : len(base)-len(".jsonl")]
		}
		pos, ok := where[name]
		if !ok {
			continue
		}
		pS := rows[0].seat
		var writes []int64
		var cwSum int64
		for i, r := range rows {
			cwSum += r.cw
			if i > 0 && r.cw > 0 {
				writes = append(writes, r.cw)
			}
		}
		sort.Slice(writes, func(i, j int) bool { return writes[i] < writes[j] })
		var gS int64
		if len(writes) > 0 {
			gS = writes[len(writes)/2]
		}
		after := main[min(pos+1, totalR-1)]
		res := max(after.cw-after.typed, 0)
		costS := 0.0
		for _, r := range rows {
			costS += costOf(r)
		}
		out = append(out, ojson.NewObject().Set("agent", name).Set("r_s", int64(len(rows))).Set("P_s", pS).Set("g_s", gS).
			Set("D_e", max(cwSum-pS, 0)).Set("Res", res).Set("C", main[pos].seat).Set("K", int64(totalR-pos-1)).Set("cost_s", costS))
	}
	return out
}

func pctNear(vals []float64, q float64) float64 {
	v := append([]float64{}, vals...)
	sort.Float64s(v)
	if len(v) == 0 {
		return 0
	}
	return v[min(int(float64(len(v))*q), len(v)-1)]
}

func midInt(vals []int64) int64 {
	v := append([]int64{}, vals...)
	sort.Slice(v, func(i, j int) bool { return v[i] < v[j] })
	if len(v) == 0 {
		return 0
	}
	return v[min(int(float64(len(v))*0.5), len(v)-1)]
}

// pooled is the one delegation shape for every dispatch in the window.
func pooled(days int) *ojson.Object {
	var rows []*ojson.Object
	for _, s := range Collect(days) {
		for _, row := range launchParams(s.Path) {
			g := func(k string) int64 { return num(ojson.Field(row, k)) }
			saved, paid, net := saving(g("C"), g("r_s"), g("D_e"), g("Res"), g("K"), g("P_s"), g("g_s"))
			row.Set("saved", saved).Set("paid", paid).Set("net", net).Set("sid", s.ID).Set("at", s.Start).Set("proj", s.Proj)
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return ojson.NewObject()
	}
	var nets []float64
	var sum = map[string]float64{}
	ints := map[string][]int64{}
	for _, r := range rows {
		nets = append(nets, fnum(r, "net"))
		for _, k := range []string{"saved", "paid", "net"} {
			sum[k] += fnum(r, k)
		}
		for _, k := range []string{"r_s", "C", "K", "D_e", "Res", "P_s", "g_s"} {
			ints[k] = append(ints[k], num(ojson.Field(r, k)))
		}
	}
	sorted := append([]*ojson.Object{}, rows...)
	sort.SliceStable(sorted, func(i, j int) bool { return fnum(sorted[i], "net") > fnum(sorted[j], "net") })
	rowList := []any{}
	for _, r := range sorted {
		rowList = append(rowList, r)
	}
	out := ojson.NewObject().Set("n", int64(len(rows))).Set("saved", py.Round(sum["saved"], tmRound)).
		Set("paid", py.Round(sum["paid"], tmRound)).Set("net", py.Round(sum["net"], tmRound)).
		Set("per", py.Round(pctNear(nets, 0.5), tmRound)).Set("per_p75", py.Round(pctNear(nets, 0.75), tmRound))
	for _, k := range []string{"r_s", "C", "K", "D_e", "Res", "P_s", "g_s"} {
		out.Set(k, midInt(ints[k]))
	}
	return out.Set("rows", rowList)
}

// onDisk is _on_disk(): the saved result for a 90-day window, else a fresh
// build written back.
func onDisk(name, key string, build func(int) *ojson.Object, refresh bool) any {
	path := filepath.Join(tmState(), name)
	var blob any
	if raw, err := os.ReadFile(path); err == nil {
		if v, err := ojson.Parse(raw); err == nil && ojson.Obj(v) != nil {
			blob = v
			if !refresh && fnum(v, "days") == tmDays {
				if got, ok := ojson.Obj(v).Get(key); ok {
					return got
				}
				blob = nil
			}
		}
	}
	got := build(tmDays)
	os.MkdirAll(tmState(), 0o755)
	out := ojson.NewObject().Set("days", int64(tmDays)).Set("at", time.Now().Unix()).Set(key, got)
	os.WriteFile(path, []byte(py.Dumps(out)), 0o644)
	_ = blob
	return got
}

// tmCached is tokenmodel.cached(): the pooled shape, built when missing.
func tmCached() any { return onDisk("delegate_model.json", "shape", pooled, false) }

// TokenmodelRefresh is tokenmodel.py --refresh (SessionEnd).
func TokenmodelRefresh() {
	onDisk("delegate_model.json", "shape", pooled, true)
}

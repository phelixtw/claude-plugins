// Package scrub keeps conversation text out of the plugin's own files: the
// writers call Delegates/Health before saving.
package scrub

import "agentperform/internal/ojson"

// RemoveKey deletes a key from an ordered object.
func RemoveKey(o *ojson.Object, key string) {
	if _, ok := o.Vals[key]; !ok {
		return
	}
	delete(o.Vals, key)
	for i, k := range o.Keys {
		if k == key {
			o.Keys = append(o.Keys[:i], o.Keys[i+1:]...)
			return
		}
	}
}

// Delegates drops each launch's task description and script bodies (subagent_costs).
func Delegates(blob any) any {
	o := ojson.Obj(blob)
	if o == nil {
		return blob
	}
	for _, k := range o.Keys {
		launches, _ := ojson.GetOr(o.Vals[k], "launches").([]any)
		for _, l := range launches {
			if lo := ojson.Obj(l); lo != nil {
				RemoveKey(lo, "what")
				RemoveKey(lo, "script_calls")
			}
		}
	}
	return blob
}

// Health blanks the command/path/URL named in a repeat reason, keeping the code (agent_health).
func Health(blob any) any {
	sessions := ojson.Obj(ojson.GetOr(blob, "sessions"))
	if sessions == nil {
		return blob
	}
	for _, k := range sessions.Keys {
		reasons := ojson.Obj(ojson.GetOr(sessions.Vals[k], "reasons"))
		if reasons == nil {
			continue
		}
		for _, rk := range reasons.Keys {
			r := ojson.Obj(reasons.Vals[rk])
			if r == nil {
				continue
			}
			for _, c := range []string{"1", "2", "3"} {
				if ojson.Truthy(ojson.Field(r, c)) {
					r.Set(c, "")
				}
			}
		}
	}
	return blob
}

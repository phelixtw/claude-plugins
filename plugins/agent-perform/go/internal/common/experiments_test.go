package common

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Every experiment flag defaults to on and the env override switches one off.
func TestExperiments(t *testing.T) {
	root, _ := filepath.Abs("../../..")
	t.Setenv("AP_PLUGIN_ROOT", root)
	raw, err := os.ReadFile(filepath.Join(root, "scripts", "experiments.json"))
	if err != nil {
		t.Fatal(err)
	}
	var flags map[string]bool
	json.Unmarshal(raw, &flags)
	for name := range flags {
		if !Experiment(name) {
			t.Errorf("%s off by default", name)
		}
	}
	t.Setenv("AP_EXP_POLLING_BLOCK", "0")
	if Experiment("polling_block") || !Experiment("cache_countdown") {
		t.Error("env override")
	}
}

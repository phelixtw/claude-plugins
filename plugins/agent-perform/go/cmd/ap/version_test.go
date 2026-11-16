package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The committed bin/ap must be rebuilt whenever plugin.json's version moves.
func TestShippedBinaryMatchesPluginVersion(t *testing.T) {
	raw, err := os.ReadFile("../../../.claude-plugin/plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct{ Version string }
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("../../../bin/ap", "--version").Output()
	if err != nil {
		t.Fatalf("bin/ap --version: %v", err)
	}
	if got, want := strings.TrimSpace(string(out)), "ap "+manifest.Version; got != want {
		t.Fatalf("bin/ap reports %q, plugin.json says %q: rebuild bin/ap (see main.go)", got, want)
	}
}

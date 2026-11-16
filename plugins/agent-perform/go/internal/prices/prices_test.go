package prices

import (
	"os"
	"testing"
)

func TestParseRealPage(t *testing.T) {
	md, err := os.ReadFile("testdata/pricing_model_table.md")
	if err != nil {
		t.Fatal(err)
	}
	table, long, err := Parse(string(md))
	if err != nil {
		t.Fatal(err)
	}
	if !plausible(table) {
		t.Fatalf("implausible: %v", table)
	}
	want := map[string]Rates{
		"opus-5-5":   {4, 20, 0.20, 8, 5},
		"sonnet-5-5": {2, 10, 0.10, 4, 2.5},
		"fable-5-1":  {10, 50, 0.25, 20, 12.5},
		"haiku-5-5":  {0.10, 0.50, 0.01, 0.20, 0.125},
		"opus-4-1":   {15, 75, 1.5, 30, 18.75},
	}
	for k, w := range want {
		if table[k] != w {
			t.Errorf("%s = %v, want %v", k, table[k], w)
		}
	}
	if long["haiku-5-5"] != (Rates{0.5, 2.5, 0.05, 1, 0.625}) {
		t.Errorf("long haiku = %v", long["haiku-5-5"])
	}
	// the built-in table must agree with the page on every row it shares
	for k, w := range table {
		if b, ok := builtin.table[k]; ok && b != w {
			t.Errorf("built-in %s = %v, page says %v", k, b, w)
		}
	}
	if _, _, err := Parse("## Model pricing\n\n| Model | Foo |\n| - | - |\n"); err == nil {
		t.Error("missing columns must fail")
	}
}

func TestMatching(t *testing.T) {
	cases := map[string]Rates{
		"claude-opus-5-5":            {4, 20, 0.20, 8, 5},
		"claude-opus-5-5[1m]":        {4, 20, 0.20, 8, 5},
		"claude-sonnet-5-5":          {2, 10, 0.10, 4, 2.5},
		"claude-sonnet-5-5[1m]":      {2, 10, 0.10, 4, 2.5},
		"claude-haiku-5-5":           {0.10, 0.50, 0.01, 0.20, 0.125},
		"claude-fable-5-1":           {10, 50, 0.25, 20, 12.5},
		"claude-fable-5":             {10, 50, 1, 20, 12.5},
		"claude-opus-4-1-20250805":   {15, 75, 1.5, 30, 18.75},
		"claude-opus-4-20250514":     {15, 75, 1.5, 30, 18.75},
		"claude-opus-4-6":            {5, 25, 0.5, 10, 6.25},
		"claude-sonnet-4-5-20250929": {3, 15, 0.3, 6, 3.75},
		"claude-3-5-haiku-20241022":  {0.8, 4, 0.08, 1.6, 1},
		"claude-haiku-4-5-20251001":  {1, 5, 0.1, 2, 1.25},
		"claude-sonnet-5":            {2, 10, 0.2, 4, 2.5},
		"something-unknown":          {2, 10, 0.2, 4, 2.5},
	}
	for m, w := range cases {
		if got := Of(m); got != w {
			t.Errorf("%s = %v, want %v", m, got, w)
		}
	}
	// haiku 5.5 over 100k prompt tokens pays the long rates; input counts cache reads
	if got := Price("claude-haiku-5-5", 0, 0, 200_000, 0, false); got != 0.01 {
		t.Errorf("long haiku cache read = %v, want 0.01", got)
	}
	if got := Price("claude-opus-5-5", 0, 0, 1_000_000, 0, false); got != 0.20 {
		t.Errorf("opus 5.5 read = %v", got)
	}
}

func TestLiveSheetMatches(t *testing.T) {
	md, _ := os.ReadFile("testdata/pricing_model_table.md")
	table, long, _ := Parse(string(md))
	s := merged(table, long)
	for _, m := range []string{"claude-opus-4-1-20250805", "claude-opus-4-20250514", "claude-sonnet-4-6", "claude-opus-5-5[1m]"} {
		if got, want := s.table[key(s, m)], builtin.table[key(builtin, m)]; got != want {
			t.Errorf("%s: live %v, built-in %v", m, got, want)
		}
	}
}

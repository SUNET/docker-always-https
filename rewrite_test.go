package main

import "testing"

func TestRuleMatch(t *testing.T) {
	tests := []struct {
		name, from, to, path, query string
		want                        string
		ok                          bool
	}{
		{"exact with query", "/a", "https://x/b?", "/a", "q=1&r=2", "https://x/b?q=1&r=2", true},
		{"exact no query drops ?", "/a", "https://x/b?", "/a", "", "https://x/b", true},
		{"anchored start", "/a", "https://x/b?", "/xa", "", "", false},
		{"anchored end", "/a", "https://x/b?", "/ab", "", "", false},
		{"alternation is anchored", "/a|/b", "https://x/?", "/bc", "", "", false},
		{"alternation match", "/a|/b", "https://x/?", "/b", "", "https://x/", true},
		{"capture group", "/old/(.*)", "https://x/new/$1?", "/old/foo/bar", "z=1", "https://x/new/foo/bar?z=1", true},
		{"root", "/", "https://x/?", "/", "", "https://x/", true},
		{"to without ? appends raw", "/a", "https://x/b", "/a", "q=1", "https://x/bq=1", true},
		{"percent in to kept", "/a", "https://x/b%20c?", "/a", "", "https://x/b%20c", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewRule(tt.from, tt.to)
			if err != nil {
				t.Fatal(err)
			}
			got, ok := r.Match(tt.path, tt.query)
			if ok != tt.ok || got != tt.want {
				t.Errorf("Match(%q, %q) = %q, %v; want %q, %v", tt.path, tt.query, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestParseRulesSplitsOnFirstPercent(t *testing.T) {
	rules, err := ParseRules("/a%https://x/b%20c?")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := MatchRules(rules, "/a", "")
	if !ok || got != "https://x/b%20c" {
		t.Errorf("got %q, %v", got, ok)
	}
}

func TestMatchRulesFirstWins(t *testing.T) {
	rules, err := ParseRules("/a%https://first/? /a%https://second/? /b%https://b/?")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := MatchRules(rules, "/a", ""); got != "https://first/" {
		t.Errorf("got %q", got)
	}
	if got, _ := MatchRules(rules, "/b", ""); got != "https://b/" {
		t.Errorf("got %q", got)
	}
	if _, ok := MatchRules(rules, "/c", ""); ok {
		t.Error("unexpected match")
	}
}

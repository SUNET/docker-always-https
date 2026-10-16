package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Rule is one URLS rewrite rule. It mirrors the Apache rule the old
// start.sh generated:
//
//	RewriteRule ^<from>$ <to>%{QUERY_STRING} [R=301,END]
//
// i.e. the whole request path must match <from>; the redirect target is
// <to> (with $1..$9 / ${name} expanded from capture groups) followed by the
// raw query string of the request. Rules are typically written with a
// trailing '?' on <to>, e.g. "/old%https://example.org/new?".
type Rule struct {
	re *regexp.Regexp
	to string
}

// NewRule compiles a from/to pair. from is anchored at both ends.
func NewRule(from, to string) (Rule, error) {
	re, err := regexp.Compile("^(?:" + from + ")$")
	if err != nil {
		return Rule{}, fmt.Errorf("invalid pattern: %w", err)
	}
	return Rule{re: re, to: to}, nil
}

// Match returns the redirect location for path/rawQuery, or ok=false if the
// rule does not apply.
func (r Rule) Match(path, rawQuery string) (location string, ok bool) {
	m := r.re.FindStringSubmatchIndex(path)
	if m == nil {
		return "", false
	}
	loc := string(r.re.ExpandString(nil, r.to, path, m)) + rawQuery
	// Like Apache, a lone trailing '?' just means "drop the query string".
	if rawQuery == "" {
		loc = strings.TrimSuffix(loc, "?")
	}
	return loc, true
}

// String returns a human readable form, used for logging.
func (r Rule) String() string {
	return r.re.String() + " -> " + r.to
}

// MatchRules returns the location of the first matching rule.
func MatchRules(rules []Rule, path, rawQuery string) (string, bool) {
	for _, r := range rules {
		if loc, ok := r.Match(path, rawQuery); ok {
			return loc, true
		}
	}
	return "", false
}

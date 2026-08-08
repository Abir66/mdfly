package handlers

import (
	"net/url"
	"testing"
)

func TestCanonicalizeQuery(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		changed bool
	}{
		{"no query", "", "", false},
		{"up alone", "up=1", "up=1", false},
		{"up plus junk", "up=2&utm=x", "up=2", true},
		{"junk alone", "utm=x&ref=y", "", true},
		{"repeated up collapses to first", "up=1&up=2", "up=1", true},
		{"empty-valued junk stripped", "foo=", "", true},
		{"empty-valued up kept", "up=", "up=", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := url.ParseQuery(tc.raw)
			if err != nil {
				t.Fatalf("parse %q: %v", tc.raw, err)
			}
			got, changed := canonicalizeQuery(q)
			if got != tc.want || changed != tc.changed {
				t.Errorf("canonicalizeQuery(%q) = (%q, %v), want (%q, %v)", tc.raw, got, changed, tc.want, tc.changed)
			}
		})
	}
}

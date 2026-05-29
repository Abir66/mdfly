package handlers

import "testing"

func TestSlugPageURL(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"y.md", "/s/y"},
		{"sub2/a.md", "/s/sub2/a"},
		{"../shared/x.md", "/s/shared/x?up=1"},
		{"../../a.md", "/s/a?up=2"},
	}
	for _, tt := range tests {
		if got := slugPageURL("s", tt.key); got != tt.want {
			t.Errorf("slugPageURL(s,%q)=%q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestNestedKey(t *testing.T) {
	tests := []struct {
		rest    string
		up      string
		wantKey string
		wantOK  bool
	}{
		{"y", "", "y.md", true},
		{"sub2/a", "", "sub2/a.md", true},
		{"/y/", "", "y.md", true},
		{"a", "2", "../../a.md", true},
		{"", "", "", false},
		{"y", "abc", "", false},
		{"y", "-1", "", false},
	}
	for _, tt := range tests {
		key, ok := nestedKey(tt.rest, tt.up)
		if ok != tt.wantOK || key != tt.wantKey {
			t.Errorf("nestedKey(%q,%q)=(%q,%v), want (%q,%v)", tt.rest, tt.up, key, ok, tt.wantKey, tt.wantOK)
		}
	}
}

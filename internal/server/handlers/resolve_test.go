package handlers

import "testing"

func TestResolveKey(t *testing.T) {
	const root = "/p/root"
	tests := []struct {
		name        string
		referrerDir string
		projectRoot string
		ref         string
		wantKey     string
		wantOK      bool
	}{
		{"relative inside from root", ".", root, "img/logo.png", "img/logo.png", true},
		{"dot-slash stripped", ".", root, "./logo.png", "logo.png", true},
		{"relative from nested referrer", "sub", root, "a.png", "sub/a.png", true},
		{"climb then stay inside", "sub", root, "../sub2/y.png", "sub2/y.png", true},
		{"bounce above then back inside", "sub", root, "../../root/sub3/z.png", "sub3/z.png", true},
		{"genuinely above root", ".", root, "../shared/x.png", "../shared/x.png", true},
		{"absolute inside root", ".", root, "/p/root/sub/a.png", "sub/a.png", true},
		{"absolute above root", ".", root, "/p/shared/x.png", "../shared/x.png", true},
		{"dotdot in the middle", ".", root, "a/../b/c.png", "b/c.png", true},
		{"dotdot mid-path bounces back inside", "sub", root, "deep/../../shared/x.png", "shared/x.png", true},
		{"no project root, absolute unresolvable", ".", "", "/x/y.png", "", false},
		{"no project root, relative logical", ".", "", "./y.png", "y.png", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, ok := resolveKey(tt.referrerDir, tt.projectRoot, tt.ref)
			if ok != tt.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tt.wantOK)
			}
			if key != tt.wantKey {
				t.Errorf("key=%q, want %q", key, tt.wantKey)
			}
		})
	}
}

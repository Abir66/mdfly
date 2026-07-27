package upstash_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Abir66/mdfly/internal/server/upstash"
)

// TestIncrementWithTTL_pipelinesIncrAndExpire pins the wire contract: one
// pipelined POST carrying INCR then EXPIRE for the key, bearer-authenticated,
// returning the incremented value.
func TestIncrementWithTTL_pipelinesIncrAndExpire(t *testing.T) {
	var (
		gotPath string
		gotAuth string
		gotBody [][]string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		json.Unmarshal(raw, &gotBody) //nolint:errcheck
		io.WriteString(w, `[{"result":7},{"result":1}]`)
	}))
	t.Cleanup(srv.Close)

	client := upstash.New(upstash.Config{URL: srv.URL, Token: "tok"})
	got, err := client.IncrementWithTTL(context.Background(), "rl:sub:1m:42", time.Minute)
	if err != nil {
		t.Fatalf("IncrementWithTTL: %v", err)
	}

	if got != 7 {
		t.Errorf("count = %d, want 7", got)
	}
	if gotPath != "/pipeline" {
		t.Errorf("path = %q, want /pipeline", gotPath)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok")
	}
	want := [][]string{{"INCR", "rl:sub:1m:42"}, {"EXPIRE", "rl:sub:1m:42", "60"}}
	if len(gotBody) != len(want) {
		t.Fatalf("body = %v, want %v", gotBody, want)
	}
	for i, cmd := range want {
		for j, arg := range cmd {
			if gotBody[i][j] != arg {
				t.Fatalf("body = %v, want %v", gotBody, want)
			}
		}
	}
}

// TestIncrementWithTTL_errors covers the failure shapes the limiter must see as
// errors so it can fail open: a non-200 status and a per-command error.
func TestIncrementWithTTL_errors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"server error", http.StatusInternalServerError, `{"error":"boom"}`},
		{"command error", http.StatusOK, `[{"error":"WRONGTYPE"},{"result":1}]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.body)
			}))
			t.Cleanup(srv.Close)

			client := upstash.New(upstash.Config{URL: srv.URL, Token: "tok"})
			if _, err := client.IncrementWithTTL(context.Background(), "k", time.Minute); err == nil {
				t.Fatal("IncrementWithTTL returned no error")
			}
		})
	}
}

package cloudflare_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/server/cloudflare"
)

type capturedRequest struct {
	method string
	path   string
	auth   string
	body   map[string]any
}

// newFakeAPI serves one canned response and records the request the client sent.
func newFakeAPI(t *testing.T, status int, respBody string) (*httptest.Server, *capturedRequest) {
	t.Helper()
	var got capturedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method = r.Method
		got.path = r.URL.Path
		got.auth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &got.body); err != nil {
			t.Errorf("request body is not JSON: %v (%s)", err, raw)
		}
		w.WriteHeader(status)
		io.WriteString(w, respBody) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func newClient(t *testing.T, srv *httptest.Server) *cloudflare.Client {
	t.Helper()
	return cloudflare.New(cloudflare.Config{
		ZoneID:  "zone123",
		Token:   "cf-token",
		BaseURL: srv.URL,
	})
}

// TestPurgePrefixes_wireFormat pins the request the Cloudflare purge API expects:
// a POST to the zone's purge_cache endpoint, bearer-authenticated, carrying the
// prefixes verbatim.
func TestPurgePrefixes_wireFormat(t *testing.T) {
	srv, got := newFakeAPI(t, http.StatusOK, `{"success":true}`)

	prefixes := []string{"mdfly.dev/abc123", "mdfly.dev/llm/abc123"}
	if err := newClient(t, srv).PurgePrefixes(context.Background(), prefixes); err != nil {
		t.Fatalf("PurgePrefixes: %v", err)
	}

	if got.method != http.MethodPost {
		t.Errorf("method = %s, want POST", got.method)
	}
	if want := "/zones/zone123/purge_cache"; got.path != want {
		t.Errorf("path = %s, want %s", got.path, want)
	}
	if want := "Bearer cf-token"; got.auth != want {
		t.Errorf("authorization = %q, want %q", got.auth, want)
	}
	sent, ok := got.body["prefixes"].([]any)
	if !ok {
		t.Fatalf("body has no prefixes array: %v", got.body)
	}
	if len(sent) != len(prefixes) {
		t.Fatalf("sent %d prefixes, want %d: %v", len(sent), len(prefixes), sent)
	}
	for i, p := range prefixes {
		if sent[i] != p {
			t.Errorf("prefixes[%d] = %v, want %s", i, sent[i], p)
		}
	}
}

// TestPurgePrefixes_rejectsEmpty keeps a pointless round trip from going out.
func TestPurgePrefixes_rejectsEmpty(t *testing.T) {
	srv, _ := newFakeAPI(t, http.StatusOK, `{"success":true}`)

	if err := newClient(t, srv).PurgePrefixes(context.Background(), nil); err == nil {
		t.Error("PurgePrefixes with no prefixes returned nil error")
	}
}

// TestPurgePrefixes_errors covers the failure surfaces the drain backs off on:
// rate limiting, other non-2xx statuses, and a 200 whose envelope reports
// failure.
func TestPurgePrefixes_errors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantText string
	}{
		{"rate limited", http.StatusTooManyRequests, `{"success":false}`, "429"},
		{"server error", http.StatusInternalServerError, `{"success":false}`, "500"},
		{"envelope failure", http.StatusOK,
			`{"success":false,"errors":[{"code":1012,"message":"zone not found"}]}`, "zone not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newFakeAPI(t, tt.status, tt.body)

			err := newClient(t, srv).PurgePrefixes(context.Background(), []string{"mdfly.dev/x"})
			if err == nil {
				t.Fatal("PurgePrefixes returned nil error")
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantText)
			}
		})
	}
}

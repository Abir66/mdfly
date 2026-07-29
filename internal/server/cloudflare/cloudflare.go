// Package cloudflare is a thin adapter for the Cloudflare API calls the backend
// makes (ADR-0012). Today that is a single operation: purging the edge cache by
// URL prefix.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// apiBaseURL is the Cloudflare v4 API root, overridable per Config for tests.
const apiBaseURL = "https://api.cloudflare.com/client/v4"

// requestTimeout bounds one purge round trip. A purge failure is retried from the
// durable queue, so a slow Cloudflare must cost a bounded delay, never a hang.
const requestTimeout = 10 * time.Second

// ErrNoPrefixes is returned when a purge is asked for with nothing to purge.
var ErrNoPrefixes = errors.New("cloudflare: no prefixes to purge")

// Config holds the zone the backend purges and the API token authorizing it.
// BaseURL is empty in production and points at apiBaseURL.
type Config struct {
	ZoneID  string
	Token   string
	BaseURL string
}

// Client talks to the Cloudflare API. Build with New.
type Client struct {
	cfg  Config
	http *http.Client
}

// New returns a Client for cfg.
func New(cfg Config) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = apiBaseURL
	}
	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	return &Client{cfg: cfg, http: &http.Client{Timeout: requestTimeout}}
}

// PurgePrefixes invalidates every cached response under each prefix (host + path,
// no scheme — e.g. "mdfly.dev/abc123") in one request. Cloudflare admits up to
// 100 prefixes per call on the free plan; the caller stays well inside that.
func (c *Client) PurgePrefixes(ctx context.Context, prefixes []string) error {
	if len(prefixes) == 0 {
		return ErrNoPrefixes
	}
	body, err := json.Marshal(map[string][]string{"prefixes": prefixes})
	if err != nil {
		return fmt.Errorf("encode purge body: %w", err)
	}

	url := fmt.Sprintf("%s/zones/%s/purge_cache", c.cfg.BaseURL, c.cfg.ZoneID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build purge request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cloudflare purge: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cloudflare purge: status %d", resp.StatusCode)
	}
	return decodeEnvelope(resp.Body)
}

// apiEnvelope is the common Cloudflare response wrapper. A 200 with
// success=false is a real failure, so the status code alone is not enough.
type apiEnvelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
}

func decodeEnvelope(body io.Reader) error {
	var env apiEnvelope
	if err := json.NewDecoder(body).Decode(&env); err != nil {
		return fmt.Errorf("decode purge response: %w", err)
	}
	if env.Success {
		return nil
	}
	if len(env.Errors) == 0 {
		return errors.New("cloudflare purge: unsuccessful with no errors reported")
	}
	msgs := make([]string, 0, len(env.Errors))
	for _, e := range env.Errors {
		msgs = append(msgs, fmt.Sprintf("%d: %s", e.Code, e.Message))
	}
	return fmt.Errorf("cloudflare purge: %s", strings.Join(msgs, "; "))
}

// Package upstash is a thin HTTPS-REST adapter for Upstash Redis (ADR-0028).
// It exposes the single counting operation the rate limiter needs; there is no
// persistent connection, every call is one HTTP round trip.
package upstash

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// requestTimeout bounds one REST round trip. The limiter fails open, so a slow
// Upstash must cost the write path a bounded delay, not a hung request.
const requestTimeout = 2 * time.Second

// Config holds the Upstash REST endpoint and its bearer token.
type Config struct {
	URL   string
	Token string
}

// Client talks to the Upstash REST API. Build with New.
type Client struct {
	cfg  Config
	http *http.Client
}

// New returns a Client for cfg.
func New(cfg Config) *Client {
	return &Client{cfg: cfg, http: &http.Client{Timeout: requestTimeout}}
}

// IncrementWithTTL increments key and arms its expiry, in one pipelined
// round trip, returning the incremented value. The EXPIRE only garbage-collects
// the key: callers own window resets by varying the key, so a lost EXPIRE leaks
// a key but never blocks a subject.
func (c *Client) IncrementWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if ttl <= 0 {
		return 0, fmt.Errorf("upstash: non-positive ttl %s", ttl)
	}
	body, err := json.Marshal([][]string{
		{"INCR", key},
		{"EXPIRE", key, strconv.FormatInt(expireSeconds(ttl), 10)},
	})
	if err != nil {
		return 0, fmt.Errorf("encode pipeline: %w", err)
	}

	results, err := c.pipeline(ctx, body)
	if err != nil {
		return 0, err
	}
	if len(results) == 0 {
		return 0, fmt.Errorf("upstash: empty pipeline response")
	}
	if results[0].Error != "" {
		return 0, fmt.Errorf("upstash INCR: %s", results[0].Error)
	}
	return results[0].Result, nil
}

// expireSeconds converts ttl to EXPIRE's whole-second argument, rounding up so a
// sub-second TTL arms at one second instead of truncating to zero — which Redis
// reads as "delete now".
func expireSeconds(ttl time.Duration) int64 {
	return int64((ttl + time.Second - 1) / time.Second)
}

// pipelineResult is one command's outcome in a pipeline response.
type pipelineResult struct {
	Result int64  `json:"result"`
	Error  string `json:"error"`
}

func (c *Client) pipeline(ctx context.Context, body []byte) ([]pipelineResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL+"/pipeline", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstash pipeline: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstash pipeline: status %d", resp.StatusCode)
	}

	var results []pipelineResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decode pipeline response: %w", err)
	}
	return results, nil
}

package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Abir66/mdfly/internal/api"
	"github.com/Abir66/mdfly/internal/blobmeta"
)

func postJSON[T any](ctx context.Context, client *http.Client, url string, body any) (T, error) {
	return postJSONAuth[T](ctx, client, url, "", body)
}

// postJSONAuth is postJSON with an optional Edit Token sent as a bearer header
// (ADR-0008). An empty token omits the header, so publish reuses it via postJSON.
func postJSONAuth[T any](ctx context.Context, client *http.Client, url, token string, body any) (T, error) {
	var zero T
	b, err := json.Marshal(body)
	if err != nil {
		return zero, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return zero, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return zero, &TransientError{err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, &TransientError{err: fmt.Errorf("reading response body: %w", err)}
	}
	if resp.StatusCode != http.StatusOK {
		return zero, parseErrorEnvelope(resp.StatusCode, data)
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		return zero, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// putBlob uploads one blob through its presigned URL. Content-Type and
// Cache-Control are signed into that URL, so they are sent verbatim from
// blobmeta against the same logical path the server keyed the blob by — an
// omitted or differing header is a 403, not a missing header.
func putBlob(ctx context.Context, client *http.Client, presignedURL, path string, content []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, presignedURL, bytes.NewReader(content))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(content))
	req.Header.Set("Content-Type", blobmeta.ContentType(path))
	req.Header.Set("Cache-Control", blobmeta.CacheControl)

	resp, err := client.Do(req)
	if err != nil {
		return &TransientError{err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return &TransientError{err: fmt.Errorf("reading response body: %w", err)}
		}
		return &APIError{Status: resp.StatusCode, Message: fmt.Sprintf("PUT status %d: %s", resp.StatusCode, body)}
	}
	return nil
}

// parseErrorEnvelope turns a non-2xx response into an *APIError. When the body
// carries the `{"error":{code,message,details?}}` envelope its fields are
// preserved; otherwise the raw body becomes the message.
func parseErrorEnvelope(status int, data []byte) error {
	var env api.ErrorResponse
	if err := json.Unmarshal(data, &env); err == nil && env.Error.Code != "" {
		return &APIError{
			Status:  status,
			Code:    env.Error.Code,
			Message: env.Error.Message,
			Details: env.Error.Details,
		}
	}
	return &APIError{Status: status, Message: fmt.Sprintf("status %d: %s", status, data)}
}

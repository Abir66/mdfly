package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Abir66/mdfly/internal/api"
)

func postJSON[T any](ctx context.Context, client *http.Client, url string, body any) (T, error) {
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
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return zero, fmt.Errorf("reading response body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		var apiErr api.ErrorResponse
		if jerr := json.Unmarshal(data, &apiErr); jerr == nil && apiErr.Error.Code != "" {
			return zero, fmt.Errorf("status %d (%s): %s", resp.StatusCode, apiErr.Error.Code, apiErr.Error.Message)
		}
		return zero, fmt.Errorf("status %d: %s", resp.StatusCode, data)
	}
	var result T
	if err := json.Unmarshal(data, &result); err != nil {
		return zero, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

func putBlob(ctx context.Context, client *http.Client, presignedURL string, content []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, presignedURL, bytes.NewReader(content))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(content))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("reading response body: %w", err)
		}
		return fmt.Errorf("PUT status %d: %s", resp.StatusCode, body)
	}
	return nil
}

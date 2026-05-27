package publish

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/Abir66/mdfly/internal/api"
)

func postJSON[T any](url string, body any) (T, error) {
	var zero T
	b, err := json.Marshal(body)
	if err != nil {
		return zero, err
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(b)) //nolint:noctx
	if err != nil {
		return zero, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
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

func putBlob(presignedURL string, content []byte, hexHash string) error {
	raw, err := hex.DecodeString(hexHash)
	if err != nil {
		return fmt.Errorf("decode hash: %w", err)
	}
	b64Hash := base64.StdEncoding.EncodeToString(raw)

	req, err := http.NewRequest(http.MethodPut, presignedURL, bytes.NewReader(content))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(content))
	req.Header.Set("x-amz-checksum-sha256", b64Hash)
	req.Header.Set("x-amz-sdk-checksum-algorithm", "SHA256")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PUT status %d: %s", resp.StatusCode, body)
	}
	return nil
}

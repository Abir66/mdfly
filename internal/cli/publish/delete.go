package publish

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const pathDocuments = "/v1/documents"

// DeleteOptions configures a document delete request.
type DeleteOptions struct {
	APIBase string
	Slug    string
	Token   string // Edit Token; sent as "Authorization: Bearer <token>" (ADR-0015)
}

// DeleteDocument sends DELETE /v1/documents/{slug} with the Edit Token, retrying
// transient failures. A 2xx is success (nil); a non-2xx yields an *APIError the
// caller inspects — 404/410 self-heal the local record, 401/403 are auth errors.
func DeleteDocument(ctx context.Context, client *http.Client, opts DeleteOptions) error {
	target, err := url.JoinPath(opts.APIBase, pathDocuments, opts.Slug)
	if err != nil {
		return fmt.Errorf("build delete URL: %w", err)
	}
	_, err = withRetry(ctx, func() (struct{}, error) {
		return struct{}{}, doDelete(ctx, client, target, opts.Token)
	})
	return err
}

func doDelete(ctx context.Context, client *http.Client, target, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return &TransientError{err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusOK {
		return nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return &TransientError{err: fmt.Errorf("reading response body: %w", err)}
	}
	return parseErrorEnvelope(resp.StatusCode, data)
}

package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// ErrBlobMissing is returned when HeadBlob cannot find the object.
var ErrBlobMissing = errors.New("blob not found in R2")

// Config holds connection parameters for the R2-compatible store.
type Config struct {
	Endpoint        string // S3-compatible endpoint URL (e.g. minio or R2)
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	PublicBaseURL   string // e.g. "https://storage.mdfly.dev" — prepended to BlobKey in responses
}

// Client wraps an AWS S3 client configured for R2/minio.
type Client struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucket        string
	publicBase    string
}

// New returns a Client configured from cfg.
func New(cfg Config) *Client {
	creds := credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")
	client := s3.New(s3.Options{
		BaseEndpoint:       aws.String(cfg.Endpoint),
		Credentials:        creds,
		Region:             "auto",
		UsePathStyle:       true, // required for minio and R2 custom domains
		EndpointResolverV2: nil,
		// Compat, not integrity: the SDK default (when_supported) auto-adds an unsigned
		// x-amz-sdk-checksum-algorithm header that R2 rejects with SignatureDoesNotMatch.
		// when_required keeps presigned PUTs plain. Do not remove.
		RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired,
	})
	return &Client{
		client:        client,
		presignClient: s3.NewPresignClient(client),
		bucket:        cfg.Bucket,
		publicBase:    strings.TrimRight(cfg.PublicBaseURL, "/"),
	}
}

// PrefixKey returns the R2 key prefix holding every blob of a slug.
// Format: documents/<slug>/
func PrefixKey(slug string) string {
	return "documents/" + slug + "/"
}

// BlobKey returns the R2 object key for a slug+hash+ext triple.
// Format: documents/<slug>/<hash>.<ext>
func BlobKey(slug, hashHex, ext string) string {
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return PrefixKey(slug) + hashHex + ext
}

// BlobPublicURL returns the public CDN URL for a blob.
func (c *Client) BlobPublicURL(key string) string {
	return c.publicBase + "/" + key
}

// PresignPUT returns a presigned PUT URL for a blob. ContentLength is a signed
// header, so the upload size is pinned (wrong size → broken signature → 403).
func (c *Client) PresignPUT(ctx context.Context, key string, size int64, ttl time.Duration) (string, error) {
	req, err := c.presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucket),
		Key:           aws.String(key),
		ContentLength: aws.Int64(size),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	return req.URL, nil
}

// HeadBlob checks that an object exists in R2 and that its size matches expectedSize.
func (c *Client) HeadBlob(ctx context.Context, key string, expectedSize int64) error {
	out, err := c.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return ErrBlobMissing
		}
		var re *smithyhttp.ResponseError
		if errors.As(err, &re) && re.HTTPStatusCode() == 404 {
			return ErrBlobMissing
		}
		return fmt.Errorf("head object %s: %w", key, err)
	}
	if out.ContentLength != nil && *out.ContentLength != expectedSize {
		return fmt.Errorf("blob %s: size mismatch (got %d, want %d): %w",
			key, *out.ContentLength, expectedSize, ErrBlobMissing)
	}
	return nil
}

// GetBlob fetches the full content of an object from R2.
func (c *Client) GetBlob(ctx context.Context, key string) ([]byte, error) {
	out, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return nil, ErrBlobMissing
		}
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, ErrBlobMissing
		}
		var re *smithyhttp.ResponseError
		if errors.As(err, &re) && re.HTTPStatusCode() == 404 {
			return nil, ErrBlobMissing
		}
		return nil, fmt.Errorf("get object %s: %w", key, err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		return nil, fmt.Errorf("read object %s: %w", key, err)
	}
	return data, nil
}

// ExtFromPath returns the file extension for a logical path (e.g. ".md", ".png").
func ExtFromPath(path string) string {
	return strings.ToLower(filepath.Ext(path))
}

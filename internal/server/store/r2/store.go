package r2

import (
	"context"
	"encoding/base64"
	"encoding/hex"
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
	PublicBaseURL   string // e.g. "https://cdn.mdfly.dev" — prepended to BlobKey in responses
}

// Store wraps an AWS S3 client configured for R2/minio.
type Store struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucket        string
	publicBase    string
}

// New returns a Store configured from cfg.
func New(cfg Config) *Store {
	creds := credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")
	client := s3.New(s3.Options{
		BaseEndpoint:       aws.String(cfg.Endpoint),
		Credentials:        creds,
		Region:             "auto",
		UsePathStyle:       true, // required for minio and R2 custom domains
		EndpointResolverV2: nil,
	})
	return &Store{
		client:        client,
		presignClient: s3.NewPresignClient(client),
		bucket:        cfg.Bucket,
		publicBase:    strings.TrimRight(cfg.PublicBaseURL, "/"),
	}
}

// BlobKey returns the R2 object key for a hash+ext pair.
// Format: documents/<hash>.<ext>
func BlobKey(hashHex, ext string) string {
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	return "documents/" + hashHex + ext
}

// BlobPublicURL returns the public CDN URL for a blob.
func (s *Store) BlobPublicURL(key string) string {
	return s.publicBase + "/" + key
}

// PresignPUT returns a presigned PUT URL for a blob that pins Content-Length and
// x-amz-checksum-sha256 into the V4 signature.
// sha256hex is the hex-encoded SHA256 of the blob content.
func (s *Store) PresignPUT(ctx context.Context, key string, size int64, sha256hex string, ttl time.Duration) (string, error) {
	checksumB64, err := hexToBase64(sha256hex)
	if err != nil {
		return "", fmt.Errorf("invalid sha256hex: %w", err)
	}

	req, err := s.presignClient.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:            aws.String(s.bucket),
		Key:               aws.String(key),
		ContentLength:     aws.Int64(size),
		ChecksumSHA256:    aws.String(checksumB64),
		ChecksumAlgorithm: types.ChecksumAlgorithmSha256,
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("presign put: %w", err)
	}
	return req.URL, nil
}

// HeadBlob checks that an object exists in R2 and that its size matches expectedSize.
func (s *Store) HeadBlob(ctx context.Context, key string, expectedSize int64) error {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
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
func (s *Store) GetBlob(ctx context.Context, key string) ([]byte, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
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

func hexToBase64(h string) (string, error) {
	b, err := hex.DecodeString(h)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

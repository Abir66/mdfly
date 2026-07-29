package storage

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// deleteBatchSize is R2's per-request cap for DeleteObjects. It is also the LIST
// page size, so every page maps to exactly one delete request.
const deleteBatchSize = 1000

// objectStore is the subset of the S3 API the prefix delete needs. *s3.Client
// satisfies it; tests substitute a fake.
type objectStore interface {
	ListObjectsV2(ctx context.Context, in *s3.ListObjectsV2Input, opts ...func(*s3.Options)) (*s3.ListObjectsV2Output, error)
	DeleteObjects(ctx context.Context, in *s3.DeleteObjectsInput, opts ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error)
}

// DeletePrefix removes every object under a slug's blob prefix. Blobs are
// per-slug namespaced with no cross-Document sharing (ADR-0004), so the whole
// prefix is authoritative and no key ledger is needed. An empty prefix is a
// no-op, which makes the call idempotent: a repeat pass after a crash finds
// nothing to delete.
func (c *Client) DeletePrefix(ctx context.Context, slug string) error {
	return deletePrefix(ctx, c.client, c.bucket, PrefixKey(slug))
}

func deletePrefix(ctx context.Context, store objectStore, bucket, prefix string) error {
	var token *string
	for {
		page, err := store.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
			Bucket:            aws.String(bucket),
			Prefix:            aws.String(prefix),
			MaxKeys:           aws.Int32(deleteBatchSize),
			ContinuationToken: token,
		})
		if err != nil {
			return fmt.Errorf("list %s: %w", prefix, err)
		}
		if err := deleteObjects(ctx, store, bucket, page.Contents); err != nil {
			return err
		}
		if !aws.ToBool(page.IsTruncated) {
			return nil
		}
		token = page.NextContinuationToken
	}
}

func deleteObjects(ctx context.Context, store objectStore, bucket string, objects []types.Object) error {
	if len(objects) == 0 {
		return nil
	}
	ids := make([]types.ObjectIdentifier, 0, len(objects))
	for _, o := range objects {
		ids = append(ids, types.ObjectIdentifier{Key: o.Key})
	}
	out, err := store.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(bucket),
		Delete: &types.Delete{Objects: ids, Quiet: aws.Bool(true)},
	})
	if err != nil {
		return fmt.Errorf("delete objects: %w", err)
	}
	if len(out.Errors) > 0 {
		first := out.Errors[0]
		return fmt.Errorf("delete objects: %d of %d failed, first %s: %s",
			len(out.Errors), len(ids), aws.ToString(first.Key), aws.ToString(first.Message))
	}
	return nil
}

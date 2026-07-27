package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// fakeObjectStore serves canned LIST pages and records every LIST prefix and
// DELETE batch it was asked for.
type fakeObjectStore struct {
	pages       []*s3.ListObjectsV2Output
	listPrefix  []string
	listTokens  []string
	deleted     [][]string
	listErr     error
	deleteErr   error
	deleteFails []types.Error
}

func (f *fakeObjectStore) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	f.listPrefix = append(f.listPrefix, aws.ToString(in.Prefix))
	f.listTokens = append(f.listTokens, aws.ToString(in.ContinuationToken))
	if f.listErr != nil {
		return nil, f.listErr
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return page, nil
}

func (f *fakeObjectStore) DeleteObjects(_ context.Context, in *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	keys := make([]string, 0, len(in.Delete.Objects))
	for _, o := range in.Delete.Objects {
		keys = append(keys, aws.ToString(o.Key))
	}
	f.deleted = append(f.deleted, keys)
	if f.deleteErr != nil {
		return nil, f.deleteErr
	}
	return &s3.DeleteObjectsOutput{Errors: f.deleteFails}, nil
}

func page(truncated bool, next string, keys ...string) *s3.ListObjectsV2Output {
	out := &s3.ListObjectsV2Output{IsTruncated: aws.Bool(truncated)}
	for _, k := range keys {
		out.Contents = append(out.Contents, types.Object{Key: aws.String(k)})
	}
	if next != "" {
		out.NextContinuationToken = aws.String(next)
	}
	return out
}

func TestDeletePrefix_deletesEveryObjectUnderTheSlug(t *testing.T) {
	store := &fakeObjectStore{pages: []*s3.ListObjectsV2Output{
		page(false, "", "documents/abc/1.md", "documents/abc/2.png"),
	}}

	if err := deletePrefix(context.Background(), store, "bucket", PrefixKey("abc")); err != nil {
		t.Fatalf("deletePrefix: %v", err)
	}

	if want := []string{"documents/abc/"}; len(store.listPrefix) != 1 || store.listPrefix[0] != want[0] {
		t.Errorf("list prefixes = %v, want %v", store.listPrefix, want)
	}
	if len(store.deleted) != 1 || len(store.deleted[0]) != 2 {
		t.Fatalf("delete batches = %v, want one batch of 2", store.deleted)
	}
	if store.deleted[0][0] != "documents/abc/1.md" || store.deleted[0][1] != "documents/abc/2.png" {
		t.Errorf("deleted keys = %v", store.deleted[0])
	}
}

// A prefix larger than one delete batch spans several LIST pages; each page must
// be deleted and the continuation token threaded into the next LIST.
func TestDeletePrefix_paginatesPastOneBatch(t *testing.T) {
	store := &fakeObjectStore{pages: []*s3.ListObjectsV2Output{
		page(true, "tok-1", "documents/abc/1.md"),
		page(true, "tok-2", "documents/abc/2.md"),
		page(false, "", "documents/abc/3.md"),
	}}

	if err := deletePrefix(context.Background(), store, "bucket", PrefixKey("abc")); err != nil {
		t.Fatalf("deletePrefix: %v", err)
	}

	if want := []string{"", "tok-1", "tok-2"}; !equalStrings(store.listTokens, want) {
		t.Errorf("continuation tokens = %v, want %v", store.listTokens, want)
	}
	if len(store.deleted) != 3 {
		t.Fatalf("delete batches = %v, want 3", store.deleted)
	}
	for i, batch := range store.deleted {
		want := fmt.Sprintf("documents/abc/%d.md", i+1)
		if len(batch) != 1 || batch[0] != want {
			t.Errorf("batch %d = %v, want [%s]", i, batch, want)
		}
	}
}

func TestDeletePrefix_emptyPrefixIsNoOp(t *testing.T) {
	store := &fakeObjectStore{pages: []*s3.ListObjectsV2Output{page(false, "")}}

	if err := deletePrefix(context.Background(), store, "bucket", PrefixKey("gone")); err != nil {
		t.Fatalf("deletePrefix: %v", err)
	}
	if len(store.deleted) != 0 {
		t.Errorf("delete batches = %v, want none", store.deleted)
	}
}

// A per-key failure inside a successful DeleteObjects response must surface, so
// the GC does not stamp blobs_deleted_at over objects still in the bucket.
func TestDeletePrefix_reportsPartialDeleteFailures(t *testing.T) {
	store := &fakeObjectStore{
		pages: []*s3.ListObjectsV2Output{page(false, "", "documents/abc/1.md")},
		deleteFails: []types.Error{{
			Key: aws.String("documents/abc/1.md"), Message: aws.String("AccessDenied"),
		}},
	}

	err := deletePrefix(context.Background(), store, "bucket", PrefixKey("abc"))
	if err == nil {
		t.Fatal("deletePrefix: want error on partial failure")
	}
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("err = %v, want it to name the failure", err)
	}
}

func TestDeletePrefix_propagatesListError(t *testing.T) {
	boom := errors.New("boom")
	store := &fakeObjectStore{listErr: boom}

	if err := deletePrefix(context.Background(), store, "bucket", PrefixKey("abc")); !errors.Is(err, boom) {
		t.Errorf("err = %v, want it to wrap boom", err)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

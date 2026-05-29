package publish

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Abir66/mdfly/internal/api"
)

// bundleOfTotalSize builds a bundle whose files sum to totalBytes,
// with each file staying under AnonMaxSingleFileBytes so only the
// total-bytes limit can trigger (not the single-file limit).
func bundleOfTotalSize(totalBytes int64) Bundle {
	chunkSize := api.AnonMaxSingleFileBytes // exactly at single-file limit — OK
	files := map[string]BundleFile{}
	remaining := totalBytes
	idx := 0
	for remaining > 0 {
		sz := chunkSize
		if remaining < chunkSize {
			sz = remaining
		}
		name := fmt.Sprintf("chunk%d.md", idx)
		files[name] = BundleFile{Path: name, Size: sz}
		remaining -= sz
		idx++
	}
	root := fmt.Sprintf("chunk%d.md", 0)
	return Bundle{RootPath: root, FilesByPath: files}
}

func bundleWithNFiles(n int) Bundle {
	files := make(map[string]BundleFile, n)
	for i := range n {
		name := strings.Repeat("x", i+1) + ".md"
		files[name] = BundleFile{Path: name, Size: 1}
	}
	return Bundle{RootPath: "x.md", FilesByPath: files}
}

func TestCheckBundleLimits_totalBytesExceeded(t *testing.T) {
	b := bundleOfTotalSize(api.AnonMaxTotalBytes + 1)
	err := checkBundleLimits(b)
	if err == nil {
		t.Fatal("expected error for total bytes exceeded, got nil")
	}
	var limitErr *BundleLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *BundleLimitError, got %T: %v", err, err)
	}
	if limitErr.Limit != "total_bytes" {
		t.Errorf("limit=%q, want total_bytes", limitErr.Limit)
	}
	if limitErr.Max != api.AnonMaxTotalBytes {
		t.Errorf("Max=%d, want %d", limitErr.Max, api.AnonMaxTotalBytes)
	}
}

func TestCheckBundleLimits_fileCountExceeded(t *testing.T) {
	b := bundleWithNFiles(api.AnonMaxFileCount + 1)
	err := checkBundleLimits(b)
	if err == nil {
		t.Fatal("expected error for file count exceeded, got nil")
	}
	var limitErr *BundleLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *BundleLimitError, got %T: %v", err, err)
	}
	if limitErr.Limit != "file_count" {
		t.Errorf("limit=%q, want file_count", limitErr.Limit)
	}
}

func TestCheckBundleLimits_singleFileBytesExceeded(t *testing.T) {
	b := Bundle{
		RootPath: "root.md",
		FilesByPath: map[string]BundleFile{
			"root.md": {Path: "root.md", Size: api.AnonMaxSingleFileBytes + 1},
		},
	}
	err := checkBundleLimits(b)
	if err == nil {
		t.Fatal("expected error for single file bytes exceeded, got nil")
	}
	var limitErr *BundleLimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected *BundleLimitError, got %T: %v", err, err)
	}
	if limitErr.Limit != "single_file_bytes" {
		t.Errorf("limit=%q, want single_file_bytes", limitErr.Limit)
	}
}

func TestCheckBundleLimits_atLimits_passes(t *testing.T) {
	b := bundleOfTotalSize(api.AnonMaxTotalBytes)
	if err := checkBundleLimits(b); err != nil {
		t.Errorf("bundle at exact total limit should pass: %v", err)
	}

	b2 := bundleWithNFiles(api.AnonMaxFileCount)
	if err := checkBundleLimits(b2); err != nil {
		t.Errorf("bundle at exact file count limit should pass: %v", err)
	}

	b3 := Bundle{
		RootPath: "root.md",
		FilesByPath: map[string]BundleFile{
			"root.md": {Path: "root.md", Size: api.AnonMaxSingleFileBytes},
		},
	}
	if err := checkBundleLimits(b3); err != nil {
		t.Errorf("file at exact single-file limit should pass: %v", err)
	}
}

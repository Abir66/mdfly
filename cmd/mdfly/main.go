package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/Abir66/mdfly/internal/cli/publish"
)

const defaultAPIBase = "http://localhost:8080"

func main() {
	if len(os.Args) < 3 || os.Args[1] != "publish" {
		fmt.Fprintln(os.Stderr, "usage: mdfly publish <file>")
		os.Exit(1)
	}

	apiBase := os.Getenv("MDFLY_API")
	if apiBase == "" {
		apiBase = defaultAPIBase
	}

	url, err := publish.Run(apiBase, os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if isUserError(err) {
			os.Exit(2)
		}
		os.Exit(1)
	}
	fmt.Println(url)
}

// isUserError reports whether err is caused by the caller's input (bad bundle,
// reused idempotency_key with a different payload) rather than a server/network
// fault. User errors exit with code 2; everything else exits 1.
func isUserError(err error) bool {
	if _, ok := errors.AsType[*publish.BundleLimitError](err); ok {
		return true
	}
	if _, ok := errors.AsType[*publish.IdempotencyMismatchError](err); ok {
		return true
	}
	return false
}

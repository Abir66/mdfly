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
		if _, ok := errors.AsType[*publish.BundleLimitError](err); ok {
			os.Exit(2)
		}
		os.Exit(1)
	}
	fmt.Println(url)
}

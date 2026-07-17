package main

import (
	"fmt"
	"os"

	"github.com/Abir66/mdfly/internal/cli/cmd"
)

// version is overridable at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	root := cmd.NewRootCmd(version)
	err := root.Execute()
	if err != nil {
		fmt.Fprintln(os.Stderr, "mdfly:", err)
	}
	os.Exit(cmd.ExitCode(err))
}

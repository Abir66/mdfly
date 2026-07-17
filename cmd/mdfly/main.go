package main

import (
	"os"

	"github.com/Abir66/mdfly/internal/cli/cmd"
	"github.com/Abir66/mdfly/internal/cli/output"
)

// version is overridable at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	root := cmd.NewRootCmd(version)
	err := root.Execute()

	jsonMode, _ := root.PersistentFlags().GetBool("json")
	verbose, _ := root.PersistentFlags().GetBool("verbose")
	output.RenderError(os.Stdout, os.Stderr, err, output.Options{JSON: jsonMode, Verbose: verbose})

	os.Exit(output.ExitCode(err))
}

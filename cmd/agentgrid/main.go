package main

import (
	"fmt"
	"os"

	"github.com/pshynin/agent-grid/internal/cli"
)

func main() {
	err := cli.NewRootCmd().Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	os.Exit(cli.ExitCode(err))
}

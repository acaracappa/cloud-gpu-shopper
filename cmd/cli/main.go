package main

import (
	"fmt"
	"os"

	"github.com/cloud-gpu-shopper/cloud-gpu-shopper/cmd/cli/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

package main

import (
	"fmt"
	"os"

	"github.com/cfbevan/multiverse/internal/admincli"
)

func main() {
	cfg, err := admincli.LoadConfigFromEnv()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if err := admincli.Execute(cfg, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

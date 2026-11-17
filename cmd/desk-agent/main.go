package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// version is overridden at build time: -ldflags "-X main.version=v0.1.0".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is separate from main so tests can call it without exiting the process.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("desk-agent", flag.ContinueOnError)
	fs.SetOutput(stderr)
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "desk-agent", version)
		return 0
	}
	fmt.Fprintln(stderr, "desk-agent: not implemented yet (Phase 0 scaffold); see PLAN.md")
	return 1
}

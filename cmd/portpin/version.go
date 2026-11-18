package main

import "fmt"

// runVersion prints the version, whether it came from the version subcommand
// or the -V/--version flag.
func runVersion() int {
	fmt.Println("portpin", version)
	return exitOK
}

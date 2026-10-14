// Command lyk is Lykeion's command line.
package main

import (
	"fmt"
	"os"

	"lykeion/server/internal/cli"
)

func main() {
	if err := cli.Root(os.Stdin, os.Stdout).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "lyk:", err)
		os.Exit(1)
	}
}

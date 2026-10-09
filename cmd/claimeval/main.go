// Command claimeval builds and scores the ticket-claims eval suite.
package main

import (
	"context"
	"os"

	"github.com/kapetan-io/dispatch/internal/claimeval"
)

func main() {
	os.Exit(claimeval.Run(context.Background(), os.Args[1:], claimeval.Options{Stdout: os.Stdout, Stderr: os.Stderr}))
}

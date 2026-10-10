// Command commenteval runs and scores the needs-human-comment eval.
package main

import (
	"context"
	"os"

	"github.com/kapetan-io/dispatch/internal/commenteval"
)

func main() {
	os.Exit(commenteval.Run(context.Background(), os.Args[1:], commenteval.Options{Stdout: os.Stdout, Stderr: os.Stderr}))
}

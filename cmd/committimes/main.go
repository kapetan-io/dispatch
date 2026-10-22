// Command committimes sets each tracked file's modification time to its last commit's time.
package main

import (
	"context"
	"os"

	"github.com/kapetan-io/dispatch/internal/committimes"
)

func main() {
	os.Exit(committimes.Run(context.Background(), os.Args[1:], committimes.Options{Stdout: os.Stdout, Stderr: os.Stderr}))
}

// Command slowtests reports the tests and packages that run over the speed budget.
package main

import (
	"context"
	"os"

	"github.com/kapetan-io/dispatch/internal/slowtests"
)

func main() {
	os.Exit(slowtests.Run(context.Background(), os.Args[1:], slowtests.Options{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}))
}

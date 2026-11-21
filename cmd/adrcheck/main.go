// Command adrcheck fails when an ADR under docs/adr cites a ticket, a file in the
// repo or the repo's history, or breaks the Status and numbering rules.
package main

import (
	"context"
	"os"

	"github.com/kapetan-io/dispatch/internal/adrcheck"
)

func main() {
	os.Exit(adrcheck.Run(context.Background(), os.Args[1:], adrcheck.Options{Stdout: os.Stdout, Stderr: os.Stderr}))
}

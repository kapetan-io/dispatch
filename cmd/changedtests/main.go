// Command changedtests labels a pull request changes-existing-tests while it modifies or
// deletes a Go test function that existed at the merge base, and removes the label once it
// doesn't. The reusable workflow .github/workflows/changes-existing-tests.yml runs it.
package main

import (
	"context"
	"os"

	"github.com/kapetan-io/dispatch/internal/changedtests"
)

func main() {
	os.Exit(changedtests.Run(context.Background(), os.Args[1:], changedtests.Options{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		APIURL: os.Getenv("GITHUB_API_URL"),
		Token:  os.Getenv("GITHUB_TOKEN"),
	}))
}

// Command showeval replays the ENG-506 explanation miss against rule 5 of the converse skill.
package main

import (
	"context"
	"os"

	"github.com/kapetan-io/dispatch/internal/showeval"
)

func main() {
	os.Exit(showeval.Run(context.Background(), os.Args[1:], showeval.Options{Stdout: os.Stdout, Stderr: os.Stderr}))
}

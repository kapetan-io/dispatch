package main

import (
	"os"

	"billing"
)

func main() {
	os.Exit(billing.Run(os.Args[1:], os.Stdout, os.Stderr, "."))
}

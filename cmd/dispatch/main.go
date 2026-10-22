package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kapetan-io/dispatch"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dir, _ := os.Getwd()
	if err := dispatch.Run(ctx, os.Args[1:], dispatch.Options{Env: os.Environ(), Dir: dir}); err != nil {
		fmt.Fprintln(os.Stderr, "dispatch:", err)
		os.Exit(dispatch.ExitCode(err))
	}
}

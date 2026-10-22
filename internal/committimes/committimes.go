// Package committimes gives a fresh checkout stable file modification times.
// go test's result cache compares the time and size of every file a test
// opens, so files stamped with checkout time never hit a cache saved by an
// earlier run.
package committimes

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	Stdout io.Writer
	Stderr io.Writer
}

// Run is the CLI. It sets the time of every file in the repository at -dir
// that history names, and returns the process exit code.
func Run(ctx context.Context, args []string, opts Options) int {
	if err := run(ctx, args, opts); err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "committimes: %v\n", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, args []string, opts Options) error {
	fs := flag.NewFlagSet("committimes", flag.ContinueOnError)
	fs.SetOutput(opts.Stderr)
	dir := fs.String("dir", ".", "repository `directory`")
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	tracked, err := git(ctx, *dir, "ls-files", "-z")
	if err != nil {
		return err
	}
	want := make(map[string]bool)
	for _, path := range strings.Split(string(tracked), "\x00") {
		if path != "" {
			want[path] = true
		}
	}

	// Newest commit first, so the first time a path appears is its last change.
	cmd := exec.CommandContext(ctx, "git", "-C", *dir, "log", "--format=@%ct", "--name-only", "--no-renames", "-z")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var stamp time.Time
	set := 0
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 0, 1<<20), 1<<26)
	scan.Split(splitNUL)
	for scan.Scan() {
		token := strings.TrimLeft(scan.Text(), "\n")
		if token == "" {
			continue
		}
		// With -z, the commit header and its first path share one token.
		if strings.HasPrefix(token, "@") {
			header, rest, _ := strings.Cut(token, "\n")
			seconds, err := strconv.ParseInt(header[1:], 10, 64)
			if err != nil {
				return fmt.Errorf("parse commit time %q: %w", header, err)
			}
			stamp = time.Unix(seconds, 0)
			token = strings.TrimLeft(rest, "\n")
			if token == "" {
				continue
			}
		}
		if !want[token] {
			continue
		}
		delete(want, token)
		if err := os.Chtimes(filepath.Join(*dir, token), stamp, stamp); err != nil {
			return err
		}
		set++
	}
	if err := scan.Err(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("git log: %w", err)
	}
	_, _ = fmt.Fprintf(opts.Stdout, "set the time of %d files; %d have no history\n", set, len(want))
	return nil
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return out, nil
}

func splitNUL(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == 0 {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
